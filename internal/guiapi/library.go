package guiapi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// historyLimit caps how many watch entries are kept. The panel shows the
// most recent handful; the rest exist so "already watched" marks survive a
// long backlog without the file growing without bound.
const historyLimit = 300

// FavoriteItem is a bookmarked title.
type FavoriteItem struct {
	Result  SearchResult `json:"result"`
	AddedAt time.Time    `json:"addedAt"`
}

// HistoryEntry is one episode the user actually started playing.
type HistoryEntry struct {
	// Key identifies the title, so the frontend can group and delete.
	Key           string       `json:"key"`
	Result        SearchResult `json:"result"`
	EpisodeNumber string       `json:"episodeNumber"`
	EpisodeNum    int          `json:"episodeNum"`
	EpisodeTitle  string       `json:"episodeTitle"`
	SeasonID      string       `json:"seasonID"`
	WatchedAt     time.Time    `json:"watchedAt"`
}

// library is the on-disk shape. Keeping both lists in one file means one
// atomic write per mutation and no chance of the two drifting apart.
type library struct {
	Favorites map[string]FavoriteItem `json:"favorites"`
	History   []HistoryEntry          `json:"history"`
}

var (
	libMu     sync.Mutex
	libCache  *library
	libLoaded bool
)

// libraryPath is where favorites and history live. It sits next to the
// downloads directory under ~/.local/goanime, following the layout the CLI
// already uses, and is deliberately plain JSON: no cgo, no SQLite, and the
// user can read or delete it by hand.
func libraryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to the working directory rather than losing the data.
		return "goanime-gui-library.json"
	}
	return filepath.Join(home, ".local", "goanime", "gui-library.json")
}

// titleKey identifies a title across sessions. The URL is the stable
// identifier where a source provides one (AllAnime IDs, TMDB IDs); a few
// results carry only a name, so that is the fallback.
func titleKey(r SearchResult) string {
	src := strings.ToLower(strings.TrimSpace(r.Source))
	if u := strings.TrimSpace(r.URL); u != "" {
		return src + "|" + strings.ToLower(u)
	}
	return src + "|name:" + strings.ToLower(normalizeTitle(r.Name))
}

// episodeKey identifies one episode within a title, season included so two
// seasons numbered from 1 do not collide.
func episodeKey(ep EpisodeResult) string {
	if ep.SeasonID != "" {
		return ep.SeasonID + ":" + ep.Number
	}
	return ep.Number
}

// loadLocked reads the library from disk once. A missing or corrupt file is
// not an error: the user simply starts with an empty library rather than
// being blocked by a bad read.
func loadLocked() *library {
	if libLoaded && libCache != nil {
		return libCache
	}
	libLoaded = true
	libCache = &library{Favorites: map[string]FavoriteItem{}}

	data, err := os.ReadFile(libraryPath())
	if err != nil {
		return libCache
	}

	var parsed library
	if err := json.Unmarshal(data, &parsed); err != nil {
		return libCache
	}
	if parsed.Favorites == nil {
		parsed.Favorites = map[string]FavoriteItem{}
	}
	libCache = &parsed
	return libCache
}

// saveLocked writes the library atomically: a temp file in the same
// directory, then a rename. A crash mid-write leaves the previous version
// intact instead of a truncated file.
func saveLocked() error {
	if libCache == nil {
		return nil
	}

	path := libraryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("não foi possível criar a pasta da biblioteca: %w", err)
	}

	data, err := json.MarshalIndent(libCache, "", "  ")
	if err != nil {
		return fmt.Errorf("não foi possível gravar a biblioteca: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".gui-library-*.tmp")
	if err != nil {
		return fmt.Errorf("não foi possível criar um arquivo temporário: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("não foi possível escrever a biblioteca: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("não foi possível fechar o arquivo temporário: %w", err)
	}

	// Windows will not rename onto an existing file.
	_ = os.Remove(path)
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("não foi possível salvar a biblioteca: %w", err)
	}
	return nil
}

// --- favorites -----------------------------------------------------------

// Favorites returns the bookmarked titles, most recently added first.
func Favorites() []FavoriteItem {
	libMu.Lock()
	defer libMu.Unlock()

	lib := loadLocked()
	out := make([]FavoriteItem, 0, len(lib.Favorites))
	for _, f := range lib.Favorites {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].AddedAt.After(out[j].AddedAt)
	})
	return out
}

// IsFavorite reports whether a title is bookmarked.
func IsFavorite(r SearchResult) bool {
	libMu.Lock()
	defer libMu.Unlock()

	_, ok := loadLocked().Favorites[titleKey(r)]
	return ok
}

// ToggleFavorite adds or removes a bookmark and returns the resulting state
// (true when the title is now a favorite).
func ToggleFavorite(r SearchResult) (bool, error) {
	libMu.Lock()
	defer libMu.Unlock()

	lib := loadLocked()
	key := titleKey(r)

	if _, exists := lib.Favorites[key]; exists {
		delete(lib.Favorites, key)
		return false, saveLocked()
	}

	lib.Favorites[key] = FavoriteItem{Result: r, AddedAt: time.Now()}
	return true, saveLocked()
}

