package guiapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// errTitleNotFound means the metadata backend answered "there is no such
// title" — AniList's 404, or an empty result set from Jikan. That is an
// answer, not a failure, and the one case a caller may cache negatively:
// everything else (timeouts, 5xx, a broken connection) could succeed on the
// next try and must not be remembered as "this title does not exist".
var errTitleNotFound = errors.New("nenhum catálogo encontrou esse título")

// The catalog comes from AniList rather than from the scrapers: none of the
// sources expose a "what aired in Spring 2024" endpoint, and AniList is the
// canonical anime database the rest of this package already queries.
//
// The consequence is worth stating plainly — a catalog entry is metadata,
// not a playable item. Clicking one runs a normal search for its title, so
// the user sees which sources actually carry it instead of being promised
// something that may not exist anywhere.

// browsePerPage is how many titles one catalog page holds. AniList caps
// perPage at 50; 40 fills a wide grid without a mostly-empty last row.
const browsePerPage = 40

// browseCache memoises catalog pages keyed by the full query. Listings
// barely change within a session, and AniList is rate-limited.
var browseCache sync.Map // map[string]*BrowsePage

// genreCache memoises AniList's genre list, which is fetched once. Hentai
// is stripped from it: every catalog query sends isAdult:false, so the entry
// could only ever produce empty pages.
var (
	genreOnce sync.Once
	genreList []GenreOption
)

// Season identifiers, matching AniList's MediaSeason enum.
const (
	SeasonWinter = "WINTER"
	SeasonSpring = "SPRING"
	SeasonSummer = "SUMMER"
	SeasonFall   = "FALL"
)

// Catalog modes. Each maps to an AniList sort (and, for upcoming, a status
// filter); "season" is the only one that pins a specific season.
const (
	ModeSeason   = "season"
	ModeTrending = "trending"
	ModePopular  = "popular"
	ModeTop      = "top"
	ModeUpcoming = "upcoming"
	ModeAiring   = "airing"
)

// seasonOrder is the calendar order used by the season picker.
var seasonOrder = []string{SeasonWinter, SeasonSpring, SeasonSummer, SeasonFall}

// BrowseQuery is everything the catalog can be narrowed by. The zero value
// is valid and means "the season airing now", so the frontend can call it
// with nothing filled in on first paint.
type BrowseQuery struct {
	// Mode selects the listing: season, trending, popular, top, upcoming,
	// airing. Anything unrecognised falls back to season.
	Mode string `json:"mode"`
	// Year filters by release year. 0 means any year; in season mode it
	// defaults to the current one.
	Year int `json:"year"`
	// Season is only used in season mode.
	Season string `json:"season"`
	// Genre is an AniList genre name ("Action"); empty means any.
	Genre string `json:"genre"`
	// Format is an AniList MediaFormat (TV, MOVIE, OVA…); empty means any.
	Format string `json:"format"`
	// Page is 1-based, matching AniList.
	Page int `json:"page"`
}

// BrowseItem is one title in the catalog.
type BrowseItem struct {
	AniListID    int      `json:"anilistID"`
	Title        string   `json:"title"`
	Romaji       string   `json:"romaji"`
	English      string   `json:"english"`
	Cover        string   `json:"cover"`
	Format       string   `json:"format"`
	Status       string   `json:"status"`
	EpisodeCount int      `json:"episodeCount"`
	Score        int      `json:"score"`
	Genres       []string `json:"genres"`
	ReleaseDate  string   `json:"releaseDate"`
	ReleaseLabel string   `json:"releaseLabel"`
	Season       string   `json:"season"`
	Year         int      `json:"year"`
}

// BrowsePage is one page of the catalog plus the context needed to render
// the heading and the pager.
//
// There is deliberately no total or last-page field: AniList caps
// pageInfo.total at 5000 for every query, so "1998 action anime" reports
// the same 5000 as "all anime". Showing that would be a made-up number.
// HasNextPage is accurate, and it is all the pager needs.
type BrowsePage struct {
	Query       BrowseQuery  `json:"query"`
	Label       string       `json:"label"`
	Page        int          `json:"page"`
	HasNextPage bool         `json:"hasNextPage"`
	Items       []BrowseItem `json:"items"`
}

