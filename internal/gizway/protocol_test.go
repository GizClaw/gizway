package gizway

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/GizClaw/gizway/internal/store"
)

func TestChatParametersAndAnthropicVersionArePreserved(t *testing.T) {
	maxTokens := 37
	parameters, err := (chatRequest{
		MaxTokens:     &maxTokens,
		StreamOptions: json.RawMessage(`{"include_usage":true,"include_obfuscation":true}`),
	}).parameters()
	if err != nil {
		t.Fatal(err)
	}
	if parameters.MaxCompletionTokens == nil || *parameters.MaxCompletionTokens != maxTokens {
		t.Fatalf("max_tokens was not preserved: %+v", parameters.MaxCompletionTokens)
	}
	if parameters.StreamOptions == nil || parameters.StreamOptions.IncludeUsage == nil || !*parameters.StreamOptions.IncludeUsage || parameters.StreamOptions.IncludeObfuscation == nil || !*parameters.StreamOptions.IncludeObfuscation {
		t.Fatalf("stream_options were not preserved: %+v", parameters.StreamOptions)
	}
	streamOptions, ok := parameters.ExtraParams["stream_options"].(map[string]any)
	if !ok || streamOptions["include_obfuscation"] != true {
		t.Fatalf("include_obfuscation compatibility passthrough = %#v", parameters.ExtraParams)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	request.Header.Set("anthropic-version", "2023-06-01")
	headers, err := protocolHeaders(request, "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if got := headers["anthropic-version"]; len(got) != 1 || got[0] != "2023-06-01" {
		t.Fatalf("anthropic-version passthrough = %v", got)
	}
}

func TestAnthropicVersionIsRequired(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	if _, err := protocolHeaders(request, "anthropic"); err == nil {
		t.Fatal("missing anthropic-version was accepted")
	}
}

func TestGeminiProtocolAllowsOnlyLiteralOperations(t *testing.T) {
	for path, wantStream := range map[string]bool{
		"/genai/v1beta/models/gemini-2.5:generateContent":       false,
		"/genai/v1beta/models/gemini-2.5:streamGenerateContent": true,
	} {
		model, stream, ok := geminiModelOperation(path)
		if !ok || model != "gemini-2.5" || stream != wantStream {
			t.Fatalf("geminiModelOperation(%q) = %q, %v, %v", path, model, stream, ok)
		}
	}
	for _, path := range []string{
		"/genai/v1beta/models/gemini-2.5:unknown",
		"/genai/v1beta/models/:generateContent",
		"/genai/v1beta/models/a/b:generateContent",
	} {
		if _, _, ok := geminiModelOperation(path); ok {
			t.Fatalf("unknown Gemini operation %q was accepted", path)
		}
	}
}

func TestPruneRealtimeSessionsRemovesUnusedExpiredSecrets(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	handler := &Handler{
		config: Config{Now: func() time.Time { return now }},
		realtime: map[string]realtimeSession{
			"expired": {ExpiresAt: now},
			"active":  {ExpiresAt: now.Add(time.Second)},
		},
	}
	handler.pruneRealtimeSessions()
	if _, ok := handler.realtime["expired"]; ok {
		t.Fatal("expired unused Realtime Session remained in memory")
	}
	if _, ok := handler.realtime["active"]; !ok {
		t.Fatal("active Realtime Session was pruned")
	}
}

func TestCeilMulDiv(t *testing.T) {
	tests := []struct {
		name                 string
		left, right, divisor int64
		want                 int64
	}{
		{name: "exact", left: 10, right: 3, divisor: 5, want: 6},
		{name: "round up", left: 1, right: 1, divisor: 3, want: 1},
		{name: "large exact product", left: math.MaxInt64, right: 1, divisor: 1, want: math.MaxInt64},
		{name: "saturates", left: math.MaxInt64, right: math.MaxInt64, divisor: 1, want: math.MaxInt64},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ceilMulDiv(test.left, test.right, test.divisor); got != test.want {
				t.Fatalf("ceilMulDiv(%d, %d, %d) = %d, want %d", test.left, test.right, test.divisor, got, test.want)
			}
		})
	}
}

func TestRateRejectsUnknownMetrics(t *testing.T) {
	usage := &schemas.BifrostLLMUsage{PromptTokens: 11, CompletionTokens: 7}
	if _, err := rate(usage, []priceRow{{Metric: "cached_token", Unit: 1, Amount: 1}}); err == nil {
		t.Fatal("unknown metric was silently billed as input_tokens")
	}
}

