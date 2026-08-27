package goanime_test

import (
	"errors"
	"testing"

	"github.com/alvarorichard/Goanime/internal/models"
	"github.com/alvarorichard/Goanime/internal/scraper"
	"github.com/alvarorichard/Goanime/pkg/goanime"
	"github.com/alvarorichard/Goanime/pkg/goanime/types"
)

// The existing tests in client_test.go reach the network and are skipped in
// -short, so the client's own logic — which source it dispatches to, which
// arguments it builds, what it does when a scraper fails — was never
// exercised. NewClientForTest exists precisely for this and had no callers.
//
// The logic worth pinning is the per-source special-casing: AllAnime is
// addressed by anime ID plus episode number, AnimeFire by a direct episode
// URL. Getting that backwards produces a plausible-looking call that returns
// the wrong stream, which no type checker catches.

type fakeScraper struct {
	episodes []models.Episode
	epErr    error

	streamURL  string
	streamMeta map[string]string
	streamErr  error

	// gotStreamArgs records what GetStreamURL was called with, which is the
	// actual subject of most of these tests.
	gotStreamURL  string
	gotStreamOpts []any
	gotEpisodeURL string
}

func (f *fakeScraper) SearchAnime(string, ...any) ([]*models.Anime, error) {
	return nil, errors.New("not used")
}

func (f *fakeScraper) GetAnimeEpisodes(animeURL string) ([]models.Episode, error) {
	f.gotEpisodeURL = animeURL
	return f.episodes, f.epErr
}

func (f *fakeScraper) GetStreamURL(episodeURL string, options ...any) (string, map[string]string, error) {
	f.gotStreamURL = episodeURL
	f.gotStreamOpts = options
	return f.streamURL, f.streamMeta, f.streamErr
}

func (f *fakeScraper) GetType() scraper.ScraperType { return scraper.AllAnimeType }

type fakeManager struct {
	results []*models.Anime
	sErr    error
	scr     scraper.UnifiedScraper
	scrErr  error

	gotQuery string
	gotType  *scraper.ScraperType
}

func (m *fakeManager) SearchAnime(query string, st *scraper.ScraperType) ([]*models.Anime, error) {
	m.gotQuery = query
	m.gotType = st
	return m.results, m.sErr
}

func (m *fakeManager) GetScraper(scraper.ScraperType) (scraper.UnifiedScraper, error) {
	return m.scr, m.scrErr
}

func TestSearchAnimeConvertsResults(t *testing.T) {
	t.Parallel()

	m := &fakeManager{results: []*models.Anime{{Name: "Bleach", Source: "AllAnime"}}}
	got, err := goanime.NewClientForTest(m).SearchAnime("bleach", nil)
	if err != nil {
		t.Fatalf("SearchAnime: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Bleach" {
		t.Fatalf("got %+v, want one converted anime", got)
	}
	if m.gotQuery != "bleach" {
		t.Errorf("query passed through as %q", m.gotQuery)
	}
	// A nil source means "every source", and must stay nil rather than
	// defaulting to whichever source happens to be first.
	if m.gotType != nil {
		t.Errorf("scraper type = %v, want nil for an all-source search", *m.gotType)
	}
}

func TestSearchAnimeNarrowsToOneSource(t *testing.T) {
	t.Parallel()

	m := &fakeManager{}
	src := types.SourceAnimeFire
	if _, err := goanime.NewClientForTest(m).SearchAnime("naruto", &src); err != nil {
		t.Fatalf("SearchAnime: %v", err)
	}
	if m.gotType == nil {
		t.Fatal("a specific source must reach the manager as a non-nil scraper type")
	}
	if *m.gotType != scraper.AnimefireType {
		t.Errorf("scraper type = %v, want AnimefireType", *m.gotType)
	}
}

func TestSearchAnimePropagatesFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("every source failed")
	got, err := goanime.NewClientForTest(&fakeManager{sErr: want}).SearchAnime("x", nil)
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want it wrapped or returned unchanged", err)
	}
	if got != nil {
		t.Errorf("results = %+v, want nil alongside an error", got)
	}
}

