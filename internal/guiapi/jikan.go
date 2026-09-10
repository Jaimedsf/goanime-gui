package guiapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
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

// jikanBreaker stops the metadata fan-out from re-testing a dead Jikan on
// every single lookup. See breaker.go.
var jikanBreaker = &apiBreaker{name: "jikan"}

// jikanGet performs one breaker-guarded GET. Use it for the metadata path,
// where one user action produces a lookup per result.
func jikanGet(path string, out any) error {
	if !jikanBreaker.allow() {
		return jikanBreaker.lastFailure()
	}
	return jikanFetch(path, out)
}

// jikanGetDirect performs a GET that ignores an open breaker.
//
// The breaker is there to stop thirty background lookups from each paying to
// rediscover an outage. A catalog page is not that: it is one request the
// user explicitly asked for, so refusing it to save a single call is only a
// way to show an error faster — and a vaguer one. It still reports its
// outcome, so a success here closes the breaker for everyone.
func jikanGetDirect(path string, out any) error {
	return jikanFetch(path, out)
}

// jikanFetch is the request itself, shared by both entry points.
func jikanFetch(path string, out any) error {
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
		wrapped := fmt.Errorf("jikan: %w", err)
		jikanBreaker.failure(wrapped)
		return wrapped
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// A 504 here is Jikan failing to reach MyAnimeList, not a fault of
		// ours; say which side is down so the fallback's reason is legible.
		failure := fmt.Errorf("o Jikan respondeu HTTP %d", resp.StatusCode)
		if resp.StatusCode == http.StatusGatewayTimeout || resp.StatusCode == http.StatusBadGateway {
			failure = fmt.Errorf("o Jikan não conseguiu falar com o MyAnimeList (HTTP %d)", resp.StatusCode)
		}
		jikanBreaker.failure(failure)
		return failure
	}

	// The API answered. An empty result set is still an answer, so the
	// breaker closes here rather than after the caller inspects the body.
	jikanBreaker.success()

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
	// Rating and ExplicitGenres are how MyAnimeList marks adult titles;
	// AniList had a single isAdult boolean.
	Rating         string       `json:"rating"`
	ExplicitGenres []jikanNamed `json:"explicit_genres"`
	Aired          struct {
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
		// Direct: this backs a genre the user picked in the catalog, and an
		// open breaker would silently drop the filter and list everything.
		if err := jikanGetDirect("/genres/anime", &parsed); err != nil {
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
	if err := jikanGetDirect("/genres/anime", &parsed); err != nil {
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
func jikanCatalogPath(q BrowseQuery) (string, error) {
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
			q.Year, jikanSeasonName(q.Season), params.Encode()), nil
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
		// Decline rather than drop the filter: listing everything under a
		// heading that names a genre is worse than saying it cannot be
		// applied here. The next backend in the chain may know it.
		index, err := jikanGenreIndex()
		if err != nil {
			return "", err
		}
		id, ok := index[strings.ToLower(q.Genre)]
		if !ok {
			return "", fmt.Errorf("o Jikan não conhece o gênero %q", q.Genre)
		}
		params.Set("genres", fmt.Sprintf("%d", id))
	}

	return "/anime?" + params.Encode(), nil
}

// jikanFetchCatalog performs the catalog request and maps it into BrowsePage.
func jikanFetchCatalog(q BrowseQuery) (*BrowsePage, error) {
	path, err := jikanCatalogPath(q)
	if err != nil {
		return nil, err
	}

	var parsed jikanListResponse
	if err := jikanGetDirect(path, &parsed); err != nil {
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

// --- title metadata --------------------------------------------------------

// jikanRating carries MyAnimeList's content rating. "Rx - Hentai" is the
// only value that marks a title adult; everything else, including "R+ -
// Mild Nudity", stays visible, matching how AniList's isAdult behaved.
func jikanIsAdult(a jikanAnime) bool {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Rating)), "rx") {
		return true
	}
	if len(a.ExplicitGenres) > 0 {
		return true
	}
	for _, g := range a.Genres {
		switch strings.ToLower(g.Name) {
		case "hentai", "erotica":
			return true
		}
	}
	return false
}

// jikanSearchOne returns the best match for a title.
//
// sfw is deliberately NOT set: the adult flag rides this lookup, and a
// filtered search would answer "no such title" for an adult one, which the
// caller would cache as a permanent miss.
func jikanSearchOne(title string) (*jikanAnime, error) {
	params := url.Values{}
	params.Set("q", title)
	params.Set("limit", "1")
	// order_by/sort left unset: Jikan's default for a text query is its own
	// relevance ranking, which matches titles better than any explicit sort.

	var parsed jikanListResponse
	if err := jikanGet("/anime?"+params.Encode(), &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) == 0 {
		return nil, errTitleNotFound
	}
	return &parsed.Data[0], nil
}

