package guiapi

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMain points the metadata cache at a throwaway file for the whole
// package run. Without it a unit run would read — and write — the real
// ~/.cache/goanime file, so a developer's browsing history could change
// what the tests see.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "goanime-metacache-*")
	if err != nil {
		panic(err)
	}
	metaCachePathOverride = filepath.Join(dir, "anilist-metadata.json")

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// resetMetaStore empties the store and repoints it, so a test can prove
// something survives a "restart" by reading the file back from scratch.
func resetMetaStore(t *testing.T, path string) {
	t.Helper()
	metaCache.mu.Lock()
	defer metaCache.mu.Unlock()

	if metaCache.flush != nil {
		metaCache.flush.Stop()
		metaCache.flush = nil
	}
	metaCachePathOverride = path
	metaCache.loaded = false
	metaCache.media = nil
	metaCache.covers = nil
}

// metaCacheForget drops one key, so seeded entries do not leak between
// tests sharing the process-wide store.
func metaCacheForget(t *testing.T, key string) {
	t.Helper()
	metaCache.mu.Lock()
	defer metaCache.mu.Unlock()
	delete(metaCache.media, key)
	delete(metaCache.covers, key)
}

// The whole point of the file: what one session learned, the next one
// starts with. A cover resolved before a restart must not cost a request
// after it.
func TestMetaCacheSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	resetMetaStore(t, path)

	metaCachePutMedia("cowboy bebop", cachedMedia{
		Info:   TitleInfo{Status: "FINISHED", Year: "1998"},
		Cover:  "https://example.test/bebop.jpg",
		Thumbs: map[string]string{"1": "https://example.test/ep1.jpg"},
	})
	metaCachePutCover("lain", "https://example.test/lain.jpg", false)
	FlushMetadataCache()

	// A fresh store, as if the app had just been opened again.
	resetMetaStore(t, path)

	got, ok := metaCacheMedia("cowboy bebop")
	if !ok {
		t.Fatal("the media entry did not survive the restart")
	}
	if got.Cover != "https://example.test/bebop.jpg" {
		t.Errorf("cover = %q", got.Cover)
	}
	if got.Thumbs["1"] != "https://example.test/ep1.jpg" {
		t.Errorf("thumbs = %v", got.Thumbs)
	}
	if cover, ok := metaCacheCover("lain"); !ok || cover.URL != "https://example.test/lain.jpg" {
		t.Errorf("cover entry = %+v, ok = %v", cover, ok)
	}
}

// A failed call must never be written down as "this title does not exist":
// one offline launch would otherwise blank those cards for days.
func TestMetaCacheNeverPersistsTransientFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	resetMetaStore(t, path)

	metaCachePutMedia("offline title", cachedMedia{Missing: true, transient: true})
	metaCachePutCover("offline cover", "", true)

	// It still answers within the session, so a page of cards does not
	// retry against an API that is down.
	if _, ok := metaCacheMedia("offline title"); !ok {
		t.Error("a transient entry should still answer in-process")
	}
	FlushMetadataCache()

	resetMetaStore(t, path)
	if _, ok := metaCacheMedia("offline title"); ok {
		t.Error("a transient failure was written to disk")
	}
	if _, ok := metaCacheCover("offline cover"); ok {
		t.Error("a transient cover miss was written to disk")
	}
}

// A definitive 404, by contrast, is an answer and is worth keeping: the
// PT-BR scrapers produce plenty of names AniList will never resolve.
func TestMetaCachePersistsDefinitiveMisses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	resetMetaStore(t, path)

	metaCachePutMedia("no such title", cachedMedia{Missing: true})
	FlushMetadataCache()

	resetMetaStore(t, path)
	got, ok := metaCacheMedia("no such title")
	if !ok {
		t.Fatal("a definitive miss should survive the restart")
	}
	if !got.Missing {
		t.Error("the entry came back without its Missing flag")
	}
}

