package app

import (
	"errors"
	"testing"
	"time"
)

func TestDependencyObservationUsesMaxAgeWithoutRemoteWork(t *testing.T) {
	tracker := newDependencyTracker()
	if _, healthy := tracker.Snapshot(time.Now(), time.Minute); healthy {
		t.Fatal("an unobserved dependency set was healthy")
	}
	tracker.Observe("zitadel", nil)
	tracker.mu.Lock()
	observed := tracker.observations["zitadel"]
	observed.ObservedAt = time.Now().Add(-2 * time.Minute)
	tracker.observations["zitadel"] = observed
	tracker.mu.Unlock()
	snapshot, healthy := tracker.Snapshot(time.Now(), time.Minute)
	if healthy || snapshot["zitadel"].(map[string]any)["status"] != "stale" {
		t.Fatalf("stale snapshot = %#v healthy=%v", snapshot, healthy)
	}
	tracker.Observe("zitadel", errors.New("down"))
	snapshot, healthy = tracker.Snapshot(time.Now(), time.Minute)
	if healthy || snapshot["zitadel"].(map[string]any)["status"] != "unavailable" {
		t.Fatalf("failed snapshot = %#v healthy=%v", snapshot, healthy)
	}
}
