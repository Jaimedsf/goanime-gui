package animefire

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const singleSeasonPayload = `{"data":{
	"format":"tv",
	"hero":{"id":"abc","titles":{"BR":"Love Hina"},"audio":"Legendado"},
	"episodes":[
		{"id":"ep2","title":"Segundo","audio":"Legendado","season":1,"number":2,"synopsis":"dois"},
		{"id":"ep1","title":"Primeiro","audio":"Legendado","season":1,"number":1,"synopsis":"um"}
	]
}}`

const multiSeasonPayload = `{"data":{
	"format":"tv",
	"hero":{"id":"abc","titles":{"BR":"Naruto"}},
	"episodes":[
		{"id":"s2e1","title":"S2 um","season":2,"number":1},
		{"id":"s1e1","title":"S1 um","season":1,"number":1},
		{"id":"s1e2","title":"S1 dois","season":1,"number":2}
	]
}}`

func TestGetAnimeEpisodesMapsAndSorts(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/anime/abc", r.URL.Path)
		_, _ = fmt.Fprint(w, singleSeasonPayload)
	}))
	defer server.Close()

	client := newTestClient(server)
	episodes, err := client.GetAnimeEpisodes(server.URL + "/anime/abc")
	require.NoError(t, err)
	require.Len(t, episodes, 2)

	// Out-of-order API data comes back in viewing order.
	assert.Equal(t, 1, episodes[0].Num)
	assert.Equal(t, 2, episodes[1].Num)

	assert.Equal(t, "Episódio 1", episodes[0].Number)
	assert.Equal(t, server.URL+"/anime/abc/ep1", episodes[0].URL)
	assert.Equal(t, "Primeiro", episodes[0].Title.Romaji)
	assert.Equal(t, "um", episodes[0].Synopsis)

	// A single-season title carries no season id, matching a source that does
	// not advertise seasons.
	assert.Empty(t, episodes[0].SeasonID)
}

func TestGetAnimeEpisodesLabelsSeasonsWhenNumberingRestarts(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, multiSeasonPayload)
	}))
	defer server.Close()

	episodes, err := newTestClient(server).GetAnimeEpisodes(server.URL + "/anime/abc")
	require.NoError(t, err)
	require.Len(t, episodes, 3)

	assert.Equal(t, "T1 Episódio 1", episodes[0].Number)
	assert.Equal(t, "1", episodes[0].SeasonID)
	assert.Equal(t, "T2 Episódio 1", episodes[2].Number)
	assert.Equal(t, "2", episodes[2].SeasonID)
}

func TestGetAnimeEpisodesHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := newTestClient(server).GetAnimeEpisodes(server.URL + "/anime/abc")
	require.Error(t, err)
}

func TestGetAnimeEpisodesErrorsWhenListEmpty(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":{"hero":{"id":"abc"},"episodes":[]}}`)
	}))
	defer server.Close()

	_, err := newTestClient(server).GetAnimeEpisodes(server.URL + "/anime/abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no episodes")
}

func TestGetAnimeEpisodesRejectsRetiredURL(t *testing.T) {
	t.Parallel()

	_, err := NewAnimefireClient().GetAnimeEpisodes("https://animefire.io/animes/love-hina-todos-os-episodios")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retired")
}

func TestGetAnimeDetailsReturnsError(t *testing.T) {
	t.Parallel()

	_, err := NewAnimefireClient().GetAnimeDetails("https://animefire.io/anime/abc")
	require.Error(t, err)
}

// AnimeFire restarts episode numbering inside each season, but upstream treats
// this source as a flat list — so Num has to stay unique and monotonic.
func TestGetAnimeEpisodesAssignsAbsoluteNumbers(t *testing.T) {
	t.Parallel()

	payload := `{"data":{
		"hero":{"id":"abc"},
		"seasons":[
			{"number":1,"first_episode_number":1},
			{"number":2,"first_episode_number":53}
		],
		"episodes":[
			{"id":"s1e1","season":1,"number":1},
			{"id":"s1e52","season":1,"number":52},
			{"id":"s2e1","season":2,"number":1},
			{"id":"s2e2","season":2,"number":2}
		]
	}}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, payload)
	}))
	defer server.Close()

	episodes, err := newTestClient(server).GetAnimeEpisodes(server.URL + "/anime/abc")
	require.NoError(t, err)
	require.Len(t, episodes, 4)

	assert.Equal(t, []int{1, 52, 53, 54}, []int{
		episodes[0].Num, episodes[1].Num, episodes[2].Num, episodes[3].Num,
	})

	// The label stays season-relative so the viewer still recognises it.
	assert.Equal(t, "T2 Episódio 1", episodes[2].Number)
}

// Without a seasons block the offsets have to be derived from the episodes.
func TestGetAnimeEpisodesDerivesOffsetsWhenSeasonsMissing(t *testing.T) {
	t.Parallel()

	payload := `{"data":{
		"hero":{"id":"abc"},
		"episodes":[
			{"id":"s1e1","season":1,"number":1},
			{"id":"s1e2","season":1,"number":2},
			{"id":"s2e1","season":2,"number":1}
		]
	}}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, payload)
	}))
	defer server.Close()

	episodes, err := newTestClient(server).GetAnimeEpisodes(server.URL + "/anime/abc")
	require.NoError(t, err)
	require.Len(t, episodes, 3)

	assert.Equal(t, []int{1, 2, 3}, []int{episodes[0].Num, episodes[1].Num, episodes[2].Num})
}
