package guiapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kitsu is the third catalog source, and the reason it exists here is
// availability rather than preference: AniList disabled its API outright and
// Jikan has been unable to reach MyAnimeList, which left the catalog with no
// working backend at all. Kitsu is a separate database with its own
// infrastructure, so it does not share either failure.
//
// It speaks JSON:API — data is a list of {id, attributes, relationships},
// genres live in a separate "categories" resource pulled in with include,
// and paging is by offset rather than page number. Everything below
// normalises that into the same AniList-shaped BrowseItem the other two
// backends produce, so the frontend cannot tell which one served a page.
const (
	kitsuBase = "https://kitsu.app/api/edge"

	// kitsuPerPage is the page size. Kitsu caps page[limit] at 20.
	kitsuPerPage = 20

	kitsuMinInterval = 300 * time.Millisecond
	kitsuTimeout     = 15 * time.Second
)

var (
	kitsuGate sync.Mutex
	kitsuLast time.Time

	// kitsuBaseURL is a variable so tests can point it at a local server.
	kitsuBaseURL = kitsuBase

	// kitsuBreaker guards the metadata path only, like the others.
	kitsuBreaker = &apiBreaker{name: "kitsu"}
)

// kitsuWait spaces requests out, holding the gate across the sleep so
// concurrent callers queue instead of bursting.
func kitsuWait() {
	kitsuGate.Lock()
	defer kitsuGate.Unlock()

	if gap := time.Since(kitsuLast); gap < kitsuMinInterval {
		time.Sleep(kitsuMinInterval - gap)
	}
	kitsuLast = time.Now()
}

// kitsuGetDirect performs a GET that ignores an open breaker, for the
// user-initiated catalog. See jikanGetDirect for why that distinction exists.
func kitsuGetDirect(path string, out any) error {
	return kitsuFetch(path, out)
}

// kitsuGet performs a breaker-guarded GET, for the metadata fan-out.
func kitsuGet(path string, out any) error {
	if !kitsuBreaker.allow() {
		return kitsuBreaker.lastFailure()
	}
	return kitsuFetch(path, out)
}

func kitsuFetch(path string, out any) error {
	kitsuWait()

	req, err := http.NewRequest(http.MethodGet, kitsuBaseURL+path, http.NoBody)
	if err != nil {
		return fmt.Errorf("kitsu: %w", err)
	}
	// JSON:API requires its own media type; Kitsu answers 406 without it.
	req.Header.Set("Accept", "application/vnd.api+json")
	req.Header.Set("User-Agent", "GoAnime-GUI/1.0")

	client := &http.Client{Timeout: kitsuTimeout}
	resp, err := client.Do(req)
	if err != nil {
		wrapped := fmt.Errorf("kitsu: %w", err)
		kitsuBreaker.failure(wrapped)
		return wrapped
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		failure := fmt.Errorf("o Kitsu respondeu HTTP %d", resp.StatusCode)
		kitsuBreaker.failure(failure)
		return failure
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		wrapped := fmt.Errorf("kitsu: %w", err)
		kitsuBreaker.failure(wrapped)
		return wrapped
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("kitsu: resposta ilegível: %w", err)
	}

	kitsuBreaker.success()
	return nil
}

// --- payloads --------------------------------------------------------------

// kitsuAnimeAttributes is the subset of an anime's attributes the catalog uses.
type kitsuAnimeAttributes struct {
	Slug           string            `json:"slug"`
	CanonicalTitle string            `json:"canonicalTitle"`
	Titles         map[string]string `json:"titles"`
	// AverageRating arrives as a string on a 0-100 scale ("84.44"), which is
	// already the scale BrowseItem.Score is rendered in.
	AverageRating string `json:"averageRating"`
	StartDate     string `json:"startDate"`
	Subtype       string `json:"subtype"`
	Status        string `json:"status"`
	EpisodeCount  int    `json:"episodeCount"`
	NSFW          bool   `json:"nsfw"`
	AgeRating     string `json:"ageRating"`
	PosterImage   struct {
		Original string `json:"original"`
		Large    string `json:"large"`
		Medium   string `json:"medium"`
		Small    string `json:"small"`
	} `json:"posterImage"`
}

// kitsuResourceID identifies one related resource.
type kitsuResourceID struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type kitsuAnime struct {
	ID            string               `json:"id"`
	Attributes    kitsuAnimeAttributes `json:"attributes"`
	Relationships struct {
		Categories struct {
			Data []kitsuResourceID `json:"data"`
		} `json:"categories"`
	} `json:"relationships"`
}

// kitsuIncluded is one entry of the sideloaded resources — categories, here.
type kitsuIncluded struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Attributes struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	} `json:"attributes"`
}