func TestRateMapsInputAndOutputMetricsExplicitly(t *testing.T) {
	usage := &schemas.BifrostLLMUsage{PromptTokens: 11, CompletionTokens: 7}
	got, err := rate(usage, []priceRow{
		{Metric: "input_tokens", Unit: 1, Amount: 2},
		{Metric: "output_tokens", Unit: 1, Amount: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != 43 {
		t.Fatalf("rated usage = %d, want 43", got)
	}
}

func TestConsumeLocalCreditSaturatesWithoutOverflow(t *testing.T) {
	handler := &Handler{credits: map[string]*creditState{
		"normal":   {available: 10},
		"boundary": {available: math.MinInt64 + 2},
	}}

	handler.consumeLocalCredit("normal", 7)
	if got := handler.credits["normal"].available; got != 3 {
		t.Fatalf("normal local Credit = %d, want 3", got)
	}

	handler.consumeLocalCredit("boundary", 3)
	if got := handler.credits["boundary"].available; got != math.MinInt64 {
		t.Fatalf("overflowing local Credit = %d, want saturated %d", got, int64(math.MinInt64))
	}
}

func TestAdmitCachesDeniedCreditUntilCheckedAtExpiry(t *testing.T) {
	clock := newCreditTestClock(time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC))
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writeCreditCheckResponse(t, w, "denied", 7, clock.Now(), 30)
			return
		}
		writeCreditCheckResponse(t, w, "allowed", 10, clock.Now(), 30)
	}))
	defer server.Close()
	handler := creditTestHandler(clock, server)

	admission, err := handler.admit(t.Context(), "denied-key")
	if err != nil || admission.allowed {
		t.Fatalf("first denied admission = allowed %v err %v", admission.allowed, err)
	}
	admission, err = handler.admit(t.Context(), "denied-key")
	if err != nil || admission.allowed {
		t.Fatalf("cached denied admission = allowed %v err %v", admission.allowed, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("denied result made %d Credit Check requests before expiry, want 1", got)
	}

	clock.Advance(30 * time.Second)
	admission, err = handler.admit(t.Context(), "denied-key")
	if err != nil || !admission.allowed {
		t.Fatalf("post-expiry admission = allowed %v err %v", admission.allowed, err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expired result made %d Credit Check requests, want 2", got)
	}
}

func TestAdmissionOwnershipSurvivesCachePruning(t *testing.T) {
	clock := newCreditTestClock(time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeCreditCheckResponse(t, w, "allowed", 10, clock.Now(), 30)
	}))
	defer server.Close()
	handler := creditTestHandler(clock, server)

	admission, err := handler.admit(t.Context(), "ownership-key")
	if err != nil || !admission.allowed {
		t.Fatalf("admission = allowed %v err %v", admission.allowed, err)
	}
	clock.Advance(30 * time.Second)
	handler.pruneCreditStates()
	if got := creditCacheSize(handler); got != 0 {
		t.Fatalf("expired Credit cache size = %d, want 0", got)
	}

	call := resolvedCall{}
	admission.applyTo(&call)
	if call.AccountID != "account" || call.SubscriptionID != "subscription" || call.ProductID != "product" || call.OwnerIssuer != "https://issuer.test" || call.OwnerSubject != "subject" {
		t.Fatalf("ownership after cache pruning = %+v", call)
	}
}

func TestAdmitCachesAllowedZeroCreditAsDenied(t *testing.T) {
	clock := newCreditTestClock(time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC))
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeCreditCheckResponse(t, w, "allowed", 0, clock.Now(), 30)
	}))
	defer server.Close()
	handler := creditTestHandler(clock, server)

	for range 2 {
		admission, err := handler.admit(t.Context(), "zero-key")
		if err != nil || admission.allowed {
			t.Fatalf("zero-Credit admission = allowed %v err %v", admission.allowed, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("zero-Credit result made %d Credit Check requests before expiry, want 1", got)
	}
}

func TestAdmitCoalescesConcurrentDeniedChecks(t *testing.T) {
	clock := newCreditTestClock(time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC))
	var calls atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		writeCreditCheckResponse(t, w, "denied", 0, clock.Now(), 30)
	}))
	defer server.Close()
	handler := creditTestHandler(clock, server)

	const requests = 32
	results := make(chan error, requests)
	for range requests {
		go func() {
			admission, err := handler.admit(t.Context(), "concurrent-denied-key")
			if err == nil && admission.allowed {
				err = errors.New("denied Credit Check allowed a request")
			}
			results <- err
		}()
	}
	<-started
	close(release)
	for range requests {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent denied admissions made %d Credit Check requests, want 1", got)
	}
}

func TestAdmitDoesNotCacheInvalidSubscriptionKey(t *testing.T) {
	clock := newCreditTestClock(time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC))
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	handler := creditTestHandler(clock, server)

	for range 2 {
		if admission, err := handler.admit(t.Context(), "invalid-key"); err == nil || admission.allowed {
			t.Fatalf("invalid Key admission = allowed %v err %v", admission.allowed, err)
		}
		if got := creditCacheSize(handler); got != 0 {
			t.Fatalf("invalid Key left %d cached records, want 0", got)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("invalid Key made %d Credit Check requests, want 2 uncached attempts", got)
	}
}

