package bifrost

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testfake/aiprovider"
)

func TestConfiguredExecutionPolicyIsRetainedForLazyClients(t *testing.T) {
	adapter := NewLazyWithExecution(2, 120*time.Second)
	defer adapter.Shutdown()
	if adapter.maxRetries != 2 || adapter.requestTimeout != 120*time.Second {
		t.Fatalf("execution policy = retries %d timeout %s", adapter.maxRetries, adapter.requestTimeout)
	}
}

func TestConfiguredRequestTimeoutCancelsProviderCall(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(time.Second):
			http.Error(w, "late", http.StatusGatewayTimeout)
		case <-r.Context().Done():
		}
	}))
	defer provider.Close()
	adapter := NewLazyWithExecution(0, 50*time.Millisecond)
	defer adapter.Shutdown()
	started := time.Now()
	_, err := adapter.ChatCompletionCandidates(t.Context(), []store.ProviderExecutionTarget{{
		Provider: "openai", Endpoint: provider.URL, Credential: "secret", Model: "model", RouteKey: "key",
	}}, nil, &schemas.ChatParameters{})
	if err == nil {
		t.Fatal("Provider call ignored configured request_timeout")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Provider call took %s; request_timeout was 50ms", elapsed)
	}
}

func TestZeroRetriesAndProviderCallbacksAffectProviderRequest(t *testing.T) {
	var calls atomic.Int64
	var baseURL, signature atomic.Value
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		baseURL.Store(r.Header.Get("X-Gizway-Public-Base-URL"))
		signature.Store(r.Header.Get("X-Gizway-Callback-Signature"))
		http.Error(w, `{"error":{"message":"fixture failure"}}`, http.StatusInternalServerError)
	}))
	defer provider.Close()
	adapter := NewLazyWithExecution(0, time.Second)
	adapter.ConfigureProviderCallbacks("https://global.example.test", []byte("callback-secret"))
	defer adapter.Shutdown()
	message := "provider callbacks"
	_, _ = adapter.ChatCompletionCandidates(t.Context(), []store.ProviderExecutionTarget{{
		Provider: "openai", Endpoint: provider.URL, Credential: "secret", Model: "model", RouteKey: "key",
	}}, []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: &message}}}, &schemas.ChatParameters{})
	if got := calls.Load(); got != 1 {
		t.Fatalf("max_retries=0 made %d Provider requests, want 1", got)
	}
	if got, _ := baseURL.Load().(string); got != "https://global.example.test" {
		t.Fatalf("provider callback base URL header = %q", got)
	}
	if got, _ := signature.Load().(string); got == "" || got == "callback-secret" {
		t.Fatalf("provider callback signature was not derived securely: %q", got)
	}
}

func TestCandidateCacheIdentityIncludesWeight(t *testing.T) {
	base := []store.ProviderExecutionTarget{{
		Provider: "openai", Endpoint: "https://provider.example", Credential: "secret",
		Model: "model", RouteKey: "key-a", Weight: 1,
	}}
	updated := append([]store.ProviderExecutionTarget(nil), base...)
	updated[0].Weight = 100
	if targetsKey(base) == targetsKey(updated) {
		t.Fatal("changing Provider Key weight did not invalidate the Bifrost client cache")
	}
}

func TestBifrostKeyPoolReturnsTheSelectedKeyID(t *testing.T) {
	provider := httptest.NewServer(aiprovider.HandlerWithCredential("good-secret"))
	defer provider.Close()
	adapter := NewLazy()
	defer adapter.Shutdown()

	targets := []store.ProviderExecutionTarget{
		{Provider: "openai", Endpoint: provider.URL, Credential: "bad-secret", Model: "fake-text-v1", RouteKey: "key-bad", Weight: 100},
		{Provider: "openai", Endpoint: provider.URL, Credential: "good-secret", Model: "fake-text-v1", RouteKey: "key-good", Weight: 1},
	}
	message := "rotate inside one Provider key pool"
	response, err := adapter.ChatCompletionCandidates(t.Context(), targets, []schemas.ChatMessage{{
		Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: &message},
	}}, &schemas.ChatParameters{})
	if err != nil {
		t.Fatal(err)
	}
	if got := response.ExtraFields.RoutingInfo.Key; got != "key-good" {
		t.Fatalf("Bifrost selected key = %q, want key-good", got)
	}
}