// jikanEpisodeThumbs fetches per-episode stills, the closest equivalent of
// AniList's streamingEpisodes.
//
// It is a second request, so it is only made when thumbnails are actually
// wanted — the episode grid — and never for a cover or a release date. A
// failure is not an error worth propagating: the grid falls back to the
// poster.
func jikanEpisodeThumbs(malID int) map[string]string {
	if malID <= 0 {
		return map[string]string{}
	}

	var parsed struct {
		Data struct {
			Episodes []struct {
				MalID   int    `json:"mal_id"`
				Title   string `json:"title"`
				Episode string `json:"episode"`
				Images  struct {
					JPG struct {
						ImageURL string `json:"image_url"`
					} `json:"jpg"`
				} `json:"images"`
			} `json:"episodes"`
		} `json:"data"`
	}

	if err := jikanGet(fmt.Sprintf("/anime/%d/videos", malID), &parsed); err != nil {
		return map[string]string{}
	}

	thumbs := make(map[string]string, len(parsed.Data.Episodes))
	for i, ep := range parsed.Data.Episodes {
		if ep.Images.JPG.ImageURL == "" {
			continue
		}
		// Jikan spells the number in "episode" ("Episode 1"); fall back to
		// the title, then to a 1-based index, so something still shows up.
		num := extractEpisodeNumber(ep.Episode)
		if num == "" {
			num = extractEpisodeNumber(ep.Title)
		}
		if num == "" {
			num = fmt.Sprintf("%d", i+1)
		}
		thumbs[num] = ep.Images.JPG.ImageURL
	}
	return thumbs
}

// fetchJikanMedia is the Jikan half of the title lookup, shaped exactly like
// fetchAniListMedia so the cache above it cannot tell them apart.
func fetchJikanMedia(title string, wantThumbs bool) (*titleMedia, error) {
	a, err := jikanSearchOne(title)
	if err != nil {
		return nil, err
	}

	cover := a.Images.JPG.LargeImageURL
	if cover == "" {
		cover = a.Images.JPG.ImageURL
	}

	season := strings.ToUpper(strings.TrimSpace(a.Season))
	date, label := formatRelease(
		a.Aired.Prop.From.Year, a.Aired.Prop.From.Month, a.Aired.Prop.From.Day,
		season, a.Year,
	)

	year := ""
	switch {
	case a.Aired.Prop.From.Year > 0:
		year = fmt.Sprintf("%d", a.Aired.Prop.From.Year)
	case a.Year > 0:
		year = fmt.Sprintf("%d", a.Year)
	}

	out := &titleMedia{
		cover:  cover,
		adult:  jikanIsAdult(*a),
		thumbs: map[string]string{},
		malID:  a.MalID,
		info: TitleInfo{
			ReleaseDate:  date,
			ReleaseLabel: label,
			Year:         year,
			Format:       jikanFormat(a.Type),
			Status:       jikanStatus(a.Status),
			EpisodeCount: a.Episodes,
			Score:        jikanScore(a.Score),
		},
	}

	if wantThumbs {
		out.thumbs = jikanEpisodeThumbs(a.MalID)
		out.thumbsFetched = true
	}
	return out, nil
}

// --- airing calendar -------------------------------------------------------

// The calendar is the one place Jikan is a genuine downgrade, so it is the
// fallback here rather than the primary.
//
// AniList answers with airingSchedules: one row per broadcast, carrying the
// exact instant and, crucially, which episode airs. Jikan has no such
// endpoint — /schedules lists the anime that air on a given weekday, with a
// recurring broadcast time and no episode number at all. So a Jikan-backed
// week says what airs and when, but not which episode; the calendar omits
// the number rather than computing one, because an episode count guessed
// from a start date goes wrong the first time a series takes a break, and a
// confidently wrong number is worse than none.

// jikanWeekdayFilter is the value /schedules expects for a weekday.
var jikanWeekdayFilter = [...]string{
	time.Sunday:    "sunday",
	time.Monday:    "monday",
	time.Tuesday:   "tuesday",
	time.Wednesday: "wednesday",
	time.Thursday:  "thursday",
	time.Friday:    "friday",
	time.Saturday:  "saturday",
}

