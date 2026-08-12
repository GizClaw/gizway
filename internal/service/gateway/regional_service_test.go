package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	gizpayclient "github.com/idy/gizway/internal/client/gizpay"
	"github.com/idy/gizway/internal/service/gatewayquota"
	"github.com/idy/gizway/internal/service/localadmission"
	"github.com/idy/gizway/internal/service/quotaexchange"
	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

type regionalExchangeCall struct {
	usage []quotaexchange.UsageRecord
}

type regionalTestClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *regionalTestClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *regionalTestClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

// regionalProtocolExecutor is intentionally local to the Refactor 01 tests:
// it exercises provider execution without requiring any central identity or
// monolith fixture in the regional database.
type regionalProtocolExecutor struct {
	responses      *schemas.BifrostResponsesResponse
	realtimeAnswer string
}

func (e *regionalProtocolExecutor) ResponsesCandidates(_ context.Context, targets []store.ProviderExecutionTarget, _ *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesResponse, error) {
	if e.responses != nil && len(targets) > 0 {
		e.responses.ExtraFields.RoutingInfo = schemas.RoutingInfo{Provider: schemas.ModelProvider("gizway-" + targets[0].RouteKey), Model: targets[0].Model}
	}
	return e.responses, nil
}

func (*regionalProtocolExecutor) ResponsesStreamCandidates(context.Context, []store.ProviderExecutionTarget, *schemas.BifrostResponsesRequest) (<-chan *schemas.BifrostStreamChunk, context.CancelFunc, error) {
	return nil, nil, errors.New("unused")
}

func (*regionalProtocolExecutor) ChatCompletionCandidates(context.Context, []store.ProviderExecutionTarget, []schemas.ChatMessage, *schemas.ChatParameters) (*schemas.BifrostChatResponse, error) {
	return nil, errors.New("unused")
}

func (*regionalProtocolExecutor) ChatCompletionStreamCandidates(context.Context, []store.ProviderExecutionTarget, []schemas.ChatMessage, *schemas.ChatParameters) (<-chan *schemas.BifrostStreamChunk, context.CancelFunc, error) {
	return nil, nil, errors.New("unused")
}

func (*regionalProtocolExecutor) EmbeddingCandidates(context.Context, []store.ProviderExecutionTarget, *schemas.BifrostEmbeddingRequest) (*schemas.BifrostEmbeddingResponse, error) {
	return nil, errors.New("unused")
}

func (*regionalProtocolExecutor) SpeechCandidates(context.Context, []store.ProviderExecutionTarget, *schemas.BifrostSpeechRequest) (*schemas.BifrostSpeechResponse, error) {
	return nil, errors.New("unused")
}

func (*regionalProtocolExecutor) TranscriptionCandidates(context.Context, []store.ProviderExecutionTarget, *schemas.BifrostTranscriptionRequest) (*schemas.BifrostTranscriptionResponse, error) {
	return nil, errors.New("unused")
}

func (*regionalProtocolExecutor) ImageGenerationCandidates(context.Context, []store.ProviderExecutionTarget, *schemas.BifrostImageGenerationRequest) (*schemas.BifrostImageGenerationResponse, error) {
	return nil, errors.New("unused")
}

func (*regionalProtocolExecutor) RealtimeWebSocketRoute(context.Context, store.ProviderExecutionTarget) (string, http.Header, error) {
	return "", nil, errors.New("unused")
}

func (*regionalProtocolExecutor) RealtimeClientEvent(context.Context, store.ProviderExecutionTarget, []byte) ([]byte, error) {
	return nil, errors.New("unused")
}

func (*regionalProtocolExecutor) RealtimeProviderEvent(context.Context, store.ProviderExecutionTarget, []byte) ([]byte, *schemas.BifrostLLMUsage, bool, error) {
	return nil, nil, false, errors.New("unused")
}

func (e *regionalProtocolExecutor) ExchangeRealtimeWebRTCSDP(context.Context, store.ProviderExecutionTarget, string, json.RawMessage) (string, error) {
	return e.realtimeAnswer, nil
}

