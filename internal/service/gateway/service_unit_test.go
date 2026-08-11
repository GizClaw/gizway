package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
	"github.com/idy/gizway/internal/timetext"
)

func TestStoryCrashRecoveryConfiguration(t *testing.T) {
	service := New(nil, nil)
	called := false
	service.ConfigureStoryCrashRecovery(time.Second, func() { called = true })
	service.providerSucceeded()
	if !called || service.executionLease != time.Second {
		t.Fatalf("crash hook called=%v lease=%s", called, service.executionLease)
	}
	service.ConfigureStoryCrashRecovery(0, nil)
	if service.executionLease != time.Second {
		t.Fatalf("non-positive lease changed configuration: %s", service.executionLease)
	}
}

func TestRecoverRealtimeProviderUsageSettlesDurableEvent(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	service := New(repository, nil)
	service.realtimeCallbackSecret = []byte("unit-test-callback-master")
	service.realtimeCallbackURL = "https://gizway.test"
	service.now = func() time.Time { return time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC) }
	created, err := service.CreateRealtimeSession(t.Context(), store.GatewayPrincipal{
		UserID: "11000000-0000-4000-8000-000000000001", AccountID: "21000000-0000-4000-8000-000000000001", APIKeyID: "31000000-0000-4000-8000-000000000001",
	}, "recover-realtime", RealtimeRequest{Model: "story-text", Transport: "webrtc"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConnectRealtimeSession(t.Context(), created.ClientSecret, "webrtc"); err != nil {
		t.Fatal(err)
	}
	payloadHash := sha256.Sum256([]byte("authenticated terminal usage"))
	if _, err := repository.RecordRealtimeProviderEvent(t.Context(), "recover-realtime-event", created.Session.ID, payloadHash[:], 12, 7, 2, 4, 2, timetext.Format(service.now())); err != nil {
		t.Fatal(err)
	}
	if err := service.RecoverRealtimeProviderEvents(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	session, err := repository.GetRealtimeSession(t.Context(), created.Session.ID)
	if err != nil || session.Status != "succeeded" {
		t.Fatalf("recovered session=%+v err=%v", session, err)
	}
	remaining, err := repository.RecoverableRealtimeProviderEvents(t.Context(), 10)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("remaining events=%+v err=%v", remaining, err)
	}
	// Recovery is idempotent after the session and provider journal are both
	// terminal; missing and non-WebRTC/non-connected sessions remain invalid.
	processed, err := service.completeRecordedRealtimeProviderEvent(t.Context(), store.RealtimeProviderUsageEvent{EventID: "recover-realtime-event", SessionID: created.Session.ID}, timetext.Format(service.now()))
	if err != nil || processed.Status != "succeeded" {
		t.Fatalf("processed replay=%+v err=%v", processed, err)
	}
	if _, err := service.completeRecordedRealtimeProviderEvent(t.Context(), store.RealtimeProviderUsageEvent{EventID: "missing", SessionID: "missing"}, timetext.Format(service.now())); !errors.Is(err, ErrInvalidProviderEvent) {
		t.Fatalf("missing recorded session err=%v", err)
	}
	websocketSession, err := service.CreateRealtimeSession(t.Context(), store.GatewayPrincipal{
		UserID: "11000000-0000-4000-8000-000000000001", AccountID: "21000000-0000-4000-8000-000000000001", APIKeyID: "31000000-0000-4000-8000-000000000001",
	}, "recover-invalid-websocket", RealtimeRequest{Model: "story-text", Transport: "websocket"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.completeRecordedRealtimeProviderEvent(t.Context(), store.RealtimeProviderUsageEvent{EventID: "websocket", SessionID: websocketSession.Session.ID}, timetext.Format(service.now())); !errors.Is(err, ErrInvalidProviderEvent) {
		t.Fatalf("websocket provider callback err=%v", err)
	}
	if err := service.RecoverRealtimeProviderEvents(t.Context(), 0); err == nil {
		t.Fatal("zero recovery batch succeeded")
	}
}

func testPrices() map[string]store.GatewayPrice {
	return map[string]store.GatewayPrice{
		"input_token":        {ID: "input", Metric: "input_token", UnitSize: 1000, EffectivePrice: 1800},
		"cached_input_token": {ID: "cached", Metric: "cached_input_token", UnitSize: 1000, EffectivePrice: 900},
		"output_token":       {ID: "output", Metric: "output_token", UnitSize: 1000, EffectivePrice: 3600},
	}
}

func TestPricingAndMessageValidation(t *testing.T) {
	prices := testPrices()
	reserved, err := reservationUpperBound(prices, 4096, 4096)
	if err != nil || reserved != 22_120 {
		t.Fatalf("reservationUpperBound = %d, %v", reserved, err)
	}
	metrics, err := pricedMetrics(prices, 12, 7)
	if err != nil || len(metrics) != 2 || metrics[0].Charge+metrics[1].Charge != 48 {
		t.Fatalf("pricedMetrics = %+v, %v", metrics, err)
	}
	if messages, err := providerMessages([]ChatMessage{{Role: "system", Content: "s"}, {Role: "user", Content: "u"}, {Role: "assistant", Content: "a"}}); err != nil || len(messages) != 3 {
		t.Fatalf("providerMessages = %+v, %v", messages, err)
	}

	for _, mutate := range []func(map[string]store.GatewayPrice){
		func(p map[string]store.GatewayPrice) { delete(p, "input_token") },
		func(p map[string]store.GatewayPrice) {
			p["input_token"] = store.GatewayPrice{Metric: "input_token", UnitSize: 0, EffectivePrice: 1}
		},
		func(p map[string]store.GatewayPrice) {
			p["input_token"] = store.GatewayPrice{Metric: "input_token", UnitSize: 1, EffectivePrice: math.MaxInt64}
		},
	} {
		candidate := testPrices()
		mutate(candidate)
		if _, err := reservationUpperBound(candidate, 4096, 4096); err == nil {
			t.Fatal("invalid reservation prices succeeded")
		}
	}
	missing := testPrices()
	delete(missing, "output_token")
	if _, err := pricedMetrics(missing, 1, 1); err == nil {
		t.Fatal("pricedMetrics without output price succeeded")
	}
	if _, err := providerMessages([]ChatMessage{{Role: "tool", Content: "x"}}); err == nil {
		t.Fatal("unsupported role succeeded")
	}
}

func TestReservationUpperBoundAllowsCompleteZeroPriceSet(t *testing.T) {
	prices := testPrices()
	for metric, price := range prices {
		price.EffectivePrice = 0
		price.DiscountBPS = 10_000
		prices[metric] = price
	}
	if reserved, err := reservationUpperBound(prices, 4096, 4096); err != nil || reserved != 0 {
		t.Fatalf("zero-price reservation = %d, %v", reserved, err)
	}
}

func TestGatewayExecutionSnapshotValidation(t *testing.T) {
	valid, err := json.Marshal([]store.GatewayCandidate{{
		ModelID:          "model-id",
		VariantID:        "variant-id",
		ProviderEndpoint: "https://provider.example/v1",
		ProviderModel:    "provider-model",
		Prices:           testPrices(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeGatewayExecutionSnapshot(valid)
	if err != nil || len(decoded) != 1 || decoded[0].VariantID != "variant-id" {
		t.Fatalf("decoded snapshot=%+v err=%v", decoded, err)
	}

	// A resumed command must never fall back to live catalog state when its
	// immutable provider/price plan is absent, malformed, empty, or incomplete.
	// Each case is rejected before any external provider can be called.
	for name, snapshot := range map[string][]byte{
		"missing":    nil,
		"malformed":  []byte(`{"broken"`),
		"empty":      []byte(`[]`),
		"incomplete": []byte(`[{"model_id":"model-id"}]`),
	} {
		t.Run(name, func(t *testing.T) {
			if candidates, err := decodeGatewayExecutionSnapshot(snapshot); err == nil || candidates != nil {
				t.Fatalf("snapshot unexpectedly accepted: %+v", candidates)
			}
		})
	}
}

func TestServiceRejectsBeforeExternalWork(t *testing.T) {
	service := New(nil, nil)
	if _, err := service.CreateRealtimeSession(context.Background(), store.GatewayPrincipal{}, "key", RealtimeRequest{}); err == nil {
		t.Fatal("invalid Realtime request succeeded")
	}
	if _, err := service.Chat(context.Background(), store.GatewayPrincipal{}, "key", ChatRequest{}); err == nil {
		t.Fatal("Chat without executor succeeded")
	}
	if err := service.StreamChat(context.Background(), store.GatewayPrincipal{}, "key", ChatRequest{}, func([]byte) error { return nil }); err == nil {
		t.Fatal("StreamChat without executor succeeded")
	}
	if _, _, err := service.CompleteRealtimeProviderEvent(context.Background(), nil, ""); err == nil {
		t.Fatal("unsigned Realtime event succeeded")
	}
}

func TestResolvedCandidateRejectsUnauthorizedWinners(t *testing.T) {
	candidates := []store.GatewayCandidate{
		{VariantID: "variant-a", ProviderModel: "wire-a", ProviderEndpoint: "https://a.test", ProviderCredential: "secret-a"},
		{VariantID: "variant-b", ProviderModel: "wire-b", ProviderEndpoint: "https://b.test", ProviderCredential: "secret-b"},
	}
	targets := candidateTargets(candidates)
	if len(targets) != 2 || targets[0].RouteKey != "variant-a" || targets[1].Model != "wire-b" {
		t.Fatalf("candidate targets=%+v", targets)
	}
	resolved, err := resolvedCandidate(candidates, schemas.ModelProvider("gizway-variant-a"), "wire-a")
	if err != nil || resolved.VariantID != "variant-a" {
		t.Fatalf("private winner=%+v err=%v", resolved, err)
	}
	if _, err := resolvedCandidate(candidates, schemas.ModelProvider("gizway-variant-a"), "attacker-model"); err == nil {
		t.Fatal("private provider accepted unauthorized model")
	}
	if _, err := resolvedCandidate(candidates, "", ""); err == nil {
		t.Fatal("multiple candidates accepted missing winner metadata")
	}
	legacy, err := resolvedCandidate(candidates[:1], "", "")
	if err != nil || legacy.VariantID != "variant-a" {
		t.Fatalf("legacy single winner=%+v err=%v", legacy, err)
	}
	byModel, err := resolvedCandidate(candidates, schemas.ModelProvider("openai"), "wire-b")
	if err != nil || byModel.VariantID != "variant-b" {
		t.Fatalf("wire winner=%+v err=%v", byModel, err)
	}
	ambiguous := append(candidates, store.GatewayCandidate{VariantID: "variant-c", ProviderModel: "wire-b"})
	if _, err := resolvedCandidate(ambiguous, schemas.ModelProvider("openai"), "wire-b"); err == nil {
		t.Fatal("ambiguous wire model accepted")
	}
	if _, err := resolvedCandidate(candidates, schemas.ModelProvider("openai"), "missing"); err == nil {
		t.Fatal("unknown winner accepted")
	}
}

func TestResolvedResponseModelUsesCurrentRoutingMetadata(t *testing.T) {
	extra := schemas.BifrostResponseExtraFields{RoutingInfo: schemas.RoutingInfo{
		Model:            "route-model",
		ResolvedKeyAlias: &schemas.ResolvedKeyAlias{ModelID: "alias-model"},
	}}
	if got := resolvedResponseModel(extra, "response-model"); got != "alias-model" {
		t.Fatalf("resolved alias model=%q", got)
	}
	extra.RoutingInfo.ResolvedKeyAlias = nil
	if got := resolvedResponseModel(extra, "response-model"); got != "route-model" {
		t.Fatalf("routing model=%q", got)
	}
	extra.RoutingInfo.Model = ""
	if got := resolvedResponseModel(extra, "response-model"); got != "response-model" {
		t.Fatalf("response fallback model=%q", got)
	}
}

func TestRealtimePricingRejectsAmbiguousOrIncompleteUsage(t *testing.T) {
	prices := testPrices()
	prices["input_audio_token"] = store.GatewayPrice{Metric: "input_audio_token", UnitSize: 1000, EffectivePrice: 2700}
	prices["output_audio_token"] = store.GatewayPrice{Metric: "output_audio_token", UnitSize: 1000, EffectivePrice: 5400}
	metrics, err := pricedRealtimeMetrics(prices, 12, 7, 2, 4, 2)
	if err != nil || len(metrics) != 5 {
		t.Fatalf("Realtime metrics=%+v err=%v", metrics, err)
	}
	if _, err := pricedRealtimeMetrics(prices, 5, 2, 3, 3, 0); err == nil {
		t.Fatal("overlapping cached/audio subsets accepted")
	}
	missing := testPrices()
	if _, err := realtimeReservationUpperBound(missing, 10, 10); err == nil {
		t.Fatal("Realtime reservation without audio prices succeeded")
	}
	delete(prices, "output_audio_token")
	if _, err := pricedRealtimeMetrics(prices, 1, 1, 0, 0, 0); err == nil {
		t.Fatal("Realtime settlement without audio price succeeded")
	}
}
