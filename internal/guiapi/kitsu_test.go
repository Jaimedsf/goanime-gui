package guiapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kitsuServer points the Kitsu layer at a local handler for one test.
func kitsuServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	previous := kitsuBaseURL
	kitsuBaseURL = srv.URL
	kitsuBreaker.reset()
	resetKitsuGenreIndex()
	t.Cleanup(func() {
		kitsuBaseURL = previous
		kitsuBreaker.reset()
		resetKitsuGenreIndex()
		srv.Close()
	})
	return srv
}

// A trimmed response in the JSON:API shape Kitsu actually answers with.
const kitsuAnimePayload = `{
	"data": [{
		"id": "7442",
		"type": "anime",
		"attributes": {
			"slug": "attack-on-titan",
			"canonicalTitle": "Attack on Titan",
			"titles": {"en": "Attack on Titan", "en_jp": "Shingeki no Kyojin", "ja_jp": "進撃の巨人"},
			"averageRating": "84.44",
			"startDate": "2013-04-07",
			"subtype": "TV",
			"status": "finished",
			"episodeCount": 25,
			"nsfw": false,
			"ageRating": "R",
			"posterImage": {
				"original": "https://media.kitsu.app/anime/7442/original.jpg",
				"large": "https://media.kitsu.app/anime/7442/large.jpg"
			}
		},
		"relationships": {"categories": {"data": [
			{"type": "categories", "id": "150"},
			{"type": "categories", "id": "157"}
		]}}
	}],
	"included": [
		{"type": "categories", "id": "150", "attributes": {"slug": "action", "title": "Action"}},
		{"type": "categories", "id": "157", "attributes": {"slug": "adventure", "title": "Adventure"}}
	],
	"meta": {"count": 22381},
	"links": {"next": "https://kitsu.app/api/edge/anime?page%5Boffset%5D=20"}
}`

func TestKitsuFetchCatalogMapsIntoAniListVocabulary(t *testing.T) {
	kitsuServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/anime", r.URL.Path)
		assert.Equal(t, "categories", r.URL.Query().Get("include"))
		_, _ = fmt.Fprint(w, kitsuAnimePayload)
	})

	page, err := kitsuFetchCatalog(BrowseQuery{Mode: ModeTop, Page: 1})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)

	item := page.Items[0]
	assert.Equal(t, 7442, item.AniListID)

	// Romaji leads, like the other backends: it is what the scrapers index on.
	assert.Equal(t, "Shingeki no Kyojin", item.Title)
	assert.Equal(t, "Shingeki no Kyojin", item.Romaji)
	assert.Equal(t, "Attack on Titan", item.English)

	// MyAnimeList/Kitsu spelling must not reach the frontend.
	assert.Equal(t, "TV", item.Format)
	assert.Equal(t, "FINISHED", item.Status)

	// Kitsu's rating is already a 0-100 string, so it only rounds.
	assert.Equal(t, 84, item.Score)

	assert.Equal(t, 25, item.EpisodeCount)
	assert.Equal(t, "https://media.kitsu.app/anime/7442/original.jpg", item.Cover)
	assert.Equal(t, []string{"Action", "Adventure"}, item.Genres,
		"genres come from the sideloaded categories")

	// Kitsu does not return a season, so it is derived from the start date.
	assert.Equal(t, "2013-04-07", item.ReleaseDate)
	assert.Equal(t, SeasonSpring, item.Season)
	assert.Equal(t, 2013, item.Year)

	assert.True(t, page.HasNextPage, "paging comes from links.next")
}

