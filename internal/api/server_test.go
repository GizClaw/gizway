package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	bifrostadapter "github.com/idy/gizway/internal/adapter/bifrost"
	paymentadapter "github.com/idy/gizway/internal/adapter/payment"
	riskadapter "github.com/idy/gizway/internal/adapter/risk"
	"github.com/idy/gizway/internal/api"
	gatewayservice "github.com/idy/gizway/internal/service/gateway"
	merchantservice "github.com/idy/gizway/internal/service/merchant"
	paymentservice "github.com/idy/gizway/internal/service/payment"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
	"github.com/idy/gizway/internal/testfake/aiprovider"
	"github.com/idy/gizway/internal/testfake/paymentprovider"
	"github.com/idy/gizway/internal/testfake/riskprovider"
)

const (
	userKey  = "gzs_story_user_active_1"
	adminKey = "gizadm_story_admin"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	database := testdb.OpenStory(t)
	t.Cleanup(func() { database.Close() })
	server := httptest.NewServer(api.New(store.New(database.SQL)).Handler())
	t.Cleanup(server.Close)
	return server
}

// This focused Go integration test exercises protocol codec and lifecycle
// branches that Hurl specifies at the business level. It intentionally checks
// only transport validity here; exact billing and durable public requirements
// remain in the commented Story 03 Hurl files.
func TestCompatibleProtocolHandlersUseBifrost(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	fakeProvider := httptest.NewServer(aiprovider.Handler())
	defer fakeProvider.Close()
	executor, err := bifrostadapter.NewOpenAI(t.Context(), fakeProvider.URL, "story-provider-key")
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Shutdown()
	repository := store.New(database.SQL)
	server := httptest.NewServer(api.NewWithServices(repository, gatewayservice.New(repository, executor), nil, merchantservice.New(repository)).Handler())
	defer server.Close()

	tests := []struct {
		name, path, body, keyHeader string
		stream                      bool
	}{
		{"responses", "/v1/responses", `{"model":"story-text","input":"hello"}`, "Authorization", false},
		{"chat fallback", "/v1/chat/completions", `{"model":"story-text","messages":[{"role":"user","content":"fallback-required"}]}`, "Authorization", false},
		{"responses stream", "/v1/responses", `{"model":"story-text","input":"hello","stream":true}`, "Authorization", true},
		{"embedding", "/v1/embeddings", `{"model":"story-text","input":"hello"}`, "Authorization", false},
		{"speech", "/v1/audio/speech", `{"model":"story-text","input":"hello","voice":"alloy"}`, "Authorization", false},
		{"image", "/v1/images/generations", `{"model":"story-text","prompt":"square"}`, "Authorization", false},
		{"anthropic", "/v1/messages", `{"model":"story-text","max_tokens":8,"messages":[{"role":"user","content":"hello"}]}`, "x-api-key", false},
		{"anthropic stream", "/v1/messages", `{"model":"story-text","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"hello"}]}`, "x-api-key", true},
		{"gemini", "/v1beta/models/story-text:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, "x-goog-api-key", false},
		{"gemini stream", "/v1beta/models/story-text:streamGenerateContent?alt=sse", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, "x-goog-api-key", true},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+test.path, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			credential := "giz_story_user_active_1"
			if test.keyHeader == "Authorization" {
				credential = "Bearer " + credential
			}
			request.Header.Set(test.keyHeader, credential)
			request.Header.Set("Idempotency-Key", "go-protocol-"+string(rune('a'+index)))
			request.Header.Set("Content-Type", "application/json")
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil || response.StatusCode != http.StatusOK || len(body) == 0 {
				t.Fatalf("status=%d body=%s readErr=%v", response.StatusCode, body, readErr)
			}
			if test.stream && !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
				t.Fatalf("stream content type=%q", response.Header.Get("Content-Type"))
			}
		})
	}

	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	_ = writer.WriteField("model", "story-text")
	part, err := writer.CreateFormFile("file", "audio.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("audio"))
	_ = writer.Close()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/v1/audio/transcriptions", &form)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer giz_story_user_active_1")
	request.Header.Set("Idempotency-Key", "go-protocol-transcription")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	requireBody, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || !bytes.Contains(requireBody, []byte("deterministic transcript")) {
		t.Fatalf("transcription status=%d body=%s err=%v", response.StatusCode, requireBody, readErr)
	}
}