// Option is a value/label pair for one of the catalog dropdowns.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// SeasonOption and GenreOption keep the older, more specific names the
// bindings already use.
type (
	SeasonOption = Option
	GenreOption  = Option
)

// ModeOptions returns the catalog listings, in the order they should be
// offered.
func ModeOptions() []Option {
	return []Option{
		{Value: ModeSeason, Label: "Por temporada"},
		{Value: ModeTrending, Label: "Em alta agora"},
		{Value: ModeAiring, Label: "Em exibição"},
		{Value: ModePopular, Label: "Mais populares"},
		{Value: ModeTop, Label: "Melhores notas"},
		{Value: ModeUpcoming, Label: "Ainda vão lançar"},
	}
}

// SeasonOptions returns the four seasons, labelled in Portuguese.
func SeasonOptions() []SeasonOption {
	out := make([]SeasonOption, 0, len(seasonOrder))
	for _, s := range seasonOrder {
		out = append(out, SeasonOption{Value: s, Label: titleCase(s)})
	}
	return out
}

// FormatOptions returns the media formats, led by an "any" entry.
func FormatOptions() []Option {
	return []Option{
		{Value: "", Label: "Qualquer tipo"},
		{Value: "TV", Label: "Série de TV"},
		{Value: "TV_SHORT", Label: "Curta de TV"},
		{Value: "MOVIE", Label: "Filme"},
		{Value: "OVA", Label: "OVA"},
		{Value: "ONA", Label: "ONA"},
		{Value: "SPECIAL", Label: "Especial"},
		{Value: "MUSIC", Label: "Videoclipe"},
	}
}

// genreLabels translates AniList's genre names. A genre missing from this
// map is shown as AniList spells it rather than dropped, so a genre added
// upstream still works — it just arrives untranslated.
var genreLabels = map[string]string{
	"Action":          "Ação",
	"Adventure":       "Aventura",
	"Comedy":          "Comédia",
	"Drama":           "Drama",
	"Ecchi":           "Ecchi",
	"Fantasy":         "Fantasia",
	"Horror":          "Terror",
	"Mahou Shoujo":    "Garota mágica",
	"Mecha":           "Mecha",
	"Music":           "Música",
	"Mystery":         "Mistério",
	"Psychological":   "Psicológico",
	"Romance":         "Romance",
	"Sci-Fi":          "Ficção científica",
	"Slice of Life":   "Slice of life",
	"Sports":          "Esportes",
	"Supernatural":    "Sobrenatural",
	"Thriller":        "Suspense",
	"Historical":      "Histórico",
	"Military":        "Militar",
	"School":          "Escolar",
	"Shounen":         "Shounen",
	"Shoujo":          "Shoujo",
	"Seinen":          "Seinen",
	"Josei":           "Josei",
	"Kids":            "Infantil",
	"Martial Arts":    "Artes marciais",
	"Police":          "Policial",
	"Space":           "Espacial",
	"Super Power":     "Superpoderes",
	"Vampire":         "Vampiros",
	"Demons":          "Demônios",
	"Magic":           "Magia",
	"Samurai":         "Samurai",
	"Parody":          "Paródia",
	"Game":            "Jogos",
	"Cars":            "Automobilismo",
	"Harem":           "Harém",
	"Dementia":        "Surreal",
	"Yaoi":            "Yaoi",
	"Yuri":            "Yuri",
	"Thriller/Horror": "Suspense/Terror",
}

// genreLabel translates a genre, falling back to AniList's own spelling.
func genreLabel(name string) string {
	if pt, ok := genreLabels[name]; ok {
		return pt
	}
	return name
}

// formatLabel renders a MediaFormat in Portuguese, falling back to the raw
// value so a format added by AniList still reads sensibly.
func formatLabel(format string) string {
	for _, o := range FormatOptions() {
		if o.Value == format {
			return o.Label
		}
	}
	return format
}