// AllAnime addresses episodes by anime ID, not by a per-episode URL, so the
// client rewrites every episode's URL to the anime's. Losing this makes the
// stream lookup fail later, far from the cause.
func TestGetAnimeEpisodesRewritesTheURLForAllAnime(t *testing.T) {
	t.Parallel()

	f := &fakeScraper{episodes: []models.Episode{{Number: "1", URL: "ep1"}, {Number: "2", URL: "ep2"}}}
	got, err := goanime.NewClientForTest(&fakeManager{scr: f}).
		GetAnimeEpisodes("anime-id-123", types.SourceAllAnime)
	if err != nil {
		t.Fatalf("GetAnimeEpisodes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d episodes, want 2", len(got))
	}
	for i, ep := range got {
		if ep.URL != "anime-id-123" {
			t.Errorf("episode %d URL = %q, want the anime id", i, ep.URL)
		}
	}
}

// AnimeFire episode URLs are real per-episode pages and must survive intact.
func TestGetAnimeEpisodesKeepsTheURLForAnimeFire(t *testing.T) {
	t.Parallel()

	f := &fakeScraper{episodes: []models.Episode{{Number: "1", URL: "https://example.invalid/ep1"}}}
	got, err := goanime.NewClientForTest(&fakeManager{scr: f}).
		GetAnimeEpisodes("https://example.invalid/anime", types.SourceAnimeFire)
	if err != nil {
		t.Fatalf("GetAnimeEpisodes: %v", err)
	}
	if got[0].URL != "https://example.invalid/ep1" {
		t.Errorf("URL = %q, want the episode page untouched", got[0].URL)
	}
	if f.gotEpisodeURL != "https://example.invalid/anime" {
		t.Errorf("scraper received %q, want the anime URL", f.gotEpisodeURL)
	}
}

func TestGetAnimeEpisodesPropagatesFailures(t *testing.T) {
	t.Parallel()

	noScraper := errors.New("no adapter")
	if _, err := goanime.NewClientForTest(&fakeManager{scrErr: noScraper}).
		GetAnimeEpisodes("x", types.SourceAllAnime); !errors.Is(err, noScraper) {
		t.Errorf("err = %v, want the GetScraper failure", err)
	}

	scrapeFailed := errors.New("episode list unavailable")
	if _, err := goanime.NewClientForTest(&fakeManager{scr: &fakeScraper{epErr: scrapeFailed}}).
		GetAnimeEpisodes("x", types.SourceAllAnime); !errors.Is(err, scrapeFailed) {
		t.Errorf("err = %v, want the scraper failure", err)
	}
}

func TestDefaultStreamOptions(t *testing.T) {
	t.Parallel()

	opts := goanime.DefaultStreamOptions()
	if opts.Quality != "best" || opts.Mode != "sub" {
		t.Errorf("defaults = %+v, want best/sub", opts)
	}
}

// AllAnime needs four arguments in a fixed order: anime id, episode number,
// quality, mode. This is the call the whole StreamOptions struct exists to
// build.
func TestGetEpisodeStreamURLBuildsTheAllAnimeCall(t *testing.T) {
	t.Parallel()

	f := &fakeScraper{streamURL: "https://cdn.invalid/v.m3u8"}
	anime := &types.Anime{Source: "AllAnime", URL: "anime-id-9"}
	episode := &types.Episode{Number: "7", URL: "ignored-for-allanime"}

	url, _, err := goanime.NewClientForTest(&fakeManager{scr: f}).
		GetEpisodeStreamURL(anime, episode, &goanime.StreamOptions{Quality: "720p", Mode: "dub"})
	if err != nil {
		t.Fatalf("GetEpisodeStreamURL: %v", err)
	}
	if url != "https://cdn.invalid/v.m3u8" {
		t.Errorf("url = %q", url)
	}
	if f.gotStreamURL != "anime-id-9" {
		t.Errorf("addressed %q, want the anime id", f.gotStreamURL)
	}
	if len(f.gotStreamOpts) != 3 {
		t.Fatalf("passed %d options, want episode/quality/mode: %v", len(f.gotStreamOpts), f.gotStreamOpts)
	}
	if f.gotStreamOpts[0] != "7" || f.gotStreamOpts[1] != "720p" || f.gotStreamOpts[2] != "dub" {
		t.Errorf("options = %v, want [7 720p dub]", f.gotStreamOpts)
	}
}