func regionalProtocolUsage() *schemas.ResponsesResponseUsage {
	return &schemas.ResponsesResponseUsage{
		InputTokens: 11, OutputTokens: 6, TotalTokens: 17,
		InputTokensDetails: &schemas.ResponsesResponseInputTokens{CachedReadTokens: 2},
	}
}

type regionalExchanger struct {
	mu    sync.Mutex
	calls []regionalExchangeCall
}

func (e *regionalExchanger) Exchange(_ context.Context, _ string, usage []quotaexchange.UsageRecord) (gizpayclient.ExchangeResponse, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, regionalExchangeCall{usage: append([]quotaexchange.UsageRecord(nil), usage...)})
	return gizpayclient.ExchangeResponse{
		Status: "allowed", Quota: gizpayclient.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 1_000_000},
		RecheckAfterSeconds: 300,
	}, nil
}

func (e *regionalExchanger) snapshot() []regionalExchangeCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]regionalExchangeCall, len(e.calls))
	copy(result, e.calls)
	return result
}

func TestPostgreSQLRegionalServiceUsesLocalQuotaThenReportsAtDeadline(t *testing.T) {
	database, err := storage.OpenGizWayPostgreSQL(testdb.NewSchema(t), true)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	clock := &regionalTestClock{now: time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)}
	timestamp := func() string { return clock.Now().Format("2006-01-02T15:04:05.000000000Z") }
	for _, statement := range []string{
		`INSERT INTO providers(id,slug,name,status,created_at,updated_at) VALUES ('provider','provider','Provider','active',$1,$1)`,
		`INSERT INTO provider_endpoints(id,provider_id,name,base_url,credential_ref,priority,weight,status,created_at,updated_at) VALUES ('endpoint','provider','Endpoint','https://provider.test','secret',1,100,'active',$1,$1)`,
		`INSERT INTO models(id,slug,name,modality,status,metadata,created_at,updated_at) VALUES ('model','regional-model','Regional','["text"]','active','{}',$1,$1)`,
		`INSERT INTO model_variants(id,model_id,provider_endpoint_id,provider_model_name,variant_slug,capabilities,context_window,max_output_tokens,status,created_at,updated_at) VALUES ('variant','model','endpoint','private-model','primary','{"responses":true}',8192,2048,'active',$1,$1)`,
		`INSERT INTO model_variant_prices(id,model_variant_id,metric,unit_size,upstream_cost_microcredits,base_customer_price_microcredits,customer_price_microcredits,discount_bps,valid_from,created_at) VALUES
		 ('rate-input','variant','input_token',1000,1,1,1,0,$1,$1),
		 ('rate-cached','variant','cached_input_token',1000,1,1,1,0,$1,$1),
		 ('rate-output','variant','output_token',1000,1,1,1,0,$1,$1),
		 ('rate-input-audio','variant','input_audio_token',1000,1,1,1,0,$1,$1),
		 ('rate-output-audio','variant','output_audio_token',1000,1,1,1,0,$1,$1)`,
	} {
		if _, err := database.SQL.Exec(statement, timestamp()); err != nil {
			t.Fatalf("regional fixture: %v", err)
		}
	}
	repository := store.New(database.SQL)
	repository.ConfigureClock(clock.Now)
	publication, err := repository.PrepareRegionalRatePublication(t.Context(), "global", "regional-publication", timestamp())
	if err != nil {
		t.Fatalf("prepare immutable regional prices: %v", err)
	}
	publication, err = repository.ActivateRegionalRatePublication(t.Context(), publication.ID, "center-publication")
	if err != nil {
		t.Fatalf("activate GizPay-confirmed prices: %v", err)
	}
	exchanger := &regionalExchanger{}
	runtime := gatewayquota.New(exchanger, localadmission.New(clock.Now), repository, clock.Now)
	workerContext, stopWorker := context.WithCancel(t.Context())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		runtime.Run(workerContext, time.Millisecond)
	}()
	defer func() {
		stopWorker()
		<-workerDone
	}()
	providerID := "provider-response"
	executor := &regionalProtocolExecutor{responses: &schemas.BifrostResponsesResponse{ID: &providerID, Model: "private-model", Usage: regionalProtocolUsage()}}
	service := NewWithRealtimeProviderCallback(repository, executor, "", "")
	service.ConfigureClock(clock.Now)
	service.ConfigureRegionalQuota(runtime)
	principal := CustomerCredential{RawAPIKey: "giz_runtime_only_secret"}
	render := func(_ context.Context, response *schemas.BifrostResponsesResponse, _ string) ([]byte, error) {
		return json.Marshal(response)
	}
	for index := range 2 {
		if _, err := service.ExecuteResponses(t.Context(), principal, "responses", "regional-model", &schemas.BifrostResponsesRequest{}, render); err != nil {
			t.Fatalf("local request %d: %v", index, err)
		}
	}
	// The production worker is running, but normal successful Usage stays in
	// the outbox until the next request needs a quota refresh. The worker exists
	// only to retry an Exchange that was already attempted and failed.
	time.Sleep(20 * time.Millisecond)
	calls := exchanger.snapshot()
	if len(calls) != 1 || len(calls[0].usage) != 0 {
		t.Fatalf("before deadline Exchange calls = %#v", calls)
	}
	clock.Advance(5 * time.Minute)
	if _, err := service.ExecuteResponses(t.Context(), principal, "responses", "regional-model", &schemas.BifrostResponsesRequest{}, render); err != nil {
		t.Fatal(err)
	}
	calls = exchanger.snapshot()
	if len(calls) != 2 || len(calls[1].usage) != 2 {
		t.Fatalf("deadline Exchange calls=%d reported=%d", len(calls), len(calls[1].usage))
	}
	for _, usage := range calls[1].usage {
		if usage.RatePublicationID != "center-publication" {
			t.Fatalf("reported Usage publication=%q, want GizPay receipt", usage.RatePublicationID)
		}
	}
	var reported, pending int
	if err := database.SQL.Get(&reported, `SELECT COUNT(*) FROM gateway_usage_outbox WHERE status='reported'`); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.Get(&pending, `SELECT COUNT(*) FROM gateway_usage_outbox WHERE status='pending'`); err != nil {
		t.Fatal(err)
	}
	if reported != 2 || pending != 1 {
		t.Fatalf("outbox reported/pending = %d/%d", reported, pending)
	}
	var executionPublications []string
	if err := database.SQL.Select(&executionPublications, `SELECT DISTINCT rate_publication_id FROM gateway_executions`); err != nil {
		t.Fatal(err)
	}
	if len(executionPublications) != 1 || executionPublications[0] != publication.ID {
		t.Fatalf("executions used publications %#v, want only %s", executionPublications, publication.ID)
	}
}