// FavoriteKeys returns the keys of every bookmarked title, so the frontend
// can mark a whole result grid without a call per card.
func FavoriteKeys() []string {
	libMu.Lock()
	defer libMu.Unlock()

	lib := loadLocked()
	out := make([]string, 0, len(lib.Favorites))
	for k := range lib.Favorites {
		out = append(out, k)
	}
	return out
}

// TitleKey exposes the identifier the frontend uses to match a search
// result against FavoriteKeys.
func TitleKey(r SearchResult) string {
	return titleKey(r)
}

// --- history -------------------------------------------------------------

// History returns watch entries, most recent first.
func History() []HistoryEntry {
	libMu.Lock()
	defer libMu.Unlock()

	lib := loadLocked()

	// Copied newest-first, then sorted *stably*. Both halves matter.
	//
	// WatchedAt is time.Now() at the moment of the write, and clocks are not
	// infinitely fine: on Windows the granularity is around 15ms, so two
	// episodes watched in quick succession land on the identical timestamp.
	// After() then reports false both ways, sort.Slice is free to order them
	// however it likes, and RecentlyWatched -- which takes the first entry
	// per title -- showed whichever one it happened to get. That is the
	// failure: the home screen listing episode 2 as the last one watched
	// when the user had just finished 3.
	//
	// lib.History is in append order, so reversing it puts later writes
	// ahead of earlier ones, and a stable sort leaves that order alone
	// wherever the timestamps tie.
	out := make([]HistoryEntry, len(lib.History))
	for i := range lib.History {
		out[len(lib.History)-1-i] = lib.History[i]
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].WatchedAt.After(out[j].WatchedAt)
	})
	return out
}

// RecentlyWatched returns at most n entries, one per title, so the home
// screen shows distinct series rather than ten episodes of the same one.
func RecentlyWatched(n int) []HistoryEntry {
	all := History()
	seen := map[string]bool{}
	out := make([]HistoryEntry, 0, n)

	for _, h := range all {
		if seen[h.Key] {
			continue
		}
		seen[h.Key] = true
		out = append(out, h)
		if len(out) >= n {
			break
		}
	}
	return out
}

// RecordWatch appends an episode to the history. Re-watching an episode
// moves the existing entry to the top instead of duplicating it.
func RecordWatch(r SearchResult, ep EpisodeResult) error {
	libMu.Lock()
	defer libMu.Unlock()

	lib := loadLocked()
	key := titleKey(r)
	epKey := episodeKey(ep)

	filtered := lib.History[:0]
	for _, h := range lib.History {
		if h.Key == key && episodeKeyOf(h) == epKey {
			continue
		}
		filtered = append(filtered, h)
	}
	lib.History = filtered

	lib.History = append(lib.History, HistoryEntry{
		Key:           key,
		Result:        r,
		EpisodeNumber: ep.Number,
		EpisodeNum:    ep.Num,
		EpisodeTitle:  ep.Title,
		SeasonID:      ep.SeasonID,
		WatchedAt:     historyNow(),
	})

	// Trim the oldest entries once the cap is exceeded.
	//
	// A plain tail slice, not a sort. Entries are only ever appended, so the
	// newest are already at the end and the last historyLimit of them are
	// exactly the ones to keep. The sort that used to do this also *left*
	// the slice in newest-first order, which quietly destroyed the append
	// order History() above depends on to break timestamp ties.
	if len(lib.History) > historyLimit {
		lib.History = lib.History[len(lib.History)-historyLimit:]
	}

	return saveLocked()
}

// historyNow is the clock RecordWatch stamps entries with. A var so tests
// can pin it and exercise what a real machine only produces by accident:
// several watches sharing one timestamp.
var historyNow = time.Now

// episodeKeyOf rebuilds the episode key from a stored entry.
func episodeKeyOf(h HistoryEntry) string {
	if h.SeasonID != "" {
		return h.SeasonID + ":" + h.EpisodeNumber
	}
	return h.EpisodeNumber
}

// WatchedEpisodes returns the episode keys already watched for a title, so
// the episode grid can mark them.
func WatchedEpisodes(r SearchResult) []string {
	libMu.Lock()
	defer libMu.Unlock()

	lib := loadLocked()
	key := titleKey(r)

	out := []string{}
	for _, h := range lib.History {
		if h.Key == key {
			out = append(out, episodeKeyOf(h))
		}
	}
	return out
}

// ForgetTitle drops every history entry for one title.
func ForgetTitle(key string) error {
	libMu.Lock()
	defer libMu.Unlock()

	lib := loadLocked()
	filtered := lib.History[:0]
	for _, h := range lib.History {
		if h.Key != key {
			filtered = append(filtered, h)
		}
	}
	lib.History = filtered
	return saveLocked()
}

// ClearHistory empties the watch history, leaving favorites untouched.
func ClearHistory() error {
	libMu.Lock()
	defer libMu.Unlock()

	loadLocked().History = nil
	return saveLocked()
}

// LibraryPath exposes the storage location so the UI can tell the user
// where their data lives.
func LibraryPath() string {
	return libraryPath()
}
