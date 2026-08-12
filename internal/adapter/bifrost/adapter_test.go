package bifrost

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testfake/aiprovider"
	"github.com/maximhq/bifrost/core/schemas"
)

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

func TestCandidatesFallbackAcrossDistinctEndpointsAndCredentials(t *testing.T) {
	var primaryCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		http.Error(w, `{"error":{"message":"primary unavailable"}}`, http.StatusInternalServerError)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(aiprovider.HandlerWithCredential("fallback-secret"))
	defer fallback.Close()

	adapter, err := NewOpenAI(t.Context(), primary.URL, "bootstrap-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Shutdown()
	targets := []store.ProviderExecutionTarget{
		{Endpoint: primary.URL, Credential: "primary-secret", Model: "fake-text-v1", RouteKey: "primary-variant"},
		{Endpoint: fallback.URL, Credential: "fallback-secret", Model: "fake-text-v2", RouteKey: "fallback-variant"},
	}
	content := "fallback-required"
	chat, err := adapter.ChatCompletionCandidates(t.Context(), targets, []schemas.ChatMessage{{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: &content}}}, nil)
	if err != nil {
		t.Fatalf("ChatCompletionCandidates: %v", err)
	}
	if chat.ExtraFields.RoutingInfo.Provider != "gizway-fallback-variant" || chat.Model != "fake-text-v2" || primaryCalls.Load() != 2 {
		t.Fatalf("chat provider=%q model=%q primary calls=%d", chat.ExtraFields.RoutingInfo.Provider, chat.Model, primaryCalls.Load())
	}

	role := schemas.ResponsesInputMessageRoleUser
	response, err := adapter.ResponsesCandidates(t.Context(), targets, &schemas.BifrostResponsesRequest{Input: []schemas.ResponsesMessage{{Role: &role}}})
	if err != nil {
		t.Fatalf("ResponsesCandidates: %v", err)
	}
	if response.ExtraFields.RoutingInfo.Provider != "gizway-fallback-variant" || response.Model != "fake-text-v2" || primaryCalls.Load() != 4 {
		t.Fatalf("responses provider=%q model=%q primary calls=%d", response.ExtraFields.RoutingInfo.Provider, response.Model, primaryCalls.Load())
	}
}
