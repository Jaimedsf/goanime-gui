package guiapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Jikan is the unofficial MyAnimeList API. It backs the catalog because
// AniList disabled its own API ("temporarily disabled due to severe
// stability issues"), which turned every listing into an HTTP 403.
//
// Jikan speaks MyAnimeList's vocabulary, not AniList's: types are "TV" and
// "Movie", statuses are sentences ("Currently Airing"), scores run 0-10 and
// seasons are lower case. Everything below normalises those back into the
// AniList shape BrowseItem already promises, so the frontend — which maps
// RELEASING/FINISHED and renders the score as a percentage — needs no
// changes and both backends stay interchangeable.
const (
	jikanBase = "https://api.jikan.moe/v4"

	// jikanPerPage is the page size. Jikan caps limit at 25, below the 40
	// AniList allowed, so the pager takes its answer from the response
	// rather than assuming a fixed size.
	jikanPerPage = 25

	// Jikan allows 3 requests a second and 60 a minute. The catalog and the
	// genre list can load together on a cold start, so requests pass through
	// one gate that spaces them out instead of bursting.
	jikanMinInterval = 400 * time.Millisecond
	jikanTimeout     = 15 * time.Second
)

var (
	jikanGate sync.Mutex
	jikanLast time.Time
)

// jikanWait spaces requests out, holding the gate across the sleep so that
// concurrent callers queue rather than all firing at once.
func jikanWait() {
	jikanGate.Lock()
	defer jikanGate.Unlock()

	if gap := time.Since(jikanLast); gap < jikanMinInterval {
		time.Sleep(jikanMinInterval - gap)
	}
	jikanLast = time.Now()
}

// jikanBaseURL is a variable so tests can point it at a local server.
var jikanBaseURL = jikanBase