// GenreOptions returns AniList's genre list, translated where known and led
// by an "any" entry. The list is fetched once; on failure a small built-in
// set is used so the dropdown is never empty.
//
// Hentai is dropped from whatever AniList returns: every catalog query sends
// isAdult:false, so offering it as a filter would only ever produce empty
// pages.
func GenreOptions() []GenreOption {
	genreOnce.Do(func() {
		names, err := fetchGenres()
		if err != nil || len(names) == 0 {
			names = fallbackGenres()
		}
		genreList = make([]GenreOption, 0, len(names)+1)
		genreList = append(genreList, GenreOption{Value: "", Label: "Qualquer gênero"})

		for _, n := range names {
			if n == "Hentai" {
				continue
			}
			genreList = append(genreList, GenreOption{Value: n, Label: genreLabel(n)})
		}
	})

	return genreList
}

// fallbackGenres is the built-in list used when AniList cannot be reached.
func fallbackGenres() []string {
	return []string{
		"Action", "Adventure", "Comedy", "Drama", "Ecchi", "Fantasy",
		"Horror", "Mahou Shoujo", "Mecha", "Music", "Mystery",
		"Psychological", "Romance", "Sci-Fi", "Slice of Life", "Sports",
		"Supernatural", "Thriller",
	}
}

// CurrentSeason returns the season airing now, in AniList's terms.
//
// December is the subtlety: AniList counts it as the *next* year's winter
// (a show premiering in December 2024 is "Winter 2025"), so returning
// 2024/WINTER there would show the user a season that ended eleven months
// ago.
func CurrentSeason() (year int, season string) {
	now := time.Now()
	return seasonFor(now.Year(), int(now.Month()))
}

// seasonFor is CurrentSeason's logic, split out so it can be tested at the
// month boundaries without faking the clock.
func seasonFor(year, month int) (int, string) {
	switch month {
	case 12:
		return year + 1, SeasonWinter
	case 1, 2:
		return year, SeasonWinter
	case 3, 4, 5:
		return year, SeasonSpring
	case 6, 7, 8:
		return year, SeasonSummer
	default: // 9, 10, 11
		return year, SeasonFall
	}
}

// YearOptions returns the years offered by the picker, newest first. It
// starts one year ahead so announced-but-unaired seasons are reachable, and
// stops at 1960 — AniList has entries before that, but they are sparse
// enough that a longer dropdown costs more than it gives.
func YearOptions() []int {
	current, _ := CurrentSeason()
	out := make([]int, 0, current-1960+2)
	for y := current + 1; y >= 1960; y-- {
		out = append(out, y)
	}
	return out
}

// validSeason normalises and validates a season name, defaulting to the
// current one when the value is empty or unrecognised.
func validSeason(season string) string {
	up := strings.ToUpper(strings.TrimSpace(season))
	switch up {
	case SeasonWinter, SeasonSpring, SeasonSummer, SeasonFall:
		return up
	case "AUTUMN":
		return SeasonFall
	}
	_, current := CurrentSeason()
	return current
}

// validMode normalises a mode, defaulting to season.
func validMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ModeTrending:
		return ModeTrending
	case ModePopular:
		return ModePopular
	case ModeTop:
		return ModeTop
	case ModeUpcoming:
		return ModeUpcoming
	case ModeAiring:
		return ModeAiring
	default:
		return ModeSeason
	}
}

// normalise fills in the defaults a raw query from the frontend may omit.
func (q BrowseQuery) normalise() BrowseQuery {
	q.Mode = validMode(q.Mode)
	if q.Page < 1 {
		q.Page = 1
	}

	if q.Mode == ModeSeason {
		// Season mode needs both halves; anything missing means "now".
		curYear, curSeason := CurrentSeason()
		if q.Year <= 0 {
			q.Year = curYear
			if strings.TrimSpace(q.Season) == "" {
				q.Season = curSeason
			}
		}
		q.Season = validSeason(q.Season)
	} else {
		// Season is meaningless outside season mode, and a stale value
		// left in the frontend's dropdown would silently narrow results.
		q.Season = ""
		if q.Year < 0 {
			q.Year = 0
		}
	}

	q.Genre = strings.TrimSpace(q.Genre)
	q.Format = strings.ToUpper(strings.TrimSpace(q.Format))
	return q
}

// cacheKey identifies a query for the page cache.
func (q BrowseQuery) cacheKey() string {
	return fmt.Sprintf("%s|%d|%s|%s|%s|%d",
		q.Mode, q.Year, q.Season, q.Genre, q.Format, q.Page)
}

