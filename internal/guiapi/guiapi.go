// Package guiapi exposes headless wrappers around the GoAnime internals
// for use by the Wails GUI. Everything here avoids terminal UI (spinners,
// fuzzyfinder, huh prompts) and returns plain JSON-friendly structs so the
// values can be bound directly to the frontend.
//
// The package sits on top of the Model B source registry
// (internal/api/source + internal/api/providers): search, episode listing
// and stream resolution all dispatch through the same registry the CLI
// uses, so a source added there shows up in the GUI with no edits here.
package guiapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alvarorichard/Goanime/internal/api"
	"github.com/alvarorichard/Goanime/internal/api/movie"
	"github.com/alvarorichard/Goanime/internal/api/providers"
	"github.com/alvarorichard/Goanime/internal/api/source"
	"github.com/alvarorichard/Goanime/internal/models"
	"github.com/alvarorichard/Goanime/internal/player"
	"github.com/alvarorichard/Goanime/internal/scraper/providers/superflix"
	"github.com/alvarorichard/Goanime/internal/util"
)

// Timeouts for the operations the GUI drives. They are deliberately more
// generous than the CLI's: a desktop user watching a spinner tolerates a
// slow source better than a terminal session does, and SuperFlix may have
// to clear a Cloudflare gate with a real browser.
const (
	searchTimeout   = 30 * time.Second
	episodesTimeout = 60 * time.Second
	streamTimeout   = 3 * time.Minute

	// tvmazeTimeout bounds the browser-free season listing.
	tvmazeTimeout = 20 * time.Second

	// superFlixBrowserTimeout must exceed the Cloudflare solve budget the
	// SuperFlix client allows itself, or the solve is cancelled mid-flight
	// and the user just sees "context deadline exceeded". It matches the
	// value api.fetchSuperFlixSeasons uses for the same call.
	superFlixBrowserTimeout = 210 * time.Second
)

// SourceInfo describes one selectable source for the frontend dropdown.
// The list is derived from the registry at runtime, so a source that is
// removed upstream or switched off via GOANIME_DISABLED_SOURCES simply
// stops appearing instead of becoming a dead menu entry.
type SourceInfo struct {
	// ID is the value the frontend sends back to Search.
	ID string `json:"id"`
	// Label is the human-readable name shown in the dropdown.
	Label string `json:"label"`
	// Language is a short tag such as "EN" or "PT-BR".
	Language string `json:"language"`
	// BrowserGated is true when playing from this source may open a real
	// browser window to clear a bot check, so the UI can warn up front.
	BrowserGated bool `json:"browserGated"`
	// Seasoned is true when the source organizes content into seasons.
	Seasoned bool `json:"seasoned"`
}

// SearchResult is a lightweight Anime row returned to the GUI.
type SearchResult struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	ImageURL  string `json:"imageURL"`
	Source    string `json:"source"`
	Year      string `json:"year"`
	MediaType string `json:"mediaType"` // "anime" | "movie" | "tv"
}

