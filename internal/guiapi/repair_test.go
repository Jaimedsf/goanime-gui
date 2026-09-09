package guiapi

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvarorichard/Goanime/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repairStore points the library at a scratch database and stubs the source
// search, so a repair pass runs without touching the network.
func repairStore(t *testing.T, search func(query string) ([]SearchResult, error)) *storage.Storage {
	t.Helper()

	s, err := storage.Open(filepath.Join(t.TempDir(), "repair.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	previousStore := storage.Default()
	storage.SetDefault(s)

	previousSearch := searchAnimeFireFor
	searchAnimeFireFor = search

	t.Cleanup(func() {
		storage.SetDefault(previousStore)
		searchAnimeFireFor = previousSearch
	})
	return s
}

const (
	legacyLoveHina  = "https://animefire.io/animes/love-hina-todos-os-episodios"
	currentLoveHina = "https://animefire.io/anime/KZKFp1Ib_SH"
)

func TestLegacyAnimeFireURL(t *testing.T) {
	t.Parallel()

	assert.True(t, legacyAnimeFireURL(legacyLoveHina))
	assert.False(t, legacyAnimeFireURL("https://animefire.io/anime/KZKFp1Ib_SH"))
	assert.False(t, legacyAnimeFireURL("https://goyabu.io/anime/love-hina"))
	assert.False(t, legacyAnimeFireURL(""))
}

func TestTitleFromLegacySlug(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		legacyLoveHina: "love hina",
		"https://animefire.io/animes/yomi-no-tsugai-todos-os-episodios": "yomi no tsugai",
		"https://animefire.io/animes/overlord":                          "overlord",
		"https://goyabu.io/anime/love-hina":                             "",
	}
	for raw, want := range tests {
		assert.Equal(t, want, titleFromLegacySlug(raw), "slug of %q", raw)
	}
}

func TestRepairRewritesFavoriteToTheNewURL(t *testing.T) {
	store := repairStore(t, func(string) ([]SearchResult, error) {
		return []SearchResult{
			{Name: "[PT-BR] Love Love?", URL: "https://animefire.io/anime/NK94VRtRyJ8", Source: "Animefire.io"},
			{Name: "[PT-BR] Love Hina", URL: currentLoveHina, Source: "Animefire.io"},
		}, nil
	})

	ctx := context.Background()
	old := storage.Favorite{
		MediaID: "animefire.io|" + legacyLoveHina,
		Source:  "Animefire.io",
		Name:    "[PT-BR] Love Hina",
		URL:     legacyLoveHina,
		AddedAt: time.Now(),
	}
	require.NoError(t, store.AddFavorite(ctx, old))

	report := RepairLibrary()
	assert.Equal(t, 1, report.Checked)
	assert.Equal(t, 1, report.Repaired)
	assert.Equal(t, 0, report.Skipped)

	favs, err := store.ListFavorites(ctx)
	require.NoError(t, err)
	require.Len(t, favs, 1, "the bookmark moved rather than being duplicated")
	assert.Equal(t, currentLoveHina, favs[0].URL)
	assert.Equal(t, "[PT-BR] Love Hina", favs[0].Name, "the title is preserved")
}

// A near-miss must not repoint the bookmark: a sequel is a different anime.
func TestRepairSkipsWhenNoExactMatch(t *testing.T) {
	store := repairStore(t, func(string) ([]SearchResult, error) {
		return []SearchResult{
			{Name: "[PT-BR] Love Hina Again", URL: "https://animefire.io/anime/OTHER", Source: "Animefire.io"},
		}, nil
	})

	ctx := context.Background()
	require.NoError(t, store.AddFavorite(ctx, storage.Favorite{
		MediaID: "animefire.io|" + legacyLoveHina,
		Source:  "Animefire.io",
		Name:    "[PT-BR] Love Hina",
		URL:     legacyLoveHina,
		AddedAt: time.Now(),
	}))

	report := RepairLibrary()
	assert.Equal(t, 1, report.Checked)
	assert.Equal(t, 0, report.Repaired)
	assert.Equal(t, 1, report.Skipped)

	favs, err := store.ListFavorites(ctx)
	require.NoError(t, err)
	require.Len(t, favs, 1)
	assert.Equal(t, legacyLoveHina, favs[0].URL, "left exactly as it was")
}

// The slug is the second opinion: it rescues an entry whose stored name has
// drifted from how the source spells it.
func TestRepairMatchesOnTheSlugWhenTheNameDrifted(t *testing.T) {
	store := repairStore(t, func(string) ([]SearchResult, error) {
		return []SearchResult{
			{Name: "[PT-BR] Love Hina", URL: currentLoveHina, Source: "Animefire.io"},
		}, nil
	})

	ctx := context.Background()
	require.NoError(t, store.AddFavorite(ctx, storage.Favorite{
		MediaID: "animefire.io|" + legacyLoveHina,
		Source:  "Animefire.io",
		Name:    "Love Hina (Dublado)",
		URL:     legacyLoveHina,
		AddedAt: time.Now(),
	}))

	report := RepairLibrary()
	assert.Equal(t, 1, report.Repaired)

	favs, err := store.ListFavorites(ctx)
	require.NoError(t, err)
	assert.Equal(t, currentLoveHina, favs[0].URL)
}

func TestRepairMovesEveryEpisodeOfAHistoryEntry(t *testing.T) {
	store := repairStore(t, func(string) ([]SearchResult, error) {
		return []SearchResult{
			{Name: "[PT-BR] Love Hina", URL: currentLoveHina, Source: "Animefire.io"},
		}, nil
	})

	ctx := context.Background()
	oldID := "animefire.io|" + legacyLoveHina
	for ep := 1; ep <= 3; ep++ {
		require.NoError(t, store.SaveProgress(ctx, storage.MediaProgress{
			MediaID:       oldID,
			Source:        "Animefire.io",
			Title:         "[PT-BR] Love Hina",
			EpisodeNumber: string(rune('0' + ep)),
			EpisodeNum:    ep,
			PlaybackTime:  ep * 100,
			Duration:      1400,
			LastUpdated:   time.Now(),
		}))
	}

	report := RepairLibrary()
	assert.Equal(t, 1, report.Repaired)

	entries, err := store.GetAllHistory(ctx, 50)
	require.NoError(t, err)
	require.Len(t, entries, 3, "every episode moved, none duplicated")

	// titleKey lowercases the URL, so build the expected id the same way the
	// library does rather than by hand.
	newID := titleKey(SearchResult{Source: "Animefire.io", URL: currentLoveHina})
	for _, e := range entries {
		assert.Equal(t, newID, e.MediaID)
	}

	// The resume point has to survive the move, or the repair costs the user
	// their place.
	byEpisode := map[int]int{}
	for _, e := range entries {
		byEpisode[e.EpisodeNum] = e.PlaybackTime
	}
	assert.Equal(t, map[int]int{1: 100, 2: 200, 3: 300}, byEpisode)
}

// Entries from other sources, and AnimeFire entries already on the new URL
// shape, must not be touched at all.
func TestRepairIgnoresEntriesThatAreNotStale(t *testing.T) {
	searches := 0
	store := repairStore(t, func(string) ([]SearchResult, error) {
		searches++
		return nil, nil
	})

	ctx := context.Background()
	require.NoError(t, store.AddFavorite(ctx, storage.Favorite{
		MediaID: "goyabu|https://goyabu.io/anime/love-hina",
		Source:  "Goyabu",
		Name:    "[PT-BR] Love Hina",
		URL:     "https://goyabu.io/anime/love-hina",
		AddedAt: time.Now(),
	}))
	require.NoError(t, store.AddFavorite(ctx, storage.Favorite{
		MediaID: "animefire.io|" + currentLoveHina,
		Source:  "Animefire.io",
		Name:    "[PT-BR] Love Hina",
		URL:     currentLoveHina,
		AddedAt: time.Now(),
	}))

	report := RepairLibrary()
	assert.Equal(t, 0, report.Checked)
	assert.Equal(t, 0, searches, "nothing stale means no network at all")
}

// Running twice must be a no-op the second time, not a second rewrite.
func TestRepairIsIdempotent(t *testing.T) {
	store := repairStore(t, func(string) ([]SearchResult, error) {
		return []SearchResult{
			{Name: "[PT-BR] Love Hina", URL: currentLoveHina, Source: "Animefire.io"},
		}, nil
	})

	ctx := context.Background()
	require.NoError(t, store.AddFavorite(ctx, storage.Favorite{
		MediaID: "animefire.io|" + legacyLoveHina,
		Source:  "Animefire.io",
		Name:    "[PT-BR] Love Hina",
		URL:     legacyLoveHina,
		AddedAt: time.Now(),
	}))

	require.Equal(t, 1, RepairLibrary().Repaired)

	second := RepairLibrary()
	assert.Equal(t, 0, second.Checked)
	assert.Equal(t, 0, second.Repaired)

	favs, err := store.ListFavorites(ctx)
	require.NoError(t, err)
	assert.Len(t, favs, 1)
}

// A search that fails leaves the library untouched, so the next launch can
// try again.
func TestRepairLeavesLibraryAloneWhenSearchFails(t *testing.T) {
	store := repairStore(t, func(string) ([]SearchResult, error) {
		return nil, assert.AnError
	})

	ctx := context.Background()
	require.NoError(t, store.AddFavorite(ctx, storage.Favorite{
		MediaID: "animefire.io|" + legacyLoveHina,
		Source:  "Animefire.io",
		Name:    "[PT-BR] Love Hina",
		URL:     legacyLoveHina,
		AddedAt: time.Now(),
	}))

	report := RepairLibrary()
	assert.Equal(t, 1, report.Skipped)

	favs, err := store.ListFavorites(ctx)
	require.NoError(t, err)
	assert.Equal(t, legacyLoveHina, favs[0].URL)
}

func TestSeasonOrdinal(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"Youjo Senki II":                            "2",
		"Kami nomi zo Shiru Sekai II":               "2",
		"Shakugan no Shana III (Final)":             "3",
		"Sousou no Frieren 2nd Season":              "2",
		"Monogatari Series: Second Season":          "2",
		"Tensei shitara Slime Datta Ken 4th Season": "4",
		"Higashi no Eden Movie II: Paradise Lost":   "2",
		// No season pinned.
		"Love Hina":      "",
		"Bakemonogatari": "",
		"Black Torch":    "",
		// A number that is part of the name, not a season, must not be read
		// as one just because it sits mid-title.
		"Code Geass R2 Lelouch": "",
	}
	for title, want := range tests {
		assert.Equal(t, want, seasonOrdinal(title), "ordinal of %q", title)
	}
}

