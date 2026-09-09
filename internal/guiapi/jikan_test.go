package guiapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jikanServer points the Jikan layer at a local handler for the duration of
// one test.
func jikanServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	previous := jikanBaseURL
	jikanBaseURL = srv.URL
	t.Cleanup(func() {
		jikanBaseURL = previous
		srv.Close()
	})
	return srv
}

// A trimmed response in the exact shape Jikan answers with.
const jikanAnimePayload = `{
	"pagination": {"has_next_page": true, "current_page": 1},
	"data": [
		{
			"mal_id": 52991,
			"images": {"jpg": {
				"image_url": "https://cdn.myanimelist.net/images/anime/1015/138006.jpg",
				"large_image_url": "https://cdn.myanimelist.net/images/anime/1015/138006l.jpg"
			}},
			"title": "Sousou no Frieren",
			"title_english": "Frieren: Beyond Journey's End",
			"title_japanese": "葬送のフリーレン",
			"type": "TV",
			"episodes": 28,
			"status": "Finished Airing",
			"score": 9.26,
			"season": "fall",
			"year": 2023,
			"genres": [
				{"mal_id": 2, "name": "Adventure"},
				{"mal_id": 8, "name": "Drama"}
			],
			"aired": {"prop": {"from": {"day": 29, "month": 9, "year": 2023}}}
		}
	]
}`

func TestJikanFetchCatalogMapsIntoAniListVocabulary(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, jikanAnimePayload)
	})

	page, err := jikanFetchCatalog(BrowseQuery{Mode: ModeTop, Page: 1})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)

	item := page.Items[0]
	assert.Equal(t, 52991, item.AniListID)
	assert.Equal(t, "Sousou no Frieren", item.Title)
	assert.Equal(t, "Frieren: Beyond Journey's End", item.English)

	// The frontend keys its labels off AniList's vocabulary, so MyAnimeList's
	// spelling must not reach it.
	assert.Equal(t, "TV", item.Format)
	assert.Equal(t, "FINISHED", item.Status)
	assert.Equal(t, "FALL", item.Season)

	// Score is rendered as a percentage, so 9.26/10 has to become 93.
	assert.Equal(t, 93, item.Score)

	assert.Equal(t, 28, item.EpisodeCount)
	assert.Equal(t, []string{"Adventure", "Drama"}, item.Genres)
	assert.Equal(t, "2023-09-29", item.ReleaseDate)
	assert.Equal(t, "https://cdn.myanimelist.net/images/anime/1015/138006l.jpg", item.Cover)
	assert.True(t, page.HasNextPage)
}