type kitsuListResponse struct {
	Data     []kitsuAnime    `json:"data"`
	Included []kitsuIncluded `json:"included"`
	Meta     struct {
		Count int `json:"count"`
	} `json:"meta"`
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
}

// --- vocabulary normalisation ---------------------------------------------

// kitsuFormat maps Kitsu's subtype onto AniList's MediaFormat, which is what
// the frontend's FORMAT_LABELS is keyed by.
func kitsuFormat(subtype string) string {
	switch strings.ToLower(strings.TrimSpace(subtype)) {
	case "tv":
		return "TV"
	case "movie":
		return "MOVIE"
	case "ova":
		return "OVA"
	case "ona":
		return "ONA"
	case "special":
		return "SPECIAL"
	case "music":
		return "MUSIC"
	}
	return strings.ToUpper(strings.TrimSpace(subtype))
}

// kitsuSubtypeParam is the reverse, for the filter. An unmappable format
// returns "" so the filter is dropped rather than sent as something Kitsu
// would reject.
func kitsuSubtypeParam(format string) string {
	switch strings.ToUpper(strings.TrimSpace(format)) {
	case "TV":
		return "TV"
	case "MOVIE":
		return "movie"
	case "OVA":
		return "OVA"
	case "ONA":
		return "ONA"
	case "SPECIAL":
		return "special"
	case "MUSIC":
		return "music"
	}
	return ""
}

// kitsuStatus maps Kitsu's status onto AniList's MediaStatus.
func kitsuStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "current":
		return "RELEASING"
	case "finished":
		return "FINISHED"
	case "upcoming", "tba", "unreleased":
		return "NOT_YET_RELEASED"
	}
	return ""
}

// kitsuScore turns Kitsu's rating string into the integer percentage the
// catalog renders. It is already on a 0-100 scale, so only rounding applies.
func kitsuScore(rating string) int {
	if strings.TrimSpace(rating) == "" {
		return 0
	}
	v, err := strconv.ParseFloat(rating, 64)
	if err != nil || v <= 0 {
		return 0
	}
	return int(v + 0.5)
}

// kitsuCover picks the largest poster Kitsu offers.
func kitsuCover(a kitsuAnimeAttributes) string {
	return firstNonEmpty(
		a.PosterImage.Original,
		a.PosterImage.Large,
		a.PosterImage.Medium,
		a.PosterImage.Small,
	)
}

// kitsuRomaji is the transliterated title. Kitsu files it under en_jp, which
// is what the scrapers index on — the same reason AniList's romaji came first.
func kitsuRomaji(a kitsuAnimeAttributes) string {
	return firstNonEmpty(a.Titles["en_jp"], a.CanonicalTitle)
}

// kitsuEnglish is the English title, when Kitsu has one distinct from the
// transliteration.
func kitsuEnglish(a kitsuAnimeAttributes) string {
	return firstNonEmpty(a.Titles["en"], a.Titles["en_us"])
}

// kitsuIsAdult reports whether the catalog should hide the title. Kitsu has
// an explicit flag, which is the closest thing to AniList's isAdult.
func kitsuIsAdult(a kitsuAnimeAttributes) bool {
	if a.NSFW {
		return true
	}
	// R18 is Kitsu's explicit rating; plain "R" is ordinary violence.
	return strings.EqualFold(strings.TrimSpace(a.AgeRating), "R18")
}

// kitsuStartDate splits an ISO "2013-04-07" into its parts, zero where absent.
func kitsuStartDate(iso string) (year, month, day int) {
	parts := strings.Split(strings.TrimSpace(iso), "-")
	if len(parts) > 0 {
		year, _ = strconv.Atoi(parts[0])
	}
	if len(parts) > 1 {
		month, _ = strconv.Atoi(parts[1])
	}
	if len(parts) > 2 {
		day, _ = strconv.Atoi(parts[2])
	}
	return year, month, day
}

// --- genres ----------------------------------------------------------------

// Kitsu filters by category slug where AniList filtered by genre name, and
// the two do not always agree on spelling — AniList's "Sci-Fi" is Kitsu's
// "science-fiction". The index is built from Kitsu itself so the mapping
// follows whatever it actually offers, with aliases only for the names that
// genuinely differ.
var (
	kitsuGenreOnce sync.Once
	kitsuGenreMap  map[string]string // normalised title -> slug
	kitsuGenreList []string          // titles, most used first
	kitsuGenreErr  error
)

// kitsuGenreAliases bridge the names AniList uses to the ones Kitsu does.
var kitsuGenreAliases = map[string]string{
	"sci-fi":        "science-fiction",
	"school":        "school-life",
	"mahou shoujo":  "magical-girl",
	"slice of life": "slice-of-life",
}