// The catalog never lists adult titles, and Kitsu has no server-side filter
// for it, so they have to be dropped on the way out.
func TestKitsuFetchCatalogDropsAdultTitles(t *testing.T) {
	kitsuServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[
			{"id":"1","attributes":{"canonicalTitle":"Normal","subtype":"TV","status":"finished","nsfw":false}},
			{"id":"2","attributes":{"canonicalTitle":"Flagged","subtype":"TV","status":"finished","nsfw":true}},
			{"id":"3","attributes":{"canonicalTitle":"Explicit","subtype":"TV","status":"finished","ageRating":"R18"}}
		]}`)
	})

	page, err := kitsuFetchCatalog(BrowseQuery{Mode: ModeTop, Page: 1})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "Normal", page.Items[0].Title)
}

func TestKitsuCatalogPathPerMode(t *testing.T) {
	tests := []struct {
		name       string
		query      BrowseQuery
		wantParams map[string]string
	}{
		{
			name:  "season filters season and year",
			query: BrowseQuery{Mode: ModeSeason, Year: 2026, Season: SeasonFall, Page: 1},
			wantParams: map[string]string{
				"filter[season]":     "fall",
				"filter[seasonYear]": "2026",
			},
		},
		{
			name:       "top sorts by rating",
			query:      BrowseQuery{Mode: ModeTop, Page: 1},
			wantParams: map[string]string{"sort": "-averageRating"},
		},
		{
			name:       "popular sorts by followers",
			query:      BrowseQuery{Mode: ModePopular, Page: 1},
			wantParams: map[string]string{"sort": "-userCount"},
		},
		{
			name:       "upcoming filters by status",
			query:      BrowseQuery{Mode: ModeUpcoming, Page: 1},
			wantParams: map[string]string{"filter[status]": "upcoming"},
		},
		{
			name:       "airing filters by status",
			query:      BrowseQuery{Mode: ModeAiring, Page: 1},
			wantParams: map[string]string{"filter[status]": "current"},
		},
		{
			name:       "format becomes a subtype filter",
			query:      BrowseQuery{Mode: ModeTop, Format: "MOVIE", Page: 1},
			wantParams: map[string]string{"filter[subtype]": "movie"},
		},
		{
			// Kitsu pages by offset, not page number.
			name:       "page three becomes an offset",
			query:      BrowseQuery{Mode: ModeTop, Page: 3},
			wantParams: map[string]string{"page[offset]": "40", "page[limit]": "20"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := kitsuCatalogPath(tt.query)
			require.NoError(t, err)

			parsed, perr := url.Parse(got)
			require.NoError(t, perr)
			assert.Equal(t, "/anime", parsed.Path)

			for key, want := range tt.wantParams {
				assert.Equal(t, want, parsed.Query().Get(key), "param %q", key)
			}
		})
	}
}

// Kitsu answers an unknown filter[categories] by ignoring the filter and
// listing everything, so an unmappable genre has to be declined rather than
// guessed at — otherwise the page is unfiltered under a heading naming a genre.
func TestKitsuDeclinesAnUnknownGenre(t *testing.T) {
	kitsuServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/categories") {
			_, _ = fmt.Fprint(w, `{"data":[
				{"type":"categories","id":"150","attributes":{"slug":"action","title":"Action"}},
				{"type":"categories","id":"1","attributes":{"slug":"science-fiction","title":"Science Fiction"}}
			]}`)
			return
		}
		_, _ = fmt.Fprint(w, kitsuAnimePayload)
	})

	_, err := kitsuCatalogPath(BrowseQuery{Mode: ModeTop, Page: 1, Genre: "Award Winning"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "não conhece o gênero")

	// One it does know resolves to the slug.
	path, err := kitsuCatalogPath(BrowseQuery{Mode: ModeTop, Page: 1, Genre: "Action"})
	require.NoError(t, err)
	parsed, perr := url.Parse(path)
	require.NoError(t, perr)
	assert.Equal(t, "action", parsed.Query().Get("filter[categories]"))
}

// AniList and Kitsu disagree on some spellings, and the alias table is what
// bridges them.
func TestKitsuGenreAliases(t *testing.T) {
	kitsuServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[]}`)
	})

	assert.Equal(t, "science-fiction", kitsuGenreSlug("Sci-Fi"))
	assert.Equal(t, "school-life", kitsuGenreSlug("School"))
	assert.Equal(t, "slice-of-life", kitsuGenreSlug("Slice of Life"))
	assert.Empty(t, kitsuGenreSlug(""))
}