func TestJikanFetchCatalogFallsBackToSmallCover(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"pagination":{"has_next_page":false},"data":[
			{"mal_id":1,"title":"X","type":"TV","status":"Currently Airing",
			 "images":{"jpg":{"image_url":"https://cdn/small.jpg","large_image_url":""}}}
		]}`)
	})

	page, err := jikanFetchCatalog(BrowseQuery{Mode: ModeTop, Page: 1})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "https://cdn/small.jpg", page.Items[0].Cover)
	assert.Equal(t, "RELEASING", page.Items[0].Status)
}

func TestJikanFetchCatalogSurfacesUpstreamOutage(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	})

	_, err := jikanFetchCatalog(BrowseQuery{Mode: ModeTop, Page: 1})
	require.Error(t, err)
	// A 504 is Jikan failing to reach MyAnimeList; the message should say so
	// rather than blaming the request.
	assert.Contains(t, err.Error(), "MyAnimeList")
}

func TestJikanCatalogPathPerMode(t *testing.T) {
	tests := []struct {
		name       string
		query      BrowseQuery
		wantPath   string
		wantParams map[string]string
	}{
		{
			name:     "season uses its own endpoint",
			query:    BrowseQuery{Mode: ModeSeason, Year: 2026, Season: SeasonFall, Page: 2},
			wantPath: "/seasons/2026/fall",
			wantParams: map[string]string{
				"page": "2", "limit": "25", "sfw": "true",
			},
		},
		{
			name:     "top sorts by score descending",
			query:    BrowseQuery{Mode: ModeTop, Page: 1},
			wantPath: "/anime",
			wantParams: map[string]string{
				"order_by": "score", "sort": "desc",
			},
		},
		{
			name:     "popular sorts ascending because rank 1 is the most popular",
			query:    BrowseQuery{Mode: ModePopular, Page: 1},
			wantPath: "/anime",
			wantParams: map[string]string{
				"order_by": "popularity", "sort": "asc",
			},
		},
		{
			name:     "upcoming filters by status",
			query:    BrowseQuery{Mode: ModeUpcoming, Page: 1},
			wantPath: "/anime",
			wantParams: map[string]string{
				"status": "upcoming",
			},
		},
		{
			name:     "airing filters by status",
			query:    BrowseQuery{Mode: ModeAiring, Page: 1},
			wantPath: "/anime",
			wantParams: map[string]string{
				"status": "airing",
			},
		},
		{
			name:     "format becomes a type filter",
			query:    BrowseQuery{Mode: ModeTop, Format: "MOVIE", Page: 1},
			wantPath: "/anime",
			wantParams: map[string]string{
				"type": "movie",
			},
		},
		{
			name:     "a year becomes a date range",
			query:    BrowseQuery{Mode: ModeTop, Year: 1998, Page: 1},
			wantPath: "/anime",
			wantParams: map[string]string{
				"start_date": "1998-01-01", "end_date": "1998-12-31",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jikanCatalogPath(tt.query)

			parsed, err := url.Parse(got)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, parsed.Path)

			for key, want := range tt.wantParams {
				assert.Equal(t, want, parsed.Query().Get(key), "param %q", key)
			}
		})
	}
}

func TestJikanFormatMapping(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"TV": "TV", "Movie": "MOVIE", "OVA": "OVA",
		"ONA": "ONA", "Special": "SPECIAL", "Music": "MUSIC",
	}
	for in, want := range cases {
		assert.Equal(t, want, jikanFormat(in), "format %q", in)
	}
}

func TestJikanStatusMapping(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "RELEASING", jikanStatus("Currently Airing"))
	assert.Equal(t, "FINISHED", jikanStatus("Finished Airing"))
	assert.Equal(t, "NOT_YET_RELEASED", jikanStatus("Not yet aired"))
	// An unknown status is dropped rather than passed through as a label the
	// frontend has no translation for.
	assert.Equal(t, "", jikanStatus("Something Else"))
}

func TestJikanScoreBecomesPercentage(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 93, jikanScore(9.26))
	assert.Equal(t, 100, jikanScore(10))
	assert.Equal(t, 0, jikanScore(0))
	assert.Equal(t, 0, jikanScore(-1))
}

func TestJikanFetchGenresReturnsNames(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/genres/anime", r.URL.Path)
		_, _ = fmt.Fprint(w, `{"data":[{"mal_id":1,"name":"Action"},{"mal_id":4,"name":"Comedy"}]}`)
	})

	names, err := jikanFetchGenres()
	require.NoError(t, err)
	assert.Equal(t, []string{"Action", "Comedy"}, names)
}

// The catalog must survive either backend being down: Jikan answers when it
// can, and AniList is tried only when it cannot.
func TestFetchCatalogFallsBackToAniListWhenJikanIsDown(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	})

	// AniList is unreachable here too, so the error has to name both sides
	// rather than silently reporting an empty catalog.
	_, err := fetchCatalog(BrowseQuery{Mode: ModeTop, Page: 1}.normalise())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Jikan")
	assert.Contains(t, err.Error(), "AniList")
}

func TestFetchCatalogPrefersJikan(t *testing.T) {
	hits := 0
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = fmt.Fprint(w, jikanAnimePayload)
	})

	page, err := fetchCatalog(BrowseQuery{Mode: ModeTop, Page: 1}.normalise())
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, 1, hits, "AniList must not be consulted when Jikan answers")
	assert.Equal(t, "Sousou no Frieren", page.Items[0].Title)
}
