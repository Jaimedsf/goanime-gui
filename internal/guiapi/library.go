package guiapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alvarorichard/Goanime/internal/storage"
)

// historyLimit caps how many watch entries are kept.
const historyLimit = 300

// FavoriteItem is a bookmarked title.
type FavoriteItem struct {
	Result  SearchResult `json:"result"`
	AddedAt time.Time    `json:"addedAt"`
}

// HistoryEntry is one episode the user actually started playing.
type HistoryEntry struct {
	Key           string       `json:"key"`
	Result        SearchResult `json:"result"`
	EpisodeNumber string       `json:"episodeNumber"`
	EpisodeNum    int          `json:"episodeNum"`
	EpisodeTitle  string       `json:"episodeTitle"`
	SeasonID      string       `json:"seasonID"`
	WatchedAt     time.Time    `json:"watchedAt"`
}

var (
	migrationOnce sync.Once
)

// ensureMigration runs once at startup or first access to migrate old JSON library data to SQLite.
func ensureMigration() {
	migrationOnce.Do(func() {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		jsonPath := filepath.Join(home, ".local", "goanime", "gui-library.json")
		data, err := os.ReadFile(jsonPath) // #nosec G304: fixed path under the user's home directory
		if err != nil {
			return
		}

		type legacyLib struct {
			Favorites map[string]FavoriteItem `json:"favorites"`
			History   []HistoryEntry          `json:"history"`
		}

		var parsed legacyLib
		if err := json.Unmarshal(data, &parsed); err != nil {
			return
		}

		ctx := context.Background()
		store := storage.Default()

		// Import favorites
		for _, f := range parsed.Favorites {
			_ = store.AddFavorite(ctx, storage.Favorite{
				MediaID:   titleKey(f.Result),
				Source:    f.Result.Source,
				Name:      f.Result.Name,
				URL:       f.Result.URL,
				ImageURL:  f.Result.ImageURL,
				Year:      f.Result.Year,
				MediaType: f.Result.MediaType,
				AddedAt:   f.AddedAt,
			})
		}

		// Import history
		for _, h := range parsed.History {
			_ = store.SaveProgress(ctx, storage.MediaProgress{
				MediaID:       h.Key,
				Source:        h.Result.Source,
				Title:         h.Result.Name,
				MediaType:     h.Result.MediaType,
				EpisodeNumber: h.EpisodeNumber,
				EpisodeNum:    h.EpisodeNum,
				EpisodeTitle:  h.EpisodeTitle,
				SeasonID:      h.SeasonID,
				LastUpdated:   h.WatchedAt,
			})
		}

		// Safely rename the legacy json file
		_ = os.Rename(jsonPath, jsonPath+".migrated")
	})
}

// titleKey identifies a title across sessions.
func titleKey(r SearchResult) string {
	src := strings.ToLower(strings.TrimSpace(r.Source))
	if u := strings.TrimSpace(r.URL); u != "" {
		return src + "|" + strings.ToLower(u)
	}
	return src + "|name:" + strings.ToLower(normalizeTitle(r.Name))
}

// --- favorites -----------------------------------------------------------

// Favorites returns the bookmarked titles, most recently added first.
func Favorites() []FavoriteItem {
	ensureMigration()
	ensureLibraryRepair()
	favs, err := storage.Default().ListFavorites(context.Background())
	if err != nil {
		return []FavoriteItem{}
	}

	out := make([]FavoriteItem, len(favs))
	for i, f := range favs {
		out[i] = FavoriteItem{
			Result: SearchResult{
				Name:      f.Name,
				URL:       f.URL,
				ImageURL:  f.ImageURL,
				Source:    f.Source,
				Year:      f.Year,
				MediaType: f.MediaType,
			},
			AddedAt: f.AddedAt,
		}
	}
	return out
}

// IsFavorite reports whether a title is bookmarked.
func IsFavorite(r SearchResult) bool {
	ensureMigration()
	isFav, err := storage.Default().IsFavorite(context.Background(), titleKey(r))
	if err != nil {
		return false
	}
	return isFav
}

