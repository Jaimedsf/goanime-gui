// Package scraper provides access to animefire.one
package animefire

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/alvarorichard/Goanime/internal/models"
	"github.com/alvarorichard/Goanime/internal/scraper/netx"
	"github.com/alvarorichard/Goanime/internal/util"
)

const (
	// AnimefireBase is the public site. It still serves the pages a user opens
	// in a browser, so it remains the shape of the URLs we hand upstream.
	AnimefireBase = "https://animefire.one"
	// AnimefireAPIBase is the JSON API the rewritten site talks to. Every
	// listing, episode and stream lookup goes through it.
	AnimefireAPIBase = "https://api.animefire.one"

	// maxSearchResults caps how many search hits we map. The API answers with
	// 30 for a broad query; more than that is noise in a picker.
	maxSearchResults = 30
)

// audio track names as the API spells them.
const (
	audioSubbed = "legendado"
	audioDubbed = "dublado"
)

// AnimefireClient handles interactions with Animefire.io.
//
// The site was rebuilt as a JSON-backed single-page app: the old
// /pesquisar/<name> and /animes/<slug>-todos-os-episodios routes now 404 and
// there is no server-rendered markup left to scrape. This client talks to the
// API those pages call instead.
type AnimefireClient struct {
	client     *http.Client
	baseURL    string
	apiBase    string
	userAgent  string
	maxRetries int
	retryDelay time.Duration
}

// NewAnimefireClient creates a new Animefire client
func NewAnimefireClient() *AnimefireClient {
	return &AnimefireClient{
		client:     util.NewFastClient(),
		baseURL:    AnimefireBase,
		apiBase:    AnimefireAPIBase,
		userAgent:  netx.UserAgent,
		maxRetries: 2,
		retryDelay: 100 * time.Millisecond,
	}
}

// --- API payloads ---------------------------------------------------------

// apiCard is one entry in a listing (search, home, recommendations).
type apiCard struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Titles      map[string]string `json:"titles"`
	Audio       string            `json:"audio"`
	PosterSrc   string            `json:"poster_src"`
	Status      string            `json:"status"`
	PublishedAt string            `json:"published_at"`
}

// name returns the card's display title. The API used to send a flat "title";
// since the move to animefire.one it sends "titles" keyed by region, so the
// Brazilian one is preferred and the others are fallbacks.
func (c apiCard) name() string {
	if t := strings.TrimSpace(c.Title); t != "" {
		return t
	}
	for _, k := range []string{"BR", "US", "JP"} {
		if t := strings.TrimSpace(c.Titles[k]); t != "" {
			return t
		}
	}
	for _, t := range c.Titles {
		if t = strings.TrimSpace(t); t != "" {
			return t
		}
	}
	return ""
}

type apiSearchResponse struct {
	Data []apiCard `json:"data"`
}

// apiHero is the header block of an anime page: the title-level metadata.
type apiHero struct {
	ID          string            `json:"id"`
	Titles      map[string]string `json:"titles"`
	Synopsis    string            `json:"synopsis"`
	PosterSrc   string            `json:"poster_src"`
	Status      string            `json:"status"`
	PublishedAt string            `json:"published_at"`
	Audio       string            `json:"audio"`
	Genres      []string          `json:"genres"`
	Score       float64           `json:"score"`
}

// apiEpisode is one entry of an anime's episode list.
type apiEpisode struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Audio    string `json:"audio"`
	Season   int    `json:"season"`
	Number   int    `json:"number"`
	StillSrc string `json:"still_src"`
	Synopsis string `json:"synopsis"`
}

// apiSeason describes one season. FirstEpisodeNumber is the season's offset in
// the title's overall run: the API numbers episodes from 1 again inside every
// season, and this is what turns that back into an absolute number.
type apiSeason struct {
	Title              string `json:"title"`
	Number             int    `json:"number"`
	FirstEpisodeNumber int    `json:"first_episode_number"`
}

type apiAnimeResponse struct {
	Data struct {
		Format   string       `json:"format"`
		Hero     apiHero      `json:"hero"`
		Seasons  []apiSeason  `json:"seasons"`
		Episodes []apiEpisode `json:"episodes"`
	} `json:"data"`
}

// apiStream is one playable track. The site ships a separate track per audio
// language rather than per quality; the qualities live inside the manifest.
type apiStream struct {
	Audio     string   `json:"audio"`
	IsMTL     bool     `json:"is_mtl"`
	IsOffline bool     `json:"is_offline"`
	URL       string   `json:"url"`
	Qualities []string `json:"qualities"`
}

