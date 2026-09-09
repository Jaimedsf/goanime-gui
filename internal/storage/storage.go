package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Default paths and timeouts
const (
	defaultCacheSize   = -32000 // 32MB page cache
	defaultBusyTimeout = 5000   // 5 seconds
)

var (
	// ErrNotFound is returned when a requested item does not exist.
	ErrNotFound = errors.New("storage: item not found")

	defaultStorage     *Storage
	defaultStorageOnce sync.Once
	defaultStorageMu   sync.Mutex
	globalWriteSeq     atomic.Int64
)

// Storage provides unified SQLite-backed storage for GoAnime without requiring CGO.
type Storage struct {
	db *sql.DB
	mu sync.RWMutex
}

// DefaultPath returns the standard location of the SQLite database.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "goanime.db"
	}
	return filepath.Join(home, ".local", "goanime", "goanime.db")
}

// Open initializes or opens a SQLite storage at the specified path.
func Open(dbPath string) (*Storage, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("storage: failed to create data directory: %w", err)
	}

	// Use URI format for modernc.org/sqlite with WAL mode and performance pragmas
	escapedPath := filepath.ToSlash(dbPath)
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(%d)&_pragma=cache_size(%d)&_pragma=temp_store(MEMORY)",
		escapedPath, defaultBusyTimeout, defaultCacheSize)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to open database: %w", err)
	}

	db.SetMaxOpenConns(max(4, runtime.NumCPU()))
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0)

	s := &Storage{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("storage: migration failed: %w", err)
	}

	return s, nil
}

// Default returns the process-wide default Storage instance.
func Default() *Storage {
	defaultStorageMu.Lock()
	defer defaultStorageMu.Unlock()

	if defaultStorage == nil {
		defaultStorageOnce.Do(func() {
			s, err := Open(DefaultPath())
			if err != nil {
				// Fallback to in-memory if disk is unwritable
				s, _ = Open("file:goanime_mem?mode=memory&cache=shared")
			}
			defaultStorage = s
		})
	}
	return defaultStorage
}

// SetDefault sets or overrides the global default Storage (e.g. for testing).
func SetDefault(s *Storage) {
	defaultStorageMu.Lock()
	defer defaultStorageMu.Unlock()
	defaultStorage = s
}

