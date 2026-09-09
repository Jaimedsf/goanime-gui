package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStorage(t *testing.T) *Storage {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_goanime.db")
	s, err := Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = s.Close()
	})
	return s
}

func TestStorage_Progress(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	mediaID := CanonicalMediaID("animefire", "naruto-shippuden")

	// 1. Initial get should be ErrNotFound
	_, err := s.GetProgress(ctx, mediaID)
	assert.ErrorIs(t, err, ErrNotFound)

	// 2. Save progress for Episode 1
	p1 := MediaProgress{
		MediaID:       mediaID,
		Source:        "animefire",
		Title:         "Naruto Shippuden",
		MediaType:     "anime",
		EpisodeNumber: "1",
		EpisodeNum:    1,
		EpisodeTitle:  "Regresso para Casa",
		SeasonID:      "1",
		PlaybackTime:  720,
		Duration:      1440,
		AnilistID:     20,
		LastUpdated:   time.Now().Add(-10 * time.Minute),
	}
	err = s.SaveProgress(ctx, p1)
	require.NoError(t, err)

	// 3. Save progress for Episode 2 (more recent)
	p2 := MediaProgress{
		MediaID:       mediaID,
		Source:        "animefire",
		Title:         "Naruto Shippuden",
		MediaType:     "anime",
		EpisodeNumber: "2",
		EpisodeNum:    2,
		EpisodeTitle:  "Os Akatsuki entram em ação",
		SeasonID:      "1",
		PlaybackTime:  300,
		Duration:      1440,
		AnilistID:     20,
		LastUpdated:   time.Now(),
	}
	err = s.SaveProgress(ctx, p2)
	require.NoError(t, err)

	// 4. GetProgress returns the most recent (Ep 2)
	latest, err := s.GetProgress(ctx, mediaID)
	require.NoError(t, err)
	assert.Equal(t, "2", latest.EpisodeNumber)
	assert.Equal(t, 300, latest.PlaybackTime)

	// 5. GetEpisodeProgress for specific ep 1
	ep1, err := s.GetEpisodeProgress(ctx, mediaID, "1", "1")
	require.NoError(t, err)
	assert.Equal(t, "1", ep1.EpisodeNumber)
	assert.Equal(t, 720, ep1.PlaybackTime)

	// 6. Watched episode keys
	keys, err := s.GetWatchedEpisodeKeys(ctx, mediaID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"1:1", "1:2"}, keys)

	// 7. Recent list
	recent, err := s.GetRecentProgress(ctx, 10)
	require.NoError(t, err)
	require.Len(t, recent, 1)
	assert.Equal(t, mediaID, recent[0].MediaID)

	// 8. Delete progress
	err = s.DeleteProgress(ctx, mediaID)
	require.NoError(t, err)
	_, err = s.GetProgress(ctx, mediaID)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestStorage_Favorites(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	mediaID := CanonicalMediaID("allanime", "abc123xyz")

	isFav, err := s.IsFavorite(ctx, mediaID)
	require.NoError(t, err)
	assert.False(t, isFav)

	fav := Favorite{
		MediaID:   mediaID,
		Source:    "allanime",
		Name:      "Frieren: Beyond Journey's End",
		URL:       "abc123xyz",
		ImageURL:  "https://example.com/cover.jpg",
		Year:      "2023",
		MediaType: "anime",
	}

	err = s.AddFavorite(ctx, fav)
	require.NoError(t, err)

	isFav, err = s.IsFavorite(ctx, mediaID)
	require.NoError(t, err)
	assert.True(t, isFav)

	favs, err := s.ListFavorites(ctx)
	require.NoError(t, err)
	require.Len(t, favs, 1)
	assert.Equal(t, "Frieren: Beyond Journey's End", favs[0].Name)

	keys, err := s.GetFavoriteKeys(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{mediaID}, keys)

	err = s.RemoveFavorite(ctx, mediaID)
	require.NoError(t, err)

	isFav, err = s.IsFavorite(ctx, mediaID)
	require.NoError(t, err)
	assert.False(t, isFav)
}

func TestStorage_MetadataCache(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	key := "anilist:title:naruto"
	value := `{"id": 20, "title": "Naruto"}`

	// Cache set with 1 second TTL
	err := s.SetCache(ctx, key, value, 1*time.Second)
	require.NoError(t, err)

	val, err := s.GetCache(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, value, val)

	// Wait for TTL expiration
	time.Sleep(1100 * time.Millisecond)

	_, err = s.GetCache(ctx, key)
	assert.ErrorIs(t, err, ErrNotFound)
}
