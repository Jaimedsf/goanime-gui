package guiapi

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alvarorichard/Goanime/internal/api"
)

// Both lookups here — the combined media query and GetCover's fallback —
// are cached by metacache.go, keyed by a normalised title. That cache is
// backed by a file, so a title resolved in one session is free in the next:
// the home screen used to re-ask AniList for every card on every launch.

// anilistTimeout bounds the direct AniList calls made here.
const anilistTimeout = 8 * time.Second

// tagsToStrip are the language and dub/sub markers the scrapers add to
// titles. AniList matches far better without them.
var tagsToStrip = []string{
	"[PT-BR]", "[Português]", "[Portuguese]", "[English]", "[Multilanguage]",
	"[Legendado]", "[Dublado]", "[Movie]", "[TV]",
	"(Dublado)", "(Legendado)", "(Dub)", "(Sub)",
	// Goyabu marks uncensored cuts this way. Stripping it is not only about
	// the adult filter: "Overflow (Sem Censura)" does not resolve on AniList
	// at all, so the card was also losing its cover and release date.
	"(Sem Censura)", "[Sem Censura]", "(Uncensored)", "[Uncensored]",
}

// normalizeTitle strips the scraper tags so AniList gets a clean query and
// cache hits are more reliable.
func normalizeTitle(title string) string {
	t := title
	for _, tag := range tagsToStrip {
		t = strings.ReplaceAll(t, tag, "")
	}
	return strings.TrimSpace(strings.Join(strings.Fields(t), " "))
}

// TitleInfo is the release metadata shown next to a title. Every field is
// best-effort: AniList has no entry for a lot of what the PT-BR sources
// carry, so an empty value means "unknown", never an error.
type TitleInfo struct {
	// ReleaseDate is ISO "2019-04-06", or just "2019-04" / "2019" when
	// AniList only knows part of it.
	ReleaseDate string `json:"releaseDate"`
	// ReleaseLabel is the same date formatted for display in pt-BR, e.g.
	// "6 abr 2019" or "Primavera 2019" when only the season is known.
	ReleaseLabel string `json:"releaseLabel"`
	Year         string `json:"year"`
	// Format is AniList's shape: TV, MOVIE, OVA, ONA, SPECIAL.
	Format string `json:"format"`
	// Status is RELEASING, FINISHED, NOT_YET_RELEASED, CANCELLED.
	Status       string `json:"status"`
	EpisodeCount int    `json:"episodeCount"`
	Score        int    `json:"score"`
}

// titleMedia is the parsed payload of the single combined query below.
type titleMedia struct {
	info   TitleInfo
	cover  string
	thumbs map[string]string
	// adult is AniList's isAdult flag, used to keep adult titles out of the
	// search results. It rides this lookup rather than a query of its own:
	// the cards already resolve most results here for their artwork and
	// dates, so the flag costs one more field on a request that was
	// happening anyway.
	adult bool
	// malID is the MyAnimeList id this resolved to, or 0 when the answer
	// came from AniList. It is what lets the episode grid ask for stills
	// later without repeating the title search.
	malID int
	// thumbsFetched marks that the stills request already happened.
	thumbsFetched bool
}

// EpisodeArt is the artwork for one title's episode grid: the per-episode
// thumbnails plus the poster to fall back on for episodes AniList has no
// still for. Returning both in one call keeps it to a single bridge
// round-trip when the episode list opens.
type EpisodeArt struct {
	Poster string            `json:"poster"`
	Thumbs map[string]string `json:"thumbs"`
}

// GetEpisodeArt returns the thumbnails and the best poster for a result.
// The poster is why episode cards used to render blank: many scrapers
// return an empty ImageURL, and the grid had no second source for artwork
// the way the result cards did.
func GetEpisodeArt(r SearchResult) EpisodeArt {
	media := lookupTitle(r.Name)

	art := EpisodeArt{Poster: r.ImageURL, Thumbs: map[string]string{}}
	if media != nil {
		art.Thumbs = lookupThumbs(r.Name)
		if art.Poster == "" {
			art.Poster = media.cover
		}
	}
	// Last resort: the shared AniList client, which matches titles a little
	// differently and sometimes finds a cover this query missed.
	if art.Poster == "" {
		art.Poster = GetCover(r.Name)
	}
	return art
}

