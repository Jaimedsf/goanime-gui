package guiapi

import (
	"errors"
	"sync"
	"time"
)

// A metadata backend that is down costs far more than one failed request.
//
// The adult filter classifies every search result, so a page of thirty hits
// is thirty lookups; each one is paced behind that backend's rate gate, and
// with two backends chained a dead pair means every title waits for both
// before failing. That is how a search that should take half a second came
// to take eighteen: the scraper answered immediately and the classification
// pass then burned its entire budget discovering, thirty times over, that
// the API was still down.
//
// The breaker makes that discovery once. After a few consecutive failures it
// short-circuits, so the remaining lookups fail instantly and the search
// returns at the speed of the scraper. It reopens on its own, and a single
// success resets it, so a backend coming back is picked up without a restart.
type apiBreaker struct {
	name string

	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

const (
	// breakerThreshold is how many consecutive failures trip the breaker. It
	// is small on purpose: the point is to stop paying for a dead backend
	// early, and one success clears the count.
	breakerThreshold = 3
	// breakerCooldown is how long it stays open. Long enough that one search
	// makes a handful of attempts rather than dozens, short enough that a
	// backend recovering is noticed within a session.
	breakerCooldown = 60 * time.Second
)

// allow reports whether a request may go out now.
func (b *apiBreaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Now().After(b.openUntil)
}

// success records a completed call and closes the breaker.
func (b *apiBreaker) success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.openUntil = time.Time{}
}

// failure records a call that did not complete, opening the breaker once the
// failures run consecutive.
func (b *apiBreaker) failure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures++
	if b.failures >= breakerThreshold {
		b.openUntil = time.Now().Add(breakerCooldown)
	}
}

// reset returns the breaker to its initial state. Tests use it so one test's
// failures cannot trip the breaker for the next.
func (b *apiBreaker) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.openUntil = time.Time{}
}

// errBackendUnavailable is what an open breaker returns. It is deliberately
// distinct from a "no such title" answer: callers must not cache it as one.
var errBackendUnavailable = errors.New("catálogo indisponível no momento")