// jikanGet performs one GET against the API and decodes it into out.
func jikanGet(path string, out any) error {
	jikanWait()

	endpoint := jikanBaseURL + path
	req, err := http.NewRequest(http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("jikan: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "GoAnime-GUI/1.0")

	client := &http.Client{Timeout: jikanTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("jikan: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// A 504 here is Jikan failing to reach MyAnimeList, not a fault of
		// ours; say which side is down so the fallback's reason is legible.
		if resp.StatusCode == http.StatusGatewayTimeout || resp.StatusCode == http.StatusBadGateway {
			return fmt.Errorf("o Jikan não conseguiu falar com o MyAnimeList (HTTP %d)", resp.StatusCode)
		}
		return fmt.Errorf("o Jikan respondeu HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("jikan: %w", err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("jikan: resposta ilegível: %w", err)
	}
	return nil
}

// --- payloads --------------------------------------------------------------

type jikanPagination struct {
	HasNextPage bool `json:"has_next_page"`
	CurrentPage int  `json:"current_page"`
}

type jikanNamed struct {
	MalID int    `json:"mal_id"`
	Name  string `json:"name"`
}

type jikanDateProp struct {
	Day   int `json:"day"`
	Month int `json:"month"`
	Year  int `json:"year"`
}

type jikanAnime struct {
	MalID  int `json:"mal_id"`
	Images struct {
		JPG struct {
			ImageURL      string `json:"image_url"`
			LargeImageURL string `json:"large_image_url"`
		} `json:"jpg"`
	} `json:"images"`
	Title         string       `json:"title"`
	TitleEnglish  string       `json:"title_english"`
	TitleJapanese string       `json:"title_japanese"`
	Type          string       `json:"type"`
	Episodes      int          `json:"episodes"`
	Status        string       `json:"status"`
	Score         float64      `json:"score"`
	Season        string       `json:"season"`
	Year          int          `json:"year"`
	Genres        []jikanNamed `json:"genres"`
	Aired         struct {
		Prop struct {
			From jikanDateProp `json:"from"`
		} `json:"prop"`
	} `json:"aired"`
}

type jikanListResponse struct {
	Pagination jikanPagination `json:"pagination"`
	Data       []jikanAnime    `json:"data"`
}

// --- vocabulary normalisation ---------------------------------------------

// jikanFormat maps MyAnimeList's type onto AniList's MediaFormat, which is
// what the frontend's FORMAT_LABELS is keyed by.
func jikanFormat(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "tv":
		return "TV"
	case "tv special":
		return "TV_SHORT"
	case "movie":
		return "MOVIE"
	case "special":
		return "SPECIAL"
	case "ova":
		return "OVA"
	case "ona":
		return "ONA"
	case "music":
		return "MUSIC"
	}
	return strings.ToUpper(strings.TrimSpace(t))
}

// jikanTypeParam is the reverse: an AniList MediaFormat as Jikan's type
// query parameter. An unmappable format returns "" so the filter is dropped
// rather than sent as something Jikan would reject.
func jikanTypeParam(format string) string {
	switch strings.ToUpper(strings.TrimSpace(format)) {
	case "TV":
		return "tv"
	case "MOVIE":
		return "movie"
	case "SPECIAL":
		return "special"
	case "OVA":
		return "ova"
	case "ONA":
		return "ona"
	case "MUSIC":
		return "music"
	}
	return ""
}

// jikanStatus maps MyAnimeList's status sentence onto AniList's MediaStatus.
func jikanStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "currently airing":
		return "RELEASING"
	case "finished airing":
		return "FINISHED"
	case "not yet aired":
		return "NOT_YET_RELEASED"
	}
	return ""
}

// jikanScore converts MyAnimeList's 0-10 score into the 0-100 integer the
// catalog renders as a percentage.
func jikanScore(score float64) int {
	if score <= 0 {
		return 0
	}
	return int(score*10 + 0.5)
}

// jikanGenreNames flattens the genre objects into the plain names BrowseItem
// carries.
func jikanGenreNames(genres []jikanNamed) []string {
	if len(genres) == 0 {
		return nil
	}
	out := make([]string, 0, len(genres))
	for _, g := range genres {
		if g.Name != "" {
			out = append(out, g.Name)
		}
	}
	return out
}

// --- genre ids -------------------------------------------------------------

// Jikan filters by numeric genre id where AniList filtered by name, so the
// name the query carries has to be resolved before it can be sent.
var (
	jikanGenreOnce sync.Once
	jikanGenreIDs  map[string]int
	jikanGenreErr  error
)

func jikanGenreIndex() (map[string]int, error) {
	jikanGenreOnce.Do(func() {
		var parsed struct {
			Data []jikanNamed `json:"data"`
		}
		if err := jikanGet("/genres/anime", &parsed); err != nil {
			jikanGenreErr = err
			return
		}
		index := make(map[string]int, len(parsed.Data))
		for _, g := range parsed.Data {
			if g.Name != "" && g.MalID > 0 {
				index[strings.ToLower(g.Name)] = g.MalID
			}
		}
		jikanGenreIDs = index
	})
	return jikanGenreIDs, jikanGenreErr
}

// jikanFetchGenres returns the genre names Jikan offers, for the filter
// dropdown.
func jikanFetchGenres() ([]string, error) {
	var parsed struct {
		Data []jikanNamed `json:"data"`
	}
	if err := jikanGet("/genres/anime", &parsed); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(parsed.Data))
	for _, g := range parsed.Data {
		if g.Name != "" {
			out = append(out, g.Name)
		}
	}
	return out, nil
}

// --- catalog ---------------------------------------------------------------

// jikanSeasonName renders AniList's SCREAMING_CASE season the way Jikan's
// path segment expects it.
func jikanSeasonName(season string) string {
	return strings.ToLower(strings.TrimSpace(season))
}

