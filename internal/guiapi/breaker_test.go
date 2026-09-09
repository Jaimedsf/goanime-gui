package guiapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	t.Parallel()
	b := &apiBreaker{name: "test"}

	for i := range breakerThreshold - 1 {
		b.failure()
		assert.True(t, b.allow(), "still closed after %d failures", i+1)
	}

	b.failure()
	assert.False(t, b.allow(), "open once the threshold is reached")
}

func TestBreakerSuccessResetsTheCount(t *testing.T) {
	t.Parallel()
	b := &apiBreaker{name: "test"}

	// A backend that fails intermittently must not trip: only a consecutive
	// run means it is actually down.
	for range breakerThreshold * 3 {
		b.failure()
		b.success()
	}
	assert.True(t, b.allow())
}

func TestBreakerRecoversAfterCooldown(t *testing.T) {
	t.Parallel()
	b := &apiBreaker{name: "test"}

	for range breakerThreshold {
		b.failure()
	}
	require.False(t, b.allow())

	// Simulate the cooldown elapsing rather than sleeping a minute.
	b.mu.Lock()
	b.openUntil = time.Now().Add(-time.Second)
	b.mu.Unlock()

	assert.True(t, b.allow(), "reopens on its own so a recovered backend is picked up")
}

// The point of the breaker: a dead backend is discovered once, not once per
// lookup. Without it, classifying a page of results costs one full request
// each.
func TestJikanBreakerStopsHammeringADeadBackend(t *testing.T) {
	hits := 0
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusGatewayTimeout)
	})

	for range 30 {
		_, _ = fetchJikanMedia("qualquer titulo", false)
	}

	assert.Equal(t, breakerThreshold, hits,
		"thirty lookups against a dead backend must cost only the attempts that trip the breaker")
}

func TestJikanBreakerReopensOnSuccess(t *testing.T) {
	down := true
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		if down {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		_, _ = fmt.Fprint(w, jikanSearchOnePayload)
	})

	for range breakerThreshold {
		_, _ = fetchJikanMedia("x", false)
	}
	require.False(t, jikanBreaker.allow(), "breaker tripped")

	// The backend comes back; clear the cooldown the way time would.
	down = false
	jikanBreaker.reset()

	m, err := fetchJikanMedia("Naruto", false)
	require.NoError(t, err)
	assert.Equal(t, 20, m.malID)
	assert.True(t, jikanBreaker.allow(), "a success closes it again")
}

// An empty result set means the API answered. Treating it as an outage would
// trip the breaker on nothing more than an obscure title.
func TestJikanBreakerIgnoresNotFound(t *testing.T) {
	jikanServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"pagination":{"has_next_page":false},"data":[]}`)
	})

	for range breakerThreshold * 2 {
		_, err := fetchJikanMedia("titulo obscuro", false)
		require.ErrorIs(t, err, errTitleNotFound)
	}
	assert.True(t, jikanBreaker.allow())
}