// GetTitleInfo returns release metadata for a title. It never errors: an
// unknown title yields a zero TitleInfo, and the frontend simply shows
// nothing rather than an error state.
func GetTitleInfo(r SearchResult) TitleInfo {
	media := lookupTitle(r.Name)
	if media == nil {
		// Fall back to whatever year the scraper itself reported.
		return TitleInfo{Year: r.Year, ReleaseLabel: r.Year, ReleaseDate: r.Year}
	}

	info := media.info
	if info.Year == "" && r.Year != "" {
		info.Year = r.Year
		if info.ReleaseLabel == "" {
			info.ReleaseLabel = r.Year
		}
	}
	return info
}

// GetEpisodeThumbnails returns episode number to thumbnail URL for the
// given title. Always returns a map (never an error) so the frontend can
// fall back to the series cover without a failure path.
func GetEpisodeThumbnails(title string) map[string]string {
	return lookupThumbs(title)
}

// lookupThumbs returns the episode stills for a title, fetching them on
// first use.
//
// They are separate from the main lookup because the two backends disagree
// about their cost: AniList returned stills inside the same query, while
// Jikan needs a second request per title. Fetching them eagerly would double
// the requests behind every home-screen card to fill a map only the episode
// grid ever reads — and Jikan allows 3 requests a second.
//
// The answer is written back to the cache, marked as fetched, so a title
// that genuinely has no stills is not asked about again.
func lookupThumbs(title string) map[string]string {
	media := lookupTitle(title)
	if media == nil {
		return map[string]string{}
	}
	if media.thumbsFetched || media.malID <= 0 {
		return media.thumbs
	}

	thumbs := jikanEpisodeThumbs(media.malID)
	key := strings.ToLower(normalizeTitle(title))
	if key != "" {
		metaCachePutMedia(key, cachedMedia{
			Info:          media.info,
			Cover:         media.cover,
			Thumbs:        thumbs,
			Adult:         media.adult,
			MalID:         media.malID,
			ThumbsFetched: true,
		})
	}
	return thumbs
}

// GetCover returns a cover-art URL for the given title from AniList.
// Results are memoised in-memory so the frontend can call it once per card
// without worrying about rate limits. Returns an empty string (not an
// error) when AniList has no match, so the frontend renders a placeholder
// instead of handling a failure path.
func GetCover(title string) string {
	clean := normalizeTitle(title)
	key := strings.ToLower(clean)
	if key == "" {
		return ""
	}
	if e, ok := metaCacheCover(key); ok {
		return e.URL
	}

	// The combined lookup already fetches the cover, so reuse it when this
	// title has been through it — otherwise a card needing both a cover and
	// a release date would cost two AniList requests instead of one.
	media, definitive := lookupTitleEntry(clean)
	if media != nil && media.cover != "" {
		metaCachePutCover(key, media.cover, false)
		return media.cover
	}

	info, err := api.FetchAnimeFromAniList(clean)
	if err != nil || info == nil {
		// This client cannot say whether it found nothing or simply
		// failed, so lean on what the combined lookup learned: it is only
		// safe to remember "no cover" when AniList positively answered
		// that the title does not exist. Otherwise a dropped connection
		// would blank the card until the negative aged out.
		metaCachePutCover(key, "", !definitive)
		return ""
	}

	url := info.Data.Media.CoverImage.Large
	if url == "" {
		url = info.Data.Media.CoverImage.Medium
	}
	metaCachePutCover(key, url, false)
	return url
}

// lookupTitle runs (and caches) the one combined query that backs covers,
// episode stills and release dates. A nil result means AniList had nothing
// or the call failed; callers treat both the same way.
func lookupTitle(title string) *titleMedia {
	m, _ := lookupTitleEntry(title)
	return m
}

