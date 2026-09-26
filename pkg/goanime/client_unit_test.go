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
// The logic worth pinning is how a call is addressed: AnimeFire takes a
// direct episode URL and nothing else. Getting that wrong produces a
// plausible-looking call that returns the wrong stream, which no type checker
// catches.

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

func (f *fakeScraper) GetStreamURL(episodeURL string, options ...any) (streamURL string, metadata map[string]string, err error) {
	f.gotStreamURL = episodeURL
	f.gotStreamOpts = options
	return f.streamURL, f.streamMeta, f.streamErr
}

func (f *fakeScraper) GetType() scraper.ScraperType { return scraper.AnimefireType }

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

	m := &fakeManager{results: []*models.Anime{{Name: "Bleach", Source: "Animefire.io"}}}
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
		GetAnimeEpisodes("x", types.SourceAnimeFire); !errors.Is(err, noScraper) {
		t.Errorf("err = %v, want the GetScraper failure", err)
	}

	scrapeFailed := errors.New("episode list unavailable")
	if _, err := goanime.NewClientForTest(&fakeManager{scr: &fakeScraper{epErr: scrapeFailed}}).
		GetAnimeEpisodes("x", types.SourceAnimeFire); !errors.Is(err, scrapeFailed) {
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

// AnimeFire takes the episode URL and nothing else; extra options here would
// be silently wrong.
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
// has to be an error rather than a silent fallback to another source.
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
		GetStreamURL("x", types.SourceAnimeFire); !errors.Is(err, noScraper) {
		t.Errorf("err = %v, want the GetScraper failure", err)
	}
}