// Close closes the underlying SQLite database connection.
func (s *Storage) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// migrate creates and updates the tables and indexes.
func (s *Storage) migrate(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS media_progress (
		media_id          TEXT NOT NULL,
		source            TEXT NOT NULL DEFAULT '',
		title             TEXT NOT NULL DEFAULT '',
		media_type        TEXT NOT NULL DEFAULT 'anime',
		episode_number    TEXT NOT NULL DEFAULT '1',
		episode_num       INTEGER NOT NULL DEFAULT 1,
		episode_title     TEXT NOT NULL DEFAULT '',
		season_id         TEXT NOT NULL DEFAULT '',
		playback_time     INTEGER NOT NULL DEFAULT 0,
		duration          INTEGER NOT NULL DEFAULT 0,
		anilist_id        INTEGER NOT NULL DEFAULT 0,
		last_updated      INTEGER NOT NULL,
		last_updated_nano INTEGER NOT NULL DEFAULT 0,
		seq               INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (media_id, season_id, episode_number)
	);

	CREATE INDEX IF NOT EXISTS idx_progress_updated ON media_progress(last_updated_nano DESC, seq DESC);
	CREATE INDEX IF NOT EXISTS idx_progress_media ON media_progress(media_id, last_updated_nano DESC, seq DESC);

	CREATE TABLE IF NOT EXISTS favorites (
		media_id   TEXT PRIMARY KEY NOT NULL,
		source     TEXT NOT NULL DEFAULT '',
		name       TEXT NOT NULL DEFAULT '',
		url        TEXT NOT NULL DEFAULT '',
		image_url  TEXT NOT NULL DEFAULT '',
		year       TEXT NOT NULL DEFAULT '',
		media_type TEXT NOT NULL DEFAULT 'anime',
		added_at   INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_favorites_added ON favorites(added_at DESC);

	CREATE TABLE IF NOT EXISTS metadata_cache (
		key        TEXT PRIMARY KEY NOT NULL,
		value      TEXT NOT NULL,
		expires_at INTEGER NOT NULL, -- Unix milliseconds; second precision expires sub-second TTLs late
		created_at INTEGER NOT NULL  -- Unix milliseconds
	);

	CREATE INDEX IF NOT EXISTS idx_cache_expires ON metadata_cache(expires_at);
	`
	_, err := s.db.ExecContext(ctx, schema)
	if err != nil {
		return err
	}

	// Try migrating legacy tables if they exist
	s.migrateLegacyProgress(ctx)
	return nil
}

// migrateLegacyProgress imports entries from old anime_progress or single-PK media_progress table.
func (s *Storage) migrateLegacyProgress(ctx context.Context) {
	var count int
	_ = s.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='anime_progress'").Scan(&count)
	if count > 0 {
		_, _ = s.db.ExecContext(ctx, `
			INSERT OR IGNORE INTO media_progress (media_id, anilist_id, episode_num, episode_number, playback_time, duration, title, media_type, last_updated, last_updated_nano, seq)
			SELECT 
				allanime_id,
				MAX(anilist_id),
				episode_number,
				CAST(episode_number AS TEXT),
				MAX(playback_time),
				MAX(duration),
				title,
				CASE WHEN title LIKE '[Movies/TV]%' OR title LIKE '[Movie]%' THEN 'movie' ELSE 'anime' END,
				MAX(last_updated),
				MAX(last_updated) * 1000000000,
				1
			FROM anime_progress
			GROUP BY allanime_id, episode_number
		`)
		_, _ = s.db.ExecContext(ctx, "DROP TABLE IF EXISTS anime_progress")
	}
}

// ----------------------------------------------------------------------------
// Media Progress (History & Resume)
// ----------------------------------------------------------------------------

// SaveProgress saves or updates the playback progress for a specific episode.
func (s *Storage) SaveProgress(ctx context.Context, p MediaProgress) error {
	if p.MediaID == "" {
		return errors.New("storage: media_id is required")
	}
	if p.LastUpdated.IsZero() {
		p.LastUpdated = time.Now()
	}
	if p.PlaybackTime < 0 {
		p.PlaybackTime = 0
	}
	if p.Duration < 0 {
		p.Duration = 0
	}
	if p.EpisodeNumber == "" {
		p.EpisodeNumber = fmt.Sprintf("%d", p.EpisodeNum)
	}

	seq := globalWriteSeq.Add(1)
	nano := p.LastUpdated.UnixNano()
	if nano <= 0 {
		nano = p.LastUpdated.Unix() * 1e9
	}

	query := `
	INSERT INTO media_progress (
		media_id, source, title, media_type, episode_number, episode_num,
		episode_title, season_id, playback_time, duration, anilist_id, last_updated, last_updated_nano, seq
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(media_id, season_id, episode_number) DO UPDATE SET
		source = CASE WHEN excluded.source != '' THEN excluded.source ELSE media_progress.source END,
		title = CASE WHEN excluded.title != '' THEN excluded.title ELSE media_progress.title END,
		media_type = CASE WHEN excluded.media_type != '' THEN excluded.media_type ELSE media_progress.media_type END,
		episode_num = excluded.episode_num,
		episode_title = excluded.episode_title,
		playback_time = excluded.playback_time,
		duration = excluded.duration,
		anilist_id = CASE WHEN excluded.anilist_id > 0 THEN excluded.anilist_id ELSE media_progress.anilist_id END,
		last_updated = excluded.last_updated,
		last_updated_nano = excluded.last_updated_nano,
		seq = excluded.seq
	`
	_, err := s.db.ExecContext(ctx, query,
		p.MediaID, p.Source, p.Title, p.MediaType, p.EpisodeNumber, p.EpisodeNum,
		p.EpisodeTitle, p.SeasonID, p.PlaybackTime, p.Duration, p.AnilistID, p.LastUpdated.Unix(),
		nano, seq,
	)
	return err
}

// GetProgress returns the most recent progress entry for a given mediaID.
func (s *Storage) GetProgress(ctx context.Context, mediaID string) (*MediaProgress, error) {
	query := `
	SELECT media_id, source, title, media_type, episode_number, episode_num,
	       episode_title, season_id, playback_time, duration, anilist_id, last_updated_nano
	FROM media_progress
	WHERE media_id = ?
	ORDER BY last_updated_nano DESC, seq DESC
	LIMIT 1
	`
	var p MediaProgress
	var nano int64
	err := s.db.QueryRowContext(ctx, query, mediaID).Scan(
		&p.MediaID, &p.Source, &p.Title, &p.MediaType, &p.EpisodeNumber, &p.EpisodeNum,
		&p.EpisodeTitle, &p.SeasonID, &p.PlaybackTime, &p.Duration, &p.AnilistID, &nano,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.LastUpdated = time.Unix(0, nano)
	return &p, nil
}

// GetEpisodeProgress returns the exact progress entry for a specific episode.
func (s *Storage) GetEpisodeProgress(ctx context.Context, mediaID, seasonID, episodeNumber string) (*MediaProgress, error) {
	query := `
	SELECT media_id, source, title, media_type, episode_number, episode_num,
	       episode_title, season_id, playback_time, duration, anilist_id, last_updated_nano
	FROM media_progress
	WHERE media_id = ? AND season_id = ? AND episode_number = ?
	`
	var p MediaProgress
	var nano int64
	err := s.db.QueryRowContext(ctx, query, mediaID, seasonID, episodeNumber).Scan(
		&p.MediaID, &p.Source, &p.Title, &p.MediaType, &p.EpisodeNumber, &p.EpisodeNum,
		&p.EpisodeTitle, &p.SeasonID, &p.PlaybackTime, &p.Duration, &p.AnilistID, &nano,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.LastUpdated = time.Unix(0, nano)
	return &p, nil
}

// GetRecentProgress returns the most recently watched distinct titles.
func (s *Storage) GetRecentProgress(ctx context.Context, limit int) ([]MediaProgress, error) {
	if limit <= 0 {
		limit = 50
	}
	// Retrieve most recent progress for distinct media_ids
	query := `
	WITH ranked AS (
		SELECT media_id, source, title, media_type, episode_number, episode_num,
		       episode_title, season_id, playback_time, duration, anilist_id, last_updated_nano, seq,
		       ROW_NUMBER() OVER (PARTITION BY media_id ORDER BY last_updated_nano DESC, seq DESC) as rn
		FROM media_progress
	)
	SELECT media_id, source, title, media_type, episode_number, episode_num,
	       episode_title, season_id, playback_time, duration, anilist_id, last_updated_nano
	FROM ranked
	WHERE rn = 1
	ORDER BY last_updated_nano DESC, seq DESC
	LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []MediaProgress
	for rows.Next() {
		var p MediaProgress
		var nano int64
		if err := rows.Scan(
			&p.MediaID, &p.Source, &p.Title, &p.MediaType, &p.EpisodeNumber, &p.EpisodeNum,
			&p.EpisodeTitle, &p.SeasonID, &p.PlaybackTime, &p.Duration, &p.AnilistID, &nano,
		); err != nil {
			return nil, err
		}
		p.LastUpdated = time.Unix(0, nano)
		list = append(list, p)
	}
	return list, rows.Err()
}

// GetAllHistory returns every watched episode entry ordered by most recent first.
func (s *Storage) GetAllHistory(ctx context.Context, limit int) ([]MediaProgress, error) {
	if limit <= 0 {
		limit = 300
	}
	query := `
	SELECT media_id, source, title, media_type, episode_number, episode_num,
	       episode_title, season_id, playback_time, duration, anilist_id, last_updated_nano
	FROM media_progress
	ORDER BY last_updated_nano DESC, seq DESC
	LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []MediaProgress
	for rows.Next() {
		var p MediaProgress
		var nano int64
		if err := rows.Scan(
			&p.MediaID, &p.Source, &p.Title, &p.MediaType, &p.EpisodeNumber, &p.EpisodeNum,
			&p.EpisodeTitle, &p.SeasonID, &p.PlaybackTime, &p.Duration, &p.AnilistID, &nano,
		); err != nil {
			return nil, err
		}
		p.LastUpdated = time.Unix(0, nano)
		list = append(list, p)
	}
	return list, rows.Err()
}

// GetWatchedEpisodeKeys returns a set of episode keys (e.g. "1", "season:ep") watched for a mediaID.
func (s *Storage) GetWatchedEpisodeKeys(ctx context.Context, mediaID string) ([]string, error) {
	query := `SELECT season_id, episode_number FROM media_progress WHERE media_id = ?`
	rows, err := s.db.QueryContext(ctx, query, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var seasonID, epNum string
		if err := rows.Scan(&seasonID, &epNum); err != nil {
			continue
		}
		if seasonID != "" {
			keys = append(keys, seasonID+":"+epNum)
		} else {
			keys = append(keys, epNum)
		}
	}
	return keys, rows.Err()
}

// DeleteProgress deletes all progress entries for a given mediaID.
func (s *Storage) DeleteProgress(ctx context.Context, mediaID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM media_progress WHERE media_id = ?", mediaID)
	return err
}

// ClearAllProgress deletes all progress history.
func (s *Storage) ClearAllProgress(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM media_progress")
	return err
}

// ----------------------------------------------------------------------------
// Favorites
// ----------------------------------------------------------------------------

// AddFavorite adds or updates a title in favorites.
func (s *Storage) AddFavorite(ctx context.Context, f Favorite) error {
	if f.MediaID == "" {
		return errors.New("storage: favorite media_id is required")
	}
	if f.AddedAt.IsZero() {
		f.AddedAt = time.Now()
	}

	query := `
	INSERT INTO favorites (media_id, source, name, url, image_url, year, media_type, added_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(media_id) DO UPDATE SET
		source = CASE WHEN excluded.source != '' THEN excluded.source ELSE favorites.source END,
		name = CASE WHEN excluded.name != '' THEN excluded.name ELSE favorites.name END,
		url = CASE WHEN excluded.url != '' THEN excluded.url ELSE favorites.url END,
		image_url = CASE WHEN excluded.image_url != '' THEN excluded.image_url ELSE favorites.image_url END,
		year = CASE WHEN excluded.year != '' THEN excluded.year ELSE favorites.year END,
		media_type = CASE WHEN excluded.media_type != '' THEN excluded.media_type ELSE favorites.media_type END,
		added_at = excluded.added_at
	`
	_, err := s.db.ExecContext(ctx, query,
		f.MediaID, f.Source, f.Name, f.URL, f.ImageURL, f.Year, f.MediaType, f.AddedAt.Unix(),
	)
	return err
}

// RemoveFavorite removes a title from favorites.
func (s *Storage) RemoveFavorite(ctx context.Context, mediaID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM favorites WHERE media_id = ?", mediaID)
	return err
}

// IsFavorite checks whether a title is in favorites.
func (s *Storage) IsFavorite(ctx context.Context, mediaID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM favorites WHERE media_id = ?", mediaID).Scan(&count)
	return count > 0, err
}

// ListFavorites returns all favorites, newest first.
func (s *Storage) ListFavorites(ctx context.Context) ([]Favorite, error) {
	query := `SELECT media_id, source, name, url, image_url, year, media_type, added_at FROM favorites ORDER BY added_at DESC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Favorite
	for rows.Next() {
		var f Favorite
		var ts int64
		if err := rows.Scan(&f.MediaID, &f.Source, &f.Name, &f.URL, &f.ImageURL, &f.Year, &f.MediaType, &ts); err != nil {
			return nil, err
		}
		f.AddedAt = time.Unix(ts, 0)
		list = append(list, f)
	}
	return list, rows.Err()
}

// GetFavoriteKeys returns a list of all favorite media_ids.
func (s *Storage) GetFavoriteKeys(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT media_id FROM favorites")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err == nil {
			keys = append(keys, k)
		}
	}
	return keys, rows.Err()
}

// ----------------------------------------------------------------------------
// Metadata Cache with TTL
// ----------------------------------------------------------------------------

// SetCache stores a key-value pair in cache with an expiration duration.
func (s *Storage) SetCache(ctx context.Context, key, value string, ttl time.Duration) error {
	now := time.Now()
	expiresAt := now.Add(ttl)

	query := `
	INSERT INTO metadata_cache (key, value, expires_at, created_at)
	VALUES (?, ?, ?, ?)
	ON CONFLICT(key) DO UPDATE SET
		value = excluded.value,
		expires_at = excluded.expires_at,
		created_at = excluded.created_at
	`
	_, err := s.db.ExecContext(ctx, query, key, value, expiresAt.UnixMilli(), now.UnixMilli())
	return err
}

// GetCache retrieves a cached value if it has not expired.
func (s *Storage) GetCache(ctx context.Context, key string) (string, error) {
	query := `SELECT value, expires_at FROM metadata_cache WHERE key = ?`
	var val string
	var exp int64
	err := s.db.QueryRowContext(ctx, query, key).Scan(&val, &exp)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}

	if time.Now().UnixMilli() >= exp {
		// Asynchronously delete expired entry
		go func() {
			_, _ = s.db.Exec("DELETE FROM metadata_cache WHERE key = ?", key)
		}()
		return "", ErrNotFound
	}

	return val, nil
}

// PruneExpiredCache deletes all expired cache entries.
func (s *Storage) PruneExpiredCache(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM metadata_cache WHERE expires_at < ?", time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CanonicalMediaID creates a consistent canonical media ID from a source and URL/slug.
func CanonicalMediaID(source, urlOrSlug string) string {
	src := strings.ToLower(strings.TrimSpace(source))
	u := strings.TrimSpace(urlOrSlug)
	if u != "" {
		return src + "|" + strings.ToLower(u)
	}
	return src + "|unknown"
}
