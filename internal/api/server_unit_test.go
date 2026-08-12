package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	paymentadapter "github.com/idy/gizway/internal/adapter/payment"
	gatewayservice "github.com/idy/gizway/internal/service/gateway"
	merchantservice "github.com/idy/gizway/internal/service/merchant"
	paymentservice "github.com/idy/gizway/internal/service/payment"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

type failingRequestBody struct{}

func (failingRequestBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingRequestBody) Close() error             { return nil }

func testGizPayServer(repository *store.Store) *Server {
	return NewWithServicesAndClockSurface(repository, nil, nil, merchantservice.NewConfigured(repository, nil, false, "https://pay.gizway.test"), time.Now, nil, SurfaceGizPay)
}

func testGizWayServer(repository *store.Store, gateway *gatewayservice.Service) *Server {
	return NewWithServicesAndClockSurface(repository, gateway, nil, nil, time.Now, nil, SurfaceGizWay)
}

func TestCompatibleProtocolValidationAndErrorMapping(t *testing.T) {
	database := testdb.OpenGizWayStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	server := testGizWayServer(repository, gatewayservice.NewWithRealtimeProviderCallback(repository, nil, "", ""))
	handler := server.Handler()
	tests := []struct {
		name, path, body, contentType string
		want                          int
	}{
		{"responses model", "/v1/responses", `{"input":"x"}`, "application/json", 400},
		{"responses provider", "/v1/responses", `{"model":"story-text","input":"x"}`, "application/json", 502},
		{"embedding input", "/v1/embeddings", `{"model":"story-text"}`, "application/json", 400},
		{"speech streaming", "/v1/audio/speech", `{"model":"story-text","input":"x","voice":"alloy","stream_format":"sse"}`, "application/json", 400},
		{"transcription multipart", "/v1/audio/transcriptions", `broken`, "multipart/form-data", 400},
		{"image prompt", "/v1/images/generations", `{"model":"story-text"}`, "application/json", 400},
		{"anthropic max tokens", "/v1/messages", `{"model":"story-text","messages":[{"role":"user","content":"x"}]}`, "application/json", 400},
		{"gemini operation", "/v1beta/models/story-text:unknown", `{"contents":[{"parts":[{"text":"x"}]}]}`, "application/json", 501},
		{"gemini contents", "/v1beta/models/story-text:generateContent", `{}`, "application/json", 400},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer giz_story_user_active_1")
			request.Header.Set("Idempotency-Key", "validation-"+string(rune('a'+index)))
			request.Header.Set("Content-Type", test.contentType)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}

	missingKey := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"story-text","input":"x"}`))
	missingKey.Header.Set("Authorization", "Bearer giz_story_user_active_1")
	missingKey.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, missingKey)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("provider failure without obsolete idempotency header status=%d", recorder.Code)
	}

	for _, test := range []struct {
		err  error
		want int
	}{
		{store.ErrNotFound, http.StatusNotFound},
		{gatewayservice.ErrQuotaDenied, http.StatusPaymentRequired},
		{errors.New("provider"), http.StatusBadGateway},
		{gatewayservice.ErrInvalidRequest, http.StatusBadRequest},
	} {
		recorder := httptest.NewRecorder()
		server.writeGatewayExecutionError(recorder, test.err)
		if recorder.Code != test.want {
			t.Fatalf("error %v status=%d", test.err, recorder.Code)
		}
	}

	unavailable := testGizPayServer(repository).Handler()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"story-text","input":"x"}`))
	request.Header.Set("Authorization", "Bearer giz_story_user_active_1")
	request.Header.Set("Idempotency-Key", "unavailable")
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	unavailable.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unavailable status=%d", recorder.Code)
	}
}