func TestIsTruncationOf(t *testing.T) {
	t.Parallel()

	// The case this exists for: the catalog offers the franchise root for a
	// specific entry's name.
	assert.True(t, isTruncationOf("bakemonogatari", "monogatari"))
	assert.True(t, isTruncationOf("monogatari", "bakemonogatari"))

	assert.False(t, isTruncationOf("lovehina", "lovehina"), "equal is a match, not a truncation")
	assert.False(t, isTruncationOf("lovehina", "loveloveq"))
	assert.False(t, isTruncationOf("", "monogatari"))
}

// The rewritten catalog is titled in Portuguese where the library holds
// romaji, so the source's own ranking is what connects them.
func TestRepairAcceptsRenamedTitleFromRanking(t *testing.T) {
	store := repairStore(t, func(string) ([]SearchResult, error) {
		return []SearchResult{
			{Name: "[PT-BR] TOCHA NEGRA", URL: "https://animefire.io/anime/NEW", Source: "Animefire.io"},
			{Name: "[PT-BR] Black Cat", URL: "https://animefire.io/anime/OTHER", Source: "Animefire.io"},
		}, nil
	})

	ctx := context.Background()
	legacy := "https://animefire.io/animes/black-torch-todos-os-episodios"
	require.NoError(t, store.AddFavorite(ctx, storage.Favorite{
		MediaID: "animefire.io|" + legacy,
		Source:  "Animefire.io",
		Name:    "[PT-BR] Black Torch",
		URL:     legacy,
		AddedAt: time.Now(),
	}))

	assert.Equal(t, 1, RepairLibrary().Repaired)

	favs, err := store.ListFavorites(ctx)
	require.NoError(t, err)
	assert.Equal(t, "https://animefire.io/anime/NEW", favs[0].URL)
}