func TestPostgreSQLRegionalRealtimeSettlesSignedUsageWithoutCentralIdentityTables(t *testing.T) {
	database := testdb.OpenGizWay(t)
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	const timestamp = "2026-08-12T00:00:00.000000000Z"
	for _, statement := range []string{
		`INSERT INTO providers(id,slug,name,status,created_at,updated_at) VALUES ('provider','provider','Provider','active',$1,$1)`,
		`INSERT INTO provider_endpoints(id,provider_id,name,base_url,credential_ref,priority,weight,status,created_at,updated_at) VALUES ('endpoint','provider','Endpoint','https://provider.test','secret',1,100,'active',$1,$1)`,
		`INSERT INTO models(id,slug,name,modality,status,metadata,created_at,updated_at) VALUES ('model','regional-realtime','Regional Realtime','["audio"]','active','{}',$1,$1)`,
		`INSERT INTO model_variants(id,model_id,provider_endpoint_id,provider_model_name,variant_slug,capabilities,context_window,max_output_tokens,status,created_at,updated_at) VALUES ('variant','model','endpoint','private-realtime','primary','{"realtime":true,"realtime_webrtc_callback":true}',8192,2048,'active',$1,$1)`,
		`INSERT INTO model_variant_prices(id,model_variant_id,metric,unit_size,upstream_cost_microcredits,base_customer_price_microcredits,customer_price_microcredits,discount_bps,valid_from,created_at) VALUES
		 ('rate-input','variant','input_token',1000,1,1,1,0,$1,$1),
		 ('rate-cached','variant','cached_input_token',1000,1,1,1,0,$1,$1),
		 ('rate-output','variant','output_token',1000,1,1,1,0,$1,$1),
		 ('rate-input-audio','variant','input_audio_token',1000,1,1,1,0,$1,$1),
		 ('rate-output-audio','variant','output_audio_token',1000,1,1,1,0,$1,$1)`,
	} {
		if _, err := database.SQL.Exec(statement, timestamp); err != nil {
			t.Fatal(err)
		}
	}
	repository := store.New(database.SQL)
	repository.ConfigureClock(func() time.Time { return now })
	publication, err := repository.PrepareRegionalRatePublication(t.Context(), "cn", "realtime-publication", timestamp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ActivateRegionalRatePublication(t.Context(), publication.ID, "gizpay-realtime-publication"); err != nil {
		t.Fatal(err)
	}
	exchanger := &regionalExchanger{}
	runtime := gatewayquota.New(exchanger, localadmission.New(func() time.Time { return now }), repository, func() time.Time { return now })
	const callbackSecret = "regional-callback-secret"
	service := NewWithRealtimeProviderCallback(repository, &regionalProtocolExecutor{realtimeAnswer: "provider-answer"}, "https://gateway.test", callbackSecret)
	service.ConfigureClock(func() time.Time { return now })
	service.ConfigureRegionalQuota(runtime)
	created, err := service.CreateRealtimeSession(t.Context(), CustomerCredential{RawAPIKey: "giz-regional-realtime"}, RealtimeRequest{
		Model: "regional-realtime", Transport: "webrtc",
	})
	if err != nil {
		t.Fatal(err)
	}
	connected, err := service.ConnectRealtimeSession(t.Context(), created.ClientSecret, "webrtc")
	if err != nil || connected.Status != "connected" {
		t.Fatalf("connected session=%+v err=%v", connected, err)
	}
	if answer, err := service.ExchangeRealtimeWebRTCSDP(t.Context(), connected, "customer-offer", nil); err != nil || answer != "provider-answer" {
		t.Fatalf("WebRTC answer=%q err=%v", answer, err)
	}
	event := RealtimeProviderEvent{
		EventID: "regional-realtime-event", Type: "realtime.session.completed", SessionID: connected.ID,
		InputTokens: 12, OutputTokens: 7, CachedInputTokens: 2, InputAudioTokens: 4, OutputAudioTokens: 2,
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(service.realtimeSessionCallbackToken(connected)))
	_, _ = mac.Write(raw)
	signature := "v1=" + hex.EncodeToString(mac.Sum(nil))
	completed, replayed, err := service.CompleteRealtimeProviderEvent(t.Context(), raw, signature)
	if err != nil || replayed || completed.Status != "succeeded" {
		t.Fatalf("completed session=%+v replayed=%t err=%v", completed, replayed, err)
	}
	if replay, replayed, err := service.CompleteRealtimeProviderEvent(t.Context(), raw, signature); err != nil || !replayed || replay.ID != completed.ID {
		t.Fatalf("callback replay=%+v replayed=%t err=%v", replay, replayed, err)
	}
	var executions, usage int
	if err := database.SQL.Get(&executions, `SELECT COUNT(*) FROM gateway_executions WHERE status='completed'`); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.Get(&usage, `SELECT COUNT(*) FROM gateway_usage_outbox WHERE status='pending'`); err != nil {
		t.Fatal(err)
	}
	if executions != 1 || usage != 1 {
		t.Fatalf("regional Realtime execution/outbox=%d/%d", executions, usage)
	}

	released, err := service.CreateRealtimeSession(t.Context(), CustomerCredential{RawAPIKey: "giz-regional-realtime"}, RealtimeRequest{
		Model: "regional-realtime", Transport: "websocket",
	})
	if err != nil {
		t.Fatal(err)
	}
	service.ReleaseExecution(t.Context(), released.Session.ID, "client_disconnect")
	var failed int
	if err := database.SQL.Get(&failed, `SELECT COUNT(*) FROM gateway_executions WHERE id=$1 AND status='failed'`, released.Session.ID); err != nil || failed != 1 {
		t.Fatalf("released execution failed=%d err=%v", failed, err)
	}
}