// An airing show gains an episode every week; a finished one does not.
// Serving both for a month would leave the episode grid short.
func TestMetaCacheTTLTracksStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		entry cachedMedia
		want  time.Duration
	}{
		{"finished", cachedMedia{Info: TitleInfo{Status: "FINISHED"}}, ttlFinished},
		{"cancelled", cachedMedia{Info: TitleInfo{Status: "CANCELLED"}}, ttlFinished},
		{"airing", cachedMedia{Info: TitleInfo{Status: "RELEASING"}}, ttlAiring},
		{"unaired", cachedMedia{Info: TitleInfo{Status: "NOT_YET_RELEASED"}}, ttlAiring},
		{"unknown status", cachedMedia{}, ttlUnknown},
		{"missing", cachedMedia{Missing: true}, ttlMissing},
		{"transient", cachedMedia{Missing: true, transient: true}, ttlTransient},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.entry.ttl(); got != tt.want {
				t.Errorf("ttl = %v, want %v", got, tt.want)
			}
		})
	}
}

// An entry past its TTL must not be served, and must not be reloaded.
func TestMetaCacheDropsExpiredEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	resetMetaStore(t, path)

	// Reach past the accessors to backdate the entry: the point is what
	// happens to something written a long time ago.
	metaCachePutMedia("stale", cachedMedia{Info: TitleInfo{Status: "RELEASING"}})
	metaCache.mu.Lock()
	e := metaCache.media["stale"]
	e.FetchedAt = time.Now().Add(-ttlAiring - time.Hour)
	metaCache.media["stale"] = e
	metaCache.mu.Unlock()

	if _, ok := metaCacheMedia("stale"); ok {
		t.Error("an expired entry was served")
	}

	FlushMetadataCache()
	resetMetaStore(t, path)
	if _, ok := metaCacheMedia("stale"); ok {
		t.Error("an expired entry was written back to disk")
	}
}

// A cache is disposable: a file from an older layout, or a corrupt one,
// must cost a cold launch rather than break one.
func TestMetaCacheToleratesAnUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	resetMetaStore(t, path)
	if _, ok := metaCacheMedia("anything"); ok {
		t.Error("a corrupt file should read as an empty cache")
	}

	if err := os.WriteFile(path, []byte(`{"version":0,"media":{"x":{"cover":"c"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resetMetaStore(t, path)
	if _, ok := metaCacheMedia("x"); ok {
		t.Error("a file from an older version should be discarded, not read")
	}
}

// The file must not grow without bound as the user browses.
func TestEvictOldestKeepsTheNewest(t *testing.T) {
	t.Parallel()

	now := time.Now()
	m := map[string]cachedCover{}
	for i := range metaMaxEntries + 10 {
		// Older keys get older timestamps, so the ten dropped are known.
		m[string(rune('a'+i%26))+string(rune(i))] = cachedCover{
			FetchedAt: now.Add(-time.Duration(i) * time.Minute),
		}
	}
	total := len(m)

	evictOldest(m, func(e cachedCover) time.Time { return e.FetchedAt })
	if len(m) != metaMaxEntries {
		t.Fatalf("kept %d of %d, want %d", len(m), total, metaMaxEntries)
	}

	cutoff := now.Add(-time.Duration(metaMaxEntries) * time.Minute)
	for k, e := range m {
		if e.FetchedAt.Before(cutoff) {
			t.Fatalf("kept %q, fetched %v — older than the cutoff %v", k, e.FetchedAt, cutoff)
		}
	}
}

// The thumbnail map travels to the frontend; the cache must not hand out
// the copy it keeps.
func TestMetaCacheDoesNotShareItsThumbnailMap(t *testing.T) {
	resetMetaStore(t, filepath.Join(t.TempDir(), "cache.json"))
	t.Cleanup(func() { metaCacheForget(t, "shared thumbs") })

	seeded := map[string]string{"1": "one"}
	metaCachePutMedia("shared thumbs", cachedMedia{Thumbs: seeded})
	seeded["1"] = "mutated after the put"

	got, ok := metaCacheMedia("shared thumbs")
	if !ok {
		t.Fatal("entry missing")
	}
	if got.Thumbs["1"] != "one" {
		t.Errorf("the caller's map reached into the cache: %v", got.Thumbs)
	}

	got.Thumbs["1"] = "mutated after the get"
	again, _ := metaCacheMedia("shared thumbs")
	if again.Thumbs["1"] != "one" {
		t.Errorf("a returned map reached into the cache: %v", again.Thumbs)
	}
}
