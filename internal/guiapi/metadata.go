package guiapi

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alvarorichard/Goanime/internal/api"
)

// coverCache memoises AniList cover lookups keyed by a normalised title so
// repeated GetCover calls from the frontend do not re-hit the API.
var coverCache sync.Map // map[string]string

// mediaCache memoises the combined AniList lookup (dates, format, status,
// episode stills) keyed by a normalised title.
var mediaCache sync.Map // map[string]*aniListMedia

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

// aniListMedia is the parsed payload of the single combined query below.
type aniListMedia struct {
	info   TitleInfo
	cover  string
	thumbs map[string]string
	// adult is AniList's isAdult flag, used to keep adult titles out of the
	// search results. It rides this lookup rather than a query of its own:
	// the cards already resolve most results here for their artwork and
	// dates, so the flag costs one more field on a request that was
	// happening anyway.
	adult bool
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
	media := lookupAniList(r.Name)

	art := EpisodeArt{Poster: r.ImageURL, Thumbs: map[string]string{}}
	if media != nil {
		art.Thumbs = media.thumbs
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
	media := lookupAniList(r.Name)
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
	media := lookupAniList(title)
	if media == nil {
		return map[string]string{}
	}
	return media.thumbs
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
	if v, ok := coverCache.Load(key); ok {
		return v.(string)
	}

	// The combined lookup already fetches the cover, so reuse it when this
	// title has been through it — otherwise a card needing both a cover and
	// a release date would cost two AniList requests instead of one.
	if media := lookupAniList(clean); media != nil && media.cover != "" {
		coverCache.Store(key, media.cover)
		return media.cover
	}

	info, err := api.FetchAnimeFromAniList(clean)
	if err != nil || info == nil {
		coverCache.Store(key, "")
		return ""
	}

	url := info.Data.Media.CoverImage.Large
	if url == "" {
		url = info.Data.Media.CoverImage.Medium
	}
	coverCache.Store(key, url)
	return url
}

// lookupAniList runs (and caches) the one combined query that backs covers,
// episode stills and release dates. A nil result means AniList had nothing
// or the call failed; callers treat both the same way.
func lookupAniList(title string) *aniListMedia {
	clean := normalizeTitle(title)
	key := strings.ToLower(clean)
	if key == "" {
		return nil
	}
	if v, ok := mediaCache.Load(key); ok {
		m, _ := v.(*aniListMedia)
		return m
	}

	m := fetchAniListMedia(clean)
	mediaCache.Store(key, m)
	return m
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
	m := lookupAniList(name)
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

// fetchAniListMedia performs the combined lookup. Returns nil on any
// network, status or parse failure.
//
// It goes through anilistPost rather than issuing its own request: that is
// where the pacing and the 429 retry live, and a cover lookup that bypassed
// them would be exactly what pushes a cold start over AniList's limit — the
// home screen can ask for a dozen of these at once.
func fetchAniListMedia(title string) *aniListMedia {
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
		return nil
	}

	m := parsed.Media
	out := &aniListMedia{
		cover:  m.CoverImage.Large,
		adult:  m.IsAdult,
		thumbs: make(map[string]string, len(m.StreamingEpisodes)),
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
	return out
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