func TestRealtimeWebSocketSettlesOnceAndRejectsReconnect(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	fakeProvider := httptest.NewServer(aiprovider.Handler())
	defer fakeProvider.Close()
	executor, err := bifrostadapter.NewOpenAI(t.Context(), fakeProvider.URL, "story-provider-key")
	if err != nil {
		t.Fatalf("initialize Bifrost: %v", err)
	}
	defer executor.Shutdown()
	repository := store.New(database.SQL)
	server := httptest.NewServer(api.NewWithServices(repository, gatewayservice.New(repository, executor), nil, merchantservice.New(repository)).Handler())
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/realtime/client_secrets", strings.NewReader(`{"model":"story-text","transport":"websocket"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer giz_story_user_active_1")
	req.Header.Set("Idempotency-Key", "go-realtime-websocket")
	req.Header.Set("Content-Type", "application/json")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("client secret status=%d", resp.StatusCode)
	}
	var created struct {
		ClientSecret struct {
			Value string `json:"value"`
		} `json:"client_secret"`
		Session struct {
			ID string `json:"session_id"`
		} `json:"session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?session_id=" + created.Session.ID
	header := http.Header{"Authorization": []string{"Bearer " + created.ClientSecret.Value}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial Realtime: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"event_id":"client-response","type":"response.create"}`)); err != nil {
		t.Fatal(err)
	}
	seenDone := false
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			break
		}
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &event) == nil && event.Type == "response.done" {
			seenDone = true
		}
	}
	if !seenDone {
		t.Fatal("provider response.done was not relayed")
	}
	_ = conn.Close(websocket.StatusNormalClosure, "test complete")

	deadline := time.Now().Add(time.Second)
	for {
		balance, err := repository.GetBalance(t.Context(), "11000000-0000-4000-8000-000000000001", "21000000-0000-4000-8000-000000000001")
		if err == nil && balance.Amount == 99_999_947 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Realtime settlement balance=%+v err=%v", balance, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	reconnect, response, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header})
	if reconnect != nil {
		_ = reconnect.Close(websocket.StatusNormalClosure, "unexpected")
	}
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reconnect err=%v response=%v", err, response)
	}

	// An idle upstream must not strand the reservation when the client closes
	// first. Reading session.created proves that both proxy legs were active.
	disconnectResponse := request(t, server.Client(), http.MethodPost, server.URL+"/v1/realtime/client_secrets", "giz_story_user_active_1", map[string]any{"model": "story-text", "transport": "websocket"})
	if disconnectResponse.StatusCode != http.StatusCreated {
		t.Fatalf("disconnect client secret status=%d", disconnectResponse.StatusCode)
	}
	disconnectSession := decode[struct {
		ClientSecret struct {
			Value string `json:"value"`
		} `json:"client_secret"`
		Session struct {
			ID string `json:"session_id"`
		} `json:"session"`
	}](t, disconnectResponse)
	disconnectURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime?session_id=" + disconnectSession.Session.ID
	disconnectHeader := http.Header{"Authorization": []string{"Bearer " + disconnectSession.ClientSecret.Value}}
	disconnect, _, err := websocket.Dial(ctx, disconnectURL, &websocket.DialOptions{HTTPHeader: disconnectHeader})
	if err != nil {
		t.Fatalf("dial disconnect session: %v", err)
	}
	if _, _, err := disconnect.Read(ctx); err != nil {
		t.Fatalf("read provider session event: %v", err)
	}
	if err := disconnect.Close(websocket.StatusNormalClosure, "client done"); err != nil {
		t.Fatalf("close disconnect session: %v", err)
	}
	deadline = time.Now().Add(time.Second)
	for {
		session, err := repository.GetRealtimeSession(t.Context(), disconnectSession.Session.ID)
		if err == nil && session.Status == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("disconnect session=%+v err=%v", session, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func request(t *testing.T, client *http.Client, method, url, key string, body any) *http.Response {
	t.Helper()
	var encoded bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req, err := http.NewRequest(method, url, &encoded)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost {
		req.Header.Set("Idempotency-Key", "go-test-"+t.Name()+"-"+url)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	return response
}

func decode[T any](t *testing.T, response *http.Response) T {
	t.Helper()
	defer response.Body.Close()
	var result T
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return result
}

func TestSeededAccountAPI(t *testing.T) {
	server := newTestServer(t)

	response := request(t, server.Client(), http.MethodGet, server.URL+"/account/v1/me", userKey, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /me status = %d, want 200", response.StatusCode)
	}
	user := decode[store.User](t, response)
	if user.Email != "active-one@gizway.test" {
		t.Fatalf("email = %q, want active-one@gizway.test", user.Email)
	}

	response = request(t, server.Client(), http.MethodGet, server.URL+"/account/v1/accounts/21000000-0000-4000-8000-000000000001/balance", userKey, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET balance status = %d, want 200", response.StatusCode)
	}
	balance := decode[store.Balance](t, response)
	if balance.Amount != 100_000_000 {
		t.Fatalf("balance = %d, want 100000000", balance.Amount)
	}
}

func TestAdminModelPriceWorkflow(t *testing.T) {
	server := newTestServer(t)
	client := server.Client()

	response := request(t, client, http.MethodPost, server.URL+"/admin/v1/models", adminKey, map[string]any{
		"slug": "test-model", "name": "Test Model", "modality": []string{"text"}, "metadata": map[string]any{},
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST model status = %d, want 201", response.StatusCode)
	}
	model := decode[store.Model](t, response)

	response = request(t, client, http.MethodPost, server.URL+"/admin/v1/models/"+model.ID+"/variants", adminKey, map[string]any{
		"provider_endpoint_id": "71000000-0000-4000-8000-000000000001",
		"provider_model_name":  "test-model-v1",
		"variant_slug":         "openai-test",
		"capabilities":         map[string]any{"chat": true},
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST variant status = %d, want 201", response.StatusCode)
	}
	variant := decode[store.ModelVariant](t, response)

	response = request(t, client, http.MethodPost, server.URL+"/admin/v1/model_variants/"+variant.ID+"/prices", adminKey, map[string]any{
		"metric": "input_token", "unit_size": 1_000_000,
		"upstream_cost_microcredits":       1_000_000,
		"base_customer_price_microcredits": 1_200_000,
		"customer_price_microcredits":      1_080_000,
		"discount_bps":                     1000, "valid_from": "2026-08-11T00:00:00.000000000Z",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST price status = %d, want 201", response.StatusCode)
	}
	price := decode[store.ModelPrice](t, response)
	if price.DiscountBPS != 1000 || price.CustomerPriceMicrocredits != 1_080_000 {
		t.Fatalf("price = %+v, want 10%% discount and 1080000 effective price", price)
	}
}

func TestRejectsWrongCredentialClass(t *testing.T) {
	server := newTestServer(t)
	response := request(t, server.Client(), http.MethodGet, server.URL+"/admin/v1/models", userKey, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("admin endpoint with user key status = %d, want 401", response.StatusCode)
	}
}

// TestHurlStoriesAgainstInstrumentedServer reuses the Hurl files as the sole
// business specification while also measuring which Go transport paths they
// execute. Each file still gets its own real story-base PostgreSQL schema.
func TestHurlStoriesAgainstInstrumentedServer(t *testing.T) {
	hurlPath, err := exec.LookPath("hurl")
	if err != nil {
		t.Skip("hurl is not installed")
	}
	storyFiles, err := filepath.Glob("../../tests/api/stories/*/*.hurl")
	if err != nil {
		t.Fatalf("discover Hurl stories: %v", err)
	}
	if len(storyFiles) == 0 {
		t.Fatal("no Hurl stories discovered")
	}
	for _, storyFile := range storyFiles {
		t.Run(strings.TrimSuffix(filepath.Base(storyFile), ".hurl"), func(t *testing.T) {
			var clockMu sync.RWMutex
			currentTime := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
			now := func() time.Time {
				clockMu.RLock()
				defer clockMu.RUnlock()
				return currentTime
			}
			advance := func(by time.Duration) time.Time {
				clockMu.Lock()
				defer clockMu.Unlock()
				currentTime = currentTime.Add(by)
				return currentTime
			}
			database := testdb.OpenStory(t)
			defer database.Close()
			fakeProvider := httptest.NewServer(aiprovider.Handler("story-ai-callback-secret"))
			defer fakeProvider.Close()
			catalogProvider := httptest.NewServer(aiprovider.HandlerWithCredential("story-catalog-provider-key", "story-ai-callback-secret"))
			defer catalogProvider.Close()
			executor, err := bifrostadapter.NewOpenAI(t.Context(), fakeProvider.URL, "story-provider-key")
			if err != nil {
				t.Fatalf("initialize Bifrost: %v", err)
			}
			defer executor.Shutdown()
			fakePayment := httptest.NewServer(paymentprovider.HandlerWithClock("story-callback-secret", now))
			defer fakePayment.Close()
			fakeRisk := httptest.NewServer(riskprovider.Handler("story-risk-key"))
			defer fakeRisk.Close()
			repository, err := store.NewWithSecretKey(database.SQL, []byte("gizway-story-secret-key-32bytes!"))
			if err != nil {
				t.Fatalf("initialize secret store: %v", err)
			}
			repository.ConfigureClock(now)
			payments := paymentservice.New(repository, paymentadapter.New(fakePayment.URL, "story-payment-key"), "story-callback-secret")
			payments.ConfigureClock(now)
			gateway := gatewayservice.NewWithRealtimeCallbackSecret(repository, executor, "story-ai-callback-secret")
			gateway.ConfigureClock(now)
			gateway.ConfigureRealtimeSessionTimeout(500 * time.Millisecond)
			merchant := merchantservice.NewWithRisk(repository, riskadapter.New(fakeRisk.URL, "story-risk-key"), true)
			merchant.ConfigureClock(now)
			apiServer := api.NewWithServicesAndClock(repository, gateway, payments, merchant, now, advance)
			apiServer.ConfigurePowerSync("https://sync.gizway.test", "powersync-story", "gizway-story-hs256", []byte("gizway-story-powersync-signing-key"))
			server := httptest.NewServer(apiServer.Handler())
			gateway.SetRealtimeProviderCallback(server.URL)
			defer server.Close()

			command := exec.Command(hurlPath, "--test",
				"--variable", "base_url="+server.URL,
				"--variable", "fake_url="+fakeProvider.URL,
				"--variable", "catalog_fake_url="+catalogProvider.URL,
				"--variable", "payment_url="+fakePayment.URL,
				"--variable", "checkout_url=https://pay.gizway.test",
				"--variable", "risk_url="+fakeRisk.URL,
				"--variable", "active_user_one_key=gzs_story_user_active_1",
				"--variable", "active_user_two_key=gzs_story_user_active_2",
				"--variable", "suspended_user_key=gzs_story_user_suspended",
				"--variable", "gateway_api_key=giz_story_user_active_1",
				"--variable", "gateway_api_key_two=giz_story_user_active_2",
				"--variable", "admin_api_key=gizadm_story_admin",
				storyFile,
			)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("run %s: %v\n%s", storyFile, err, output)
			}
		})
	}
}

// TestGatewayRecoveryWorkerReplaysWithoutClientRetry is a focused process-crash
// companion to the Hurl recovery contract. The panic occurs at the exact
// provider-success/before-outbox seam, leaving the real encrypted request and
// reservation behind; only RecoverGatewayCommands is allowed to finish it.
func TestGatewayRecoveryWorkerReplaysWithoutClientRetry(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	fakeProvider := httptest.NewServer(aiprovider.Handler())
	defer fakeProvider.Close()
	executor, err := bifrostadapter.NewOpenAI(t.Context(), fakeProvider.URL, "story-provider-key")
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Shutdown()
	repository, err := store.NewWithSecretKey(database.SQL, []byte("gizway-story-secret-key-32bytes!"))
	if err != nil {
		t.Fatal(err)
	}
	var clockMu sync.RWMutex
	current := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	now := func() time.Time {
		clockMu.RLock()
		defer clockMu.RUnlock()
		return current
	}
	advance := func(by time.Duration) time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		current = current.Add(by)
		return current
	}
	repository.ConfigureClock(now)
	gateway := gatewayservice.New(repository, executor)
	gateway.ConfigureClock(now)
	gateway.ConfigureStoryCrashRecovery(time.Second, func() { panic("provider-success crash seam") })
	apiServer := api.NewWithServicesAndClock(repository, gateway, nil, merchantservice.New(repository), now, advance)
	server := httptest.NewServer(apiServer.Handler())
	defer server.Close()

	crashRequest := func(key, content string, stream bool) {
		t.Helper()
		body := fmt.Sprintf(`{"model":"story-text","messages":[{"role":"user","content":%q}],"stream":%t}`, content, stream)
		request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer giz_story_user_active_1")
		request.Header.Set("Idempotency-Key", key)
		request.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		wantStatus := http.StatusInternalServerError
		if stream {
			// Provider frames precede the injected panic; the durable request is
			// still started even though the HTTP stream had already committed 200.
			wantStatus = http.StatusOK
		}
		if response.StatusCode != wantStatus {
			t.Fatalf("crash seam status=%d want=%d", response.StatusCode, wantStatus)
		}
	}
	crashRequest("go-worker-no-client-retry", "recover automatically", true)
	crashRequest("go-worker-payload-mismatch", "persisted envelope will not match", false)
	if _, err := database.SQL.Exec(`UPDATE gateway_requests SET payload_hash=$1 WHERE idempotency_key='go-worker-payload-mismatch'`, []byte("migration-corrupted-hash")); err != nil {
		t.Fatal(err)
	}

	gateway.ConfigureStoryCrashRecovery(time.Second, nil)
	advance(2 * time.Second)
	// Two replicas race the same due rows. Exactly one may renew the valid
	// lease; an idempotency_in_progress loser must not increment failure counts.
	start := make(chan struct{})
	recoveryErrors := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			recoveryErrors <- apiServer.RecoverGatewayCommands(t.Context(), 4)
		}()
	}
	close(start)
	for range 2 {
		<-recoveryErrors // payload mismatch is expected to return a durable error.
	}
	var status, recoveryStatus string
	var attempts, recoveryAttempts int
	if err := database.SQL.QueryRow(`SELECT status,recovery_status,execution_attempts,recovery_attempts FROM gateway_requests WHERE idempotency_key='go-worker-no-client-retry'`).Scan(&status, &recoveryStatus, &attempts, &recoveryAttempts); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || recoveryStatus != "completed" || attempts != 2 || recoveryAttempts != 0 {
		t.Fatalf("status=%s recovery=%s attempts=%d recovery failures=%d", status, recoveryStatus, attempts, recoveryAttempts)
	}
	if err := database.SQL.QueryRow(`SELECT status,recovery_status,recovery_attempts FROM gateway_requests WHERE idempotency_key='go-worker-payload-mismatch'`).Scan(&status, &recoveryStatus, &recoveryAttempts); err != nil {
		t.Fatal(err)
	}
	if status != "started" || recoveryStatus != "reconciliation_required" || recoveryAttempts != 1 {
		t.Fatalf("mismatch status=%s recovery=%s failures=%d", status, recoveryStatus, recoveryAttempts)
	}
	events, err := http.Get(fakeProvider.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	var counts map[string]int
	if err := json.NewDecoder(events.Body).Decode(&counts); err != nil || counts["chat_calls"] != 2 || counts["stream_calls"] != 1 {
		t.Fatalf("provider events=%v err=%v", counts, err)
	}
}

func TestTransportValidationPaths(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	server := httptest.NewServer(api.New(store.New(database.SQL)).Handler())
	defer server.Close()

	tests := []struct {
		name, method, path, key, body string
		want                          int
	}{
		{name: "empty profile", method: http.MethodPatch, path: "/account/v1/me", key: "gzs_story_user_active_1", body: `{}`, want: 400},
		{name: "invalid profile JSON", method: http.MethodPatch, path: "/account/v1/me", key: "gzs_story_user_active_1", body: `{`, want: 400},
		{name: "invalid key", method: http.MethodPost, path: "/account/v1/accounts/21000000-0000-4000-8000-000000000001/api_keys", key: "gzs_story_user_active_1", body: `{}`, want: 400},
		{name: "missing idempotency", method: http.MethodPost, path: "/account/v1/merchant_accounts", key: "gzs_story_user_active_1", body: `{}`, want: 400},
		{name: "invalid merchant", method: http.MethodPost, path: "/account/v1/merchant_accounts", key: "gzs_story_user_active_1", body: `{}`, want: 400},
		{name: "invalid from", method: http.MethodGet, path: "/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=x&to=2026-08-11T00:00:00Z", key: "gzs_story_user_active_1", want: 400},
		{name: "invalid to", method: http.MethodGet, path: "/account/v1/accounts/21000000-0000-4000-8000-000000000001/usage?from=2026-08-10T00:00:00Z&to=x", key: "gzs_story_user_active_1", want: 400},
		{name: "invalid transfer", method: http.MethodPost, path: "/account/v1/accounts/21000000-0000-4000-8000-000000000001/transfers", key: "gzs_story_user_active_1", body: `{}`, want: 400},
		{name: "invalid admin login", method: http.MethodPost, path: "/admin/v1/auth/login", body: `{`, want: 400},
		{name: "invalid administrator", method: http.MethodPost, path: "/admin/v1/administrators", key: "gizadm_story_admin", body: `{}`, want: 400},
		{name: "invalid administrator update", method: http.MethodPatch, path: "/admin/v1/administrators/missing", key: "gizadm_story_admin", body: `{`, want: 400},
		{name: "invalid administrator key", method: http.MethodPost, path: "/admin/v1/administrators/missing/api_keys", key: "gizadm_story_admin", body: `{}`, want: 400},
		{name: "invalid user status", method: http.MethodPost, path: "/admin/v1/users/missing/status", key: "gizadm_story_admin", body: `{}`, want: 400},
		{name: "invalid merchant decision", method: http.MethodPost, path: "/admin/v1/merchants/missing/decision", key: "gizadm_story_admin", body: `{}`, want: 400},
		{name: "invalid provider", method: http.MethodPost, path: "/admin/v1/providers", key: "gizadm_story_admin", body: `{}`, want: 400},
		{name: "invalid provider update", method: http.MethodPatch, path: "/admin/v1/providers/missing", key: "gizadm_story_admin", body: `{`, want: 400},
		{name: "invalid endpoint", method: http.MethodPost, path: "/admin/v1/providers/missing/endpoints", key: "gizadm_story_admin", body: `{}`, want: 400},
		{name: "invalid endpoint update", method: http.MethodPatch, path: "/admin/v1/provider_endpoints/missing", key: "gizadm_story_admin", body: `{`, want: 400},
		{name: "invalid credential rotation", method: http.MethodPost, path: "/admin/v1/provider_endpoints/missing/rotate_credential", key: "gizadm_story_admin", body: `{}`, want: 400},
		{name: "invalid ledger adjustment", method: http.MethodPost, path: "/admin/v1/ledger/adjustments", key: "gizadm_story_admin", body: `{`, want: 400},
		{name: "invalid model", method: http.MethodPost, path: "/admin/v1/models", key: "gizadm_story_admin", body: `{}`, want: 400},
		{name: "invalid model update", method: http.MethodPatch, path: "/admin/v1/models/missing", key: "gizadm_story_admin", body: `{}`, want: 400},
		{name: "missing model update", method: http.MethodPatch, path: "/admin/v1/models/missing", key: "gizadm_story_admin", body: `{"name":"Missing","status":"disabled"}`, want: 404},
		{name: "invalid variant", method: http.MethodPost, path: "/admin/v1/models/81000000-0000-4000-8000-000000000001/variants", key: "gizadm_story_admin", body: `{}`, want: 400},
		{name: "invalid price", method: http.MethodPost, path: "/admin/v1/model_variants/91000000-0000-4000-8000-000000000001/prices", key: "gizadm_story_admin", body: `{}`, want: 400},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequest(test.method, server.URL+test.path, strings.NewReader(test.body))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Authorization", "Bearer "+test.key)
			if test.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if test.method != http.MethodGet && test.name != "missing idempotency" {
				req.Header.Set("Idempotency-Key", "validation-"+strings.ReplaceAll(test.name, " ", "-"))
			}
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			response.Body.Close()
			if response.StatusCode != test.want {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.want)
			}
		})
	}
}