func kitsuLoadGenres() (map[string]string, []string, error) {
	kitsuGenreOnce.Do(func() {
		var parsed struct {
			Data []kitsuIncluded `json:"data"`
		}
		// Direct: this backs a genre the user picked, and an open breaker
		// would silently drop the filter and list everything.
		path := "/categories?" + url.Values{
			"page[limit]": {"40"},
			"sort":        {"-totalMediaCount"},
		}.Encode()
		if err := kitsuGetDirect(path, &parsed); err != nil {
			kitsuGenreErr = err
			return
		}

		index := make(map[string]string, len(parsed.Data))
		titles := make([]string, 0, len(parsed.Data))
		for _, c := range parsed.Data {
			title := strings.TrimSpace(c.Attributes.Title)
			slug := strings.TrimSpace(c.Attributes.Slug)
			if title == "" || slug == "" {
				continue
			}
			index[strings.ToLower(title)] = slug
			titles = append(titles, title)
		}
		kitsuGenreMap = index
		kitsuGenreList = titles
	})
	return kitsuGenreMap, kitsuGenreList, kitsuGenreErr
}

// kitsuGenreSlug resolves a genre name to Kitsu's category slug, or "" when
// it has no equivalent.
//
// It deliberately does not guess. Kitsu answers an unknown filter[categories]
// by ignoring the filter entirely and listing everything, so a guessed slug
// does not fail — it silently returns an unfiltered page under a heading
// that names the genre. Better to report that the genre cannot be applied
// than to show the wrong results as if they were right.
func kitsuGenreSlug(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return ""
	}
	if slug, ok := kitsuGenreAliases[key]; ok {
		return slug
	}
	index, _, err := kitsuLoadGenres()
	if err != nil {
		return ""
	}
	return index[key]
}

// kitsuFetchGenres returns the genre names Kitsu offers, for the dropdown.
func kitsuFetchGenres() ([]string, error) {
	_, titles, err := kitsuLoadGenres()
	if err != nil {
		return nil, err
	}
	return titles, nil
}

// --- catalog ---------------------------------------------------------------

// kitsuCatalogPath builds the endpoint and query for one catalog page.
func kitsuCatalogPath(q BrowseQuery) (string, error) {
	params := url.Values{}
	params.Set("page[limit]", strconv.Itoa(kitsuPerPage))
	params.Set("page[offset]", strconv.Itoa((q.Page-1)*kitsuPerPage))
	// Genres ride along so an item can report them without a request each.
	params.Set("include", "categories")

	switch q.Mode {
	case ModeTop:
		params.Set("sort", "-averageRating")
	case ModeUpcoming:
		params.Set("filter[status]", "upcoming")
		params.Set("sort", "-userCount")
	case ModeAiring, ModeTrending:
		// Kitsu has no trending sort, so "em alta" is what is airing now and
		// has the most followers — the same reading the other backends use.
		params.Set("filter[status]", "current")
		params.Set("sort", "-userCount")
	default: // season, popular
		params.Set("sort", "-userCount")
	}

	if q.Mode == ModeSeason {
		params.Set("filter[season]", strings.ToLower(strings.TrimSpace(q.Season)))
		if q.Year > 0 {
			params.Set("filter[seasonYear]", strconv.Itoa(q.Year))
		}
	} else if q.Year > 0 {
		params.Set("filter[seasonYear]", strconv.Itoa(q.Year))
	}

	if s := kitsuSubtypeParam(q.Format); s != "" {
		params.Set("filter[subtype]", s)
	}
	if q.Genre != "" {
		slug := kitsuGenreSlug(q.Genre)
		if slug == "" {
			return "", fmt.Errorf("o Kitsu não conhece o gênero %q", q.Genre)
		}
		params.Set("filter[categories]", slug)
	}

	return "/anime?" + params.Encode(), nil
}