type apiEpisodeResponse struct {
	Data struct {
		ID      string      `json:"id"`
		Title   string      `json:"title"`
		Season  int         `json:"season"`
		Number  int         `json:"number"`
		Streams []apiStream `json:"streams"`
	} `json:"data"`
}

// --- HTTP ------------------------------------------------------------------

// getJSON performs a GET against the API and decodes the body into out,
// retrying transport and 5xx failures the same way the scraper used to.
func (c *AnimefireClient) getJSON(endpoint string, out any) error {
	var lastErr error
	attempts := c.maxRetries + 1

	for attempt := range attempts {
		req, err := http.NewRequest(http.MethodGet, endpoint, http.NoBody)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		c.decorateRequest(req)

		resp, err := c.client.Do(req) // #nosec G704
		if err != nil {
			lastErr = fmt.Errorf("failed to make request: %w", err)
			if c.shouldRetry(attempt) {
				c.sleep()
				continue
			}
			return lastErr
		}

		if err := netx.CheckHTTPStatus(resp, "AnimeFire API"); err != nil {
			lastErr = err
			_ = resp.Body.Close()
			if c.shouldRetry(attempt) {
				c.sleep()
				continue
			}
			return lastErr
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read response: %w", err)
			if c.shouldRetry(attempt) {
				c.sleep()
				continue
			}
			return lastErr
		}

		if err := json.Unmarshal(body, out); err != nil {
			// A non-JSON body here means an interstitial or an error page, not
			// a transient fault: retrying would just fetch it again.
			return fmt.Errorf("failed to parse AnimeFire API response: %w", err)
		}
		return nil
	}

	if lastErr != nil {
		return lastErr
	}
	return errors.New("AnimeFire API request failed")
}

func (c *AnimefireClient) decorateRequest(req *http.Request) {
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "pt-BR,pt;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Origin", c.baseURL)
	req.Header.Set("Referer", c.baseURL+"/")
}

func (c *AnimefireClient) shouldRetry(attempt int) bool {
	return attempt < c.maxRetries
}

func (c *AnimefireClient) sleep() {
	if c.retryDelay <= 0 {
		return
	}
	time.Sleep(c.retryDelay)
}

// --- identity --------------------------------------------------------------

// animeURL builds the public page URL for an anime id. This is what upstream
// stores as the title's identity, so it has to stay stable and shareable.
func (c *AnimefireClient) animeURL(id string) string {
	return c.baseURL + "/anime/" + id
}

// episodeURL builds the handle for one episode. The site itself renders
// episodes inside the anime page rather than on their own route, so this URL
// exists to carry both ids back to GetEpisodeStreamURL.
func (c *AnimefireClient) episodeURL(animeID, episodeID string) string {
	return c.baseURL + "/anime/" + animeID + "/" + episodeID
}

// animeIDFromURL pulls the opaque anime id out of a stored URL. It accepts the
// current /anime/<id> shape and tolerates a trailing episode segment.
func animeIDFromURL(raw string) (string, error) {
	id, _, err := idsFromURL(raw)
	return id, err
}

// idsFromURL splits a stored URL into its anime id and, when present, its
// episode id.
func idsFromURL(raw string) (animeID, episodeID string, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", errors.New("empty AnimeFire URL")
	}

	path := trimmed
	if parsed, perr := url.Parse(trimmed); perr == nil && parsed.Path != "" {
		path = parsed.Path
	}

	parts := make([]string, 0, 3)
	for _, seg := range strings.Split(path, "/") {
		if seg != "" {
			parts = append(parts, seg)
		}
	}

	// Expected shapes: anime/<id> and anime/<id>/<episodeID>.
	for i, seg := range parts {
		if seg != "anime" {
			continue
		}
		if i+1 >= len(parts) {
			break
		}
		animeID = parts[i+1]
		if i+2 < len(parts) {
			episodeID = parts[i+2]
		}
		return animeID, episodeID, nil
	}

	// The pre-rewrite catalog stored /animes/<slug>-todos-os-episodios URLs.
	// Those pages are gone, so say so plainly instead of failing on a parse.
	if len(parts) >= 2 && parts[0] == "animes" {
		return "", "", fmt.Errorf("AnimeFire URL %q uses the retired /animes/<slug> format; search the title again to refresh it", raw)
	}

	return "", "", fmt.Errorf("could not extract an AnimeFire id from %q", raw)
}

// --- search ----------------------------------------------------------------