// sortFor maps a mode to AniList's sort enum.
func sortFor(mode string) []string {
	switch mode {
	case ModeTrending:
		return []string{"TRENDING_DESC", "POPULARITY_DESC"}
	case ModeTop:
		return []string{"SCORE_DESC"}
	case ModeUpcoming:
		return []string{"POPULARITY_DESC"}
	case ModeAiring:
		return []string{"TRENDING_DESC"}
	default: // season, popular
		return []string{"POPULARITY_DESC"}
	}
}

// statusFor returns the MediaStatus filter a mode implies, if any.
func statusFor(mode string) string {
	switch mode {
	case ModeUpcoming:
		return "NOT_YET_RELEASED"
	case ModeAiring:
		return "RELEASING"
	}
	return ""
}

// labelFor is the heading shown above the grid.
func labelFor(q BrowseQuery) string {
	var base string
	switch q.Mode {
	case ModeTrending:
		base = "Em alta agora"
	case ModeAiring:
		base = "Em exibição"
	case ModePopular:
		base = "Mais populares"
	case ModeTop:
		base = "Melhores notas"
	case ModeUpcoming:
		base = "Ainda vão lançar"
	default:
		return fmt.Sprintf("%s %d", titleCase(q.Season), q.Year)
	}

	var extra []string
	if q.Format != "" {
		extra = append(extra, formatLabel(q.Format))
	}
	if q.Year > 0 {
		extra = append(extra, fmt.Sprintf("%d", q.Year))
	}
	if q.Genre != "" {
		extra = append(extra, genreLabel(q.Genre))
	}
	if len(extra) > 0 {
		return base + " · " + strings.Join(extra, " · ")
	}
	return base
}

// Browse returns one page of the catalog for the given query. Invalid or
// missing fields are normalised rather than rejected, so the frontend can
// call it with a partly-filled query.
func Browse(q BrowseQuery) (BrowsePage, error) {
	q = q.normalise()

	key := q.cacheKey()
	if v, ok := browseCache.Load(key); ok {
		return *(v.(*BrowsePage)), nil
	}

	result, err := fetchCatalog(q)
	if err != nil {
		return BrowsePage{}, err
	}

	browseCache.Store(key, result)
	return *result, nil
}

// browseQuery asks AniList for one page. Every filter is a nullable
// variable: AniList treats a null argument as absent, so one static query
// covers every mode instead of a string built per request.
const browseQuery = `query (
	$page: Int, $perPage: Int,
	$season: MediaSeason, $seasonYear: Int,
	$sort: [MediaSort], $genre: String,
	$format: MediaFormat, $status: MediaStatus
) {
	Page(page: $page, perPage: $perPage) {
		pageInfo { hasNextPage }
		media(
			type: ANIME
			# Literal, not a variable: AniList's isAdult is three-valued —
			# false lists only non-adult titles, true only adult ones, and an
			# absent argument lists both. The catalog always wants the first.
			isAdult: false
			season: $season
			seasonYear: $seasonYear
			sort: $sort
			genre: $genre
			format: $format
			status: $status
		) {
			id
			title { romaji english native }
			format
			status
			episodes
			averageScore
			genres
			season
			seasonYear
			startDate { year month day }
			coverImage { large medium }
		}
	}
}`

// catalogBackend is one source the catalog can be built from. They are tried
// in order, and they all produce the same AniList-shaped BrowseItem, so the
// frontend cannot tell which one served a page.
type catalogBackend struct {
	name   string
	page   func(BrowseQuery) (*BrowsePage, error)
	genres func() ([]string, error)
}

// catalogBackends is the order the catalog is attempted in.
//
// There are three because two were not enough: AniList disabled its API
// ("temporarily disabled due to severe stability issues", HTTP 403 to
// everything) and Jikan has been unable to reach MyAnimeList (HTTP 504),
// which between them left the catalog with no working source at all. Kitsu is
// a separate database on separate infrastructure, so it does not share either
// failure.
//
// The order is by how close each one's data sits to what the app already
// speaks, not by who is up — availability is what the fallback is for.
var catalogBackends = []catalogBackend{
	{name: "Jikan", page: jikanFetchCatalog, genres: jikanFetchGenres},
	{name: "Kitsu", page: kitsuFetchCatalog, genres: kitsuFetchGenres},
	{name: "AniList", page: fetchCatalogFromAniList, genres: anilistFetchGenres},
}