// EpisodeResult is a lightweight Episode row returned to the GUI.
type EpisodeResult struct {
	Number   string `json:"number"`
	Num      int    `json:"num"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	DataID   string `json:"dataID"`
	SeasonID string `json:"seasonID"`
	Aired    string `json:"aired"`
	IsFiller bool   `json:"isFiller"`
}

// EpisodeList is what GetEpisodes returns: the episodes of the selected
// season plus the full season list, so a seasoned source (SuperFlix) can
// render a season switcher instead of the CLI's blocking fuzzy picker.
// For a flat anime source Seasons is empty and Season is "".
type EpisodeList struct {
	Seasons  []string        `json:"seasons"`
	Season   string          `json:"season"`
	Episodes []EpisodeResult `json:"episodes"`
}

// ptbrKinds is the set of sources searched by the "ptbr" pseudo-source.
var ptbrKinds = []source.SourceKind{source.AnimeFire, source.Goyabu, source.SuperFlix}

// Sources returns the live source list for the frontend dropdown, led by
// the "all" and "ptbr" pseudo-entries.
func Sources() []SourceInfo {
	out := []SourceInfo{
		{ID: "all", Label: "Todas as fontes"},
		{ID: "ptbr", Label: "Somente PT-BR", Language: "PT-BR"},
	}
	for _, s := range source.ActiveSources() {
		kind := s.Describe().Kind
		out = append(out, SourceInfo{
			ID:           strings.ToLower(string(kind)),
			Label:        string(kind),
			Language:     languageOf(kind),
			BrowserGated: source.IsBrowserGated(s),
			Seasoned:     source.IsSeasoned(s),
		})
	}
	return out
}

// languageOf mirrors providers.languageTag, which is unexported.
func languageOf(kind source.SourceKind) string {
	if kind == source.HiAnime {
		return "EN"
	}
	return "PT-BR"
}

// kindsFor maps a frontend source ID to the registry kinds to query.
// A nil result means "every searchable source".
func kindsFor(id string) []source.SourceKind {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "", "all":
		return nil
	case "ptbr", "pt-br":
		return ptbrKinds
	}
	for _, s := range source.ActiveSources() {
		kind := s.Describe().Kind
		if strings.EqualFold(string(kind), id) {
			return []source.SourceKind{kind}
		}
	}
	// Unknown ID — search everything rather than returning nothing.
	return nil
}

// searchCancel holds the cancel func of the in-flight search so the UI can
// abandon a slow fan-out without waiting out the timeout.
var (
	searchMu     sync.Mutex
	searchCancel context.CancelFunc
)

// CancelSearch aborts the search currently in flight, if any. Calling it
// when nothing is running is a no-op.
func CancelSearch() {
	searchMu.Lock()
	defer searchMu.Unlock()
	if searchCancel != nil {
		searchCancel()
		searchCancel = nil
	}
}

// Search runs a headless fan-out against the selected source (or every
// searchable source when the ID is "all"/empty) and returns a flat list
// ready for the frontend. No terminal UI is involved.
func Search(query, sourceID string) ([]SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("digite algo para buscar")
	}

	ctx, done := beginSearch()
	defer done()

	results, err := searchWithContext(ctx, query, sourceID)
	if err != nil {
		return nil, err
	}
	return dropAdult(results), nil
}

// beginSearch supersedes any in-flight search and registers the new one as
// the cancellable one. The returned func must be called when the search
// finishes.
//
// It is separate from Search because SearchTitles runs several queries as a
// single logical search: if each one called Search directly they would
// cancel each other through the shared searchCancel.
func beginSearch() (context.Context, func()) {
	CancelSearch()

	ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
	searchMu.Lock()
	searchCancel = cancel
	searchMu.Unlock()

	return ctx, func() {
		cancel()
		searchMu.Lock()
		searchCancel = nil
		searchMu.Unlock()
	}
}

// searchWithContext is the fan-out itself, without any of the
// cancel-the-previous-search bookkeeping.
func searchWithContext(ctx context.Context, query, sourceID string) ([]SearchResult, error) {
	animes, err := providers.SearchAll(ctx, query, kindsFor(sourceID)...)
	if err != nil {
		// Nothing matching is not a failure. It is the everyday answer for a
		// title the scrapers do not carry — an AniList-only entry, a hentai
		// one, anything obscure — and the frontend has an empty state that
		// explains exactly that. Returning an error here buried it behind a
		// red "a busca falhou" instead.
		if errors.Is(err, providers.ErrNoResults) {
			return []SearchResult{}, nil
		}
		// Unwrapped: both callers in the frontend already prefix the message
		// with "A busca falhou:", and wrapping it again here is what produced
		// "A busca falhou: a busca falhou: …".
		return nil, err
	}

	results := make([]SearchResult, 0, len(animes))
	for _, a := range animes {
		if a == nil {
			continue
		}
		results = append(results, SearchResult{
			Name:      a.Name,
			URL:       a.URL,
			ImageURL:  a.ImageURL,
			Source:    a.Source,
			Year:      a.Year,
			MediaType: string(a.MediaType),
		})
	}
	return results, nil
}

// adultFilterConcurrency bounds the AniList lookups the filter needs. It
// matches the enrichment passes in the frontend for the same reason: a
// rate-limited API answers 429 to a burst, and these lookups are shared with
// the cards' artwork so most of them are cache hits anyway.
const adultFilterConcurrency = 4

// adultFilterTimeout bounds the classification pass.
//
// It is deliberately a budget of its own rather than whatever the search had
// left. Sharing the search's deadline meant a slow fan-out — AnimeFire
// retrying, say — left nothing for classification, and the filter then failed
// open precisely when it had the most work to do. That failure was silent:
// fail-open is indistinguishable from "nothing adult here". A var so tests
// can shrink it.
var adultFilterTimeout = 15 * time.Second

// dropAdult removes titles AniList flags as adult.
//
// The filter is best-effort by construction — see isAdultTitle — so it hides
// adult content rather than guaranteeing its absence. It is a browsing
// convenience, not a parental control, and the UI must not promise more than
// that.
//
// It runs at the entry points rather than inside searchWithContext so that
// SearchTitles classifies its merged, de-duplicated list once instead of once
// per title variant.
func dropAdult(results []SearchResult) []SearchResult {
	if len(results) == 0 {
		return results
	}

	ctx, cancel := context.WithTimeout(context.Background(), adultFilterTimeout)
	defer cancel()

	flags := make([]bool, len(results))
	var (
		wg sync.WaitGroup
		ch = make(chan int, len(results))
	)
	for i := range results {
		ch <- i
	}
	close(ch)

	workers := min(adultFilterConcurrency, len(results))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				// Out of budget: the remaining entries keep their zero value
				// and are shown, the same fail-open rule an unknown title
				// gets.
				if ctx.Err() != nil {
					return
				}
				flags[i] = isAdultTitle(results[i].Name)
			}
		}()
	}
	wg.Wait()

	kept := results[:0]
	for i, r := range results {
		if flags[i] {
			continue
		}
		kept = append(kept, r)
	}
	return kept
}

// animeFromResult reconstructs a minimal *models.Anime from a GUI
// SearchResult so the registry can resolve it back to its source.
func animeFromResult(r SearchResult) *models.Anime {
	a := &models.Anime{
		Name:     r.Name,
		URL:      r.URL,
		ImageURL: r.ImageURL,
		Source:   r.Source,
		Year:     r.Year,
	}
	switch strings.ToLower(r.MediaType) {
	case "movie":
		a.MediaType = models.MediaTypeMovie
	case "tv":
		a.MediaType = models.MediaTypeTV
	default:
		a.MediaType = models.MediaTypeAnime
	}
	return a
}

// isSuperFlix reports whether a result belongs to the seasoned SuperFlix
// source, which needs the headless season path below.
func isSuperFlix(r SearchResult) bool {
	return strings.EqualFold(r.Source, string(source.SuperFlix))
}

// GetEpisodes returns the episodes for a search result. For SuperFlix it
// returns the first season plus the full season list; for flat anime
// sources it returns every episode and no seasons.
func GetEpisodes(r SearchResult) (EpisodeList, error) {
	return GetSeasonEpisodes(r, "")
}

// GetSeasonEpisodes is GetEpisodes for an explicit season. An empty season
// means "the first one" and is what GetEpisodes passes. Non-seasoned
// sources ignore the argument.
func GetSeasonEpisodes(r SearchResult, season string) (EpisodeList, error) {
	anime := animeFromResult(r)

	// SuperFlix gets its own, far longer budget: it may have to drive a real
	// browser through a Cloudflare gate. It is handled before the shared
	// context is created so it is not capped by episodesTimeout.
	if isSuperFlix(r) {
		return superFlixEpisodes(anime, season)
	}

	ctx, cancel := context.WithTimeout(context.Background(), episodesTimeout)
	defer cancel()

	// Best-effort metadata enrichment (non-fatal): fills the AniList
	// details the episode titles come from.
	_ = api.FetchAnimeDetails(anime)

	eps, err := providers.FetchEpisodes(ctx, anime)
	if err != nil {
		return EpisodeList{}, fmt.Errorf("não foi possível carregar os episódios: %w", err)
	}
	return EpisodeList{Episodes: toEpisodeResults(eps)}, nil
}

// superFlixEpisodes lists SuperFlix content without the CLI's interactive
// season picker. api.GetSuperFlixEpisodes would block on a fuzzy finder
// that has no terminal to draw into, so the GUI talks to the client
// directly and hands the season list to the frontend instead.
func superFlixEpisodes(anime *models.Anime, season string) (EpisodeList, error) {
	tmdbID := anime.URL
	if tmdbID == "" {
		return EpisodeList{}, fmt.Errorf("este título do SuperFlix não tem identificador TMDB")
	}

	// A movie is a single playable item — no seasons involved.
	if anime.MediaType == models.MediaTypeMovie {
		return EpisodeList{
			Episodes: []EpisodeResult{{
				Number: "1",
				Num:    1,
				URL:    tmdbID,
				Title:  anime.Name,
			}},
		}, nil
	}

	all, err := fetchSuperFlixSeasons(anime, tmdbID)
	if err != nil {
		return EpisodeList{}, err
	}
	if len(all) == 0 {
		return EpisodeList{}, fmt.Errorf(
			"o SuperFlix não listou nenhuma temporada para este título — tente procurá-lo em outra fonte")
	}

	seasons := sortedSeasons(all)
	selected := season
	if selected == "" || all[selected] == nil {
		selected = seasons[0]
	}

	out := make([]EpisodeResult, 0, len(all[selected]))
	for _, ep := range all[selected] {
		num := 0
		if n, err := ep.EpiNum.Int64(); err == nil {
			num = int(n)
		}
		out = append(out, EpisodeResult{
			Number:   ep.EpiNum.String(),
			Num:      num,
			URL:      tmdbID,
			Title:    ep.Title,
			SeasonID: selected,
			Aired:    ep.AirDate,
		})
	}

	return EpisodeList{Seasons: seasons, Season: selected, Episodes: out}, nil
}

// fetchSuperFlixSeasons lists a series' seasons, preferring the browser-free
// TVmaze listing and only falling back to the headed browser.
//
// The ordering and the budgets mirror api.fetchSuperFlixSeasons, which is
// unexported. Getting them wrong is not subtle: the browser path has to
// clear a Cloudflare gate, and a context shorter than its solve budget
// cancels the solve mid-flight and surfaces as a bare
// "context deadline exceeded".
func fetchSuperFlixSeasons(anime *models.Anime, tmdbID string) (map[string][]superflix.SuperFlixEpisode, error) {
	// Fast path: TVmaze answers over plain HTTP in a second or two and needs
	// no browser at all. It is keyed by IMDB ID, which the scrapers do not
	// provide, so enrich first — best-effort, since that itself depends on
	// TMDB/OMDb being reachable.
	if anime.IMDBID == "" {
		if err := movie.EnrichMedia(anime); err != nil {
			util.Debug("SuperFlix: enrichment for the TVmaze fast path failed", "err", err)
		}
	}

	if anime.IMDBID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), tvmazeTimeout)
		eps, err := superflix.GetEpisodesFromTVmaze(ctx, http.DefaultClient, anime.IMDBID)
		cancel()
		if err == nil && len(eps) > 0 {
			util.Debug("SuperFlix: seasons via TVmaze", "imdb", anime.IMDBID, "seasons", len(eps))
			return eps, nil
		}
		util.Debug("SuperFlix: TVmaze had no listing; falling back to the browser",
			"imdb", anime.IMDBID, "err", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), superFlixBrowserTimeout)
	defer cancel()

	eps, err := superflix.SharedSuperFlixClient().GetEpisodes(ctx, tmdbID)
	if err != nil {
		return nil, describeSuperFlixFailure(err)
	}
	return eps, nil
}

// describeSuperFlixFailure turns a browser-path failure into something the
// user can act on. The raw errors are jargon ("context deadline exceeded",
// "failed to load serie page") and say nothing about what to do next.
// superflixHeadless reports whether SuperFlix has no display to work with.
//
// Indirected through a var so tests can drive both branches. The underlying
// check is hardcoded to "a display exists" on Windows and macOS, so the
// headless path was unreachable on two of the three platforms and the test
// covering it passed vacuously there -- which is exactly why the bug above
// survived until a Linux runner ran it.
var superflixHeadless = superflix.HeadlessEnvironment

func describeSuperFlixFailure(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf(
			"o SuperFlix demorou demais para responder. Isso costuma ser a verificação " +
				"de robô que não foi concluída — se uma janela de navegador abriu, deixe-a " +
				"terminar e tente de novo. Se insistir, procure o título em outra fonte")

	case errors.Is(err, superflix.ErrSuperFlixNoEpisodeList):
		return fmt.Errorf(
			"o SuperFlix abriu a página deste título, mas ela não trouxe a lista de " +
				"episódios — tente procurá-lo em outra fonte")

	// Last of the specific cases, and it wraps. Being headless explains why
	// SuperFlix failed, but it is not itself the failure: this branch tests
	// the machine, not err, so as an early case it replaced *every*
	// unrecognised cause with this sentence and the real one was gone.
	case superflixHeadless():
		return fmt.Errorf(
			"o SuperFlix precisa abrir uma janela de navegador para provar que você não "+
				"é um robô, mas nenhuma tela foi encontrada: %w", err)
	}

	return fmt.Errorf("não foi possível carregar as temporadas do SuperFlix: %w", err)
}

// sortedSeasons orders season keys numerically ("2" before "10"), falling
// back to string order for non-numeric keys such as "Especiais".
func sortedSeasons(all map[string][]superflix.SuperFlixEpisode) []string {
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ni, erri := strconv.Atoi(keys[i])
		nj, errj := strconv.Atoi(keys[j])
		switch {
		case erri == nil && errj == nil:
			return ni < nj
		case erri == nil:
			return true
		case errj == nil:
			return false
		default:
			return keys[i] < keys[j]
		}
	})
	return keys
}

// toEpisodeResults flattens models.Episode values into the frontend shape,
// picking the first non-empty title across the language variants.
func toEpisodeResults(eps []models.Episode) []EpisodeResult {
	out := make([]EpisodeResult, 0, len(eps))
	for _, e := range eps {
		title := e.Title.English
		if title == "" {
			title = e.Title.Romaji
		}
		if title == "" {
			title = e.Title.Japanese
		}
		out = append(out, EpisodeResult{
			Number:   e.Number,
			Num:      e.Num,
			URL:      e.URL,
			Title:    title,
			DataID:   e.DataID,
			SeasonID: e.SeasonID,
			Aired:    e.Aired,
			IsFiller: e.IsFiller,
		})
	}
	return out
}

// episodeFromResult rebuilds the model the stream resolver expects.
func episodeFromResult(ep EpisodeResult) *models.Episode {
	return &models.Episode{
		Number:   ep.Number,
		Num:      ep.Num,
		URL:      ep.URL,
		DataID:   ep.DataID,
		SeasonID: ep.SeasonID,
	}
}

// ResolveStreamURL turns an episode into a playable URL through the source
// registry. For a browser-gated source this is where the Cloudflare solve
// happens, which is why it carries the longest timeout of the three.
func ResolveStreamURL(r SearchResult, ep EpisodeResult) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), streamTimeout)
	defer cancel()

	anime := animeFromResult(r)
	if n, err := strconv.Atoi(ep.SeasonID); err == nil {
		anime.CurrentSeason = n
	}

	url, err := player.GetVideoURLForEpisodeEnhanced(ctx, episodeFromResult(ep), anime)
	if err != nil {
		return "", fmt.Errorf("não foi possível obter o link do vídeo: %w", err)
	}
	return url, nil
}

// Play launches mpv on the given stream URL and returns the mpv IPC socket
// path, the same handle the CLI uses for playback control.
func Play(streamURL string) (string, error) {
	if strings.TrimSpace(streamURL) == "" {
		return "", fmt.Errorf("link do vídeo vazio")
	}
	socket, err := player.StartVideo(streamURL, nil)
	if err != nil {
		return "", fmt.Errorf("não foi possível iniciar o mpv: %w", err)
	}
	return socket, nil
}

// SourceNeedsBrowser reports whether playing from the named source may open
// a real browser window to clear a bot check, so the UI can say so before
// the window appears rather than after.
func SourceNeedsBrowser(sourceName string) bool {
	for _, s := range source.ActiveSources() {
		if strings.EqualFold(string(s.Describe().Kind), sourceName) {
			return source.IsBrowserGated(s)
		}
	}
	return false
}

// currentReferer returns the referer the scraper recorded for the stream it
// just resolved. It is only meaningful immediately after ResolveStreamURL,
// and is how an external player gets past hotlink protection.
func currentReferer() string {
	return util.GetGlobalReferer()
}
