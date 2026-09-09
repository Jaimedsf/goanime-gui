package animefire

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func episodePayload(streams string) string {
	return fmt.Sprintf(`{"data":{"id":"ep1","title":"Um","season":1,"number":1,"streams":[%s]}}`, streams)
}

func TestGetEpisodeStreamURLPrefersSubbed(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/episode/ep1", r.URL.Path)
		_, _ = fmt.Fprint(w, episodePayload(`
			{"audio":"dublado","is_mtl":false,"is_offline":false,"url":"https://cdn/dub/m.jpg","qualities":["480p"]},
			{"audio":"legendado","is_mtl":false,"is_offline":false,"url":"https://cdn/sub/m.jpg","qualities":["480p"]}
		`))
	}))
	defer server.Close()

	url, err := newTestClient(server).GetEpisodeStreamURL(server.URL + "/anime/abc/ep1")
	require.NoError(t, err)
	assert.Equal(t, "https://cdn/sub/m.jpg", url)
}

func TestGetEpisodeStreamURLFallsBackToDubbed(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, episodePayload(`
			{"audio":"dublado","is_mtl":false,"is_offline":false,"url":"https://cdn/dub/m.jpg","qualities":["720p"]}
		`))
	}))
	defer server.Close()

	url, err := newTestClient(server).GetEpisodeStreamURL(server.URL + "/anime/abc/ep1")
	require.NoError(t, err)
	assert.Equal(t, "https://cdn/dub/m.jpg", url)
}

func TestGetEpisodeStreamURLPrefersCleanTrackOverMTL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, episodePayload(`
			{"audio":"legendado","is_mtl":true,"is_offline":false,"url":"https://cdn/mtl/m.jpg","qualities":["480p"]},
			{"audio":"dublado","is_mtl":false,"is_offline":false,"url":"https://cdn/dub/m.jpg","qualities":["480p"]}
		`))
	}))
	defer server.Close()

	url, err := newTestClient(server).GetEpisodeStreamURL(server.URL + "/anime/abc/ep1")
	require.NoError(t, err)
	assert.Equal(t, "https://cdn/dub/m.jpg", url, "a machine-translated track loses to a clean one")
}

func TestGetEpisodeStreamURLUsesDegradedTrackAsLastResort(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, episodePayload(`
			{"audio":"legendado","is_mtl":true,"is_offline":false,"url":"https://cdn/mtl/m.jpg","qualities":["480p"]}
		`))
	}))
	defer server.Close()

	url, err := newTestClient(server).GetEpisodeStreamURL(server.URL + "/anime/abc/ep1")
	require.NoError(t, err)
	assert.Equal(t, "https://cdn/mtl/m.jpg", url)
}

func TestGetEpisodeStreamURLErrorsWhenNoStream(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, episodePayload(``))
	}))
	defer server.Close()

	_, err := newTestClient(server).GetEpisodeStreamURL(server.URL + "/anime/abc/ep1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no playable stream")
}

func TestGetEpisodeStreamURLIgnoresStreamsWithoutURL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, episodePayload(`
			{"audio":"legendado","is_mtl":false,"is_offline":false,"url":"","qualities":[]},
			{"audio":"dublado","is_mtl":false,"is_offline":false,"url":"https://cdn/dub/m.jpg","qualities":["480p"]}
		`))
	}))
	defer server.Close()

	url, err := newTestClient(server).GetEpisodeStreamURL(server.URL + "/anime/abc/ep1")
	require.NoError(t, err)
	assert.Equal(t, "https://cdn/dub/m.jpg", url)
}

func TestGetEpisodeStreamURLRequiresEpisodeID(t *testing.T) {
	t.Parallel()

	// An anime-level URL has no episode to resolve.
	_, err := NewAnimefireClient().GetEpisodeStreamURL("https://animefire.io/anime/abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no episode id")
}

func TestGetEpisodeStreamURLBlockedPage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	_, err := newTestClient(server).GetEpisodeStreamURL(server.URL + "/anime/abc/ep1")
	require.Error(t, err)
}