// jikanCatalogPath builds the endpoint and query for one catalog request.
//
// Season mode has its own endpoint; every other mode is the general anime
// listing narrowed by ordering and status. Jikan has no direct equivalent of
// AniList's TRENDING sort, so "em alta" becomes the most-watched titles that
// are actually airing — the same thing the listing is meant to convey.
func jikanCatalogPath(q BrowseQuery) string {
	params := url.Values{}
	params.Set("page", fmt.Sprintf("%d", q.Page))
	params.Set("limit", fmt.Sprintf("%d", jikanPerPage))
	// sfw mirrors AniList's isAdult:false — the catalog never lists adult titles.
	params.Set("sfw", "true")

	if q.Mode == ModeSeason {
		if t := jikanTypeParam(q.Format); t != "" {
			params.Set("filter", t)
		}
		return fmt.Sprintf("/seasons/%d/%s?%s",
			q.Year, jikanSeasonName(q.Season), params.Encode())
	}

	switch q.Mode {
	case ModeTop:
		params.Set("order_by", "score")
		params.Set("sort", "desc")
	case ModePopular:
		// Jikan ranks popularity ascending: 1 is the most popular title.
		params.Set("order_by", "popularity")
		params.Set("sort", "asc")
	case ModeTrending:
		params.Set("status", "airing")
		params.Set("order_by", "members")
		params.Set("sort", "desc")
	case ModeAiring:
		params.Set("status", "airing")
		params.Set("order_by", "members")
		params.Set("sort", "desc")
	case ModeUpcoming:
		params.Set("status", "upcoming")
		params.Set("order_by", "members")
		params.Set("sort", "desc")
	default:
		params.Set("order_by", "popularity")
		params.Set("sort", "asc")
	}

	if t := jikanTypeParam(q.Format); t != "" {
		params.Set("type", t)
	}
	if q.Year > 0 {
		// Jikan has no year filter, but it does take a date range.
		params.Set("start_date", fmt.Sprintf("%d-01-01", q.Year))
		params.Set("end_date", fmt.Sprintf("%d-12-31", q.Year))
	}
	if q.Genre != "" {
		if index, err := jikanGenreIndex(); err == nil {
			if id, ok := index[strings.ToLower(q.Genre)]; ok {
				params.Set("genres", fmt.Sprintf("%d", id))
			}
		}
	}

	return "/anime?" + params.Encode()
}

// jikanFetchCatalog performs the catalog request and maps it into BrowsePage.
func jikanFetchCatalog(q BrowseQuery) (*BrowsePage, error) {
	var parsed jikanListResponse
	if err := jikanGet(jikanCatalogPath(q), &parsed); err != nil {
		return nil, err
	}

	out := &BrowsePage{
		Query:       q,
		Label:       labelFor(q),
		Page:        q.Page,
		HasNextPage: parsed.Pagination.HasNextPage,
		Items:       make([]BrowseItem, 0, len(parsed.Data)),
	}

	for _, a := range parsed.Data {
		cover := a.Images.JPG.LargeImageURL
		if cover == "" {
			cover = a.Images.JPG.ImageURL
		}

		season := strings.ToUpper(strings.TrimSpace(a.Season))
		date, label := formatRelease(
			a.Aired.Prop.From.Year, a.Aired.Prop.From.Month, a.Aired.Prop.From.Day,
			season, a.Year,
		)

		out.Items = append(out.Items, BrowseItem{
			// MyAnimeList ids are not AniList ids. The field keeps its name
			// for the frontend's sake, but it is only ever passed back to
			// SearchTitles, which searches by title rather than by id.
			AniListID: a.MalID,
			// Jikan's "title" is the romaji one, which is what the scrapers
			// index on — the same reason AniList's romaji came first.
			Title:        firstNonEmpty(a.Title, a.TitleEnglish, a.TitleJapanese),
			Romaji:       a.Title,
			English:      a.TitleEnglish,
			Cover:        cover,
			Format:       jikanFormat(a.Type),
			Status:       jikanStatus(a.Status),
			EpisodeCount: a.Episodes,
			Score:        jikanScore(a.Score),
			Genres:       jikanGenreNames(a.Genres),
			ReleaseDate:  date,
			ReleaseLabel: label,
			Season:       season,
			Year:         a.Year,
		})
	}

	return out, nil
}
