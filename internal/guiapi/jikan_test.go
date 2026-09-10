package guiapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

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
	// Tests that stage an outage would otherwise leave the breaker open for
	// the next one.
	jikanBreaker.reset()
	t.Cleanup(func() {
		jikanBaseURL = previous
		jikanBreaker.reset()
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
			got, buildErr := jikanCatalogPath(tt.query)
			require.NoError(t, buildErr)

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

// With every backend down the error has to name each one rather than
// silently reporting an empty catalog. Kitsu is stubbed as down too, or the
// chain would reach the real API and legitimately succeed.
func TestFetchCatalogFallsBackToAniListWhenJikanIsDown(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	})
	kitsuServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	_, err := fetchCatalog(BrowseQuery{Mode: ModeTop, Page: 1}.normalise())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Jikan")
	assert.Contains(t, err.Error(), "Kitsu")
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

// --- title metadata --------------------------------------------------------

const jikanSearchOnePayload = `{"pagination":{"has_next_page":false},"data":[{
	"mal_id": 20,
	"images": {"jpg": {"image_url":"https://cdn/small.jpg","large_image_url":"https://cdn/large.jpg"}},
	"title": "Naruto",
	"title_english": "Naruto",
	"type": "TV",
	"episodes": 220,
	"status": "Finished Airing",
	"score": 8.02,
	"season": "fall",
	"year": 2002,
	"rating": "PG-13 - Teens 13 or older",
	"genres": [{"mal_id":1,"name":"Action"}],
	"explicit_genres": [],
	"aired": {"prop": {"from": {"day":3,"month":10,"year":2002}}}
}]}`

func TestFetchJikanMediaMapsTitleInfo(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/anime", r.URL.Path)
		assert.Equal(t, "Naruto", r.URL.Query().Get("q"))
		// The adult flag rides this lookup, so the search must not be
		// filtered — a filtered miss would be cached as "no such title".
		assert.Empty(t, r.URL.Query().Get("sfw"))
		_, _ = fmt.Fprint(w, jikanSearchOnePayload)
	})

	m, err := fetchJikanMedia("Naruto", false)
	require.NoError(t, err)

	assert.Equal(t, "https://cdn/large.jpg", m.cover)
	assert.Equal(t, 20, m.malID)
	assert.False(t, m.adult)
	assert.Equal(t, "TV", m.info.Format)
	assert.Equal(t, "FINISHED", m.info.Status)
	assert.Equal(t, 220, m.info.EpisodeCount)
	assert.Equal(t, 80, m.info.Score)
	assert.Equal(t, "2002-10-03", m.info.ReleaseDate)
	assert.Equal(t, "2002", m.info.Year)

	// Thumbs were not requested, so the entry must not claim to have them.
	assert.Empty(t, m.thumbs)
	assert.False(t, m.thumbsFetched)
}