// genreBackends is the order the genre dropdown is filled from, and it is
// deliberately not the order above.
//
// The dropdown must only offer genres the backend that serves the catalog can
// actually filter by, and the three vocabularies barely overlap — 44 of
// Jikan's 78 genre names have no Kitsu equivalent. Jikan's /genres/anime is
// served from its cache and answers even while its catalog cannot, so taking
// the list from there would fill the dropdown with names the page that
// follows has no way to honour.
//
// Kitsu leads because it is the backend most likely to answer a page. When a
// name does not map, the backend declines the query and the chain moves on
// rather than quietly listing everything.
var genreBackends = []catalogBackend{
	{name: "Kitsu", genres: kitsuFetchGenres},
	{name: "Jikan", genres: jikanFetchGenres},
	{name: "AniList", genres: anilistFetchGenres},
}

// fetchCatalog returns one catalog page from the first backend that answers.
//
// When none does, the error names every one of them and why: an outage the
// user can do nothing about is still worth stating precisely, and a single
// generic message made three different failures look like one.
func fetchCatalog(q BrowseQuery) (*BrowsePage, error) {
	reasons := make([]string, 0, len(catalogBackends))

	for _, backend := range catalogBackends {
		page, err := backend.page(q)
		if err == nil {
			return page, nil
		}
		reasons = append(reasons, fmt.Sprintf("%s: %v", backend.name, err))
	}

	return nil, fmt.Errorf("nenhum catálogo disponível — %s", strings.Join(reasons, "; "))
}

// fetchCatalogFromAniList is the original AniList-backed catalog request.
func fetchCatalogFromAniList(q BrowseQuery) (*BrowsePage, error) {
	vars := map[string]any{
		"page":    q.Page,
		"perPage": browsePerPage,
		"sort":    sortFor(q.Mode),
	}
	// Only set the filters that apply: a nil entry is the same as absent,
	// but an empty string would filter on "".
	if q.Season != "" {
		vars["season"] = q.Season
	}
	if q.Year > 0 {
		vars["seasonYear"] = q.Year
	}
	if q.Genre != "" {
		vars["genre"] = q.Genre
	}
	if q.Format != "" {
		vars["format"] = q.Format
	}
	if s := statusFor(q.Mode); s != "" {
		vars["status"] = s
	}

	var parsed struct {
		Page struct {
			PageInfo struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pageInfo"`
			Media []struct {
				ID    int `json:"id"`
				Title struct {
					Romaji  string `json:"romaji"`
					English string `json:"english"`
					Native  string `json:"native"`
				} `json:"title"`
				Format       string   `json:"format"`
				Status       string   `json:"status"`
				Episodes     int      `json:"episodes"`
				AverageScore int      `json:"averageScore"`
				Genres       []string `json:"genres"`
				Season       string   `json:"season"`
				SeasonYear   int      `json:"seasonYear"`
				StartDate    struct {
					Year  int `json:"year"`
					Month int `json:"month"`
					Day   int `json:"day"`
				} `json:"startDate"`
				CoverImage struct {
					Large  string `json:"large"`
					Medium string `json:"medium"`
				} `json:"coverImage"`
			} `json:"media"`
		} `json:"Page"`
	}

	if err := anilistPostDirect(browseQuery, vars, &parsed); err != nil {
		return nil, err
	}

	out := &BrowsePage{
		Query:       q,
		Label:       labelFor(q),
		Page:        q.Page,
		HasNextPage: parsed.Page.PageInfo.HasNextPage,
		Items:       make([]BrowseItem, 0, len(parsed.Page.Media)),
	}

	for _, m := range parsed.Page.Media {
		cover := m.CoverImage.Large
		if cover == "" {
			cover = m.CoverImage.Medium
		}

		date, label := formatRelease(
			m.StartDate.Year, m.StartDate.Month, m.StartDate.Day,
			m.Season, m.SeasonYear,
		)

		out.Items = append(out.Items, BrowseItem{
			AniListID: m.ID,
			// Romaji first: it is what the scrapers index on, so it gives
			// the best hit rate when the user clicks through to a search.
			Title:        firstNonEmpty(m.Title.Romaji, m.Title.English, m.Title.Native),
			Romaji:       m.Title.Romaji,
			English:      m.Title.English,
			Cover:        cover,
			Format:       m.Format,
			Status:       m.Status,
			EpisodeCount: m.Episodes,
			Score:        m.AverageScore,
			Genres:       m.Genres,
			ReleaseDate:  date,
			ReleaseLabel: label,
			Season:       m.Season,
			Year:         m.SeasonYear,
		})
	}

	return out, nil
}

