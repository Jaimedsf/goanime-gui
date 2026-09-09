package tracking

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alvarorichard/Goanime/internal/storage"
)

// IsCgoEnabled is retained for backwards compatibility. With pure-Go SQLite, it is always true.
var IsCgoEnabled = true

// Error constants
var (
	ErrCgoDisabled      = errors.New("CGO disabled: sqlite tracking not available")
	ErrTrackerNotInited = errors.New("tracker not initialized")
)

// Anime represents tracked media (anime, movie, or TV show).
type Anime struct {
	AnilistID     int       `json:"anilist_id"`
	AllanimeID    string    `json:"allanime_id"` // Unique ID per content/source
	EpisodeNumber int       `json:"episode_number"`
	PlaybackTime  int       `json:"playback_time"`
	Duration      int       `json:"duration"`
	Title         string    `json:"title"`
	MediaType     string    `json:"media_type"` // "anime" or "movie"
	LastUpdated   time.Time `json:"last_updated"`
}

// LocalTracker wraps the unified pure-Go storage layer for playback tracking.
type LocalTracker struct {
	store *storage.Storage
}

var (
	globalTracker     *LocalTracker
	globalTrackerPath string
	trackerMutex      = &sync.Mutex{}
)

// GetGlobalTracker returns the cached global tracker instance.
func GetGlobalTracker() *LocalTracker {
	trackerMutex.Lock()
	defer trackerMutex.Unlock()
	return globalTracker
}

// CloseGlobalTracker closes the global tracker and clears the cache.
func CloseGlobalTracker() error {
	trackerMutex.Lock()
	defer trackerMutex.Unlock()

	if globalTracker != nil {
		err := globalTracker.Close()
		globalTracker = nil
		globalTrackerPath = ""
		return err
	}
	return nil
}

// NewLocalTracker creates or opens a tracker database at dbPath.
var NewLocalTracker func(dbPath string) *LocalTracker

func newLocalTrackerImpl(dbPath string) *LocalTracker {
	trackerMutex.Lock()
	defer trackerMutex.Unlock()

	if globalTracker != nil && globalTrackerPath == dbPath {
		return globalTracker
	}

	store, err := storage.Open(dbPath)
	if err != nil {
		fmt.Printf("Error opening tracking database: %v\n", err)
		return nil
	}

	tracker := &LocalTracker{store: store}
	globalTracker = tracker
	globalTrackerPath = dbPath
	return tracker
}

// UpdateProgress persists the playback position of an anime/movie episode.
func (t *LocalTracker) UpdateProgress(a Anime) error {
	if t == nil || t.store == nil {
		return ErrTrackerNotInited
	}
	if a.Duration <= 0 {
		return fmt.Errorf("invalid duration value (%d): must be greater than 0", a.Duration)
	}
	if a.PlaybackTime < 0 {
		a.PlaybackTime = 0
	}

	mediaType := a.MediaType
	if mediaType == "" {
		mediaType = "anime"
	}

	p := storage.MediaProgress{
		MediaID:       a.AllanimeID,
		Title:         a.Title,
		MediaType:     mediaType,
		EpisodeNum:    a.EpisodeNumber,
		EpisodeNumber: fmt.Sprintf("%d", a.EpisodeNumber),
		PlaybackTime:  a.PlaybackTime,
		Duration:      a.Duration,
		AnilistID:     a.AnilistID,
		LastUpdated:   a.LastUpdated,
	}

	return t.store.SaveProgress(context.Background(), p)
}

// GetAnime retrieves tracking data for a media ID.
func (t *LocalTracker) GetAnime(anilistID int, allanimeID string) (*Anime, error) {
	if t == nil || t.store == nil {
		return nil, ErrTrackerNotInited
	}

	p, err := t.store.GetProgress(context.Background(), allanimeID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &Anime{
		AnilistID:     p.AnilistID,
		AllanimeID:    p.MediaID,
		EpisodeNumber: p.EpisodeNum,
		PlaybackTime:  p.PlaybackTime,
		Duration:      p.Duration,
		Title:         p.Title,
		MediaType:     p.MediaType,
		LastUpdated:   p.LastUpdated,
	}, nil
}

// GetAllAnime retrieves all tracked media.
func (t *LocalTracker) GetAllAnime() ([]Anime, error) {
	if t == nil || t.store == nil {
		return nil, ErrTrackerNotInited
	}

	progressList, err := t.store.GetRecentProgress(context.Background(), 500)
	if err != nil {
		return nil, err
	}

	out := make([]Anime, len(progressList))
	for i, p := range progressList {
		out[i] = Anime{
			AnilistID:     p.AnilistID,
			AllanimeID:    p.MediaID,
			EpisodeNumber: p.EpisodeNum,
			PlaybackTime:  p.PlaybackTime,
			Duration:      p.Duration,
			Title:         p.Title,
			MediaType:     p.MediaType,
			LastUpdated:   p.LastUpdated,
		}
	}
	return out, nil
}

// DeleteAnime removes tracking data for a media ID.
func (t *LocalTracker) DeleteAnime(anilistID int, allanimeID string) error {
	if t == nil || t.store == nil {
		return ErrTrackerNotInited
	}
	return t.store.DeleteProgress(context.Background(), allanimeID)
}

// Close closes the underlying storage.
func (t *LocalTracker) Close() error {
	if t == nil || t.store == nil {
		return nil
	}
	return t.store.Close()
}

func init() {
	IsCgoEnabled = true
	NewLocalTracker = newLocalTrackerImpl
}