func TestFetchJikanMediaReportsNotFoundOnEmptyResult(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"pagination":{"has_next_page":false},"data":[]}`)
	})

	_, err := fetchJikanMedia("nao existe", false)
	require.ErrorIs(t, err, errTitleNotFound)
}

// A real miss must not cost a second lookup: both backends index the same
// anime, so AniList would only answer "not found" too.
func TestFetchTitleMediaDoesNotFallBackOnNotFound(t *testing.T) {
	hits := 0
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = fmt.Fprint(w, `{"pagination":{"has_next_page":false},"data":[]}`)
	})

	_, err := fetchTitleMedia("nao existe")
	require.ErrorIs(t, err, errTitleNotFound)
	assert.Equal(t, 1, hits)
}

func TestFetchTitleMediaPrefersJikan(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, jikanSearchOnePayload)
	})

	m, err := fetchTitleMedia("Naruto")
	require.NoError(t, err)
	assert.Equal(t, 20, m.malID)
	assert.Equal(t, "https://cdn/large.jpg", m.cover)
}

func TestJikanIsAdult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		anime jikanAnime
		want  bool
	}{
		{"teen rating is not adult", jikanAnime{Rating: "PG-13 - Teens 13 or older"}, false},
		{"mild nudity stays visible", jikanAnime{Rating: "R+ - Mild Nudity"}, false},
		{"Rx is adult", jikanAnime{Rating: "Rx - Hentai"}, true},
		{"explicit genres mark adult", jikanAnime{ExplicitGenres: []jikanNamed{{Name: "Hentai"}}}, true},
		{"hentai genre marks adult", jikanAnime{Genres: []jikanNamed{{Name: "Hentai"}}}, true},
		{"unrated is not adult", jikanAnime{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, jikanIsAdult(tt.anime))
		})
	}
}

func TestJikanEpisodeThumbsKeysByEpisodeNumber(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/anime/20/videos", r.URL.Path)
		_, _ = fmt.Fprint(w, `{"data":{"episodes":[
			{"mal_id":1,"title":"Enter: Naruto Uzumaki!","episode":"Episode 1",
			 "images":{"jpg":{"image_url":"https://cdn/ep1.jpg"}}},
			{"mal_id":2,"title":"My Name is Konohamaru!","episode":"Episode 2",
			 "images":{"jpg":{"image_url":"https://cdn/ep2.jpg"}}},
			{"mal_id":3,"title":"No image","episode":"Episode 3",
			 "images":{"jpg":{"image_url":""}}}
		]}}`)
	})

	thumbs := jikanEpisodeThumbs(20)
	assert.Equal(t, map[string]string{
		"1": "https://cdn/ep1.jpg",
		"2": "https://cdn/ep2.jpg",
	}, thumbs, "an episode without an image is skipped")
}

// Stills are best-effort: the grid falls back to the poster, so an outage
// must not surface as an error.
func TestJikanEpisodeThumbsSwallowsFailure(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	})

	assert.Empty(t, jikanEpisodeThumbs(20))
	assert.Empty(t, jikanEpisodeThumbs(0), "a missing id makes no request at all")
}

// --- airing calendar -------------------------------------------------------

const jikanSchedulePayload = `{"pagination":{"has_next_page":false},"data":[
	{
		"mal_id": 52991,
		"images": {"jpg": {"large_image_url":"https://cdn/frieren.jpg"}},
		"title": "Sousou no Frieren",
		"title_english": "Frieren: Beyond Journey's End",
		"title_synonyms": ["Frieren at the Funeral"],
		"type": "TV",
		"status": "Currently Airing",
		"score": 9.26,
		"rating": "PG-13 - Teens 13 or older",
		"broadcast": {"day":"Fridays","time":"23:00","timezone":"Asia/Tokyo"}
	},
	{
		"mal_id": 999,
		"title": "Sem horario",
		"type": "TV",
		"status": "Currently Airing",
		"broadcast": {"day":"Fridays","time":"","timezone":"Asia/Tokyo"}
	}
]}`

func TestJikanScheduleForDayMapsEntries(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/schedules", r.URL.Path)
		assert.Equal(t, "friday", r.URL.Query().Get("filter"))
		assert.Equal(t, "true", r.URL.Query().Get("sfw"))
		_, _ = fmt.Fprint(w, jikanSchedulePayload)
	})

	// 2026-09-11 is a Friday.
	day := time.Date(2026, 9, 11, 0, 0, 0, 0, time.Local)
	entries, err := jikanScheduleForDay(day)
	require.NoError(t, err)

	// The entry with no broadcast time is dropped rather than placed at midnight.
	require.Len(t, entries, 1)

	e := entries[0]
	assert.Equal(t, 52991, e.AniListID)
	assert.Equal(t, "Sousou no Frieren", e.Title)
	assert.Equal(t, "Frieren: Beyond Journey's End", e.English)
	assert.Equal(t, "https://cdn/frieren.jpg", e.Cover)
	assert.Equal(t, "TV", e.Format)
	assert.Equal(t, "RELEASING", e.Status)

	// Jikan has no episode number, and a guessed one would be worse than none.
	assert.Zero(t, e.Episode)

	// 23:00 JST on that Friday, expressed as an instant.
	want := time.Date(2026, 9, 11, 23, 0, 0, 0, tokyo)
	assert.Equal(t, want.Unix(), e.AiringAt)
	assert.Equal(t, want.Local().Format("15:04"), e.Time)

	// Synonyms feed favorite matching.
	assert.Contains(t, e.matchKeys, matchKey("Frieren at the Funeral"))
}

func TestBroadcastAt(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 9, 11, 0, 0, 0, 0, time.Local)

	at, ok := broadcastAt(day, jikanBroadcast{Time: "23:00", Timezone: "Asia/Tokyo"})
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 9, 11, 23, 0, 0, 0, tokyo).Unix(), at.Unix())

	// An unknown zone still resolves, falling back to JST — the zone almost
	// every broadcast uses, and one a Windows build may not have tzdata for.
	at, ok = broadcastAt(day, jikanBroadcast{Time: "01:30", Timezone: "Nowhere/Fake"})
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 9, 11, 1, 30, 0, 0, tokyo).Unix(), at.Unix())

	for _, bad := range []string{"", "  ", "abc", "2500"} {
		_, ok := broadcastAt(day, jikanBroadcast{Time: bad})
		assert.False(t, ok, "time %q must be rejected", bad)
	}
}

func TestJikanFetchScheduleCoversTheWholeWeek(t *testing.T) {
	days := map[string]int{}
	jikanServer(t, func(w http.ResponseWriter, r *http.Request) {
		days[r.URL.Query().Get("filter")]++
		_, _ = fmt.Fprint(w, `{"pagination":{"has_next_page":false},"data":[]}`)
	})

	start := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local) // Monday
	_, _, err := jikanFetchSchedule(start, start.AddDate(0, 0, scheduleDays))
	require.NoError(t, err)

	assert.Len(t, days, 7, "every weekday is asked for exactly once")
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"} {
		assert.Equal(t, 1, days[d], "weekday %s", d)
	}
}

// One bad day should not throw away the rest of the week.
func TestJikanFetchScheduleMarksAPartialWeek(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("filter") == "wednesday" {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		_, _ = fmt.Fprint(w, jikanSchedulePayload)
	})

	start := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)
	entries, partial, err := jikanFetchSchedule(start, start.AddDate(0, 0, scheduleDays))
	require.NoError(t, err)
	assert.True(t, partial, "the week is reported incomplete")
	assert.NotEmpty(t, entries, "the days that worked are kept")
}

func TestJikanFetchScheduleFailsWhenNoDayWorks(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	})

	start := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)
	_, _, err := jikanFetchSchedule(start, start.AddDate(0, 0, scheduleDays))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MyAnimeList")
}

// A genre the backend cannot map must make it decline, not drop the filter:
// Jikan would list everything under a heading naming the genre.
func TestJikanDeclinesAnUnknownGenre(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[{"mal_id":1,"name":"Action"}]}`)
	})
	resetJikanGenreIndex()

	_, err := jikanCatalogPath(BrowseQuery{Mode: ModeTop, Page: 1, Genre: "Gênero Inventado"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "não conhece o gênero")

	// One it does know still resolves to an id.
	path, err := jikanCatalogPath(BrowseQuery{Mode: ModeTop, Page: 1, Genre: "Action"})
	require.NoError(t, err)
	parsed, perr := url.Parse(path)
	require.NoError(t, perr)
	assert.Equal(t, "1", parsed.Query().Get("genres"))
}
