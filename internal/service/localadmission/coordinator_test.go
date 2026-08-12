package localadmission

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExchangeReplacesRatherThanAddsQuota(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	coordinator := New(func() time.Time { return now })

	coordinator.ReplaceResult("giz_secret", 100, now.Add(5*time.Minute), false)
	if admitted, _ := coordinator.TryAdmit("giz_secret", 60); !admitted {
		t.Fatal("first local admission was denied")
	}
	coordinator.ReplaceResult("giz_secret", 80, now.Add(5*time.Minute), false)
	if admitted, refresh := coordinator.TryAdmit("giz_secret", 80); !admitted || refresh {
		t.Fatalf("replacement was not an independent 80-credit answer: admitted=%t refresh=%t", admitted, refresh)
	}
	if admitted, refresh := coordinator.TryAdmit("giz_secret", 1); admitted || !refresh {
		t.Fatalf("replacement was accumulated with prior quota: admitted=%t refresh=%t", admitted, refresh)
	}
}

func TestConcurrentAdmissionNeverExceedsLocalQuota(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	coordinator := New(func() time.Time { return now })
	coordinator.ReplaceResult("giz_secret", 100, now.Add(5*time.Minute), false)

	var admitted atomic.Int64
	var wait sync.WaitGroup
	for range 100 {
		wait.Go(func() {
			if accepted, _ := coordinator.TryAdmit("giz_secret", 3); accepted {
				admitted.Add(3)
			}
		})
	}
	wait.Wait()
	if got := admitted.Load(); got != 99 {
		t.Fatalf("admitted = %d, want 99", got)
	}
}

func TestDeadlineFailsClosedAndDifferentKeysAreIsolated(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	coordinator := New(func() time.Time { return now })
	coordinator.ReplaceResult("key-one", 10, now.Add(5*time.Minute), false)
	coordinator.ReplaceResult("key-two", 20, now.Add(5*time.Minute), false)

	now = now.Add(5 * time.Minute)
	if admitted, refresh := coordinator.TryAdmit("key-one", 1); admitted || !refresh {
		t.Fatalf("deadline result = admitted %t, refresh %t", admitted, refresh)
	}
	if admitted, refresh := coordinator.TryAdmit("key-two", 1); admitted || !refresh {
		t.Fatalf("expired second key result = admitted %t, refresh %t", admitted, refresh)
	}
}

func TestProcessRestartStartsWithoutPriorQuota(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	first := New(func() time.Time { return now })
	first.ReplaceResult("giz_secret", 100, now.Add(5*time.Minute), false)

	second := New(func() time.Time { return now })
	if admitted, refresh := second.TryAdmit("giz_secret", 1); admitted || !refresh {
		t.Fatalf("new process result = admitted %t, refresh %t", admitted, refresh)
	}
}

func TestCompletionReplacesCommitmentWithActualUsage(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	coordinator := New(func() time.Time { return now })
	coordinator.ReplaceResult("giz_secret", 100, now.Add(5*time.Minute), false)
	if admitted, _ := coordinator.TryAdmit("giz_secret", 60); !admitted {
		t.Fatal("commitment was denied")
	}
	coordinator.Complete("giz_secret", 60, 25)
	if admitted, _ := coordinator.TryAdmit("giz_secret", 70); !admitted {
		t.Fatal("second commitment was denied")
	}
	coordinator.Complete("giz_secret", 70, 90)
	if admitted, refresh := coordinator.TryAdmit("giz_secret", 1); admitted || !refresh {
		t.Fatalf("post-overage result = admitted %t, refresh %t", admitted, refresh)
	}
}

func TestDeniedAndForgettableLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	coordinator := New(func() time.Time { return now })
	if !coordinator.Forgettable("missing") {
		t.Fatal("missing state was not forgettable")
	}
	coordinator.ReplaceResult("denied", -1, now.Add(time.Minute), true)
	if admitted, refresh := coordinator.TryAdmit("denied", 1); admitted || refresh {
		t.Fatalf("fresh denial = admitted %t, refresh %t", admitted, refresh)
	}
	if coordinator.Forgettable("denied") {
		t.Fatal("fresh denial was discarded before its deadline")
	}
	now = now.Add(time.Minute)
	if !coordinator.Forgettable("denied") {
		t.Fatal("expired denial was not forgettable")
	}
	coordinator.Delete("denied")
	if admitted, refresh := coordinator.TryAdmit("denied", 1); admitted || !refresh {
		t.Fatalf("deleted state = admitted %t, refresh %t", admitted, refresh)
	}
}

func TestInFlightStateIsNotForgettable(t *testing.T) {
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	coordinator := New(func() time.Time { return now })
	coordinator.ReplaceResult("active", 1, now.Add(time.Minute), false)
	if admitted, _ := coordinator.TryAdmit("active", 1); !admitted {
		t.Fatal("active state was not admitted")
	}
	if coordinator.Forgettable("active") {
		t.Fatal("in-flight state was forgettable")
	}
	coordinator.Complete("active", -1, 0)
	if coordinator.Forgettable("active") {
		t.Fatal("invalid completion changed in-flight state")
	}
	coordinator.Complete("active", 1, 1)
	if !coordinator.Forgettable("active") {
		t.Fatal("depleted completed state was not forgettable")
	}
}