func TestOptionalExecutionServicesFailClosed(t *testing.T) {
	server := newTestServer(t)
	tests := []struct {
		name, method, path, key, body string
		idempotent                    bool
	}{
		{name: "chat", method: http.MethodPost, path: "/v1/chat/completions", key: "giz_story_user_active_1", body: `{}`, idempotent: true},
		{name: "Realtime secret", method: http.MethodPost, path: "/v1/realtime/client_secrets", key: "giz_story_user_active_1", body: `{}`, idempotent: true},
		{name: "Realtime websocket", method: http.MethodGet, path: "/v1/realtime"},
		{name: "Realtime WebRTC", method: http.MethodPost, path: "/v1/realtime/calls", body: `v=0`},
		{name: "Realtime callback", method: http.MethodPost, path: "/callbacks/v1/realtime_events", body: `{}`},
		{name: "topup", method: http.MethodPost, path: "/account/v1/accounts/21000000-0000-4000-8000-000000000001/topups", key: userKey, body: `{}`, idempotent: true},
		{name: "refund", method: http.MethodPost, path: "/account/v1/accounts/21000000-0000-4000-8000-000000000001/topups/topup/refunds", key: userKey, body: `{}`, idempotent: true},
		{name: "payment callback", method: http.MethodPost, path: "/callbacks/v1/payment_events", body: `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequest(test.method, server.URL+test.path, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			if test.key != "" {
				req.Header.Set("Authorization", "Bearer "+test.key)
			}
			if test.idempotent {
				req.Header.Set("Idempotency-Key", "unavailable-"+strings.ReplaceAll(test.name, " ", "-"))
			}
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", response.StatusCode)
			}
		})
	}
}

func TestConfiguredServicesRejectInvalidRequests(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	fakeAI := httptest.NewServer(aiprovider.Handler("story-ai-callback-secret"))
	defer fakeAI.Close()
	executor, err := bifrostadapter.NewOpenAI(t.Context(), fakeAI.URL, "story-provider-key")
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Shutdown()
	fakePayment := httptest.NewServer(paymentprovider.Handler("story-callback-secret"))
	defer fakePayment.Close()
	repository := store.New(database.SQL)
	gateway := gatewayservice.NewWithRealtimeCallbackSecret(repository, executor, "story-ai-callback-secret")
	server := httptest.NewServer(api.NewWithServices(
		repository,
		gateway,
		paymentservice.New(repository, paymentadapter.New(fakePayment.URL, "story-payment-key"), "story-callback-secret"),
		merchantservice.NewForStoryTests(repository),
	).Handler())
	gateway.SetRealtimeProviderCallback(server.URL)
	defer server.Close()
	keyResponse := request(t, server.Client(), http.MethodPost, server.URL+"/account/v1/accounts/22000000-0000-4000-8000-000000000002/api_keys", "gzs_story_user_active_2", map[string]any{
		"name": "quality payment key", "kind": "payment",
		"scopes": []string{"pay:intents:write", "pay:transactions:read", "pay:webhooks:write"},
	})
	if keyResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create payment key status = %d", keyResponse.StatusCode)
	}
	paymentKey := decode[struct {
		Secret string `json:"secret"`
	}](t, keyResponse).Secret

	tests := []struct {
		name, method, path, key, body string
		want                          int
		idempotent                    bool
	}{
		{name: "malformed Realtime", method: http.MethodPost, path: "/v1/realtime/client_secrets", key: "giz_story_user_active_1", body: `{`, want: 400, idempotent: true},
		{name: "invalid Realtime", method: http.MethodPost, path: "/v1/realtime/client_secrets", key: "giz_story_user_active_1", body: `{}`, want: 400, idempotent: true},
		{name: "unsigned Realtime callback", method: http.MethodPost, path: "/callbacks/v1/realtime_events", body: `{}`, want: 401},
		{name: "malformed topup", method: http.MethodPost, path: "/account/v1/accounts/21000000-0000-4000-8000-000000000001/topups", key: userKey, body: `{`, want: 400, idempotent: true},
		{name: "invalid topup", method: http.MethodPost, path: "/account/v1/accounts/21000000-0000-4000-8000-000000000001/topups", key: userKey, body: `{}`, want: 400, idempotent: true},
		{name: "invalid refund", method: http.MethodPost, path: "/account/v1/accounts/21000000-0000-4000-8000-000000000001/topups/missing/refunds", key: userKey, body: `{}`, want: 400, idempotent: true},
		{name: "unsigned payment callback", method: http.MethodPost, path: "/callbacks/v1/payment_events", body: `{}`, want: 401},
		{name: "invalid intent", method: http.MethodPost, path: "/pay/v1/payment_intents", key: paymentKey, body: `{}`, want: 400, idempotent: true},
		{name: "missing intent", method: http.MethodGet, path: "/pay/v1/payment_intents/missing", key: paymentKey, want: 404},
		{name: "missing checkout", method: http.MethodGet, path: "/pay/v1/checkout/payment_intents/missing", key: userKey, want: 404},
		{name: "missing confirm", method: http.MethodPost, path: "/pay/v1/payment_intents/missing/confirm", key: userKey, want: 404, idempotent: true},
		{name: "missing cancel", method: http.MethodPost, path: "/pay/v1/payment_intents/missing/cancel", key: paymentKey, want: 409, idempotent: true},
		{name: "invalid webhook", method: http.MethodPost, path: "/pay/v1/webhook_endpoints", key: paymentKey, body: `{}`, want: 400},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequest(test.method, server.URL+test.path, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			if test.key != "" {
				req.Header.Set("Authorization", "Bearer "+test.key)
			}
			if test.idempotent {
				req.Header.Set("Idempotency-Key", "invalid-"+strings.ReplaceAll(test.name, " ", "-"))
			}
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != test.want {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.want)
			}
		})
	}
}