// lookupTitleEntry is lookupTitle plus the reason behind a nil:
// definitive is true only when AniList itself said the title does not
// exist, and false when the lookup merely failed to complete. GetCover
// needs that distinction to decide whether an empty answer is worth
// remembering; everyone else can ignore it.
func lookupTitleEntry(title string) (media *titleMedia, definitive bool) {
	clean := normalizeTitle(title)
	key := strings.ToLower(clean)
	if key == "" {
		return nil, false
	}
	if e, ok := metaCacheMedia(key); ok {
		if e.Missing {
			return nil, !e.transient
		}
		return &titleMedia{
			info: e.Info, cover: e.Cover, thumbs: e.Thumbs, adult: e.Adult,
			malID: e.MalID, thumbsFetched: e.ThumbsFetched,
		}, true
	}

	m, err := fetchTitleMedia(clean)
	switch {
	case err == nil && m != nil:
		metaCachePutMedia(key, cachedMedia{
			Info:          m.info,
			Cover:         m.cover,
			Thumbs:        m.thumbs,
			Adult:         m.adult,
			MalID:         m.malID,
			ThumbsFetched: m.thumbsFetched,
		})
		return m, true

	case errors.Is(err, errTitleNotFound):
		// A real "no such title". Worth keeping: the PT-BR scrapers
		// produce plenty of names AniList will never resolve, and each one
		// used to cost a request on every single launch.
		metaCachePutMedia(key, cachedMedia{Missing: true})
		return nil, true
	}

	// Anything else failed rather than answered. Remember it briefly and
	// in memory only, so a page of forty cards does not retry forty times
	// against an API that is down, without teaching the file on disk that
	// these titles do not exist.
	metaCachePutMedia(key, cachedMedia{Missing: true, transient: true})
	return nil, false
}

// isAdultTitle reports whether AniList flags a scraper result as adult.
//
// It is deliberately **fail-open**: a title AniList does not recognise, or a
// lookup that failed, answers false and the result is shown. The scrapers
// decorate titles heavily ("[PT-BR] Overflow (Sem Censura) (Dublado)"), and
// normalizeTitle does not always land on the right entry — so the failure
// mode has to be chosen. Hiding on an unknown would silently drop ordinary
// results the user searched for by name, which is a worse outcome than the
// filter leaking. See searchWithContext for what that means for the toggle.
func isAdultTitle(name string) bool {
	m := lookupTitle(name)
	return m != nil && m.adult
}

// anilistQuery pulls everything the GUI needs about a title in one request:
// artwork, release dates and the episode stills. Splitting these across
// separate queries would triple the calls against a rate-limited API for no
// benefit.
const anilistQuery = `query ($search: String) {
	Media(search: $search, type: ANIME) {
		isAdult
		format
		status
		episodes
		averageScore
		seasonYear
		season
		startDate { year month day }
		coverImage { large medium }
		streamingEpisodes { title thumbnail }
	}
}`

// fetchTitleMedia resolves one title's metadata, preferring Jikan and
// falling back to AniList — the same order, and for the same reasons, as the
// catalog in browse.go: AniList disabled its API, but the outage is declared
// temporary and Jikan has upstream outages of its own.
//
// A "no such title" from the primary backend is not a reason to try the
// secondary: the two index the same anime, so a real miss is a real miss,
// and asking twice would double the cost of every unmatched PT-BR scraper
// name — of which there are many. Only a failure to complete falls through.
//
// Thumbnails are not requested here. AniList returned them in the same
// query, but Jikan needs a second call per title, and this path runs for
// every card on the home screen. GetEpisodeArt asks for them separately,
// where they are actually shown.
func fetchTitleMedia(title string) (*titleMedia, error) {
	m, err := fetchJikanMedia(title, false)
	if err == nil {
		return m, nil
	}
	if errors.Is(err, errTitleNotFound) {
		return nil, err
	}
	return fetchAniListMedia(title)
}