// fetchGenres pulls the genre list from the same backends in the same order,
// so the filter offers whatever the source that will answer actually has.
func fetchGenres() ([]string, error) {
	var lastErr error
	for _, backend := range genreBackends {
		names, err := backend.genres()
		if err == nil && len(names) > 0 {
			return names, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errBackendUnavailable
}

// anilistFetchGenres reads AniList's genre collection.
func anilistFetchGenres() ([]string, error) {
	var parsed struct {
		GenreCollection []string `json:"GenreCollection"`
	}
	if err := anilistPostDirect(`query { GenreCollection }`, nil, &parsed); err != nil {
		return nil, err
	}
	return parsed.GenreCollection, nil
}

// AniList allows roughly 90 requests a minute and answers 429 once that is
// exceeded. The GUI can burst well past it on a cold start — the calendar
// alone walks several pages while the catalog and the genre list load — so
// two things keep it under the limit:
//
//   - every request passes through one gate that spaces them out, so
//     concurrent callers queue instead of all firing at once;
//   - a 429 is retried after the delay AniList itself asks for, rather than
//     handed to the user as "tente de novo em alguns segundos".
//
// The user only sees an error if the retries are also refused.
const (
	anilistMinInterval = 700 * time.Millisecond
	anilistMaxRetries  = 2
	// anilistMaxBackoff caps how long one retry waits. AniList sometimes
	// asks for a minute; waiting that long behind a spinner is worse than
	// failing and letting the user retry.
	anilistMaxBackoff = 5 * time.Second
)

var (
	anilistGate sync.Mutex
	anilistLast time.Time
)

// anilistWait blocks until anilistMinInterval has passed since the previous
// request. It holds the gate across the sleep on purpose: that is what turns
// a burst of parallel callers into a queue.
func anilistWait() {
	anilistGate.Lock()
	defer anilistGate.Unlock()

	if gap := time.Since(anilistLast); gap < anilistMinInterval {
		time.Sleep(anilistMinInterval - gap)
	}
	anilistLast = time.Now()
}

// anilistPost runs a GraphQL request and decodes the "data" object into out.
// It is the single place this package talks to AniList, so the timeout,
// headers, pacing and rate-limit handling all live in one spot.
// anilistBreaker keeps a disabled AniList from costing a request — and its
// 700ms pacing slot — on every lookup. See breaker.go.
var anilistBreaker = &apiBreaker{name: "anilist"}

// anilistPost is the breaker-guarded entry point, for the metadata fan-out.
func anilistPost(query string, variables map[string]any, out any) error {
	if !anilistBreaker.allow() {
		return anilistBreaker.lastFailure()
	}
	return anilistSend(query, variables, out)
}

// anilistPostDirect ignores an open breaker. The catalog is one request the
// user asked for, not one of thirty background lookups, so it always attempts
// and reports the real reason when it fails.
func anilistPostDirect(query string, variables map[string]any, out any) error {
	return anilistSend(query, variables, out)
}

func anilistSend(query string, variables map[string]any, out any) error {
	payload := map[string]any{"query": query}
	if variables != nil {
		payload["variables"] = variables
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("não foi possível montar a consulta: %w", err)
	}

	for attempt := 0; ; attempt++ {
		anilistWait()

		retryAfter, err := anilistTry(body, out)
		if err == nil {
			anilistBreaker.success()
			return nil
		}
		// A missing title is an answer, not an outage: AniList is up and
		// talking to us, so it must not count against the breaker.
		if errors.Is(err, errTitleNotFound) {
			anilistBreaker.success()
			return err
		}
		// retryAfter is only set for a 429; anything else is final.
		if retryAfter <= 0 || attempt >= anilistMaxRetries {
			anilistBreaker.failure(err)
			return err
		}
		time.Sleep(retryAfter)
	}
}

// anilistTry performs one attempt. A non-zero first result means the call
// was rate-limited and is worth retrying after that delay.
func anilistTry(body []byte, out any) (time.Duration, error) {
	req, err := http.NewRequest(http.MethodPost, "https://graphql.anilist.co", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("consulta inválida: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// AniList 403s browser-like User-Agents; keep this a plain tool string.
	req.Header.Set("User-Agent", "GoAnime-GUI/1.0")

	client := &http.Client{Timeout: anilistTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("não foi possível falar com o AniList: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		_, _ = io.Copy(io.Discard, resp.Body)
		return retryAfterDelay(resp.Header.Get("Retry-After")),
			fmt.Errorf("o AniList pediu para esperar um pouco; tente de novo em alguns segundos")
	}
	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		return 0, errTitleNotFound
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return 0, fmt.Errorf("o AniList respondeu HTTP %d", resp.StatusCode)
	}

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return 0, fmt.Errorf("não foi possível ler a resposta do AniList: %w", err)
	}
	if len(envelope.Data) == 0 {
		return 0, fmt.Errorf("o AniList não retornou dados")
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return 0, fmt.Errorf("resposta do AniList em formato inesperado: %w", err)
	}
	return 0, nil
}

// retryAfterDelay reads AniList's Retry-After header, which is in seconds.
// A missing or unusable value still yields a delay: the 429 is real either
// way, and retrying immediately would just earn another one.
func retryAfterDelay(header string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || secs <= 0 {
		return time.Second
	}
	d := time.Duration(secs) * time.Second
	if d > anilistMaxBackoff {
		return anilistMaxBackoff
	}
	return d
}

// SearchTitles searches every source for a catalog entry, trying each of
// the title's variants and merging the results.
//
// One query is not enough. The catalog speaks AniList's romaji ("Kimetsu no
// Yaiba"), the English sources index that, but the PT-BR sources very often
// list the localised or English name instead ("Demon Slayer") — so a
// romaji-only search comes back with English sources only, which is exactly
// what a user browsing a season sees. Searching the variants in parallel and
// merging gets both.
func SearchTitles(item BrowseItem, sourceID string) ([]SearchResult, error) {
	queries := titleVariants(item)
	if len(queries) == 0 {
		return nil, fmt.Errorf("este título não tem nome para buscar")
	}

	type outcome struct {
		results []SearchResult
		err     error
	}

	// One context for all the variants: they are a single logical search, so
	// cancelling from the UI must stop all of them, and they must not
	// supersede one another.
	ctx, done := beginSearch()
	defer done()

	var wg sync.WaitGroup
	results := make([]outcome, len(queries))

	for i, q := range queries {
		wg.Add(1)
		go func(i int, q string) {
			defer wg.Done()
			r, err := searchWithContext(ctx, q, sourceID)
			results[i] = outcome{results: r, err: err}
		}(i, q)
	}
	wg.Wait()

	// Merge in variant order (romaji first), de-duplicating by the same key
	// the library uses, so the same title from the same source appears once.
	var (
		merged []SearchResult
		seen   = map[string]bool{}
		lastEr error
	)
	for _, o := range results {
		if o.err != nil {
			lastEr = o.err
			continue
		}
		for _, r := range o.results {
			key := titleKey(r)
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, r)
		}
	}

	if len(merged) == 0 && lastEr != nil {
		return nil, lastEr
	}
	// Classify once, on the merged list: the variants overlap heavily, so
	// filtering per variant would repeat the same work three times over.
	return dropAdult(merged), nil
}

// titleVariants returns the distinct names worth searching for, romaji
// first because that is what most scrapers index on. Comparison is
// case-insensitive so "Bleach"/"BLEACH" is not searched twice.
func titleVariants(item BrowseItem) []string {
	var out []string
	seen := map[string]bool{}

	for _, candidate := range []string{item.Romaji, item.English, item.Title} {
		c := strings.TrimSpace(candidate)
		if c == "" {
			continue
		}
		key := strings.ToLower(c)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

// firstNonEmpty returns the first argument that is not blank.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
