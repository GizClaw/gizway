package app

import (
	"errors"
	"net/http"
	"sync"
	"time"
)

type dependencyObservation struct {
	Status     string    `json:"status"`
	ObservedAt time.Time `json:"observed_at"`
}

type observingRoundTripper struct {
	name    string
	tracker *dependencyTracker
	next    http.RoundTripper
}

func (t observingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.next.RoundTrip(request)
	observationErr := err
	if observationErr == nil && response.StatusCode >= http.StatusInternalServerError {
		observationErr = errors.New(response.Status)
	}
	t.tracker.Observe(t.name, observationErr)
	return response, err
}

func observedHTTPClient(name string, tracker *dependencyTracker, timeout time.Duration) *http.Client {
	return &http.Client{Transport: observingRoundTripper{name: name, tracker: tracker, next: http.DefaultTransport}, Timeout: timeout}
}

// dependencyTracker is updated only by real business traffic. Health reads a
// snapshot; it never turns a probe into a remote dependency request.
type dependencyTracker struct {
	mu           sync.RWMutex
	observations map[string]dependencyObservation
}

func newDependencyTracker() *dependencyTracker {
	return &dependencyTracker{observations: make(map[string]dependencyObservation)}
}

func (t *dependencyTracker) Observe(name string, err error) {
	status := "available"
	if err != nil {
		status = "unavailable"
	}
	t.mu.Lock()
	t.observations[name] = dependencyObservation{Status: status, ObservedAt: time.Now().UTC()}
	t.mu.Unlock()
}

func (t *dependencyTracker) Snapshot(now time.Time, maxAge time.Duration) (map[string]any, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make(map[string]any, len(t.observations))
	healthy := len(t.observations) != 0
	for name, observation := range t.observations {
		status := observation.Status
		if maxAge > 0 && now.Sub(observation.ObservedAt) > maxAge {
			status = "stale"
		}
		if status != "available" {
			healthy = false
		}
		result[name] = map[string]any{"status": status, "observed_at": observation.ObservedAt}
	}
	return result, healthy
}