func TestRetainedUnsupportedProtocolsAndRealtimeCallbackFailures(t *testing.T) {
	database := testdb.OpenGizWayStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	gateway := gatewayservice.NewWithRealtimeProviderCallback(repository, nil, "", "callback-secret")
	server := testGizWayServer(repository, gateway)
	handler := server.Handler()

	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/images/edits"},
		{http.MethodPost, "/v1/messages/batches"},
		{http.MethodGet, "/v1/messages/batches"},
		{http.MethodGet, "/v1/messages/batches/batch"},
		{http.MethodPost, "/v1/messages/batches/batch/cancel"},
		{http.MethodDelete, "/v1/messages/batches/batch"},
		{http.MethodGet, "/v1/messages/batches/batch/results"},
		{http.MethodPost, "/v1/files"},
		{http.MethodGet, "/v1/files"},
		{http.MethodGet, "/v1/files/file"},
		{http.MethodDelete, "/v1/files/file"},
		{http.MethodGet, "/v1/files/file/content"},
		{http.MethodPost, "/upload/v1beta/files"},
		{http.MethodGet, "/v1beta/files"},
		{http.MethodGet, "/v1beta/files/file"},
		{http.MethodDelete, "/v1beta/files/file"},
	} {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer giz_story_user_active_1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotImplemented {
			t.Fatalf("%s %s status=%d body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}

	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{name: "invalid signature", body: `{}`, want: http.StatusUnauthorized},
		{name: "oversized", body: strings.Repeat("x", (1<<20)+1), want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/callbacks/v1/realtime_events", strings.NewReader(test.body))
			request.Header.Set("X-Gizway-Signature", "v1=invalid")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	unavailable := testGizWayServer(repository, nil).Handler()
	response := httptest.NewRecorder()
	unavailable.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/callbacks/v1/realtime_events", strings.NewReader(`{}`)))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured callback status=%d", response.Code)
	}
}

func TestHTTPTransportBoundaryFailureBranches(t *testing.T) {
	database := testdb.OpenGizPayStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	server := testGizPayServer(repository)

	writer := &bufferedResponseWriter{header: make(http.Header)}
	if _, err := writer.Write([]byte("body")); err != nil || writer.status != http.StatusOK {
		t.Fatalf("buffered writer status=%d err=%v", writer.status, err)
	}

	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, test := range []struct {
		name    string
		request *http.Request
		want    int
	}{
		{name: "missing idempotency key", request: httptest.NewRequest(http.MethodPatch, "/account/v1/me", nil), want: http.StatusBadRequest},
		{name: "unreadable body", request: func() *http.Request {
			r := httptest.NewRequest(http.MethodPatch, "/account/v1/me?mode=test", nil)
			r.Header.Set("Idempotency-Key", "unreadable")
			r.Body = failingRequestBody{}
			return r
		}(), want: http.StatusBadRequest},
		{name: "read-only bypass", request: httptest.NewRequest(http.MethodHead, "/account/v1/me", nil), want: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.idempotencyMiddleware(next).ServeHTTP(response, test.request)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	for _, test := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/admin/v1/users", false},
		{http.MethodPut, "/callbacks/v1/payment_events", false},
		{http.MethodPost, "/v1/files", false},
		{http.MethodPost, "/upload/v1beta/files", false},
		{http.MethodPost, "/v1beta/files", false},
		{http.MethodPost, "/admin/v1/webhook_deliveries/id/retry", false},
		{http.MethodPost, "/admin/v1/administrators/id/api_keys", false},
		{http.MethodPost, "/admin/v1/users/id/status", true},
		{http.MethodPost, "/account/v1/powersync/credentials", false},
		{http.MethodPost, "/account/v1/accounts/id/topups", false},
		{http.MethodPost, "/account/v1/accounts/id/services", false},
		{http.MethodPost, "/account/v1/accounts/id/transfers", false},
		{http.MethodPost, "/account/v1/accounts/id/api_keys", false},
		{http.MethodPatch, "/account/v1/me", true},
		{http.MethodPost, "/pay/v1/webhook_endpoints", false},
		{http.MethodPost, "/pay/v1/payment_intents", false},
		{http.MethodPost, "/pay/v1/payment_intents/id/cancel", true},
		{http.MethodPost, "/admin/v1/auth/login", true},
		{http.MethodPost, "/unknown", false},
	} {
		if got := journaledMutationPath(test.method, test.path); got != test.want {
			t.Fatalf("journaledMutationPath(%s, %s)=%v want=%v", test.method, test.path, got, test.want)
		}
	}

	for _, test := range []struct {
		name    string
		handler http.Handler
	}{
		{name: "user session", handler: server.requireUserSession(next)},
		{name: "payment key", handler: server.requirePaymentKey("scope", next)},
		{name: "user or Gateway", handler: server.requireUserOrGatewayScope("account:self", next)},
	} {
		response := httptest.NewRecorder()
		test.handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", test.name, response.Code)
		}
	}
}

func TestPowerSyncTokenRejectsMalformedSecurityComponents(t *testing.T) {
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	config := powerSyncConfig{Endpoint: "https://sync.invalid", Audience: "audience", KeyID: "key", Key: []byte("0123456789abcdef0123456789abcdef")}
	valid, err := signPowerSyncToken(config, powerSyncClaims{Subject: "user", Audience: "audience", IssuedAt: now.Unix(), Expires: now.Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(valid, ".")
	badHeader := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"key"}`)) + "." + parts[1] + "." + parts[2]
	badPayload := parts[0] + ".!." + parts[2]
	badSignature := parts[0] + "." + parts[1] + ".!"
	wrongClaims, err := signPowerSyncToken(config, powerSyncClaims{Subject: "", Audience: "other", IssuedAt: now.Add(time.Minute).Unix(), Expires: now.Add(2 * time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"one-part", "!.payload.signature", badHeader, badPayload, badSignature, wrongClaims} {
		if _, err := verifyPowerSyncToken(config, token, now); err == nil {
			t.Fatalf("malformed PowerSync token accepted: %q", token)
		}
	}
}

func TestUnconfiguredAndOversizedProviderBoundaries(t *testing.T) {
	database := testdb.OpenGizPayStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	server := testGizPayServer(repository)

	for _, test := range []struct {
		name, method, path, body string
		handler                  http.HandlerFunc
	}{
		{name: "Realtime secret", method: http.MethodPost, path: "/", body: `{}`, handler: server.createRealtimeClientSecret},
		{name: "Realtime websocket", method: http.MethodGet, path: "/", handler: server.realtimeWebSocket},
		{name: "Realtime SDP", method: http.MethodPost, path: "/", body: "v=0", handler: server.realtimeWebRTCSDP},
		{name: "payment callback", method: http.MethodPost, path: "/", body: `{}`, handler: server.paymentProviderCallback},
		{name: "topup", method: http.MethodPost, path: "/", body: `{}`, handler: server.createTopup},
		{name: "refund", method: http.MethodPost, path: "/", body: `{}`, handler: server.refundTopup},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Idempotency-Key", "unconfigured-"+strings.ReplaceAll(test.name, " ", "-"))
			response := httptest.NewRecorder()
			test.handler(response, request)
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	gateway := gatewayservice.NewWithRealtimeProviderCallback(repository, nil, "", "callback-secret")
	regional := testGizWayServer(repository, gateway)
	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
		headers map[string]string
		body    io.Reader
		want    int
	}{
		{name: "missing websocket secret", handler: regional.realtimeWebSocket, want: http.StatusUnauthorized},
		{name: "oversized SDP", handler: regional.realtimeWebRTCSDP, headers: map[string]string{"Authorization": "Bearer secret"}, body: bytes.NewReader(bytes.Repeat([]byte("x"), (1<<20)+1)), want: http.StatusRequestEntityTooLarge},
		{name: "invalid SDP secret", handler: regional.realtimeWebRTCSDP, headers: map[string]string{"Authorization": "Bearer invalid"}, body: strings.NewReader("v=0"), want: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", test.body)
			for key, value := range test.headers {
				request.Header.Set(key, value)
			}
			response := httptest.NewRecorder()
			test.handler(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	payment := paymentservice.New(repository, paymentadapter.New("http://127.0.0.1:1", "provider-key"), "callback-secret")
	configured := NewWithServicesAndClockSurface(repository, nil, payment, nil, time.Now, nil, SurfaceGizPay)
	for _, test := range []struct {
		name, body, signature string
		want                  int
	}{
		{name: "oversized", body: string(bytes.Repeat([]byte("x"), (1<<20)+1)), want: http.StatusRequestEntityTooLarge},
		{name: "invalid signature", body: `{}`, signature: "v1=invalid", want: http.StatusUnauthorized},
	} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
		request.Header.Set("X-Gizway-Signature", test.signature)
		response := httptest.NewRecorder()
		configured.paymentProviderCallback(response, request)
		if response.Code != test.want {
			t.Fatalf("%s status=%d body=%s", test.name, response.Code, response.Body.String())
		}
	}

	response := httptest.NewRecorder()
	server.advanceStoryClock(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"by":"1s"}`)))
	if response.Code != http.StatusNotFound {
		t.Fatalf("clock without driver status=%d", response.Code)
	}
}