func TestCreditCacheWorkerPrunesLargeExpiredSet(t *testing.T) {
	clock := newCreditTestClock(time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC))
	handler := &Handler{
		config: Config{Now: clock.Now, CreditCacheCleanupInterval: time.Millisecond},
		credits: map[string]*creditState{
			"future":  {expires: clock.Now().Add(time.Minute)},
			"loading": {expires: clock.Now().Add(-time.Minute), loading: true},
		},
		realtime: map[string]realtimeSession{}, stop: make(chan struct{}), done: make(chan struct{}),
	}
	for index := range 4096 {
		handler.credits["expired-"+strconv.Itoa(index)] = &creditState{expires: clock.Now().Add(-time.Minute)}
	}
	go handler.runBackgroundWorkers()
	t.Cleanup(func() { _ = handler.Close() })

	waitForCreditCacheSize(t, handler, 2)
	handler.creditMu.Lock()
	handler.credits["loading"].loading = false
	handler.creditMu.Unlock()
	waitForCreditCacheSize(t, handler, 1)
}

func TestOutboxTicksDoNotPruneCreditCache(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	ticks := make(chan struct{}, 16)
	handler := &Handler{
		config: Config{
			Now: func() time.Time {
				select {
				case ticks <- struct{}{}:
				default:
				}
				return now
			},
			OutboxRetryInterval: time.Millisecond,
		},
		credits:  map[string]*creditState{"expired": {expires: now.Add(-time.Minute)}},
		realtime: map[string]realtimeSession{}, stop: make(chan struct{}), done: make(chan struct{}),
	}
	workerDone := make(chan struct{})
	go func() {
		handler.runOutbox()
		close(workerDone)
	}()
	t.Cleanup(func() {
		close(handler.stop)
		<-workerDone
	})

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for range 5 {
		select {
		case <-ticks:
		case <-deadline.C:
			t.Fatal("Outbox worker did not produce five ticks")
		}
	}
	if got := creditCacheSize(handler); got != 1 {
		t.Fatalf("Outbox ticks changed Credit cache size to %d, want 1", got)
	}
}

type creditTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func newCreditTestClock(now time.Time) *creditTestClock { return &creditTestClock{now: now} }

func (c *creditTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *creditTestClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

func creditTestHandler(clock *creditTestClock, server *httptest.Server) *Handler {
	return &Handler{
		config: Config{
			Now: clock.Now, GizPayURL: server.URL, HTTPClient: server.Client(),
			ServiceToken: func(context.Context) (string, error) { return "service-token", nil },
		},
		credits: map[string]*creditState{},
	}
}

func writeCreditCheckResponse(t *testing.T, w http.ResponseWriter, status string, available int64, checkedAt time.Time, recheckAfter int64) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status": status, "available_microcredits": available,
		"account_id": "account", "subscription_id": "subscription", "product_id": "product",
		"billing_mode": "pay_as_you_go", "owner_identity_issuer": "https://issuer.test",
		"owner_identity_subject": "subject", "checked_at": checkedAt, "recheck_after_seconds": recheckAfter,
	}); err != nil {
		t.Error(err)
	}
}

func creditCacheSize(handler *Handler) int {
	handler.creditMu.Lock()
	defer handler.creditMu.Unlock()
	return len(handler.credits)
}

func waitForCreditCacheSize(t *testing.T, handler *Handler, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if got := creditCacheSize(handler); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Credit cache size = %d, want %d", creditCacheSize(handler), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCheapestTargetsKeepsOnlyLowestPriceTies(t *testing.T) {
	targets := []store.ProviderExecutionTarget{
		{RouteKey: "expensive", Weight: 1},
		{RouteKey: "cheap-a", Weight: 1},
		{RouteKey: "cheap-b", Weight: 2},
	}
	payments := map[string]keyPayment{
		"expensive": {Prices: []priceRow{{Metric: "input_tokens", Unit: 1_000_000, Amount: 1_900}}},
		"cheap-a":   {Prices: []priceRow{{Metric: "input_tokens", Unit: 1_000, Amount: 1}}},
		"cheap-b":   {Prices: []priceRow{{Metric: "input_tokens", Unit: 2_000, Amount: 2}}},
	}

	got := cheapestTargets(targets, payments)
	if len(got) != 2 || got[0].RouteKey != "cheap-a" || got[1].RouteKey != "cheap-b" {
		t.Fatalf("cheapest targets = %+v, want the two equal-price keys", got)
	}
}

func TestPricesCoverEveryCustomerMetric(t *testing.T) {
	customer := []priceRow{{Metric: "input_tokens"}, {Metric: "output_tokens"}}
	tests := []struct {
		name     string
		provider []priceRow
		customer []priceRow
		want     bool
	}{
		{name: "complete", provider: []priceRow{{Metric: "input_tokens"}, {Metric: "output_tokens"}}, customer: customer, want: true},
		{name: "extra metric", provider: []priceRow{{Metric: "input_tokens"}, {Metric: "output_tokens"}, {Metric: "cached_tokens"}}, customer: customer, want: true},
		{name: "missing metric", provider: []priceRow{{Metric: "input_tokens"}}, customer: customer, want: false},
		{name: "no customer prices", provider: []priceRow{{Metric: "input_tokens"}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := pricesCoverMetrics(test.provider, test.customer); got != test.want {
				t.Fatalf("pricesCoverMetrics() = %v, want %v", got, test.want)
			}
		})
	}
}