func TestKitsuVocabularyMapping(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "TV", kitsuFormat("TV"))
	assert.Equal(t, "MOVIE", kitsuFormat("movie"))
	assert.Equal(t, "OVA", kitsuFormat("OVA"))
	assert.Equal(t, "SPECIAL", kitsuFormat("special"))

	assert.Equal(t, "RELEASING", kitsuStatus("current"))
	assert.Equal(t, "FINISHED", kitsuStatus("finished"))
	assert.Equal(t, "NOT_YET_RELEASED", kitsuStatus("upcoming"))
	assert.Equal(t, "NOT_YET_RELEASED", kitsuStatus("tba"))
	assert.Empty(t, kitsuStatus("something else"))

	assert.Equal(t, 84, kitsuScore("84.44"))
	assert.Equal(t, 85, kitsuScore("84.5"))
	assert.Equal(t, 0, kitsuScore(""))
	assert.Equal(t, 0, kitsuScore("not a number"))
}

func TestKitsuStartDate(t *testing.T) {
	t.Parallel()

	y, m, d := kitsuStartDate("2013-04-07")
	assert.Equal(t, [3]int{2013, 4, 7}, [3]int{y, m, d})

	y, m, d = kitsuStartDate("2013")
	assert.Equal(t, [3]int{2013, 0, 0}, [3]int{y, m, d})

	y, m, d = kitsuStartDate("")
	assert.Equal(t, [3]int{0, 0, 0}, [3]int{y, m, d})
}

// filter[text] is fuzzy enough to answer with the wrong show, and a wrong
// cover is worse than the placeholder shown when nothing is found.
func TestKitsuMetadataRequiresATitleMatch(t *testing.T) {
	kitsuServer(t, func(w http.ResponseWriter, _ *http.Request) {
		// What Kitsu really returns for "titulo-que-nao-existe-xyz": it
		// latched onto "xyz".
		_, _ = fmt.Fprint(w, `{"data":[
			{"id":"1","attributes":{"canonicalTitle":"Pokemon XY&Z","titles":{"en_jp":"Pokemon XY&Z"},"subtype":"TV","status":"finished"}}
		]}`)
	})

	_, err := fetchKitsuMedia("titulo que nao existe xyz")
	require.ErrorIs(t, err, errTitleNotFound)
}

func TestKitsuMetadataAcceptsAnExactMatchFurtherDown(t *testing.T) {
	kitsuServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[
			{"id":"1","attributes":{"canonicalTitle":"Love Hina Again","titles":{"en_jp":"Love Hina Again"},"subtype":"OVA","status":"finished"}},
			{"id":"2","attributes":{"canonicalTitle":"Love Hina","titles":{"en_jp":"Love Hina"},"subtype":"TV","status":"finished","episodeCount":24,"averageRating":"70.1","startDate":"2000-04-19","posterImage":{"original":"https://cdn/lh.jpg"}}}
		]}`)
	})

	m, err := fetchKitsuMedia("Love Hina")
	require.NoError(t, err)
	assert.Equal(t, "https://cdn/lh.jpg", m.cover)
	assert.Equal(t, 24, m.info.EpisodeCount)
	assert.Equal(t, 70, m.info.Score)
	assert.Equal(t, "2000", m.info.Year)

	// Kitsu has no per-episode stills, so there is nothing more to ask for.
	assert.True(t, m.thumbsFetched)
	assert.Empty(t, m.thumbs)
}

func TestKitsuSurfacesHTTPFailure(t *testing.T) {
	kitsuServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	_, err := kitsuFetchCatalog(BrowseQuery{Mode: ModeTop, Page: 1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Kitsu")
}

// The chain must name every backend it tried, so three different outages do
// not read as one generic failure.
func TestFetchCatalogNamesEveryBackendItTried(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	})
	kitsuServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	anilistServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	_, err := fetchCatalog(BrowseQuery{Mode: ModeTop, Page: 1}.normalise())
	require.Error(t, err)
	for _, name := range []string{"Jikan", "Kitsu", "AniList"} {
		assert.Contains(t, err.Error(), name)
	}
}

// Kitsu answers when Jikan cannot, which is the whole reason it was added.
func TestFetchCatalogFallsThroughToKitsu(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	})
	kitsuServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, kitsuAnimePayload)
	})

	page, err := fetchCatalog(BrowseQuery{Mode: ModeTop, Page: 1}.normalise())
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "Shingeki no Kyojin", page.Items[0].Title)
}
