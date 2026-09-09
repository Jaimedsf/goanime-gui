package animefire

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient points a client at srv for both the site and the API, with
// retries disabled unless the test asks for them.
func newTestClient(srv *httptest.Server) *AnimefireClient {
	c := NewAnimefireClient()
	c.baseURL = srv.URL
	c.apiBase = srv.URL
	c.maxRetries = 0
	c.retryDelay = 0
	return c
}

const searchPayload = `{"data":[
	{"id":"eU7t5IvcNKU","title":"Naruto","audio":"Dublado & Legendado","poster_src":"https://img/naruto.jpg","status":"completed","published_at":"2002-10-03"},
	{"id":"V2Q_qcvaKhb","title":"Naruto Shippuden","audio":"Legendado","poster_src":"https://img/shippuden.jpg","status":"completed","published_at":"2007-02-15"}
]}`

func TestSearchAnimeMapsAPIResults(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/animes/pesquisar", r.URL.Path)
		assert.Equal(t, "naruto", r.URL.Query().Get("q"))
		_, _ = fmt.Fprint(w, searchPayload)
	}))
	defer server.Close()

	results, err := newTestClient(server).SearchAnime("naruto")
	require.NoError(t, err)
	require.Len(t, results, 2)

	assert.Equal(t, "Naruto", results[0].Name)
	assert.Equal(t, server.URL+"/anime/eU7t5IvcNKU", results[0].URL)
	assert.Equal(t, "https://img/naruto.jpg", results[0].ImageURL)
	assert.Equal(t, "2002", results[0].Year)

	// Sub-only titles carry the audio in the name so upstream tagging sees it.
	assert.Equal(t, "Naruto Shippuden (Legendado)", results[1].Name)
}

func TestSearchAnimeRetriesOnServerError(t *testing.T) {
	t.Parallel()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = fmt.Fprint(w, searchPayload)
	}))
	defer server.Close()

	client := newTestClient(server)
	client.maxRetries = 2

	results, err := client.SearchAnime("naruto")
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, 2, attempts)
}

func TestSearchAnimeReturnsEmptyWhenNoMatch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()

	results, err := newTestClient(server).SearchAnime("nao-existe")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSearchAnimeRejectsEmptyQuery(t *testing.T) {
	t.Parallel()

	_, err := NewAnimefireClient().SearchAnime("   ")
	require.Error(t, err)
}

func TestSearchAnimeErrorsOnNonJSONBody(t *testing.T) {
	t.Parallel()

	// An interstitial or block page is not a transient fault, so it must not
	// be retried into a success-shaped empty result.
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = fmt.Fprint(w, `<html><body>Just a moment...</body></html>`)
	}))
	defer server.Close()

	client := newTestClient(server)
	client.maxRetries = 2

	_, err := client.SearchAnime("naruto")
	require.Error(t, err)
	assert.Equal(t, 1, hits, "a non-JSON body should not be retried")
}

func TestDecorateTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		title string
		audio string
		want  string
	}{
		{"both tracks stay clean", "Naruto", "Dublado & Legendado", "Naruto"},
		{"dub only", "Naruto", "Dublado", "Naruto (Dublado)"},
		{"sub only", "Naruto", "Legendado", "Naruto (Legendado)"},
		{"unknown audio", "Naruto", "", "Naruto"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, decorateTitle(tt.title, tt.audio))
		})
	}
}

func TestIDsFromURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		wantAnime string
		wantEp    string
		wantErr   bool
	}{
		{"anime only", "https://animefire.io/anime/eU7t5IvcNKU", "eU7t5IvcNKU", "", false},
		{"anime and episode", "https://animefire.io/anime/eU7t5IvcNKU/WCrJufyJmQn", "eU7t5IvcNKU", "WCrJufyJmQn", false},
		{"trailing slash", "https://animefire.io/anime/eU7t5IvcNKU/", "eU7t5IvcNKU", "", false},
		{"retired slug format", "https://animefire.io/animes/naruto-todos-os-episodios", "", "", true},
		{"empty", "", "", "", true},
		{"unrelated", "https://example.com/foo", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			animeID, epID, err := idsFromURL(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantAnime, animeID)
			assert.Equal(t, tt.wantEp, epID)
		})
	}
}

// A library entry saved before the site rewrite must fail with an explanation
// the user can act on, not an opaque parse error.
func TestIDsFromURLExplainsRetiredFormat(t *testing.T) {
	t.Parallel()

	_, _, err := idsFromURL("https://animefire.io/animes/love-hina-todos-os-episodios")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retired")
	assert.Contains(t, err.Error(), "search the title again")
}