// jikanBroadcast is when a series airs each week, in its own timezone.
type jikanBroadcast struct {
	Day      string `json:"day"`
	Time     string `json:"time"`
	Timezone string `json:"timezone"`
}

// tokyo is the fallback zone for a broadcast. Asia/Tokyo is UTC+9 with no
// daylight saving, so a fixed offset is exact — which matters because
// LoadLocation needs a tzdata database that a Windows build may not have.
var tokyo = time.FixedZone("JST", 9*60*60)

// broadcastAt resolves a recurring broadcast onto a specific date, returning
// the instant it airs. ok is false when the broadcast time is unusable.
func broadcastAt(day time.Time, b jikanBroadcast) (time.Time, bool) {
	hhmm := strings.TrimSpace(b.Time)
	if len(hhmm) < 4 || !strings.Contains(hhmm, ":") {
		return time.Time{}, false
	}

	parsed, err := time.Parse("15:04", hhmm)
	if err != nil {
		return time.Time{}, false
	}

	loc := tokyo
	if b.Timezone != "" {
		if l, err := time.LoadLocation(b.Timezone); err == nil {
			loc = l
		}
	}

	at := time.Date(day.Year(), day.Month(), day.Day(),
		parsed.Hour(), parsed.Minute(), 0, 0, loc)
	return at.Local(), true
}

// jikanScheduleAnime is one entry of /schedules.
type jikanScheduleAnime struct {
	jikanAnime
	Broadcast     jikanBroadcast `json:"broadcast"`
	TitleSynonyms []string       `json:"title_synonyms"`
}

type jikanScheduleResponse struct {
	Pagination jikanPagination      `json:"pagination"`
	Data       []jikanScheduleAnime `json:"data"`
}

// jikanFetchSchedule builds the week by asking for each weekday in the
// window. The second result is true when some day could not be fetched, so
// the caller can show what it has and mark the week partial.
func jikanFetchSchedule(start, end time.Time) ([]ScheduleEntry, bool, error) {
	var (
		out      []ScheduleEntry
		partial  bool
		lastErr  error
		anyDayOK bool
	)

	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		entries, err := jikanScheduleForDay(day)
		if err != nil {
			lastErr = err
			partial = true
			continue
		}
		anyDayOK = true
		out = append(out, entries...)
	}

	if !anyDayOK {
		if lastErr == nil {
			lastErr = errBackendUnavailable
		}
		return nil, false, lastErr
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].AiringAt < out[j].AiringAt
	})
	return out, partial, nil
}

// jikanScheduleForDay fetches everything airing on one date.
func jikanScheduleForDay(day time.Time) ([]ScheduleEntry, error) {
	params := url.Values{}
	params.Set("filter", jikanWeekdayFilter[day.Weekday()])
	params.Set("limit", fmt.Sprintf("%d", jikanPerPage))
	params.Set("sfw", "true")

	var parsed jikanScheduleResponse
	// Direct: the calendar is a user-initiated load, like the catalog.
	if err := jikanGetDirect("/schedules?"+params.Encode(), &parsed); err != nil {
		return nil, err
	}

	out := make([]ScheduleEntry, 0, len(parsed.Data))
	for _, a := range parsed.Data {
		at, ok := broadcastAt(day, a.Broadcast)
		if !ok {
			continue
		}

		cover := a.Images.JPG.LargeImageURL
		if cover == "" {
			cover = a.Images.JPG.ImageURL
		}

		out = append(out, ScheduleEntry{
			// A MyAnimeList id, not an AniList one. The field keeps its name
			// for the frontend; nothing joins on it, a click runs a search.
			AniListID: a.MalID,
			Title:     firstNonEmpty(a.Title, a.TitleEnglish, a.TitleJapanese),
			Romaji:    a.Title,
			English:   a.TitleEnglish,
			Cover:     cover,
			// Deliberately left unset: see the note above.
			Episode:  0,
			AiringAt: at.Unix(),
			Time:     at.Format("15:04"),
			Format:   jikanFormat(a.Type),
			Status:   jikanStatus(a.Status),
			adult:    jikanIsAdult(a.jikanAnime),
			matchKeys: matchKeysFor(append([]string{
				a.Title, a.TitleEnglish, a.TitleJapanese,
			}, a.TitleSynonyms...)),
		})
	}
	return out, nil
}

// resetJikanGenreIndex clears the memoised genre index. Tests use it so one
// test's stub server does not fix the index for the next.
func resetJikanGenreIndex() {
	jikanGenreOnce = sync.Once{}
	jikanGenreIDs = nil
	jikanGenreErr = nil
}