// A bookmark for season two must not be moved to season one, even when the
// source ranks season one first.
func TestRepairRefusesToCrossSeasons(t *testing.T) {
	store := repairStore(t, func(string) ([]SearchResult, error) {
		return []SearchResult{
			{Name: "[PT-BR] Saga of Tanya the Evil", URL: "https://animefire.io/anime/S1", Source: "Animefire.io"},
		}, nil
	})

	ctx := context.Background()
	legacy := "https://animefire.io/animes/youjo-senki-ii-todos-os-episodios"
	require.NoError(t, store.AddFavorite(ctx, storage.Favorite{
		MediaID: "animefire.io|" + legacy,
		Source:  "Animefire.io",
		Name:    "[PT-BR] Youjo Senki II",
		URL:     legacy,
		AddedAt: time.Now(),
	}))

	report := RepairLibrary()
	assert.Equal(t, 0, report.Repaired)
	assert.Equal(t, 1, report.Skipped)

	favs, err := store.ListFavorites(ctx)
	require.NoError(t, err)
	assert.Equal(t, legacy, favs[0].URL, "left broken rather than pointed at the wrong season")
}

// "Monogatari" is the top hit for "Bakemonogatari" and is a different anime.
func TestRepairRefusesATruncatedTitle(t *testing.T) {
	store := repairStore(t, func(string) ([]SearchResult, error) {
		return []SearchResult{
			{Name: "[PT-BR] Monogatari", URL: "https://animefire.io/anime/ROOT", Source: "Animefire.io"},
		}, nil
	})

	ctx := context.Background()
	legacy := "https://animefire.io/animes/bakemonogatari-todos-os-episodios"
	require.NoError(t, store.AddFavorite(ctx, storage.Favorite{
		MediaID: "animefire.io|" + legacy,
		Source:  "Animefire.io",
		Name:    "[PT-BR] Bakemonogatari",
		URL:     legacy,
		AddedAt: time.Now(),
	}))

	assert.Equal(t, 1, RepairLibrary().Skipped)
}
