package storage

import (
	"time"
)

// MediaProgress represents playback progress for an anime, movie, or series.
type MediaProgress struct {
	MediaID       string    `json:"media_id"`       // Canonical identifier (e.g., "animefire|slug" or "allanime|id")
	Source        string    `json:"source"`         // Source kind (e.g., "animefire", "allanime", "superflix")
	Title         string    `json:"title"`          // Media display title
	MediaType     string    `json:"media_type"`     // "anime", "movie", or "tv"
	EpisodeNumber string    `json:"episode_number"` // Display episode number ("1", "12.5", "Especial")
	EpisodeNum    int       `json:"episode_num"`    // Numeric episode number for sorting
	EpisodeTitle  string    `json:"episode_title"`  // Episode title / name
	SeasonID      string    `json:"season_id"`      // Season identifier (e.g., "1", "2", "specials")
	PlaybackTime  int       `json:"playback_time"`  // Current position in seconds
	Duration      int       `json:"duration"`       // Total duration in seconds
	AnilistID     int       `json:"anilist_id"`     // Optional AniList ID
	LastUpdated   time.Time `json:"last_updated"`   // Timestamp of last playback update
}

// Favorite represents a bookmarked title.
type Favorite struct {
	MediaID   string    `json:"media_id"`   // Canonical identifier
	Source    string    `json:"source"`     // Source kind
	Name      string    `json:"name"`       // Title name
	URL       string    `json:"url"`        // URL or TMDb ID
	ImageURL  string    `json:"image_url"`  // Poster / cover URL
	Year      string    `json:"year"`       // Release year
	MediaType string    `json:"media_type"` // "anime", "movie", or "tv"
	AddedAt   time.Time `json:"added_at"`   // When it was bookmarked
}

// CacheEntry represents a cached key-value entry with expiration.
type CacheEntry struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}