// SearchAnime searches for anime on Animefire.io.
func (c *AnimefireClient) SearchAnime(query string) ([]*models.Anime, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, errors.New("empty search query")
	}

	endpoint := fmt.Sprintf("%s/animes/pesquisar?q=%s", c.apiBase, url.QueryEscape(trimmed))
	util.Debug("AnimeFire search", "query", trimmed, "url", endpoint)

	var parsed apiSearchResponse
	if err := c.getJSON(endpoint, &parsed); err != nil {
		return nil, err
	}

	animes := make([]*models.Anime, 0, len(parsed.Data))
	for _, card := range parsed.Data {
		title := card.name()
		if card.ID == "" || title == "" {
			continue
		}
		animes = append(animes, &models.Anime{
			Name:      decorateTitle(title, card.Audio),
			URL:       c.animeURL(card.ID),
			ImageURL:  card.PosterSrc,
			Year:      yearOf(card.PublishedAt),
			MediaType: models.MediaTypeAnime,
		})
		if len(animes) >= maxSearchResults {
			break
		}
	}

	util.Debug("AnimeFire search results", "query", trimmed, "count", len(animes))
	return animes, nil
}

// decorateTitle appends the audio track when a title is exclusively dubbed or
// exclusively subbed. Upstream tagging reads that word off the name to label
// results, and the rewritten API moved it out of the title into its own field.
func decorateTitle(title, audio string) string {
	name := strings.TrimSpace(title)
	lower := strings.ToLower(strings.TrimSpace(audio))
	switch {
	case lower == "":
		return name
	case strings.Contains(lower, audioDubbed) && strings.Contains(lower, audioSubbed):
		// Both tracks exist; leave the name clean.
		return name
	case strings.Contains(lower, audioDubbed):
		return name + " (Dublado)"
	case strings.Contains(lower, audioSubbed):
		return name + " (Legendado)"
	}
	return name
}

// yearOf takes the year out of an ISO date such as "2002-10-03".
func yearOf(published string) string {
	if len(published) >= 4 {
		return published[:4]
	}
	return ""
}

// --- episodes --------------------------------------------------------------

// GetAnimeEpisodes fetches the list of episodes for a given anime.
func (c *AnimefireClient) GetAnimeEpisodes(animeURL string) ([]models.Episode, error) {
	animeID, err := animeIDFromURL(animeURL)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/anime/%s", c.apiBase, url.PathEscape(animeID))
	util.Debug("AnimeFire episodes", "animeID", animeID, "url", endpoint)

	var parsed apiAnimeResponse
	if err := c.getJSON(endpoint, &parsed); err != nil {
		return nil, err
	}

	raw := parsed.Data.Episodes
	if len(raw) == 0 {
		return nil, fmt.Errorf("no episodes listed for AnimeFire anime %q", animeID)
	}

	// The API returns every season in one list. Sort by (season, number) so the
	// order upstream shows matches the order a viewer expects.
	sort.SliceStable(raw, func(i, j int) bool {
		if raw[i].Season != raw[j].Season {
			return raw[i].Season < raw[j].Season
		}
		return raw[i].Number < raw[j].Number
	})

	multiSeason := hasMultipleSeasons(raw)
	offsets := seasonOffsets(parsed.Data.Seasons, raw)

	episodes := make([]models.Episode, 0, len(raw))
	for _, ep := range raw {
		if ep.ID == "" {
			continue
		}
		episodes = append(episodes, models.Episode{
			Number:   episodeLabel(ep, multiSeason),
			Num:      absoluteNumber(ep, offsets),
			URL:      c.episodeURL(animeID, ep.ID),
			Title:    models.TitleDetails{Romaji: ep.Title},
			Synopsis: ep.Synopsis,
			SeasonID: seasonID(ep.Season, multiSeason),
		})
	}

	if len(episodes) == 0 {
		return nil, fmt.Errorf("no usable episodes for AnimeFire anime %q", animeID)
	}

	util.Debug("AnimeFire episodes parsed", "animeID", animeID, "count", len(episodes))
	return episodes, nil
}

// seasonOffsets maps a season number to the absolute number its first episode
// carries. AnimeFire restarts numbering inside every season (Naruto lists
// 1..52 four times over), but upstream treats this source as a flat episode
// list, so a duplicated Num would make ordering and resume ambiguous.
//
// The API's own seasons block carries the offsets; when it is missing or
// incomplete, the offsets are recomputed by counting the episodes of each
// preceding season.
func seasonOffsets(seasons []apiSeason, eps []apiEpisode) map[int]int {
	offsets := make(map[int]int, len(seasons))
	for _, s := range seasons {
		if s.FirstEpisodeNumber > 0 {
			offsets[s.Number] = s.FirstEpisodeNumber
		}
	}

	// Fill in whatever the API did not describe, walking seasons in order and
	// accumulating their episode counts.
	counts := make(map[int]int, len(eps))
	ordered := make([]int, 0, len(eps))
	for _, ep := range eps {
		if _, seen := counts[ep.Season]; !seen {
			ordered = append(ordered, ep.Season)
		}
		counts[ep.Season]++
	}
	sort.Ints(ordered)

	running := 1
	for _, season := range ordered {
		if _, ok := offsets[season]; !ok {
			offsets[season] = running
		}
		running = offsets[season] + counts[season]
	}

	return offsets
}