func TestMutationHandlersRejectInvalidCommands(t *testing.T) {
	database := testdb.OpenGizPayStory(t)
	defer database.Close()
	server := testGizPayServer(store.New(database.SQL))
	tests := []struct {
		name, body string
		handler    http.HandlerFunc
		paths      map[string]string
		want       int
	}{
		{"merchant service JSON", `{`, server.createMerchantService, map[string]string{"account_id": "missing"}, 400},
		{"merchant service validation", `{}`, server.createMerchantService, map[string]string{"account_id": "missing"}, 400},
		{"webhook JSON", `{`, server.createWebhookEndpoint, nil, 400},
		{"webhook URL", `{"url":"http://127.0.0.1/hook","events":[]}`, server.createWebhookEndpoint, nil, 400},
		{"webhook status JSON", `{`, server.updateWebhookEndpoint, map[string]string{"endpoint_id": "missing"}, 400},
		{"webhook status value", `{"status":"unknown"}`, server.updateWebhookEndpoint, map[string]string{"endpoint_id": "missing"}, 400},
		{"webhook rotate missing", `{}`, server.rotateWebhookEndpointSecret, map[string]string{"endpoint_id": "missing"}, 404},
		{"webhook delete missing", `{}`, server.deleteWebhookEndpoint, map[string]string{"endpoint_id": "missing"}, 404},
		{"variant create JSON", `{`, server.createModelVariant, map[string]string{"model_id": "missing"}, 400},
		{"variant create fields", `{}`, server.createModelVariant, map[string]string{"model_id": "missing"}, 400},
		{"variant update JSON", `{`, server.updateModelVariant, map[string]string{"variant_id": "missing"}, 400},
		{"variant update fields", `{}`, server.updateModelVariant, map[string]string{"variant_id": "missing"}, 400},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			request.Header.Set("Idempotency-Key", "mutation-"+string(rune('a'+index)))
			ctx := context.WithValue(request.Context(), userIDKey, "11000000-0000-4000-8000-000000000001")
			ctx = context.WithValue(ctx, accountIDKey, "22000000-0000-4000-8000-000000000002")
			ctx = context.WithValue(ctx, administratorIDKey, "41000000-0000-4000-8000-000000000001")
			request = request.WithContext(ctx)
			for key, value := range test.paths {
				request.SetPathValue(key, value)
			}
			recorder := httptest.NewRecorder()
			test.handler(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	for name, handler := range map[string]http.HandlerFunc{
		"merchant service": server.createMerchantService,
		"webhook create":   server.createWebhookEndpoint,
		"webhook status":   server.updateWebhookEndpoint,
		"webhook rotate":   server.rotateWebhookEndpointSecret,
		"webhook delete":   server.deleteWebhookEndpoint,
		"variant create":   server.createModelVariant,
	} {
		t.Run(name+" requires idempotency", func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d", recorder.Code)
			}
		})
	}
}

