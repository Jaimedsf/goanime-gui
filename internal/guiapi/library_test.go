package guiapi

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withTempLibrary points the library at a throwaway file and resets the
// in-memory cache, so these tests never touch the real ~/.local/goanime
// data. Not parallel-safe: the library is package-level state.
func withTempLibrary(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // os.UserHomeDir on Windows

	libMu.Lock()
	libCache = nil
	libLoaded = false
	libMu.Unlock()

	t.Cleanup(func() {
		libMu.Lock()
		libCache = nil
		libLoaded = false
		libMu.Unlock()
	})
}

func sampleResult() SearchResult {
	return SearchResult{
		Name:   "Cowboy Bebop",
		URL:    "https://example.test/cowboy-bebop",
		Source: "AllAnime",
	}
}

func TestToggleFavoriteRoundTrips(t *testing.T) {
	withTempLibrary(t)
	r := sampleResult()

	if IsFavorite(r) {
		t.Fatal("a fresh library should have no favorites")
	}

	on, err := ToggleFavorite(r)
	if err != nil {
		t.Fatalf("ToggleFavorite: %v", err)
	}
	if !on || !IsFavorite(r) {
		t.Fatal("toggling once should add the favorite")
	}

	favs := Favorites()
	if len(favs) != 1 || favs[0].Result.Name != r.Name {
		t.Fatalf("Favorites() = %+v", favs)
	}

	off, err := ToggleFavorite(r)
	if err != nil {
		t.Fatalf("ToggleFavorite: %v", err)
	}
	if off || IsFavorite(r) {
		t.Fatal("toggling twice should remove the favorite")
	}
	if len(Favorites()) != 0 {
		t.Fatal("favorites should be empty again")
	}
}

// Favorites must survive a restart, which is the whole point of writing
// them to disk rather than to the webview's localStorage.
func TestFavoritesPersistAcrossReload(t *testing.T) {
	withTempLibrary(t)
	r := sampleResult()

	if _, err := ToggleFavorite(r); err != nil {
		t.Fatalf("ToggleFavorite: %v", err)
	}

	// Drop the cache: the next read has to come off disk.
	libMu.Lock()
	libCache = nil
	libLoaded = false
	libMu.Unlock()

	if !IsFavorite(r) {
		t.Fatal("favorite did not survive a reload")
	}
}

func TestFavoriteKeysMatchesTitleKey(t *testing.T) {
	withTempLibrary(t)
	r := sampleResult()

	if _, err := ToggleFavorite(r); err != nil {
		t.Fatalf("ToggleFavorite: %v", err)
	}

	keys := FavoriteKeys()
	if len(keys) != 1 {
		t.Fatalf("FavoriteKeys() = %v", keys)
	}
	if keys[0] != TitleKey(r) {
		t.Fatalf("key %q does not match TitleKey %q", keys[0], TitleKey(r))
	}
}

// The frontend rebuilds the key as "source|url" in lowercase to mark cards
// without a call each. If that format ever changes, stars silently stop
// lighting up — so pin it here.
func TestTitleKeyFormat(t *testing.T) {
	t.Parallel()

	got := TitleKey(SearchResult{Source: "AllAnime", URL: "ABC123"})
	if got != "allanime|abc123" {
		t.Fatalf("TitleKey = %q, want %q", got, "allanime|abc123")
	}

	// No URL: fall back to the normalised name so the entry is still stable.
	noURL := TitleKey(SearchResult{Source: "Goyabu", Name: "Naruto [PT-BR]"})
	if noURL != "goyabu|name:naruto" {
		t.Fatalf("name-based TitleKey = %q", noURL)
	}
}

func TestRecordWatchDeduplicatesAndOrders(t *testing.T) {
	withTempLibrary(t)
	r := sampleResult()

	if err := RecordWatch(r, EpisodeResult{Number: "1", Num: 1}); err != nil {
		t.Fatalf("RecordWatch: %v", err)
	}
	if err := RecordWatch(r, EpisodeResult{Number: "2", Num: 2}); err != nil {
		t.Fatalf("RecordWatch: %v", err)
	}
	// Re-watching episode 1 must move it up, not duplicate it.
	time.Sleep(2 * time.Millisecond)
	if err := RecordWatch(r, EpisodeResult{Number: "1", Num: 1}); err != nil {
		t.Fatalf("RecordWatch: %v", err)
	}

	hist := History()
	if len(hist) != 2 {
		t.Fatalf("history has %d entries, want 2 (re-watch should dedupe)", len(hist))
	}
	if hist[0].EpisodeNumber != "1" {
		t.Fatalf("most recent entry is episode %q, want 1", hist[0].EpisodeNumber)
	}
}