// absoluteNumber turns a season-relative episode number into the title's
// overall episode number.
func absoluteNumber(ep apiEpisode, offsets map[int]int) int {
	offset, ok := offsets[ep.Season]
	if !ok {
		return ep.Number
	}
	return offset + ep.Number - 1
}

// hasMultipleSeasons reports whether the list spans more than one season.
func hasMultipleSeasons(eps []apiEpisode) bool {
	if len(eps) == 0 {
		return false
	}
	first := eps[0].Season
	for _, ep := range eps[1:] {
		if ep.Season != first {
			return true
		}
	}
	return false
}

// episodeLabel is the human-facing episode string. Single-season titles keep
// the plain "Episódio N" the old scraper produced; multi-season ones need the
// season to stay unambiguous, since numbering restarts.
func episodeLabel(ep apiEpisode, multiSeason bool) string {
	if multiSeason {
		return fmt.Sprintf("T%d Episódio %d", ep.Season, ep.Number)
	}
	return fmt.Sprintf("Episódio %d", ep.Number)
}

// seasonID identifies the season for multi-season titles and stays empty for
// single-season ones, matching what the rest of the app expects from a source
// that does not advertise seasons.
func seasonID(season int, multiSeason bool) string {
	if !multiSeason {
		return ""
	}
	return fmt.Sprintf("%d", season)
}

// --- stream ----------------------------------------------------------------

// GetEpisodeStreamURL gets the streaming URL for a specific episode.
//
// The rewritten site serves MPEG-DASH manifests (an .mpd behind a .jpg path),
// one per audio track, so there is no per-quality URL to pick between any
// more: the manifest carries every rendition and the player adapts.
func (c *AnimefireClient) GetEpisodeStreamURL(episodeURL string) (string, error) {
	_, episodeID, err := idsFromURL(episodeURL)
	if err != nil {
		return "", err
	}
	if episodeID == "" {
		return "", fmt.Errorf("AnimeFire URL %q carries no episode id", episodeURL)
	}

	endpoint := fmt.Sprintf("%s/episode/%s", c.apiBase, url.PathEscape(episodeID))
	util.Debug("AnimeFire stream URL", "episodeID", episodeID, "url", endpoint)

	var parsed apiEpisodeResponse
	if err := c.getJSON(endpoint, &parsed); err != nil {
		return "", err
	}

	stream := pickStream(parsed.Data.Streams)
	if stream == nil {
		return "", fmt.Errorf("no playable stream for AnimeFire episode %q", episodeID)
	}

	util.Debug("AnimeFire stream selected",
		"episodeID", episodeID, "audio", stream.Audio, "qualities", strings.Join(stream.Qualities, ","))
	return stream.URL, nil
}

// pickStream chooses the track to play: subbed first, then dubbed, then
// whatever is left. Machine-translated and offline tracks are a last resort.
func pickStream(streams []apiStream) *apiStream {
	var subbed, dubbed, other, degraded *apiStream

	for i := range streams {
		s := &streams[i]
		if s.URL == "" {
			continue
		}
		if s.IsOffline || s.IsMTL {
			if degraded == nil {
				degraded = s
			}
			continue
		}
		switch strings.ToLower(strings.TrimSpace(s.Audio)) {
		case audioSubbed:
			if subbed == nil {
				subbed = s
			}
		case audioDubbed:
			if dubbed == nil {
				dubbed = s
			}
		default:
			if other == nil {
				other = s
			}
		}
	}

	for _, candidate := range []*apiStream{subbed, dubbed, other, degraded} {
		if candidate != nil {
			return candidate
		}
	}
	return nil
}

// GetAnimeDetails is a placeholder method; details are fetched by the API layer.
func (c *AnimefireClient) GetAnimeDetails(animeURL string) (*models.Anime, error) {
	return nil, fmt.Errorf("anime details should be fetched using API layer, not scraper")
}

// NewClientForTest returns a client pointed at a test server with retries
// disabled. Only for tests.
func NewClientForTest(serverURL string) *AnimefireClient {
	c := NewAnimefireClient()
	c.baseURL = serverURL
	c.apiBase = serverURL
	c.maxRetries = 0
	c.retryDelay = 0
	return c
}
