package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bifrostadapter "github.com/idy/gizway/internal/adapter/bifrost"
	gizpayclient "github.com/idy/gizway/internal/client/gizpay"
	gatewayservice "github.com/idy/gizway/internal/service/gateway"
	"github.com/idy/gizway/internal/service/gatewayquota"
	"github.com/idy/gizway/internal/service/localadmission"
	"github.com/idy/gizway/internal/service/quotaexchange"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
	"github.com/idy/gizway/internal/testfake/aiprovider"
)

type regionalAllowedQuota struct{}

func (regionalAllowedQuota) Exchange(context.Context, string, []quotaexchange.UsageRecord) (gizpayclient.ExchangeResponse, error) {
	return gizpayclient.ExchangeResponse{
		Status: "allowed", Quota: gizpayclient.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 1_000_000}, RecheckAfterSeconds: 300,
	}, nil
}

// This test deliberately enters through the regional HTTP surface. It proves
// that the administrator stored in one Gateway database is the principal that
// creates that Gateway's provider, model and price data; no GizPay admin or
// cross-region control-plane table participates in the write path.
func TestRegionalAdministratorOwnsLocalCatalogAndRatePublication(t *testing.T) {
	database := testdb.OpenGizWay(t)
	repository, err := store.NewWithSecretKey(database.SQL, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) }
	repository.ConfigureClock(now)
	if _, _, err := repository.BootstrapRegionalAdministrator(t.Context(), "cn-admin@gizway.invalid", "CN Operator", "regional-password", "2026-08-12T00:00:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(aiprovider.Handler())
	defer provider.Close()
	executor := bifrostadapter.NewLazy()
	defer executor.Shutdown()
	quota := gatewayquota.New(regionalAllowedQuota{}, localadmission.New(now), repository, now)
	gateway := gatewayservice.NewWithRealtimeProviderCallback(repository, executor, "https://gateway.invalid", "regional-callback-secret")
	gateway.ConfigureClock(now)
	gateway.ConfigureRegionalQuota(quota)
	server := NewWithServicesAndClockSurface(repository, gateway, nil, nil, now, nil, SurfaceGizWay)
	server.ConfigureRegionalRatePublication("cn", func(ctx context.Context, id string, _ int64, _ string, _ []store.PublishedPrice) (string, string, error) {
		var digest string
		if err := database.SQL.GetContext(ctx, &digest, `SELECT encode(content_hash,'hex') FROM rate_publications WHERE id=$1`, id); err != nil {
			return "", "", err
		}
		return "gizpay-" + id, digest, nil
	})
	handler := server.Handler()

	login := regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/auth/login", "", "login-cn", map[string]any{
		"email": "cn-admin@gizway.invalid", "password": "regional-password",
	}, http.StatusOK)
	token, _ := login["access_token"].(string)
	if token == "" {
		t.Fatalf("login response: %+v", login)
	}
	administrator, _ := login["administrator"].(map[string]any)
	administratorID := requiredString(t, administrator, "id")
	automationKey := regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/administrators/"+administratorID+"/api_keys", token, "regional-automation-key", map[string]any{
		"name": "CN Catalog automation",
	}, http.StatusCreated)
	operatorKey := requiredString(t, automationKey, "secret")

	providerResponse := regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/providers", operatorKey, "provider-cn", map[string]any{
		"slug": "cn-provider", "name": "CN Provider",
	}, http.StatusCreated)
	providerID := requiredString(t, providerResponse, "id")
	endpoint := regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/providers/"+providerID+"/endpoints", token, "endpoint-cn", map[string]any{
		"name": "CN Endpoint", "base_url": provider.URL, "credential": "story-provider-key",
		"region": "cn", "priority": 1, "weight": 100,
	}, http.StatusCreated)
	endpointID := requiredString(t, endpoint, "id")
	if _, leaked := endpoint["credential"]; leaked {
		t.Fatalf("endpoint response leaked credential: %+v", endpoint)
	}

	model := regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/models", token, "model-cn", map[string]any{
		"slug": "cn-model", "name": "CN Model", "modality": []string{"text"}, "metadata": map[string]any{"region": "cn"},
	}, http.StatusCreated)
	modelID := requiredString(t, model, "id")
	variant := regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/models/"+modelID+"/variants", token, "variant-cn", map[string]any{
		"provider_endpoint_id": endpointID, "provider_model_name": "upstream-cn", "variant_slug": "primary",
		"capabilities": map[string]any{
			"responses": true, "chat": true, "messages": true, "generateContent": true,
			"embeddings": true, "audio_speech": true, "audio_transcriptions": true,
			"image_generation": true, "realtime": true, "realtime_webrtc_callback": true,
		}, "context_window": 8192, "max_output_tokens": 2048,
	}, http.StatusCreated)
	variantID := requiredString(t, variant, "id")
	for index, metric := range []string{"input_token", "cached_input_token", "output_token", "input_audio_token", "output_audio_token", "audio_second", "image", "request"} {
		regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/model_variants/"+variantID+"/prices", token, fmt.Sprintf("price-cn-%d", index), map[string]any{
			"metric": metric, "unit_size": 1000, "upstream_cost_microcredits": 1,
			"base_customer_price_microcredits": 10, "customer_price_microcredits": 10,
			"discount_bps": 0, "valid_from": "2026-08-11T00:00:00Z", "valid_until": nil,
		}, http.StatusCreated)
	}
	regionalJSONRequest(t, handler, http.MethodPatch, "/admin/v1/providers/"+providerID, token, "provider-cn-update", map[string]any{
		"name": "CN Provider Updated", "status": "active",
	}, http.StatusOK)
	regionalJSONRequest(t, handler, http.MethodPatch, "/admin/v1/provider_endpoints/"+endpointID, token, "endpoint-cn-update", map[string]any{
		"name": "CN Endpoint Updated", "base_url": provider.URL, "region": "cn", "priority": 0, "weight": 100, "status": "active",
	}, http.StatusOK)
	regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/provider_endpoints/"+endpointID+"/rotate_credential", token, "endpoint-cn-rotate", map[string]any{
		"credential": "story-provider-key",
	}, http.StatusNoContent)
	regionalJSONRequest(t, handler, http.MethodPatch, "/admin/v1/models/"+modelID, token, "model-cn-update", map[string]any{
		"name": "CN Model Updated", "status": "active",
	}, http.StatusOK)
	regionalJSONRequest(t, handler, http.MethodPatch, "/admin/v1/model_variants/"+variantID, token, "variant-cn-update", map[string]any{
		"provider_model_name": "upstream-cn", "capabilities": map[string]any{
			"responses": true, "chat": true, "messages": true, "generateContent": true,
			"embeddings": true, "audio_speech": true, "audio_transcriptions": true,
			"image_generation": true, "realtime": true, "realtime_webrtc_callback": true,
		}, "context_window": 8192, "max_output_tokens": 2048, "status": "active",
	}, http.StatusOK)
	for _, path := range []string{
		"/admin/v1/models", "/admin/v1/models/" + modelID + "/variants",
		"/admin/v1/model_variants/" + variantID + "/prices",
		"/admin/v1/providers/" + providerID + "/endpoints",
	} {
		regionalJSONRequest(t, handler, http.MethodGet, path, token, "", nil, http.StatusOK)
	}
	publication := regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/rate_publications", token, "publication-cn", map[string]any{
		"source_publication_id": "cn-publication", "effective_at": "2026-08-11T00:00:00Z",
	}, http.StatusCreated)
	if publication["status"] != "active" {
		t.Fatalf("publication response: %+v", publication)
	}

	// The same local Catalog is immediately the execution source of truth. The
	// raw customer key is not looked up in this regional database; it is used
	// only by the local quota runtime when a report-and-query is required.
	regionalJSONRequest(t, handler, http.MethodPost, "/v1/responses", "giz-regional-customer-key", "response-cn", map[string]any{
		"model": "cn-model", "input": "regional response",
	}, http.StatusOK)
	regionalJSONRequest(t, handler, http.MethodPost, "/v1/chat/completions", "giz-regional-customer-key", "chat-cn-1", map[string]any{
		"model": "cn-model", "messages": []map[string]any{{"role": "user", "content": "regional chat"}},
	}, http.StatusOK)
	regionalJSONRequest(t, handler, http.MethodGet, "/v1/models", "giz-regional-customer-key", "", nil, http.StatusOK)
	regionalJSONRequest(t, handler, http.MethodGet, "/v1beta/models", "giz-regional-customer-key", "", nil, http.StatusOK)
	// Every retained compatibility codec executes through the same regional
	// Catalog and Quota runtime. These are native Go HTTP tests; Hurl remains a
	// separate black-box contract and is not invoked to manufacture coverage.
	for _, protocol := range []struct {
		name, path, header, body string
	}{
		{"chat stream", "/v1/chat/completions", "Authorization", `{"model":"cn-model","messages":[{"role":"user","content":"stream"}],"stream":true,"max_tokens":64}`},
		{"responses stream", "/v1/responses", "Authorization", `{"model":"cn-model","input":"stream","stream":true,"max_output_tokens":64}`},
		{"embedding", "/v1/embeddings", "Authorization", `{"model":"cn-model","input":"embed"}`},
		{"speech", "/v1/audio/speech", "Authorization", `{"model":"cn-model","input":"speak","voice":"alloy","response_format":"mp3"}`},
		{"image", "/v1/images/generations", "Authorization", `{"model":"cn-model","prompt":"square","n":1,"response_format":"b64_json"}`},
		{"anthropic", "/v1/messages", "x-api-key", `{"model":"cn-model","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`},
		{"anthropic stream", "/v1/messages", "x-api-key", `{"model":"cn-model","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"stream"}]}`},
		{"gemini", "/v1beta/models/cn-model:generateContent", "x-goog-api-key", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":64}}`},
		{"gemini stream", "/v1beta/models/cn-model:streamGenerateContent?alt=sse", "x-goog-api-key", `{"contents":[{"role":"user","parts":[{"text":"stream"}]}],"generationConfig":{"maxOutputTokens":64}}`},
		{"gemini embedding", "/v1beta/models/cn-model:embedContent", "x-goog-api-key", `{"content":{"parts":[{"text":"embed"}]},"outputDimensionality":3}`},
	} {
		t.Run(protocol.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, protocol.path, strings.NewReader(protocol.body))
			credential := "giz-regional-customer-key"
			if protocol.header == "Authorization" {
				credential = "Bearer " + credential
			}
			request.Header.Set(protocol.header, credential)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	var transcription bytes.Buffer
	form := multipart.NewWriter(&transcription)
	_ = form.WriteField("model", "cn-model")
	_ = form.WriteField("response_format", "verbose_json")
	part, err := form.CreateFormFile("file", "audio.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("deterministic audio"))
	_ = form.Close()
	transcriptionRequest := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &transcription)
	transcriptionRequest.Header.Set("Authorization", "Bearer giz-regional-customer-key")
	transcriptionRequest.Header.Set("Content-Type", form.FormDataContentType())
	transcriptionResponse := httptest.NewRecorder()
	handler.ServeHTTP(transcriptionResponse, transcriptionRequest)
	if transcriptionResponse.Code != http.StatusOK {
		t.Fatalf("transcription status=%d body=%s", transcriptionResponse.Code, transcriptionResponse.Body.String())
	}
	realtime := regionalJSONRequest(t, handler, http.MethodPost, "/v1/realtime/client_secrets", "giz-regional-customer-key", "realtime-cn", map[string]any{
		"model": "cn-model", "transport": "websocket",
	}, http.StatusCreated)
	if clientSecret, _ := realtime["client_secret"].(map[string]any); requiredString(t, clientSecret, "value") == "" {
		t.Fatalf("Realtime response: %+v", realtime)
	}
	webrtc := regionalJSONRequest(t, handler, http.MethodPost, "/v1/realtime/client_secrets", "giz-regional-customer-key", "webrtc-cn", map[string]any{
		"model": "cn-model", "transport": "webrtc",
	}, http.StatusCreated)
	webrtcSecret := requiredString(t, webrtc["client_secret"].(map[string]any), "value")
	mismatchRequest := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls?session_id=wrong-session", bytes.NewBufferString("v=0"))
	mismatchRequest.Header.Set("Authorization", "Bearer "+webrtcSecret)
	mismatchResponse := httptest.NewRecorder()
	handler.ServeHTTP(mismatchResponse, mismatchRequest)
	if mismatchResponse.Code != http.StatusUnauthorized {
		t.Fatalf("Realtime mismatch status=%d body=%s", mismatchResponse.Code, mismatchResponse.Body.String())
	}
	webrtcSuccess := regionalJSONRequest(t, handler, http.MethodPost, "/v1/realtime/client_secrets", "giz-regional-customer-key", "webrtc-cn-success", map[string]any{
		"model": "cn-model", "transport": "webrtc",
	}, http.StatusCreated)
	successSecret := requiredString(t, webrtcSuccess["client_secret"].(map[string]any), "value")
	successSessionID := requiredString(t, webrtcSuccess["session"].(map[string]any), "session_id")
	successRequest := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls?session_id="+successSessionID, bytes.NewBufferString("v=0\r\ns=regional-offer\r\n"))
	successRequest.Header.Set("Authorization", "Bearer "+successSecret)
	successResponse := httptest.NewRecorder()
	handler.ServeHTTP(successResponse, successRequest)
	if successResponse.Code != http.StatusCreated {
		t.Fatalf("Realtime signaling status=%d body=%s", successResponse.Code, successResponse.Body.String())
	}
	for _, test := range []struct {
		name, token, body string
		status            int
	}{
		{name: "missing secret", body: "v=0", status: http.StatusUnauthorized},
		{name: "empty offer", token: "unused-realtime-secret", status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls?session_id=unused", bytes.NewBufferString(test.body))
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	providers := regionalJSONRequest(t, handler, http.MethodGet, "/admin/v1/providers", token, "", nil, http.StatusOK)
	if data, _ := providers["data"].([]any); len(data) != 1 {
		t.Fatalf("provider page: %+v", providers)
	}
	ratePublications := regionalJSONRequest(t, handler, http.MethodGet, "/admin/v1/rate_publications", token, "", nil, http.StatusOK)
	if data, _ := ratePublications["data"].([]any); len(data) != 1 {
		t.Fatalf("publication page: %+v", ratePublications)
	}
	regionalJSONRequest(t, handler, http.MethodGet, "/admin/v1/usage_outbox?status=pending", token, "", nil, http.StatusOK)
	executions := regionalJSONRequest(t, handler, http.MethodGet, "/admin/v1/gateway_executions?status=completed&limit=1", token, "", nil, http.StatusOK)
	executionID := firstPageID(t, executions)
	regionalJSONRequest(t, handler, http.MethodGet, "/admin/v1/gateway_executions/"+executionID, token, "", nil, http.StatusOK)
	regionalJSONRequest(t, handler, http.MethodGet, "/admin/v1/gateway_executions/missing", token, "", nil, http.StatusNotFound)
	regionalJSONRequest(t, handler, http.MethodPatch, "/admin/v1/providers/missing", token, "update-missing-provider", map[string]any{
		"name": "Missing Provider", "status": "active",
	}, http.StatusNotFound)
	regionalJSONRequest(t, handler, http.MethodPatch, "/admin/v1/provider_endpoints/missing", token, "update-missing-endpoint", map[string]any{
		"name": "Missing Endpoint", "base_url": provider.URL, "region": "cn", "priority": 1, "weight": 1, "status": "active",
	}, http.StatusNotFound)
	regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/provider_endpoints/missing/rotate_credential", token, "rotate-missing-endpoint", map[string]any{
		"credential": "replacement-secret",
	}, http.StatusNotFound)
	regionalJSONRequest(t, handler, http.MethodPatch, "/admin/v1/models/missing", token, "update-missing-model", map[string]any{
		"name": "Missing Model", "status": "active",
	}, http.StatusNotFound)
	regionalJSONRequest(t, handler, http.MethodPatch, "/admin/v1/model_variants/missing", token, "update-missing-variant", map[string]any{
		"provider_model_name": "missing", "capabilities": map[string]any{"responses": true}, "status": "active",
	}, http.StatusNotFound)
	for index, test := range []struct {
		name       string
		publishErr error
		digest     string
		status     int
	}{
		{name: "certificate rejected", publishErr: gizpayclient.ErrInvalidNodeIdentity, status: http.StatusUnauthorized},
		{name: "GizPay unavailable", publishErr: gizpayclient.ErrTemporarilyUnavailable, status: http.StatusServiceUnavailable},
		{name: "GizPay rejected", publishErr: errors.New("rejected"), status: http.StatusBadGateway},
		{name: "snapshot mismatch", digest: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", status: http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			failureServer := NewWithServicesAndClockSurface(repository, gateway, nil, nil, now, nil, SurfaceGizWay)
			failureServer.ConfigureRegionalRatePublication("cn", func(context.Context, string, int64, string, []store.PublishedPrice) (string, string, error) {
				return "gizpay-failure", test.digest, test.publishErr
			})
			regionalJSONRequest(t, failureServer.Handler(), http.MethodPost, "/admin/v1/rate_publications", token, fmt.Sprintf("publication-failure-%d", index), map[string]any{
				"source_publication_id": fmt.Sprintf("cn-publication-failure-%d", index), "effective_at": "2026-08-11T00:00:00Z",
			}, test.status)
		})
	}
}

func regionalJSONRequest(t *testing.T, handler http.Handler, method, path, token, idempotencyKey string, body any, wantStatus int) map[string]any {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf("%s %s status=%d body=%s", method, path, response.Code, response.Body.String())
	}
	if response.Body.Len() == 0 {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode %s %s response: %v: %s", method, path, err, response.Body.String())
	}
	return result
}

func requiredString(t *testing.T, object map[string]any, field string) string {
	t.Helper()
	value, _ := object[field].(string)
	if value == "" {
		t.Fatalf("missing %s in %+v", field, object)
	}
	return value
}