// kitsuFetchCatalog performs the catalog request and maps it into BrowsePage.
func kitsuFetchCatalog(q BrowseQuery) (*BrowsePage, error) {
	path, err := kitsuCatalogPath(q)
	if err != nil {
		return nil, err
	}

	var parsed kitsuListResponse
	// Direct: a catalog page is a load the user asked for.
	if err := kitsuGetDirect(path, &parsed); err != nil {
		return nil, err
	}

	// Index the sideloaded categories so each item can name its genres.
	genres := make(map[string]string, len(parsed.Included))
	for _, inc := range parsed.Included {
		if inc.Type == "categories" && inc.Attributes.Title != "" {
			genres[inc.ID] = inc.Attributes.Title
		}
	}

	out := &BrowsePage{
		Query:       q,
		Label:       labelFor(q),
		Page:        q.Page,
		HasNextPage: parsed.Links.Next != "",
		Items:       make([]BrowseItem, 0, len(parsed.Data)),
	}

	for _, a := range parsed.Data {
		attr := a.Attributes
		if kitsuIsAdult(attr) {
			// The catalog never lists adult titles. Kitsu has no server-side
			// filter for it, so they are dropped here.
			continue
		}

		year, month, day := kitsuStartDate(attr.StartDate)
		// Kitsu does not return the season, so derive it from the start date
		// the same way the season picker does.
		season := ""
		seasonYear := year
		if year > 0 && month > 0 {
			seasonYear, season = seasonFor(year, month)
		}
		date, label := formatRelease(year, month, day, season, seasonYear)

		names := make([]string, 0, len(a.Relationships.Categories.Data))
		for _, ref := range a.Relationships.Categories.Data {
			if name, ok := genres[ref.ID]; ok {
				names = append(names, name)
			}
		}

		// A Kitsu id, not an AniList one. The field keeps its name for the
		// frontend; nothing joins on it, a click runs a title search.
		id, _ := strconv.Atoi(a.ID)

		out.Items = append(out.Items, BrowseItem{
			AniListID:    id,
			Title:        firstNonEmpty(kitsuRomaji(attr), kitsuEnglish(attr), attr.CanonicalTitle),
			Romaji:       kitsuRomaji(attr),
			English:      kitsuEnglish(attr),
			Cover:        kitsuCover(attr),
			Format:       kitsuFormat(attr.Subtype),
			Status:       kitsuStatus(attr.Status),
			EpisodeCount: attr.EpisodeCount,
			Score:        kitsuScore(attr.AverageRating),
			Genres:       names,
			ReleaseDate:  date,
			ReleaseLabel: label,
			Season:       season,
			Year:         seasonYear,
		})
	}

	return out, nil
}

// --- title metadata --------------------------------------------------------

// kitsuSearchOne returns the entry whose title matches, for the metadata path.
//
// filter[text] is fuzzy enough to answer confidently with the wrong show — a
// lookup for "titulo-que-nao-existe-xyz" comes back with "Pokemon XY&Z",
// because it latched onto "xyz". This layer feeds covers and release dates
// onto cards, so a loose match does not fail visibly; it just puts another
// series' artwork on the title. That is worse than the placeholder the
// frontend shows when nothing is found, so a candidate has to actually match
// the name asked for.
//
// A handful of candidates is requested rather than one, so an exact match
// ranked below a looser one is still found.
func kitsuSearchOne(title string) (*kitsuAnime, error) {
	params := url.Values{}
	params.Set("filter[text]", title)
	params.Set("page[limit]", "5")

	var parsed kitsuListResponse
	if err := kitsuGet("/anime?"+params.Encode(), &parsed); err != nil {
		return nil, err
	}

	want := matchKey(title)
	if want == "" {
		return nil, errTitleNotFound
	}

	for i := range parsed.Data {
		attr := parsed.Data[i].Attributes
		for _, candidate := range append([]string{
			attr.CanonicalTitle,
			kitsuRomaji(attr),
			kitsuEnglish(attr),
		}, mapValues(attr.Titles)...) {
			if candidate != "" && matchKey(candidate) == want {
				return &parsed.Data[i], nil
			}
		}
	}
	return nil, errTitleNotFound
}

// mapValues returns a map's values, for scanning every title Kitsu carries.
func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// fetchKitsuMedia is the Kitsu half of the title lookup, shaped exactly like
// the other two so the cache above it cannot tell them apart.
//
// Kitsu has no per-episode still images, so thumbs are always empty and
// marked fetched: there is nothing more to ask it for.
func fetchKitsuMedia(title string) (*titleMedia, error) {
	a, err := kitsuSearchOne(title)
	if err != nil {
		return nil, err
	}
	attr := a.Attributes

	year, month, day := kitsuStartDate(attr.StartDate)
	season := ""
	seasonYear := year
	if year > 0 && month > 0 {
		seasonYear, season = seasonFor(year, month)
	}
	date, label := formatRelease(year, month, day, season, seasonYear)

	yearStr := ""
	if seasonYear > 0 {
		yearStr = strconv.Itoa(seasonYear)
	}

	return &titleMedia{
		cover:         kitsuCover(attr),
		adult:         kitsuIsAdult(attr),
		thumbs:        map[string]string{},
		thumbsFetched: true,
		info: TitleInfo{
			ReleaseDate:  date,
			ReleaseLabel: label,
			Year:         yearStr,
			Format:       kitsuFormat(attr.Subtype),
			Status:       kitsuStatus(attr.Status),
			EpisodeCount: attr.EpisodeCount,
			Score:        kitsuScore(attr.AverageRating),
		},
	}, nil
}

// resetKitsuGenreIndex clears the memoised category index. Tests use it so
// one test's stub server does not fix the index for the next.
func resetKitsuGenreIndex() {
	kitsuGenreOnce = sync.Once{}
	kitsuGenreMap = nil
	kitsuGenreList = nil
	kitsuGenreErr = nil
}
