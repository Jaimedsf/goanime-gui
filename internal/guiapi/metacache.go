package guiapi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// The AniList lookups behind covers, episode thumbnails and release dates
// used to live in process memory only, so every launch re-fetched all of
// them: a grid of forty cards is forty GraphQL requests against a
// rate-limited API before the first cover paints. This file gives those
// lookups a disk cache, so the second launch paints from local data.
//
// It caches the *answers* (URLs, dates), not the image bytes — those are
// still fetched by the webview from AniList's CDN.

const (
	// metaCacheVersion guards the on-disk shape. A bump discards the file
	// rather than trying to migrate: it is a cache, so the cost of throwing
	// it away is one slow launch.
	metaCacheVersion = 1

	// metaFlushDelay debounces the write. A search burst fills dozens of
	// entries in a second or two, and rewriting the whole file per entry —
	// what streamcache.go does, with its handful of entries — would not
	// scale to thousands.
	metaFlushDelay = 5 * time.Second

	// metaMaxEntries caps each map at save time, newest kept. A year of
	// browsing should not grow an unbounded file.
	metaMaxEntries = 4000
)

// How long an answer stays good. AniList data ages at very different rates:
// a finished show's cover and dates are effectively permanent, while an
// airing one gains an episode every week.
const (
	ttlFinished  = 30 * 24 * time.Hour
	ttlAiring    = 24 * time.Hour
	ttlUnknown   = 7 * 24 * time.Hour
	ttlMissing   = 72 * time.Hour
	ttlTransient = 10 * time.Minute
)

// cachedMedia is one persisted lookup.
type cachedMedia struct {
	Info   TitleInfo         `json:"info"`
	Cover  string            `json:"cover,omitempty"`
	Thumbs map[string]string `json:"thumbs,omitempty"`
	Adult  bool              `json:"adult,omitempty"`
	// Missing marks a title AniList answered 404 for — a real "no such
	// entry", worth remembering so the scrapers' undecorated oddities do
	// not cost a request on every launch.
	Missing   bool      `json:"missing,omitempty"`
	FetchedAt time.Time `json:"fetchedAt"`

	// transient marks an entry that stands in for a *failed* call rather
	// than for an answer. It keeps a burst of cards from hammering AniList
	// while it is down, and is deliberately unexported so it is never
	// written to disk: persisting it would mean one offline launch left
	// those titles blank for days.
	transient bool
}

// cachedCover is GetCover's fallback answer. It is cached separately
// because it comes from a different query, one that sometimes matches where
// the combined lookup does not.
type cachedCover struct {
	URL       string    `json:"url,omitempty"`
	FetchedAt time.Time `json:"fetchedAt"`
	transient bool
}

// ttl is how long this entry may be served before it is fetched again.
func (e cachedMedia) ttl() time.Duration {
	switch {
	case e.transient:
		return ttlTransient
	case e.Missing:
		return ttlMissing
	}
	switch e.Info.Status {
	case "FINISHED", "CANCELLED":
		return ttlFinished
	case "RELEASING", "NOT_YET_RELEASED":
		return ttlAiring
	}
	return ttlUnknown
}

func (e cachedMedia) expired(now time.Time) bool {
	return now.Sub(e.FetchedAt) > e.ttl()
}

// ttl for a cover: a URL that resolved is as stable as a finished title's,
// and an empty answer is a miss worth retrying sooner.
func (e cachedCover) ttl() time.Duration {
	switch {
	case e.transient:
		return ttlTransient
	case e.URL == "":
		return ttlMissing
	}
	return ttlFinished
}

func (e cachedCover) expired(now time.Time) bool {
	return now.Sub(e.FetchedAt) > e.ttl()
}

// metaFile is the on-disk shape.
type metaFile struct {
	Version int                    `json:"version"`
	Media   map[string]cachedMedia `json:"media,omitempty"`
	Covers  map[string]cachedCover `json:"covers,omitempty"`
}

// metaStore holds both maps and owns the file. Reads are frequent and
// cheap; the network call they spare is not. So a plain mutex is enough,
// and it is never held across a call to AniList.
type metaStore struct {
	mu     sync.Mutex
	loaded bool
	flush  *time.Timer
	media  map[string]cachedMedia
	covers map[string]cachedCover
}

var metaCache = &metaStore{}

// metaCachePathOverride lets tests point the store at a temp file, so a
// unit run never touches — or inherits — the user's real cache.
var metaCachePathOverride string

// metaCachePath is where the cache lives. It sits under the OS cache dir
// next to superflix-stream-cache.json: regenerable data, not user data, so
// it belongs there rather than in ~/.local/goanime with the library.
func metaCachePath() string {
	if metaCachePathOverride != "" {
		return metaCachePathOverride
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "goanime", "anilist-metadata.json")
}

