package app

import (
	"sync"
	"testing"
	"time"
)

func TestMutableClockIsDeterministicAndConcurrentSafe(t *testing.T) {
	origin := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	clock := newMutableClock(origin)
	if got := clock.Now(); !got.Equal(origin) {
		t.Fatalf("initial clock=%s", got)
	}
	const workers = 8
	var wait sync.WaitGroup
	for range workers {
		wait.Go(func() {
			clock.Advance(time.Nanosecond)
		})
	}
	wait.Wait()
	if got, want := clock.Now(), origin.Add(workers*time.Nanosecond); !got.Equal(want) {
		t.Fatalf("advanced clock=%s want=%s", got, want)
	}
}
