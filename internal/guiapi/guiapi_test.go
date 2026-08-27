package guiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alvarorichard/Goanime/internal/api/source"
	"github.com/alvarorichard/Goanime/internal/models"
	"github.com/alvarorichard/Goanime/internal/scraper/providers/superflix"
	"github.com/alvarorichard/Goanime/internal/util"
)

func TestKindsFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
		want []source.SourceKind
	}{
		{"empty means all", "", nil},
		{"all means all", "all", nil},
		{"unknown id falls back to all", "flixhq", nil},
		{"ptbr group", "ptbr", ptbrKinds},
		{"ptbr hyphenated", "PT-BR", ptbrKinds},
		{"single source is case-insensitive", "allanime", []source.SourceKind{source.AllAnime}},
		{"single source exact", "SuperFlix", []source.SourceKind{source.SuperFlix}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := kindsFor(tt.id)
			if len(got) != len(tt.want) {
				t.Fatalf("kindsFor(%q) = %v, want %v", tt.id, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("kindsFor(%q)[%d] = %q, want %q", tt.id, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// Sources must always offer the two pseudo-entries the frontend relies on,
// and must never emit a duplicate id (which would silently break the
// dropdown's selected-value restore).
func TestSourcesHasPseudoEntriesAndUniqueIDs(t *testing.T) {
	t.Parallel()

	got := Sources()
	if len(got) < 2 {
		t.Fatalf("Sources() returned %d entries, want at least the 2 pseudo-entries", len(got))
	}
	if got[0].ID != "all" || got[1].ID != "ptbr" {
		t.Fatalf("Sources() must lead with all/ptbr, got %q/%q", got[0].ID, got[1].ID)
	}

	seen := map[string]bool{}
	for _, s := range got {
		if seen[s.ID] {
			t.Fatalf("duplicate source id %q", s.ID)
		}
		seen[s.ID] = true
		if s.Label == "" {
			t.Fatalf("source %q has an empty label", s.ID)
		}
	}
}

func TestSortedSeasonsOrdersNumerically(t *testing.T) {
	t.Parallel()

	all := map[string][]superflix.SuperFlixEpisode{
		"10":        nil,
		"2":         nil,
		"1":         nil,
		"Especiais": nil,
	}
	got := sortedSeasons(all)
	want := []string{"1", "2", "10", "Especiais"}

	if len(got) != len(want) {
		t.Fatalf("sortedSeasons() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sortedSeasons()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAnimeFromResultMapsMediaType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want models.MediaType
	}{
		{"movie", models.MediaTypeMovie},
		{"TV", models.MediaTypeTV},
		{"anime", models.MediaTypeAnime},
		{"", models.MediaTypeAnime},
		{"nonsense", models.MediaTypeAnime},
	}

	for _, tt := range tests {
		got := animeFromResult(SearchResult{Name: "x", MediaType: tt.in})
		if got.MediaType != tt.want {
			t.Fatalf("mediaType %q mapped to %q, want %q", tt.in, got.MediaType, tt.want)
		}
	}
}

func TestToEpisodeResultsPicksFirstNonEmptyTitle(t *testing.T) {
	t.Parallel()

	eps := []models.Episode{
		{Number: "1", Title: models.TitleDetails{Romaji: "Romaji only"}},
		{Number: "2", Title: models.TitleDetails{English: "English wins", Romaji: "Romaji"}},
		{Number: "3", Title: models.TitleDetails{Japanese: "Japanese only"}},
		{Number: "4"},
	}
	got := toEpisodeResults(eps)

	want := []string{"Romaji only", "English wins", "Japanese only", ""}
	for i, w := range want {
		if got[i].Title != w {
			t.Fatalf("episode %d title = %q, want %q", i+1, got[i].Title, w)
		}
	}
}

// The frontend consumes these structs as JSON, so the field names the JS
// reads must not drift silently.
func TestEpisodeListJSONShape(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(EpisodeList{
		Seasons:  []string{"1"},
		Season:   "1",
		Episodes: []EpisodeResult{{Number: "1", Num: 1}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"seasons", "season", "episodes"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("EpisodeList JSON is missing %q: %s", key, b)
		}
	}
}

func TestNormalizeTitleStripsScraperTags(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"One Piece [PT-BR]", "One Piece"},
		{"Naruto [English] (Dublado)", "Naruto"},
		{"  Bleach   [Legendado]  ", "Bleach"},
		{"Cowboy Bebop", "Cowboy Bebop"},
		{"Matrix [Movie]", "Matrix"},
	}

	for _, tt := range tests {
		if got := normalizeTitle(tt.in); got != tt.want {
			t.Fatalf("normalizeTitle(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExtractEpisodeNumber(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"Episode 12 - The Beginning", "12"},
		{"Episode 1", "1"},
		{"No digits here", ""},
		{"3 - Straight to it", "3"},
	}

	for _, tt := range tests {
		if got := extractEpisodeNumber(tt.in); got != tt.want {
			t.Fatalf("extractEpisodeNumber(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestArgsForPlayer(t *testing.T) {
	t.Parallel()

	if got := argsForPlayer(`C:\vlc.exe`, ""); got != nil {
		t.Fatalf("no referer should produce no args, got %v", got)
	}

	tests := []struct {
		path string
		want string
	}{
		{`C:\Program Files\VideoLAN\VLC\vlc.exe`, "--http-referrer=https://x.test"},
		{"/usr/bin/mpv", "--referrer=https://x.test"},
		{`C:\Program Files\MPC-HC\mpc-hc64.exe`, "/referer"},
		{`C:\PotPlayer\PotPlayerMini64.exe`, "/referer=https://x.test"},
	}

	for _, tt := range tests {
		got := argsForPlayer(tt.path, "https://x.test")
		if len(got) == 0 || got[0] != tt.want {
			t.Fatalf("argsForPlayer(%q) = %v, want first arg %q", tt.path, got, tt.want)
		}
	}

	if got := argsForPlayer("/usr/bin/unknown-player", "https://x.test"); got != nil {
		t.Fatalf("unknown player should get no flags, got %v", got)
	}
}

func TestFallbackRefererCoversEveryActiveSource(t *testing.T) {
	t.Parallel()

	// A source with no fallback referer silently plays without one in
	// external players, which usually means a 403. Catch that here rather
	// than in a bug report.
	for _, s := range source.ActiveSources() {
		name := string(s.Describe().Kind)
		if fallbackReferer(name) == "" {
			t.Errorf("no fallback referer for active source %q", name)
		}
	}

	// The scraper stamps AnimeFire as "Animefire.io", not the registry kind.
	if fallbackReferer("Animefire.io") == "" {
		t.Error(`no fallback referer for the scraper spelling "Animefire.io"`)
	}
	if fallbackReferer("Nonexistent") != "" {
		t.Error("unknown source should have no fallback referer")
	}
}

func TestPlayWithRejectsEmptyURL(t *testing.T) {
	t.Parallel()

	if err := PlayWith("", "vlc", "AllAnime"); err == nil {
		t.Fatal("PlayWith with an empty URL should fail")
	}
	if _, err := Play(""); err == nil {
		t.Fatal("Play with an empty URL should fail")
	}
}

func TestSearchRejectsEmptyQuery(t *testing.T) {
	t.Parallel()

	if _, err := Search("   ", "all"); err == nil {
		t.Fatal("Search with a blank query should fail")
	}
}

// CancelSearch must be safe to call when nothing is running.
func TestCancelSearchIsNoopWhenIdle(t *testing.T) {
	CancelSearch()
	CancelSearch()
}

// --- downloads -----------------------------------------------------------

func TestQualitiesIncludeBestAndAreUnique(t *testing.T) {
	t.Parallel()

	qs := Qualities()
	if len(qs) == 0 {
		t.Fatal("Qualities() returned nothing")
	}
	if qs[0].Value != "best" {
		t.Fatalf("first quality is %q, want the safe default %q", qs[0].Value, "best")
	}

	seen := map[string]bool{}
	for _, q := range qs {
		if q.Label == "" {
			t.Fatalf("quality %q has no label", q.Value)
		}
		if seen[q.Value] {
			t.Fatalf("duplicate quality value %q", q.Value)
		}
		seen[q.Value] = true
	}
}

func TestDownloadLabel(t *testing.T) {
	t.Parallel()

	got := downloadLabel(SearchResult{Name: "Cowboy Bebop"}, EpisodeResult{Number: "5"})
	if got != "Cowboy Bebop — Ep. 5" {
		t.Fatalf("flat label = %q", got)
	}

	got = downloadLabel(
		SearchResult{Name: "Breaking Bad"},
		EpisodeResult{Number: "3", SeasonID: "2"},
	)
	if got != "Breaking Bad — T2E3" {
		t.Fatalf("seasoned label = %q", got)
	}
}

func TestDownloadPathUsesEpisodeNumberFallback(t *testing.T) {
	t.Parallel()

	// Num is 0 here, so the path builder must fall back to parsing Number.
	path, err := downloadPath(
		SearchResult{Name: "Naruto"},
		EpisodeResult{Number: "7", Num: 0},
	)
	if err != nil {
		t.Fatalf("downloadPath: %v", err)
	}
	if !strings.Contains(path, "07") && !strings.Contains(path, "E7") {
		t.Fatalf("path %q does not encode episode 7", path)
	}
	if filepath.Ext(path) == "" {
		t.Fatalf("path %q has no extension", path)
	}
}

func TestDownloadPathRejectsUnusableName(t *testing.T) {
	t.Parallel()

	if _, err := downloadPath(SearchResult{Name: "   "}, EpisodeResult{Number: "1"}); err == nil {
		t.Fatal("a blank title should not produce a download path")
	}
}

func TestStartDownloadRejectsEmptyTitle(t *testing.T) {
	t.Parallel()

	if _, err := StartDownload(SearchResult{}, EpisodeResult{Number: "1"}, "best"); err == nil {
		t.Fatal("StartDownload with no title should fail")
	}
}

func TestCancelUnknownDownloadReturnsFalse(t *testing.T) {
	t.Parallel()

	if CancelDownload("job-does-not-exist") {
		t.Fatal("cancelling an unknown job should report false")
	}
}

// resolveWithQuality must leave the process-wide quality exactly as it found
// it, or a download would silently change what the next playback resolves.
func TestResolveWithQualityRestoresGlobal(t *testing.T) {
	previous := util.GlobalQuality
	t.Cleanup(func() { util.GlobalQuality = previous })

	util.GlobalQuality = "720p"
	// The resolve itself fails (no real source), which is the interesting
	// case: the restore has to happen on the error path too.
	_, _, _ = resolveWithQuality(SearchResult{Name: "x", Source: "Nope"}, EpisodeResult{Number: "1"}, "1080p")

	if util.GlobalQuality != "720p" {
		t.Fatalf("GlobalQuality left as %q, want it restored to %q", util.GlobalQuality, "720p")
	}
}

func TestJobNum(t *testing.T) {
	t.Parallel()

	if got := jobNum("job-12"); got != 12 {
		t.Fatalf("jobNum(\"job-12\") = %d", got)
	}
	if got := jobNum("nonsense"); got != 0 {
		t.Fatalf("jobNum on a bad id = %d, want 0", got)
	}
}

// --- SuperFlix season path ------------------------------------------------

// The browser path has to outlast the Cloudflare solve budget. A context
// shorter than the solve cancels it mid-flight and the user gets a bare
// "context deadline exceeded" — which is exactly the bug this pins.
func TestSuperFlixBrowserTimeoutOutlastsTheSolve(t *testing.T) {
	t.Parallel()

	// api.fetchSuperFlixSeasons uses 210s for the same call.
	if superFlixBrowserTimeout < 210*time.Second {
		t.Fatalf("superFlixBrowserTimeout = %s, must be at least the CLI's 210s",
			superFlixBrowserTimeout)
	}
	// It must also not be capped by the generic episode budget, which is
	// why SuperFlix builds its own context instead of inheriting one.
	if superFlixBrowserTimeout <= episodesTimeout {
		t.Fatalf("superFlixBrowserTimeout (%s) must exceed episodesTimeout (%s)",
			superFlixBrowserTimeout, episodesTimeout)
	}
	if tvmazeTimeout >= superFlixBrowserTimeout {
		t.Fatalf("the TVmaze fast path (%s) should be far shorter than the browser path (%s)",
			tvmazeTimeout, superFlixBrowserTimeout)
	}
}

// A timeout must not reach the user as "context deadline exceeded".
func TestDescribeSuperFlixFailureIsActionable(t *testing.T) {
	t.Parallel()

	got := describeSuperFlixFailure(
		fmt.Errorf("failed to load serie page: %w", context.DeadlineExceeded)).Error()

	for _, jargon := range []string{"context", "deadline", "serie page"} {
		if strings.Contains(strings.ToLower(got), jargon) {
			t.Errorf("the message still leaks %q: %s", jargon, got)
		}
	}
	if !strings.Contains(got, "outra fonte") {
		t.Errorf("a timeout message should suggest what to do next: %s", got)
	}
}

func TestDescribeSuperFlixFailureKeepsUnknownCauses(t *testing.T) {
	t.Parallel()

	got := describeSuperFlixFailure(errors.New("algo inesperado")).Error()
	if !strings.Contains(got, "algo inesperado") {
		t.Fatalf("an unrecognised cause must survive for debugging: %s", got)
	}
}

// withHeadless forces the display check for one test. Not parallel-safe, so
// the tests using it do not call t.Parallel.
func withHeadless(t *testing.T, headless bool) {
	t.Helper()
	prev := superflixHeadless
	superflixHeadless = func() bool { return headless }
	t.Cleanup(func() { superflixHeadless = prev })
}

// The headless branch describes the machine, not the error, so it used to
// run first and swallow whatever actually went wrong -- on a headless box
// every SuperFlix failure read "no screen was found" and the real cause was
// unrecoverable. It only ever failed on Linux, because the display check is
// hardcoded true on Windows and macOS.
func TestDescribeSuperFlixFailureKeepsTheCauseWhenHeadless(t *testing.T) {
	withHeadless(t, true)

	got := describeSuperFlixFailure(errors.New("algo inesperado")).Error()
	if !strings.Contains(got, "nenhuma tela foi encontrada") {
		t.Errorf("a headless machine should still be explained: %s", got)
	}
	if !strings.Contains(got, "algo inesperado") {
		t.Errorf("the cause must survive alongside the explanation: %s", got)
	}
}

// A cause the function recognises must win over the headless explanation:
// "the page carried no episode list" is more useful than "you have no
// screen", and it is true whether or not a display exists.
func TestDescribeSuperFlixFailurePrefersAKnownCauseOverHeadless(t *testing.T) {
	withHeadless(t, true)

	got := describeSuperFlixFailure(superflix.ErrSuperFlixNoEpisodeList).Error()
	if !strings.Contains(got, "lista de") {
		t.Errorf("the specific cause should be reported: %s", got)
	}
	if strings.Contains(got, "nenhuma tela") {
		t.Errorf("a known cause must not be replaced by the headless message: %s", got)
	}
}

// The timeout message is checked for jargon elsewhere; here the point is
// that being headless does not hijack it either.
func TestDescribeSuperFlixFailurePrefersTimeoutOverHeadless(t *testing.T) {
	withHeadless(t, true)

	got := describeSuperFlixFailure(
		fmt.Errorf("failed to load serie page: %w", context.DeadlineExceeded)).Error()
	if !strings.Contains(got, "demorou demais") {
		t.Errorf("a timeout should still be reported as a timeout: %s", got)
	}
}

// A movie is a single item and must never touch the season machinery — no
// TVmaze call, no browser, no timeout.
func TestSuperFlixMovieSkipsTheSeasonPath(t *testing.T) {
	t.Parallel()

	start := time.Now()
	list, err := superFlixEpisodes(&models.Anime{
		Name:      "Um filme",
		URL:       "12345",
		Source:    "SuperFlix",
		MediaType: models.MediaTypeMovie,
	}, "")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("superFlixEpisodes: %v", err)
	}
	if len(list.Episodes) != 1 || list.Episodes[0].Number != "1" {
		t.Fatalf("a movie should yield exactly one item, got %+v", list.Episodes)
	}
	if len(list.Seasons) != 0 {
		t.Fatalf("a movie has no seasons, got %v", list.Seasons)
	}
	if elapsed > time.Second {
		t.Fatalf("the movie path took %s — it must not hit the network", elapsed)
	}
}

func TestSuperFlixWithoutTMDBIDFails(t *testing.T) {
	t.Parallel()

	_, err := superFlixEpisodes(&models.Anime{
		Name:      "Sem id",
		Source:    "SuperFlix",
		MediaType: models.MediaTypeTV,
	}, "")
	if err == nil {
		t.Fatal("a SuperFlix title with no TMDB id should fail fast")
	}
}

// The adult filter is fail-open by design: a title AniList does not
// recognise must be shown, not hidden. These exercise dropAdult against a pre-seeded
// mediaCache so no network is involved — the cache is the same one the
// artwork lookups fill, which is why the filter costs no extra request.
func seedAdultCache(t *testing.T, title string, adult bool) {
	t.Helper()
	key := strings.ToLower(normalizeTitle(title))
	mediaCache.Store(key, &aniListMedia{adult: adult})
	t.Cleanup(func() { mediaCache.Delete(key) })
}

func TestDropAdultRemovesFlaggedTitles(t *testing.T) {
	seedAdultCache(t, "Filtered Adult Title", true)
	seedAdultCache(t, "Filtered Safe Title", false)

	in := []SearchResult{{Name: "Filtered Adult Title"}, {Name: "Filtered Safe Title"}}
	got := dropAdult(append([]SearchResult(nil), in...))
	if len(got) != 1 {
		t.Fatalf("kept %d results, want 1", len(got))
	}
	if got[0].Name != "Filtered Safe Title" {
		t.Errorf("kept %q, want the non-adult title", got[0].Name)
	}
}

// Nothing to classify must not cost a pass.
func TestDropAdultOnAnEmptyListIsANoOp(t *testing.T) {
	if got := dropAdult(nil); got != nil {
		t.Errorf("dropAdult(nil) = %v, want nil", got)
	}
}

// The important half. A scraper title AniList cannot resolve — which the
// PT-BR sources produce constantly — must survive the filter. Hiding on an
// unknown would silently drop ordinary results the user searched for.
func TestDropAdultShowsTitlesItCannotClassify(t *testing.T) {
	seedAdultCache(t, "Unresolvable Scraper Title", false)
	// Stored as nil, the shape lookupAniList caches for "AniList had nothing".
	key := strings.ToLower(normalizeTitle("Nil Cached Title"))
	mediaCache.Store(key, (*aniListMedia)(nil))
	t.Cleanup(func() { mediaCache.Delete(key) })

	in := []SearchResult{{Name: "Unresolvable Scraper Title"}, {Name: "Nil Cached Title"}}
	got := dropAdult(append([]SearchResult(nil), in...))
	if len(got) != 2 {
		t.Fatalf("kept %d of %d; an unclassifiable title must be shown, not hidden", len(got), len(in))
	}
}

// Out of budget, classification stops and shows what it did not get to,
// rather than blocking or hiding it.
func TestDropAdultOutOfBudgetKeepsResults(t *testing.T) {
	seedAdultCache(t, "Out Of Budget Adult Title", true)

	prev := adultFilterTimeout
	// A deadline already in the past, so the budget is deterministically
	// gone rather than racing the first (cache-hit, instant) lookup.
	adultFilterTimeout = -time.Second
	t.Cleanup(func() { adultFilterTimeout = prev })

	in := []SearchResult{{Name: "Out Of Budget Adult Title"}}
	got := dropAdult(append([]SearchResult(nil), in...))
	if len(got) != 1 {
		t.Fatalf("kept %d results with no budget, want 1 (fail open)", len(got))
	}
}

// The filter must not inherit the search's remaining time. It used to, and a
// slow fan-out then left classification with an already-expired deadline —
// so the filter silently passed adult titles through exactly when the search
// had been slowest. Its budget is its own, independent of any caller context.
func TestDropAdultDoesNotInheritAnExhaustedCallerDeadline(t *testing.T) {
	seedAdultCache(t, "Exhausted Deadline Adult Title", true)

	// The shape that used to break it: a caller context long past its
	// deadline. dropAdult takes no context now, so this cannot reach it.
	expired, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-expired.Done()

	in := []SearchResult{{Name: "Exhausted Deadline Adult Title"}}
	got := dropAdult(append([]SearchResult(nil), in...))
	if len(got) != 0 {
		t.Fatalf("kept %d results, want 0: an exhausted caller deadline must not disable the filter", len(got))
	}
}