func TestBifrostKeyPoolRejectsCrossProviderOrModelCandidates(t *testing.T) {
	adapter := NewLazy()
	defer adapter.Shutdown()
	_, err := adapter.ChatCompletionCandidates(t.Context(), []store.ProviderExecutionTarget{
		{Endpoint: "http://provider-a.test", Credential: "a", Model: "model-a", RouteKey: "a"},
		{Endpoint: "http://provider-b.test", Credential: "b", Model: "model-b", RouteKey: "b"},
	}, nil, &schemas.ChatParameters{})
	if err == nil {
		t.Fatal("mixed Provider/Model candidates were accepted")
	}
	_, err = adapter.ChatCompletionCandidates(t.Context(), []store.ProviderExecutionTarget{
		{Provider: "openai", Endpoint: "http://provider.test", Credential: "a", Model: "model", RouteKey: "a"},
		{Provider: "anthropic", Endpoint: "http://provider.test", Credential: "b", Model: "model", RouteKey: "b"},
	}, nil, &schemas.ChatParameters{})
	if err == nil {
		t.Fatal("mixed Provider kinds were accepted")
	}
}

func TestMilestone02ProviderKinds(t *testing.T) {
	for _, kind := range []string{"openai", "anthropic", "gemini"} {
		provider, err := providerForTarget(store.ProviderExecutionTarget{Provider: kind})
		if err != nil || string(provider) != kind {
			t.Fatalf("providerForTarget(%q) = %q, %v", kind, provider, err)
		}
	}
	if _, err := providerForTarget(store.ProviderExecutionTarget{Provider: "legacy-custom"}); err == nil {
		t.Fatal("unsupported Provider kind was accepted")
	}
}

// These tests cover generic embedded-client lifecycle and Realtime codec
// behavior. The former cross-Provider/credential fallback test was removed
// because Milestone 02 explicitly forbids that business contract.
func TestClientCacheDoesNotEvictAnActiveClient(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer upstream.Close()
	adapter := NewLazy()
	defer adapter.Shutdown()

	activeTarget := store.ProviderExecutionTarget{Endpoint: upstream.URL + "/active", Credential: "secret-active", Model: "model"}
	activeClient, _, releaseActive, err := adapter.clientFor(t.Context(), activeTarget)
	if err != nil {
		t.Fatal(err)
	}
	for index := range maxCachedBifrostClients + 4 {
		target := store.ProviderExecutionTarget{
			Endpoint:   upstream.URL + fmt.Sprintf("/candidate-%d", index),
			Credential: fmt.Sprintf("secret-%d", index), Model: "model",
		}
		_, _, release, err := adapter.clientFor(t.Context(), target)
		if err != nil {
			t.Fatal(err)
		}
		release()
		entry, ok := adapter.clients[targetKey(activeTarget)]
		if !ok || entry.client != activeClient || entry.active != 1 {
			t.Fatalf("active client was evicted during request: ok=%v entry=%+v", ok, entry)
		}
	}
	releaseActive()
	entry := adapter.clients[targetKey(activeTarget)]
	if entry.active != 0 {
		t.Fatalf("active reference count after release = %d", entry.active)
	}
}

func TestEvictIdleClientSkipsPinnedAndActiveEntries(t *testing.T) {
	adapter := NewLazy()
	adapter.clients = map[string]cachedClient{
		"old-idle": {lastUsed: 1},
		"new-idle": {lastUsed: 2},
		"pinned":   {lastUsed: 0, pinned: true},
		"active":   {lastUsed: 0, active: 1},
	}
	adapter.evictIdleClientLocked()
	if _, ok := adapter.clients["old-idle"]; ok {
		t.Fatal("oldest idle client was not evicted")
	}
	if _, ok := adapter.clients["new-idle"]; !ok {
		t.Fatal("newer idle client was evicted first")
	}
	delete(adapter.clients, "new-idle")
	if victim := adapter.evictIdleClientLocked(); victim != nil {
		t.Fatal("pinned or active client was selected for eviction")
	}
}

func TestRealtimeEventCodecAndUsage(t *testing.T) {
	provider := httptest.NewServer(aiprovider.Handler())
	defer provider.Close()

	adapter, err := NewOpenAI(context.Background(), provider.URL, "story-provider-key")
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	defer adapter.Shutdown()

	target := store.ProviderExecutionTarget{Model: "fake-text-v1"}
	clientEvent, err := adapter.RealtimeClientEvent(t.Context(), target, []byte(`{"event_id":"client-response","type":"response.create"}`))
	if err != nil {
		t.Fatalf("RealtimeClientEvent: %v", err)
	}
	if string(clientEvent) != `{"event_id":"client-response","type":"response.create"}` {
		t.Fatalf("translated client event = %s", clientEvent)
	}

	providerEvent := []byte(`{"event_id":"rt-done","type":"response.done","response":{"id":"rt-response-001","status":"completed","output":[],"usage":{"input_tokens":12,"output_tokens":7,"total_tokens":19}}}`)
	public, usage, terminal, err := adapter.RealtimeProviderEvent(t.Context(), target, providerEvent)
	if err != nil {
		t.Fatalf("RealtimeProviderEvent: %v", err)
	}
	if string(public) != string(providerEvent) || !terminal || usage == nil || usage.PromptTokens != 12 || usage.CompletionTokens != 7 {
		t.Fatalf("public=%s terminal=%v usage=%+v", public, terminal, usage)
	}
}