// Two seasons both numbered from 1 must not collide.
func TestRecordWatchSeparatesSeasons(t *testing.T) {
	withTempLibrary(t)
	r := sampleResult()

	if err := RecordWatch(r, EpisodeResult{Number: "1", SeasonID: "1"}); err != nil {
		t.Fatalf("RecordWatch: %v", err)
	}
	if err := RecordWatch(r, EpisodeResult{Number: "1", SeasonID: "2"}); err != nil {
		t.Fatalf("RecordWatch: %v", err)
	}

	if got := len(History()); got != 2 {
		t.Fatalf("history has %d entries, want 2 — seasons collided", got)
	}

	watched := WatchedEpisodes(r)
	if len(watched) != 2 {
		t.Fatalf("WatchedEpisodes = %v, want two distinct keys", watched)
	}
}

func TestWatchedEpisodesOnlyMatchesItsTitle(t *testing.T) {
	withTempLibrary(t)

	a := sampleResult()
	b := SearchResult{Name: "Other", URL: "https://example.test/other", Source: "AllAnime"}

	if err := RecordWatch(a, EpisodeResult{Number: "1"}); err != nil {
		t.Fatalf("RecordWatch: %v", err)
	}
	if err := RecordWatch(b, EpisodeResult{Number: "9"}); err != nil {
		t.Fatalf("RecordWatch: %v", err)
	}

	got := WatchedEpisodes(a)
	if len(got) != 1 || got[0] != "1" {
		t.Fatalf("WatchedEpisodes(a) = %v, want [1]", got)
	}
}

func TestRecentlyWatchedIsOnePerTitle(t *testing.T) {
	withTempLibrary(t)
	r := sampleResult()

	for _, n := range []string{"1", "2", "3"} {
		if err := RecordWatch(r, EpisodeResult{Number: n}); err != nil {
			t.Fatalf("RecordWatch: %v", err)
		}
	}

	recent := RecentlyWatched(10)
	if len(recent) != 1 {
		t.Fatalf("RecentlyWatched returned %d entries, want 1 per title", len(recent))
	}
	if recent[0].EpisodeNumber != "3" {
		t.Fatalf("recent entry is episode %q, want the latest (3)", recent[0].EpisodeNumber)
	}
}

func TestClearHistoryKeepsFavorites(t *testing.T) {
	withTempLibrary(t)
	r := sampleResult()

	if _, err := ToggleFavorite(r); err != nil {
		t.Fatalf("ToggleFavorite: %v", err)
	}
	if err := RecordWatch(r, EpisodeResult{Number: "1"}); err != nil {
		t.Fatalf("RecordWatch: %v", err)
	}

	if err := ClearHistory(); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	if len(History()) != 0 {
		t.Fatal("history should be empty")
	}
	if !IsFavorite(r) {
		t.Fatal("clearing history must not touch favorites")
	}
}

func TestForgetTitleDropsOnlyThatTitle(t *testing.T) {
	withTempLibrary(t)

	a := sampleResult()
	b := SearchResult{Name: "Other", URL: "https://example.test/other", Source: "AllAnime"}

	if err := RecordWatch(a, EpisodeResult{Number: "1"}); err != nil {
		t.Fatalf("RecordWatch: %v", err)
	}
	if err := RecordWatch(b, EpisodeResult{Number: "1"}); err != nil {
		t.Fatalf("RecordWatch: %v", err)
	}

	if err := ForgetTitle(TitleKey(a)); err != nil {
		t.Fatalf("ForgetTitle: %v", err)
	}

	hist := History()
	if len(hist) != 1 || hist[0].Key != TitleKey(b) {
		t.Fatalf("history after ForgetTitle = %+v", hist)
	}
}

// A corrupt file must not brick the app: the user starts empty instead.
func TestCorruptLibraryIsTolerated(t *testing.T) {
	withTempLibrary(t)

	path := libraryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	libMu.Lock()
	libCache = nil
	libLoaded = false
	libMu.Unlock()

	if len(Favorites()) != 0 || len(History()) != 0 {
		t.Fatal("a corrupt library should read as empty")
	}

	// And it must still be writable afterwards.
	if _, err := ToggleFavorite(sampleResult()); err != nil {
		t.Fatalf("ToggleFavorite after corruption: %v", err)
	}
	if len(Favorites()) != 1 {
		t.Fatal("could not recover from a corrupt library")
	}
}

func TestHistoryIsCapped(t *testing.T) {
	withTempLibrary(t)

	// One extra beyond the cap, each a distinct title so nothing dedupes.
	for i := 0; i < historyLimit+5; i++ {
		r := SearchResult{
			Name:   "Title",
			URL:    "https://example.test/" + string(rune('a'+i%26)) + itoa(i),
			Source: "AllAnime",
		}
		if err := RecordWatch(r, EpisodeResult{Number: "1"}); err != nil {
			t.Fatalf("RecordWatch: %v", err)
		}
	}

	if got := len(History()); got != historyLimit {
		t.Fatalf("history has %d entries, want it capped at %d", got, historyLimit)
	}
}

// itoa avoids pulling strconv in just for the loop above.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
