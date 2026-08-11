package app

import (
	"sync"
	"time"
)

// storyFixtureInstant is the documented origin for every executable Hurl
// story. Keeping it in composition (rather than production domain code) makes
// calendar-boundary cases repeatable without changing production clocks.
var storyFixtureInstant = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

type mutableClock struct {
	mu      sync.RWMutex
	current time.Time
}

func newMutableClock(at time.Time) *mutableClock {
	return &mutableClock{current: at.UTC()}
}

func (clock *mutableClock) Now() time.Time {
	clock.mu.RLock()
	defer clock.mu.RUnlock()
	return clock.current
}

func (clock *mutableClock) Advance(by time.Duration) time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.current = clock.current.Add(by)
	return clock.current
}
