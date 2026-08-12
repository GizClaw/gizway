// Package localadmission owns the deliberately ephemeral quota counters used
// by one regional GizWay process.
package localadmission

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"sync"
	"time"
)

type quotaState struct {
	remaining int64
	deadline  time.Time
	denied    bool
	inFlight  int64
}

// Coordinator indexes quota by a per-process HMAC of the raw key. Neither the
// key nor this digest is persisted or sent to GizPay, and constructing a new
// Coordinator intentionally loses every prior answer after process restart.
type Coordinator struct {
	mu     sync.Mutex
	salt   [32]byte
	now    func() time.Time
	states map[[32]byte]quotaState
}

func New(now func() time.Time) *Coordinator {
	if now == nil {
		now = time.Now
	}
	coordinator := &Coordinator{now: now, states: make(map[[32]byte]quotaState)}
	if _, err := rand.Read(coordinator.salt[:]); err != nil {
		panic("local admission requires process entropy: " + err.Error())
	}
	return coordinator
}

// ReplaceResult records both the current amount and whether GizPay explicitly
// denied the key. A denied zero is a fresh answer that must be cached; an
// allowed counter reaching zero means the locally granted quota was consumed
// and may trigger an early refresh.
func (c *Coordinator) ReplaceResult(rawAPIKey string, microcredits int64, deadline time.Time, denied bool) {
	if microcredits < 0 {
		microcredits = 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := c.digest(rawAPIKey)
	state := c.states[key]
	state.remaining = microcredits
	state.deadline = deadline
	state.denied = denied
	c.states[key] = state
}

// TryAdmit atomically distinguishes three outcomes: admitted locally, rejected
// by a still-fresh denied answer, or requiring a GizPay refresh because no
// answer exists, the deadline arrived, or an allowed counter was depleted.
func (c *Coordinator) TryAdmit(rawAPIKey string, microcredits int64) (admitted, refresh bool) {
	if microcredits <= 0 {
		return false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := c.digest(rawAPIKey)
	state, ok := c.states[key]
	if !ok || !c.now().Before(state.deadline) {
		return false, true
	}
	if state.denied {
		return false, false
	}
	if state.remaining < microcredits {
		return false, true
	}
	state.remaining -= microcredits
	state.inFlight++
	c.states[key] = state
	return true, false
}

// Complete releases the unused part of an in-flight commitment, or charges a
// measured overage. A negative local remainder is intentional: the request was
// already admitted and may finish, but no later request can start before the
// next independent GizPay answer replaces this state.
func (c *Coordinator) Complete(rawAPIKey string, committed, actual int64) {
	if committed < 0 || actual < 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := c.digest(rawAPIKey)
	state, ok := c.states[key]
	if !ok {
		return
	}
	state.remaining += committed - actual
	if state.inFlight > 0 {
		state.inFlight--
	}
	c.states[key] = state
}

// Forgettable reports whether no admitted provider call still owns this state
// and the state no longer grants useful quota. A fresh denied result is kept
// until its deadline so repeated customer attempts remain local rejections.
func (c *Coordinator) Forgettable(rawAPIKey string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.states[c.digest(rawAPIKey)]
	if !ok {
		return true
	}
	if state.inFlight != 0 || (state.denied && c.now().Before(state.deadline)) {
		return false
	}
	return !c.now().Before(state.deadline) || state.remaining <= 0
}

// Delete removes one process-local key state after the Runtime has also proved
// that its current-process Usage outbox contains no retryable Usage for that key.
func (c *Coordinator) Delete(rawAPIKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.states, c.digest(rawAPIKey))
}

func (c *Coordinator) digest(rawAPIKey string) [32]byte {
	mac := hmac.New(sha256.New, c.salt[:])
	_, _ = mac.Write([]byte(rawAPIKey))
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}