// fetchAniListMedia performs the combined lookup. The error is what tells a
// nil result apart: errTitleNotFound means AniList has no such title,
// anything else means the call did not complete.
//
// It goes through anilistPost rather than issuing its own request: that is
// where the pacing and the 429 retry live, and a cover lookup that bypassed
// them would be exactly what pushes a cold start over AniList's limit — the
// home screen can ask for a dozen of these at once.
func fetchAniListMedia(title string) (*titleMedia, error) {
	var parsed struct {
		Media struct {
			IsAdult      bool   `json:"isAdult"`
			Format       string `json:"format"`
			Status       string `json:"status"`
			Episodes     int    `json:"episodes"`
			AverageScore int    `json:"averageScore"`
			SeasonYear   int    `json:"seasonYear"`
			Season       string `json:"season"`
			StartDate    struct {
				Year  int `json:"year"`
				Month int `json:"month"`
				Day   int `json:"day"`
			} `json:"startDate"`
			CoverImage struct {
				Large  string `json:"large"`
				Medium string `json:"medium"`
			} `json:"coverImage"`
			StreamingEpisodes []struct {
				Title     string `json:"title"`
				Thumbnail string `json:"thumbnail"`
			} `json:"streamingEpisodes"`
		} `json:"Media"`
	}

	if err := anilistPost(anilistQuery, map[string]any{"search": title}, &parsed); err != nil {
		return nil, err
	}

	m := parsed.Media
	out := &titleMedia{
		cover:  m.CoverImage.Large,
		adult:  m.IsAdult,
		thumbs: make(map[string]string, len(m.StreamingEpisodes)),
		// AniList returns the stills in this same query, so nothing more
		// needs fetching for this entry.
		thumbsFetched: true,
	}
	if out.cover == "" {
		out.cover = m.CoverImage.Medium
	}

	for i, se := range m.StreamingEpisodes {
		if se.Thumbnail == "" {
			continue
		}
		num := extractEpisodeNumber(se.Title)
		if num == "" {
			// Fall back to a 1-based index so something still shows up.
			num = strconv.Itoa(i + 1)
		}
		out.thumbs[num] = se.Thumbnail
	}

	date, label := formatRelease(m.StartDate.Year, m.StartDate.Month, m.StartDate.Day, m.Season, m.SeasonYear)
	year := ""
	switch {
	case m.StartDate.Year > 0:
		year = strconv.Itoa(m.StartDate.Year)
	case m.SeasonYear > 0:
		year = strconv.Itoa(m.SeasonYear)
	}

	out.info = TitleInfo{
		ReleaseDate:  date,
		ReleaseLabel: label,
		Year:         year,
		Format:       m.Format,
		Status:       m.Status,
		EpisodeCount: m.Episodes,
		Score:        m.AverageScore,
	}
	return out, nil
}

var monthNames = [...]string{
	"jan", "fev", "mar", "abr", "mai", "jun",
	"jul", "ago", "set", "out", "nov", "dez",
}

// formatRelease turns AniList's partial date into an ISO string and a
// display label. AniList routinely knows only the year, or only the
// broadcast season, so every level of precision has to render sensibly
// rather than printing "0" for the missing parts.
func formatRelease(year, month, day int, season string, seasonYear int) (iso, label string) {
	switch {
	case year > 0 && month > 0 && day > 0:
		return fmt.Sprintf("%04d-%02d-%02d", year, month, day),
			fmt.Sprintf("%d %s %d", day, monthNames[month-1], year)

	case year > 0 && month > 0:
		return fmt.Sprintf("%04d-%02d", year, month),
			fmt.Sprintf("%s %d", monthNames[month-1], year)

	case year > 0:
		if season != "" {
			return strconv.Itoa(year), fmt.Sprintf("%s %d", titleCase(season), year)
		}
		return strconv.Itoa(year), strconv.Itoa(year)

	case seasonYear > 0:
		if season != "" {
			return strconv.Itoa(seasonYear), fmt.Sprintf("%s %d", titleCase(season), seasonYear)
		}
		return strconv.Itoa(seasonYear), strconv.Itoa(seasonYear)
	}
	return "", ""
}

// titleCase renders AniList's SCREAMING_CASE season as "Spring".
func titleCase(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "WINTER":
		return "Inverno"
	case "SPRING":
		return "Primavera"
	case "SUMMER":
		return "Verão"
	case "FALL", "AUTUMN":
		return "Outono"
	}
	s = strings.ToLower(s)
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// extractEpisodeNumber pulls the first integer token out of an AniList
// streamingEpisode title: "Episode 12 - The Beginning" gives "12".
func extractEpisodeNumber(title string) string {
	var digits strings.Builder
	seen := false
	for _, r := range title {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
			seen = true
			continue
		}
		if seen {
			break
		}
	}
	return digits.String()
}