// ensureLoadedLocked reads the file once. A missing, unreadable or corrupt
// file is not an error: the user simply gets one cold launch.
func (s *metaStore) ensureLoadedLocked() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.media = map[string]cachedMedia{}
	s.covers = map[string]cachedCover{}

	data, err := os.ReadFile(metaCachePath())
	if err != nil {
		return
	}
	var parsed metaFile
	if err := json.Unmarshal(data, &parsed); err != nil || parsed.Version != metaCacheVersion {
		return
	}

	// Drop what has already aged out, rather than carrying it in memory
	// until something happens to ask for it.
	now := time.Now()
	for k, e := range parsed.Media {
		if !e.expired(now) {
			s.media[k] = e
		}
	}
	for k, e := range parsed.Covers {
		if !e.expired(now) {
			s.covers[k] = e
		}
	}
}

// markDirtyLocked schedules a write. Every mutation pushes the deadline
// out, so a burst of lookups costs one write once it settles.
func (s *metaStore) markDirtyLocked() {
	if s.flush == nil {
		s.flush = time.AfterFunc(metaFlushDelay, FlushMetadataCache)
		return
	}
	s.flush.Reset(metaFlushDelay)
}

// saveLocked writes the cache atomically: a temp file in the same
// directory, then a rename, so a crash mid-write leaves the previous
// version intact instead of a truncated one.
func (s *metaStore) saveLocked() error {
	if !s.loaded {
		return nil
	}

	now := time.Now()
	out := metaFile{
		Version: metaCacheVersion,
		Media:   map[string]cachedMedia{},
		Covers:  map[string]cachedCover{},
	}
	for k, e := range s.media {
		if e.transient || e.expired(now) {
			continue
		}
		out.Media[k] = e
	}
	for k, e := range s.covers {
		if e.transient || e.expired(now) {
			continue
		}
		out.Covers[k] = e
	}
	evictOldest(out.Media, func(e cachedMedia) time.Time { return e.FetchedAt })
	evictOldest(out.Covers, func(e cachedCover) time.Time { return e.FetchedAt })

	path := metaCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("não foi possível criar a pasta do cache: %w", err)
	}

	data, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("não foi possível montar o cache: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".anilist-metadata-*.tmp")
	if err != nil {
		return fmt.Errorf("não foi possível criar um arquivo temporário: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("não foi possível escrever o cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("não foi possível fechar o arquivo temporário: %w", err)
	}

	// Windows will not rename onto an existing file.
	_ = os.Remove(path)
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("não foi possível salvar o cache: %w", err)
	}
	return nil
}

// evictOldest trims a map to metaMaxEntries, keeping the most recently
// fetched. Generic so both maps share one implementation.
func evictOldest[T any](m map[string]T, at func(T) time.Time) {
	if len(m) <= metaMaxEntries {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return at(m[keys[i]]).After(at(m[keys[j]]))
	})
	for _, k := range keys[metaMaxEntries:] {
		delete(m, k)
	}
}

// FlushMetadataCache writes the AniList cache to disk now. The debounced
// timer covers the normal case; this is what the GUI calls on shutdown, so
// a session's last lookups are not lost with the process.
func FlushMetadataCache() {
	metaCache.mu.Lock()
	defer metaCache.mu.Unlock()
	_ = metaCache.saveLocked()
}

// --- accessors -----------------------------------------------------------

// metaCacheMedia returns a live entry for the key, if there is one.
func metaCacheMedia(key string) (cachedMedia, bool) {
	metaCache.mu.Lock()
	defer metaCache.mu.Unlock()
	metaCache.ensureLoadedLocked()

	e, ok := metaCache.media[key]
	if !ok || e.expired(time.Now()) {
		return cachedMedia{}, false
	}
	// Hand out a copy of the thumbnails: the entry is shared by every
	// caller, and this map travels all the way to the frontend.
	e.Thumbs = copyThumbs(e.Thumbs)
	return e, true
}

// metaCachePutMedia records a lookup, stamping it as fetched now.
func metaCachePutMedia(key string, e cachedMedia) {
	e.FetchedAt = time.Now()
	e.Thumbs = copyThumbs(e.Thumbs)

	metaCache.mu.Lock()
	defer metaCache.mu.Unlock()
	metaCache.ensureLoadedLocked()

	metaCache.media[key] = e
	if !e.transient {
		metaCache.markDirtyLocked()
	}
}

func metaCacheCover(key string) (cachedCover, bool) {
	metaCache.mu.Lock()
	defer metaCache.mu.Unlock()
	metaCache.ensureLoadedLocked()

	e, ok := metaCache.covers[key]
	if !ok || e.expired(time.Now()) {
		return cachedCover{}, false
	}
	return e, true
}

func metaCachePutCover(key, url string, transient bool) {
	metaCache.mu.Lock()
	defer metaCache.mu.Unlock()
	metaCache.ensureLoadedLocked()

	metaCache.covers[key] = cachedCover{URL: url, FetchedAt: time.Now(), transient: transient}
	if !transient {
		metaCache.markDirtyLocked()
	}
}

// copyThumbs returns a private copy of an entry's thumbnail map, so a
// caller cannot reach into the cache through the map it was handed.
func copyThumbs(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