// A nil StreamOptions must mean the defaults, and a partly-filled one must
// only override the fields it actually set — an empty Quality is "unset",
// not a request for an empty quality string.
func TestGetEpisodeStreamURLFillsInDefaults(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		opts        *goanime.StreamOptions
		wantQuality string
		wantMode    string
	}{
		{"nil options", nil, "best", "sub"},
		{"empty struct", &goanime.StreamOptions{}, "best", "sub"},
		{"quality only", &goanime.StreamOptions{Quality: "480p"}, "480p", "sub"},
		{"mode only", &goanime.StreamOptions{Mode: "dub"}, "best", "dub"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeScraper{}
			anime := &types.Anime{Source: "AllAnime", URL: "id"}
			if _, _, err := goanime.NewClientForTest(&fakeManager{scr: f}).
				GetEpisodeStreamURL(anime, &types.Episode{Number: "1"}, tc.opts); err != nil {
				t.Fatalf("GetEpisodeStreamURL: %v", err)
			}
			if f.gotStreamOpts[1] != tc.wantQuality || f.gotStreamOpts[2] != tc.wantMode {
				t.Errorf("options = %v, want quality %q mode %q",
					f.gotStreamOpts, tc.wantQuality, tc.wantMode)
			}
		})
	}
}

// AnimeFire takes the episode URL and nothing else. Passing the AllAnime
// argument list here would be silently wrong.
func TestGetEpisodeStreamURLBuildsTheAnimeFireCall(t *testing.T) {
	t.Parallel()

	f := &fakeScraper{}
	anime := &types.Anime{Source: "AnimeFire", URL: "https://example.invalid/anime"}
	episode := &types.Episode{Number: "3", URL: "https://example.invalid/anime/3"}

	if _, _, err := goanime.NewClientForTest(&fakeManager{scr: f}).
		GetEpisodeStreamURL(anime, episode, nil); err != nil {
		t.Fatalf("GetEpisodeStreamURL: %v", err)
	}
	if f.gotStreamURL != "https://example.invalid/anime/3" {
		t.Errorf("addressed %q, want the episode URL", f.gotStreamURL)
	}
	if len(f.gotStreamOpts) != 0 {
		t.Errorf("passed %v, want no extra options for AnimeFire", f.gotStreamOpts)
	}
}

// The Source string comes back from a scraper result, so an unrecognised one
// has to be an error rather than a silent fallback to AllAnime.
func TestGetEpisodeStreamURLRejectsAnUnknownSource(t *testing.T) {
	t.Parallel()

	_, _, err := goanime.NewClientForTest(&fakeManager{scr: &fakeScraper{}}).
		GetEpisodeStreamURL(&types.Anime{Source: "Goyabu"}, &types.Episode{Number: "1"}, nil)
	if err == nil {
		t.Fatal("a source the public API cannot address must fail loudly")
	}
}

func TestGetStreamURLDelegates(t *testing.T) {
	t.Parallel()

	f := &fakeScraper{streamURL: "u", streamMeta: map[string]string{"quality": "1080p"}}
	url, meta, err := goanime.NewClientForTest(&fakeManager{scr: f}).
		GetStreamURL("https://example.invalid/ep", types.SourceAnimeFire)
	if err != nil {
		t.Fatalf("GetStreamURL: %v", err)
	}
	if url != "u" || meta["quality"] != "1080p" {
		t.Errorf("url=%q meta=%v", url, meta)
	}
	if f.gotStreamURL != "https://example.invalid/ep" {
		t.Errorf("addressed %q", f.gotStreamURL)
	}

	noScraper := errors.New("no adapter")
	if _, _, err := goanime.NewClientForTest(&fakeManager{scrErr: noScraper}).
		GetStreamURL("x", types.SourceAllAnime); !errors.Is(err, noScraper) {
		t.Errorf("err = %v, want the GetScraper failure", err)
	}
}