func TestAPIKeyValidation(t *testing.T) {
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	tests := []struct {
		name, keyName, kind string
		scopes              []string
		expires             *string
		wantError           bool
	}{
		{name: "gateway", keyName: "valid", kind: "gateway", scopes: []string{"gateway:invoke", "account:self"}, expires: &future},
		{name: "payment", keyName: "valid", kind: "payment", scopes: []string{"pay:intents:write"}},
		{name: "empty name", kind: "gateway", scopes: []string{"gateway:invoke"}, wantError: true},
		{name: "unknown kind", keyName: "valid", kind: "other", scopes: []string{"gateway:invoke"}, wantError: true},
		{name: "empty scopes", keyName: "valid", kind: "gateway", wantError: true},
		{name: "wrong scope", keyName: "valid", kind: "gateway", scopes: []string{"pay:intents:write"}, wantError: true},
		{name: "duplicate scope", keyName: "valid", kind: "gateway", scopes: []string{"gateway:invoke", "gateway:invoke"}, wantError: true},
		{name: "past expiry", keyName: "valid", kind: "gateway", scopes: []string{"gateway:invoke"}, expires: &past, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAPIKeyRequest(test.keyName, test.kind, test.scopes, test.expires, time.Now())
			if (err != nil) != test.wantError {
				t.Fatalf("validateAPIKeyRequest() error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}

func TestPriceAndHTTPHelpers(t *testing.T) {
	valid := store.ModelPrice{ModelVariantID: "variant", Metric: "input_token", UnitSize: 1000,
		BaseCustomerPriceMicrocredits: 2000, CustomerPriceMicrocredits: 1800, DiscountBPS: 1000, ValidFrom: "2026-08-10T00:00:00.000000000Z"}
	if err := validatePrice(&valid); err != nil {
		t.Fatalf("validatePrice(valid): %v", err)
	}
	invalid := []store.ModelPrice{
		{},
		{ModelVariantID: "v", Metric: "m", UnitSize: 1, DiscountBPS: -1, ValidFrom: "now"},
		{ModelVariantID: "v", Metric: "input_token", UnitSize: 1, BaseCustomerPriceMicrocredits: 1, CustomerPriceMicrocredits: 2, ValidFrom: "2026-08-10T00:00:00.000000000Z"},
		{ModelVariantID: "v", Metric: "input_token", UnitSize: 1, BaseCustomerPriceMicrocredits: 100, CustomerPriceMicrocredits: 95, DiscountBPS: 1000, ValidFrom: "2026-08-10T00:00:00.000000000Z"},
		{ModelVariantID: "v", Metric: "input_token", UnitSize: 1, UpstreamCostMicrocredits: -1, ValidFrom: "2026-08-10T00:00:00.000000000Z"},
		{ModelVariantID: "v", Metric: "input_token", UnitSize: 1, BaseCustomerPriceMicrocredits: math.MaxInt64, CustomerPriceMicrocredits: math.MaxInt64, ValidFrom: "2026-08-10T00:00:00.000000000Z"},
		{ModelVariantID: "v", Metric: "input_token", UnitSize: 1, ValidFrom: "not-a-time"},
		{ModelVariantID: "v", Metric: "input_token", UnitSize: 1, ValidFrom: "2026-08-10T00:00:00.000000000Z", ValidUntil: new("2026-08-09T00:00:00.000000000Z")},
	}
	for i, price := range invalid {
		if err := validatePrice(&price); err == nil {
			t.Fatalf("validatePrice(invalid %d) succeeded", i)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer token")
	if token, ok := bearerToken(request); !ok || token != "token" {
		t.Fatalf("bearerToken = %q, %v", token, ok)
	}
	if !oneOf("b", "a", "b") || oneOf("c", "a", "b") {
		t.Fatal("oneOf returned an unexpected result")
	}
	if encoded := mustJSON([]string{"x"}); string(encoded) != `["x"]` {
		t.Fatalf("mustJSON = %s", encoded)
	}
	pageValue := page([]string{"x"})
	if pageValue["data"] == nil || pageValue["page"] == nil {
		t.Fatalf("page = %#v", pageValue)
	}
	for _, body := range []string{`{"value":1}{"value":2}`, `{"unknown":true}`} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		var destination struct {
			Value int `json:"value"`
		}
		if err := decodeJSON(request, &destination); err == nil {
			t.Fatalf("decodeJSON(%q) succeeded", body)
		}
	}
	oversized := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"value":1}`+strings.Repeat(" ", 1<<20)))
	var oversizedDestination map[string]any
	if err := decodeJSON(oversized, &oversizedDestination); err == nil {
		t.Fatal("decodeJSON accepted an oversized body")
	}
}

func TestErrorWritersAndRecovery(t *testing.T) {
	for _, test := range []struct {
		name  string
		write func(http.ResponseWriter)
		want  int
	}{
		{name: "not found", write: func(w http.ResponseWriter) { writeDataError(w, store.ErrNotFound) }, want: http.StatusNotFound},
		{name: "internal", write: func(w http.ResponseWriter) { writeDataError(w, errors.New("db")) }, want: http.StatusInternalServerError},
		{name: "conflict", write: func(w http.ResponseWriter) { writeConflictOrDataError(w, errors.New("UNIQUE constraint")) }, want: http.StatusConflict},
		{name: "typed conflict", write: func(w http.ResponseWriter) { writeConflictOrDataError(w, store.ErrIdempotencyConflict) }, want: http.StatusConflict},
		{name: "other data", write: func(w http.ResponseWriter) { writeConflictOrDataError(w, errors.New("db")) }, want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.write(recorder)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
		})
	}

	recorder := httptest.NewRecorder()
	handler := recoverMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", strings.NewReader("")))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d", recorder.Code)
	}
}

// Database failures must be translated at every HTTP boundary instead of
// panicking or accidentally returning a success response. Calling handlers
// directly keeps this a transport-quality test; business behavior stays in
// the Hurl stories.
func TestHandlersTranslateDatabaseFailures(t *testing.T) {
	database := testdb.OpenGizPayStory(t)
	repository := store.New(database.SQL)
	server := testGizPayServer(repository)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	type handlerCase struct {
		name, method, body string
		handler            http.HandlerFunc
		paths              map[string]string
		idempotent         bool
	}
	cases := []handlerCase{
		{name: "current user", method: http.MethodGet, handler: server.getCurrentUser},
		{name: "accounts", method: http.MethodGet, handler: server.listAccounts},
		{name: "balance", method: http.MethodGet, handler: server.getBalance, paths: map[string]string{"account_id": "account"}},
		{name: "api keys", method: http.MethodGet, handler: server.listAPIKeys, paths: map[string]string{"account_id": "account"}},
		{name: "usage", method: http.MethodGet, handler: server.listGatewayUsage, paths: map[string]string{"account_id": "account"}},
		{name: "transactions", method: http.MethodGet, handler: server.listAccountTransactions, paths: map[string]string{"account_id": "account"}},
		{name: "transfers", method: http.MethodGet, handler: server.listCreditTransfers, paths: map[string]string{"account_id": "account"}},
		{name: "topups", method: http.MethodGet, handler: server.listTopups, paths: map[string]string{"account_id": "account"}},
		{name: "models", method: http.MethodGet, handler: server.listModels},
		{name: "public models", method: http.MethodGet, handler: server.listPublicModels},
		{name: "variants", method: http.MethodGet, handler: server.listModelVariants, paths: map[string]string{"model_id": "model"}},
		{name: "prices", method: http.MethodGet, handler: server.listModelPrices, paths: map[string]string{"variant_id": "variant"}},
		{name: "payment intent", method: http.MethodGet, handler: server.getPaymentIntent, paths: map[string]string{"payment_intent_id": "intent"}},
		{name: "checkout intent", method: http.MethodGet, handler: server.getCheckoutPaymentIntent, paths: map[string]string{"payment_intent_id": "intent"}},
		{name: "merchant transactions", method: http.MethodGet, handler: server.listMerchantTransactions},
		{name: "webhook endpoints", method: http.MethodGet, handler: server.listWebhookEndpoints},
		{name: "current admin", method: http.MethodGet, handler: server.getCurrentAdministrator},
		{name: "administrators", method: http.MethodGet, handler: server.listAdministrators},
		{name: "administrator", method: http.MethodGet, handler: server.getAdministrator, paths: map[string]string{"administrator_id": "admin"}},
		{name: "admin keys", method: http.MethodGet, handler: server.listAdministratorAPIKeys, paths: map[string]string{"administrator_id": "admin"}},
		{name: "overview", method: http.MethodGet, handler: server.getAdminOverview},
		{name: "users", method: http.MethodGet, handler: server.adminListUsers},
		{name: "user", method: http.MethodGet, handler: server.adminGetUser, paths: map[string]string{"user_id": "user"}},
		{name: "merchants", method: http.MethodGet, handler: server.adminListMerchants},
		{name: "merchant", method: http.MethodGet, handler: server.adminGetMerchant, paths: map[string]string{"account_id": "merchant"}},
		{name: "providers", method: http.MethodGet, handler: server.listProviders},
		{name: "provider endpoints", method: http.MethodGet, handler: server.listProviderEndpoints, paths: map[string]string{"provider_id": "provider"}},
		{name: "admin api keys", method: http.MethodGet, handler: server.adminListAPIKeys},
		{name: "received usage", method: http.MethodGet, handler: server.adminListReceivedUsage},
		{name: "payment rows", method: http.MethodGet, handler: server.adminListPayments},
		{name: "ledger accounts", method: http.MethodGet, handler: server.adminListLedgerAccounts},
		{name: "ledger transactions", method: http.MethodGet, handler: server.adminListLedgerTransactions},
		{name: "delivery rows", method: http.MethodGet, handler: server.adminListWebhookDeliveries},
		{name: "audit rows", method: http.MethodGet, handler: server.adminListAuditEvents},
		{name: "create admin", method: http.MethodPost, handler: server.createAdministrator, idempotent: true, body: `{"email":"next@example.test","display_name":"Next","password":"long-password"}`},
		{name: "change user", method: http.MethodPatch, handler: server.changeUserStatus, idempotent: true, paths: map[string]string{"user_id": "user"}, body: `{"status":"suspended","reason":"quality test"}`},
		{name: "decide merchant", method: http.MethodPost, handler: server.decideMerchant, idempotent: true, paths: map[string]string{"account_id": "merchant"}, body: `{"decision":"approve","review_level":"standard","reason":"quality test"}`},
		{name: "create provider", method: http.MethodPost, handler: server.createProvider, idempotent: true, body: `{"slug":"p","name":"Provider"}`},
		{name: "update provider", method: http.MethodPatch, handler: server.updateProvider, paths: map[string]string{"provider_id": "provider"}, body: `{"name":"Provider","status":"active"}`},
		{name: "create endpoint", method: http.MethodPost, handler: server.createProviderEndpoint, idempotent: true, paths: map[string]string{"provider_id": "provider"}, body: `{"name":"Endpoint","base_url":"https://provider.test","credential":"secret"}`},
		{name: "update endpoint", method: http.MethodPatch, handler: server.updateProviderEndpoint, paths: map[string]string{"endpoint_id": "endpoint"}, body: `{"name":"Endpoint","base_url":"https://provider.test","status":"active","priority":1,"weight":1}`},
		{name: "rotate credential", method: http.MethodPost, handler: server.rotateProviderCredential, idempotent: true, paths: map[string]string{"endpoint_id": "endpoint"}, body: `{"credential":"secret"}`},
		{name: "revoke admin key", method: http.MethodDelete, handler: server.revokeAdministratorAPIKey, idempotent: true, paths: map[string]string{"administrator_id": "admin", "admin_api_key_id": "key"}, body: `{}`},
		{name: "revoke customer key", method: http.MethodDelete, handler: server.adminRevokeAPIKey, idempotent: true, paths: map[string]string{"api_key_id": "key"}, body: `{"reason":"quality test"}`},
		{name: "adjust ledger", method: http.MethodPost, handler: server.createLedgerAdjustment, idempotent: true, body: `{"description":"test","reason":"quality","entries":[]}`},
		{name: "reverse ledger", method: http.MethodPost, handler: server.reverseLedgerTransaction, idempotent: true, paths: map[string]string{"transaction_id": "tx"}, body: `{"reason":"quality test"}`},
		{name: "retry delivery", method: http.MethodPost, handler: server.retryWebhookDelivery, idempotent: true, paths: map[string]string{"delivery_id": "delivery"}},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/", strings.NewReader(test.body))
			ctx := context.WithValue(request.Context(), userIDKey, "user")
			ctx = context.WithValue(ctx, accountIDKey, "account")
			ctx = context.WithValue(ctx, administratorIDKey, "admin")
			request = request.WithContext(ctx)
			for key, value := range test.paths {
				request.SetPathValue(key, value)
			}
			if test.idempotent {
				request.Header.Set("Idempotency-Key", "quality-"+strings.ReplaceAll(test.name, " ", "-"))
			}
			recorder := httptest.NewRecorder()
			test.handler(recorder, request)
			if recorder.Code < 400 {
				t.Fatalf("database failure status = %d", recorder.Code)
			}
		})
	}
}

func TestAuthenticationDatabaseFailuresFailClosed(t *testing.T) {
	database := testdb.OpenGizPayStory(t)
	repository := store.New(database.SQL)
	server := testGizPayServer(repository)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	regionalDatabase := testdb.OpenGizWayStory(t)
	regionalServer := testGizWayServer(store.New(regionalDatabase.SQL), nil)
	if err := regionalDatabase.Close(); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name, path string
		server     *Server
		want       int
	}{
		{name: "user", path: "/account/v1/me", server: server, want: http.StatusInternalServerError},
		{name: "gateway", path: "/v1/models", server: regionalServer, want: http.StatusServiceUnavailable},
		{name: "payment", path: "/pay/v1/payment_intents/missing", server: server, want: http.StatusInternalServerError},
		{name: "administrator", path: "/admin/v1/users", server: server, want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Authorization", "Bearer credential")
			recorder := httptest.NewRecorder()
			test.server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
		})
	}

	login := httptest.NewRequest(http.MethodPost, "/admin/v1/auth/login", strings.NewReader(`{"email":"admin@example.test","password":"valid-password"}`))
	login.Header.Set("Idempotency-Key", "closed-database-login")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, login)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("login status = %d, want 500", recorder.Code)
	}
}

func TestAdminListQueryValidationAndPageEnvelope(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/admin/v1/received_usage?cursor=2&limit=3&query=alice&status=succeeded&account_id=a&api_key_id=k&model_id=m&key_prefix=giz&type=topup&kind=user_credit&owner_account_id=o&transaction_type=transfer&reference_id=r&merchant_account_id=merchant&actor_user_id=actor&action=created&resource_type=model&resource_id=resource&from=2026-08-10T00:00:00Z&to=2026-08-11T00:00:00Z", nil)
	query, err := parseAdminListQuery(request)
	if err != nil {
		t.Fatal(err)
	}
	if query.Cursor != "2" || query.Limit != 3 || query.Query != "alice" || query.Status != "succeeded" || query.AccountID != "a" || query.APIKeyID != "k" || query.ModelID != "m" || query.KeyPrefix != "giz" || query.Type != "topup" || query.Kind != "user_credit" || query.OwnerAccountID != "o" || query.TransactionType != "transfer" || query.ReferenceID != "r" || query.MerchantID != "merchant" || query.ActorID != "actor" || query.Action != "created" || query.ResourceType != "model" || query.ResourceID != "resource" || query.From == "" || query.To == "" {
		t.Fatalf("parsed query=%+v", query)
	}
	for _, rawQuery := range []string{"cursor=bad", "limit=0", "limit=101", "limit=text", "cursor=" + strings.Repeat("1", 256)} {
		invalid := httptest.NewRequest(http.MethodGet, "/admin/v1/users?"+rawQuery, nil)
		if _, err := parseAdminListQuery(invalid); err == nil {
			t.Fatalf("invalid query %q succeeded", rawQuery)
		}
	}

	cursor := "3"
	recorder := httptest.NewRecorder()
	writeAdminPage(recorder, store.AdminPage[string]{Items: []string{"one"}, NextCursor: &cursor, HasMore: true})
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"next_cursor":"3"`) || !strings.Contains(recorder.Body.String(), `"has_more":true`) {
		t.Fatalf("page response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if journaledMutationPath(http.MethodPost, "/account/v1/powersync/credentials") {
		t.Fatal("short-lived PowerSync credential issuance must not replay an old journaled JWT")
	}
}

func TestStoryClockAndGatewayScopeHelpers(t *testing.T) {
	database := testdb.OpenGizPayStory(t)
	defer database.Close()
	current := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { return current }
	advance := func(duration time.Duration) time.Time {
		current = current.Add(duration)
		return current
	}
	repository := store.New(database.SQL)
	repository.ConfigureClock(now)
	server := NewWithServicesAndClockSurface(repository, nil, nil, nil, now, advance, SurfaceGizPay)
	for _, test := range []struct {
		body string
		want int
	}{
		{`{"by":"1m"}`, http.StatusOK},
		{`{"by":"0s"}`, http.StatusBadRequest},
		{`{"by":"bad"}`, http.StatusBadRequest},
		{`{"by":"9000h"}`, http.StatusBadRequest},
		{`{"unknown":true}`, http.StatusBadRequest},
	} {
		request := httptest.NewRequest(http.MethodPost, "/test/v1/clock/advance", strings.NewReader(test.body))
		response := httptest.NewRecorder()
		server.advanceStoryClock(response, request)
		if response.Code != test.want {
			t.Fatalf("clock body=%s status=%d body=%s", test.body, response.Code, response.Body.String())
		}
	}

	if !gatewayPrincipalHasScope(store.GatewayKeyPrincipal{Scopes: store.JSON(`["account:self","gateway:usage:read"]`)}, "gateway:invoke|gateway:usage:read") {
		t.Fatal("alternative Gateway scope was not accepted")
	}
	if gatewayPrincipalHasScope(store.GatewayKeyPrincipal{Scopes: store.JSON(`not-json`)}, "account:self") {
		t.Fatal("malformed Gateway scopes were accepted")
	}
	writer := &bufferedResponseWriter{header: make(http.Header)}
	writer.Flush()
}