// ToggleFavorite adds or removes a bookmark and returns the resulting state.
func ToggleFavorite(r SearchResult) (bool, error) {
	ensureMigration()
	ctx := context.Background()
	store := storage.Default()
	key := titleKey(r)

	isFav, err := store.IsFavorite(ctx, key)
	if err != nil {
		return false, err
	}

	if isFav {
		if err := store.RemoveFavorite(ctx, key); err != nil {
			return false, err
		}
		return false, nil
	}

	fav := storage.Favorite{
		MediaID:   key,
		Source:    r.Source,
		Name:      r.Name,
		URL:       r.URL,
		ImageURL:  r.ImageURL,
		Year:      r.Year,
		MediaType: r.MediaType,
		AddedAt:   time.Now(),
	}
	if err := store.AddFavorite(ctx, fav); err != nil {
		return false, err
	}
	return true, nil
}

// FavoriteKeys returns the keys of every bookmarked title.
func FavoriteKeys() []string {
	ensureMigration()
	keys, err := storage.Default().GetFavoriteKeys(context.Background())
	if err != nil {
		return []string{}
	}
	return keys
}

// TitleKey exposes the identifier the frontend uses to match a search result against FavoriteKeys.
func TitleKey(r SearchResult) string {
	return titleKey(r)
}

// --- history -------------------------------------------------------------

// History returns watch entries, most recent first.
func History() []HistoryEntry {
	ensureMigration()
	ensureLibraryRepair()
	entries, err := storage.Default().GetAllHistory(context.Background(), historyLimit)
	if err != nil {
		return []HistoryEntry{}
	}

	out := make([]HistoryEntry, len(entries))
	for i, e := range entries {
		out[i] = HistoryEntry{
			Key: e.MediaID,
			Result: SearchResult{
				Name:      e.Title,
				Source:    e.Source,
				MediaType: e.MediaType,
			},
			EpisodeNumber: e.EpisodeNumber,
			EpisodeNum:    e.EpisodeNum,
			EpisodeTitle:  e.EpisodeTitle,
			SeasonID:      e.SeasonID,
			WatchedAt:     e.LastUpdated,
		}
	}
	return out
}

// RecentlyWatched returns at most n entries, one per title.
func RecentlyWatched(n int) []HistoryEntry {
	ensureMigration()
	ensureLibraryRepair()
	entries, err := storage.Default().GetRecentProgress(context.Background(), n)
	if err != nil {
		return []HistoryEntry{}
	}

	out := make([]HistoryEntry, len(entries))
	for i, e := range entries {
		out[i] = HistoryEntry{
			Key: e.MediaID,
			Result: SearchResult{
				Name:      e.Title,
				Source:    e.Source,
				MediaType: e.MediaType,
			},
			EpisodeNumber: e.EpisodeNumber,
			EpisodeNum:    e.EpisodeNum,
			EpisodeTitle:  e.EpisodeTitle,
			SeasonID:      e.SeasonID,
			WatchedAt:     e.LastUpdated,
		}
	}
	return out
}

// RecordWatch appends or updates an episode in the watch history.
func RecordWatch(r SearchResult, ep EpisodeResult) error {
	ensureMigration()
	key := titleKey(r)

	p := storage.MediaProgress{
		MediaID:       key,
		Source:        r.Source,
		Title:         r.Name,
		MediaType:     r.MediaType,
		EpisodeNumber: ep.Number,
		EpisodeNum:    ep.Num,
		EpisodeTitle:  ep.Title,
		SeasonID:      ep.SeasonID,
		LastUpdated:   historyNow(),
	}

	return storage.Default().SaveProgress(context.Background(), p)
}

// historyNow is the clock RecordWatch stamps entries with (injectable for tests).
var historyNow = time.Now

// WatchedEpisodes returns the episode keys already watched for a title.
func WatchedEpisodes(r SearchResult) []string {
	ensureMigration()
	key := titleKey(r)
	keys, err := storage.Default().GetWatchedEpisodeKeys(context.Background(), key)
	if err != nil {
		return []string{}
	}
	return keys
}

// ForgetTitle drops every history entry for one title.
func ForgetTitle(key string) error {
	ensureMigration()
	return storage.Default().DeleteProgress(context.Background(), key)
}

// ClearHistory empties the watch history, leaving favorites untouched.
func ClearHistory() error {
	ensureMigration()
	return storage.Default().ClearAllProgress(context.Background())
}

// LibraryPath exposes the storage location so the UI can tell the user where their data lives.
func LibraryPath() string {
	return storage.DefaultPath()
}
