package app

import (
	"sync"
	"time"
)

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
