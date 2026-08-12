package aiprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"
)

func fakeRequest(t *testing.T, client *http.Client, method, url, contentType string, body io.Reader) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-provider-key")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func requireStatus(t *testing.T, response *http.Response, status int) []byte {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	return body
}

func TestProviderHTTPProtocols(t *testing.T) {
	server := httptest.NewServer(HandlerWithCredential("test-provider-key"))
	defer server.Close()
	client := server.Client()

	requireStatus(t, fakeRequest(t, client, http.MethodGet, server.URL+"/healthz", "", nil), http.StatusNoContent)
	unauthorized, err := client.Post(server.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"m"}`))
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, unauthorized, http.StatusUnauthorized)

	cases := []struct {
		path string
		body string
		want string
	}{
		{"/v1/chat/completions", `{"model":"m","messages":[{"content":"hello"}]}`, "deterministic story response"},
		{"/v1/chat/completions", `{"model":"m","stream":true,"messages":[{"content":"hello"}]}`, "data: [DONE]"},
		{"/v1/responses", `{"model":"m"}`, "fake-response-001"},
		{"/v1/responses", `{"model":"m","stream":true}`, "response.completed"},
		{"/v1/embeddings", `{"model":"m","input":"hello"}`, "embedding"},
		{"/v1/audio/speech", `{"model":"m","input":"hello","voice":"alloy"}`, "GIZWAY-DETERMINISTIC-MP3"},
		{"/v1/images/generations", `{"model":"m","prompt":"square"}`, "fake-image-001"},
	}
	for _, test := range cases {
		t.Run(test.path+test.want, func(t *testing.T) {
			body := requireStatus(t, fakeRequest(t, client, http.MethodPost, server.URL+test.path, "application/json", strings.NewReader(test.body)), http.StatusOK)
			if !bytes.Contains(body, []byte(test.want)) {
				t.Fatalf("body %q does not contain %q", body, test.want)
			}
		})
	}

	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	_ = writer.WriteField("model", "m")
	part, err := writer.CreateFormFile("file", "audio.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("audio"))
	_ = writer.Close()
	transcription := requireStatus(t, fakeRequest(t, client, http.MethodPost, server.URL+"/v1/audio/transcriptions", writer.FormDataContentType(), &form), http.StatusOK)
	if !bytes.Contains(transcription, []byte("deterministic transcript")) {
		t.Fatalf("transcription=%s", transcription)
	}

	var sdp bytes.Buffer
	sdpWriter := multipart.NewWriter(&sdp)
	_ = sdpWriter.WriteField("sdp", "v=0")
	_ = sdpWriter.Close()
	requireStatus(t, fakeRequest(t, client, http.MethodPost, server.URL+"/v1/realtime/calls", sdpWriter.FormDataContentType(), &sdp), http.StatusOK)

	events := requireStatus(t, fakeRequest(t, client, http.MethodGet, server.URL+"/events", "", nil), http.StatusOK)
	var counts map[string]int64
	if json.Unmarshal(events, &counts) != nil || counts["responses_calls"] != 2 || counts["image_calls"] != 1 {
		t.Fatalf("events=%s", events)
	}
}

func TestProviderRealtimeWebSocket(t *testing.T) {
	server := httptest.NewServer(HandlerWithCredential("test-provider-key"))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime"
	header := http.Header{"Authorization": []string{"Bearer test-provider-key"}}
	connection, response, err := websocket.Dial(t.Context(), url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial response=%v err=%v", response, err)
	}
	defer connection.CloseNow()
	_, created, err := connection.Read(t.Context())
	if err != nil || !bytes.Contains(created, []byte("session.created")) {
		t.Fatalf("created=%s err=%v", created, err)
	}
	if err := connection.Write(t.Context(), websocket.MessageText, []byte(`{"type":"response.create"}`)); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		_, event, err := connection.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 && !bytes.Contains(event, []byte("response.done")) {
			t.Fatalf("terminal=%s", event)
		}
	}
}

func TestProviderProtocolFailureFixtures(t *testing.T) {
	server := httptest.NewServer(HandlerWithCredential("test-provider-key", "callback-secret"))
	defer server.Close()
	client := server.Client()
	for _, test := range []struct {
		name, path, body string
	}{
		{"responses invalid", "/v1/responses", `{}`},
		{"embedding invalid", "/v1/embeddings", `{}`},
		{"speech invalid", "/v1/audio/speech", `{}`},
		{"image invalid", "/v1/images/generations", `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			requireStatus(t, fakeRequest(t, client, http.MethodPost, server.URL+test.path, "application/json", strings.NewReader(test.body)), http.StatusBadRequest)
		})
	}
	for _, test := range []struct {
		name, path, body string
	}{
		{"embedding fallback", "/v1/embeddings", `{"model":"fake-fallback-primary","input":"x"}`},
		{"speech fallback", "/v1/audio/speech", `{"model":"fake-fallback-primary","input":"x","voice":"alloy"}`},
		{"image fallback", "/v1/images/generations", `{"model":"fake-fallback-primary","prompt":"x"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			requireStatus(t, fakeRequest(t, client, http.MethodPost, server.URL+test.path, "application/json", strings.NewReader(test.body)), http.StatusInternalServerError)
		})
	}
	requireStatus(t, fakeRequest(t, client, http.MethodPost, server.URL+"/v1/audio/transcriptions", "application/json", strings.NewReader(`{}`)), http.StatusBadRequest)
	requireStatus(t, fakeRequest(t, client, http.MethodPost, server.URL+"/v1/realtime/calls", "application/json", strings.NewReader(`{}`)), http.StatusBadRequest)
	requireStatus(t, fakeRequest(t, client, http.MethodPost, server.URL+"/drive-realtime", "application/json", strings.NewReader(`{}`)), http.StatusBadRequest)
	requireStatus(t, fakeRequest(t, client, http.MethodPost, server.URL+"/complete-realtime", "application/json", strings.NewReader(`{"session_id":"missing"}`)), http.StatusConflict)

	unauthorizedURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime"
	connection, response, err := websocket.Dial(t.Context(), unauthorizedURL, nil)
	if connection != nil {
		connection.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized websocket response=%v err=%v", response, err)
	}
}

func TestProviderProtocolVariantsAndCounters(t *testing.T) {
	server := httptest.NewServer(HandlerWithCredential("test-provider-key", "callback-secret"))
	defer server.Close()
	client := server.Client()

	for _, test := range []struct {
		name, path, body, contains string
		status                     int
	}{
		{name: "response tool", path: "/v1/responses", body: `{"model":"m","tools":[{"type":"function","name":"get_weather"}]}`, contains: "call_weather_001", status: http.StatusOK},
		{name: "embedding batch dimensions", path: "/v1/embeddings", body: `{"model":"m","input":["a","b"],"dimensions":2}`, contains: `"index":1`, status: http.StatusOK},
		{name: "chat tool", path: "/v1/chat/completions", body: `{"model":"m","tools":[{"type":"function"}],"messages":[{"content":"hello"}]}`, contains: "tool_calls", status: http.StatusOK},
		{name: "chat cached", path: "/v1/chat/completions", body: `{"model":"m","messages":[{"content":"cached chat"}]}`, contains: "cached_tokens", status: http.StatusOK},
		{name: "chat cached stream", path: "/v1/chat/completions", body: `{"model":"m","stream":true,"messages":[{"content":"cached stream"}]}`, contains: "cached_tokens", status: http.StatusOK},
		{name: "chat response format", path: "/v1/chat/completions", body: `{"model":"m","response_format":{"type":"json_object"},"messages":[{"content":"hello"}]}`, contains: "deterministic", status: http.StatusOK},
		{name: "chat provider error", path: "/v1/chat/completions", body: `{"model":"m","messages":[{"content":"provider-error"}]}`, contains: "fixture failure", status: http.StatusInternalServerError},
		{name: "chat fallback required", path: "/v1/chat/completions", body: `{"model":"fake-text-v1","messages":[{"content":"fallback-required"}]}`, contains: "primary fixture variant unavailable", status: http.StatusInternalServerError},
		{name: "chat fallback", path: "/v1/chat/completions", body: `{"model":"fake-fallback-primary","messages":[{"content":"hello"}]}`, contains: "primary fixture variant unavailable", status: http.StatusInternalServerError},
		{name: "chat malformed", path: "/v1/chat/completions", body: `{`, contains: "invalid request", status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := requireStatus(t, fakeRequest(t, client, http.MethodPost, server.URL+test.path, "application/json", strings.NewReader(test.body)), test.status)
			if !bytes.Contains(body, []byte(test.contains)) {
				t.Fatalf("body=%s, want %q", body, test.contains)
			}
		})
	}

	makeTranscription := func(t *testing.T, model string, includeFile bool) (*bytes.Buffer, string) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("model", model)
		if includeFile {
			part, err := writer.CreateFormFile("file", "audio.txt")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = part.Write([]byte("audio"))
		}
		_ = writer.Close()
		return &body, writer.FormDataContentType()
	}
	missingFile, missingFileType := makeTranscription(t, "m", false)
	requireStatus(t, fakeRequest(t, client, http.MethodPost, server.URL+"/v1/audio/transcriptions", missingFileType, missingFile), http.StatusBadRequest)
	fallback, fallbackType := makeTranscription(t, "fake-fallback-primary", true)
	requireStatus(t, fakeRequest(t, client, http.MethodPost, server.URL+"/v1/audio/transcriptions", fallbackType, fallback), http.StatusInternalServerError)

	events := requireStatus(t, fakeRequest(t, client, http.MethodGet, server.URL+"/events", "", nil), http.StatusOK)
	if !bytes.Contains(events, []byte(`"embedding_calls":1`)) || !bytes.Contains(events, []byte(`"chat_calls":8`)) {
		t.Fatalf("events=%s", events)
	}

	for _, path := range []string{"/v1/embeddings", "/v1/audio/speech", "/v1/audio/transcriptions", "/v1/images/generations", "/v1/realtime/calls", "/v1/chat/completions"} {
		response, err := client.Post(server.URL+path, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		requireStatus(t, response, http.StatusUnauthorized)
	}

	for _, session := range []string{"{", `{"model":"m","gizway_callback":{}}`} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("sdp", "v=0")
		_ = writer.WriteField("session", session)
		_ = writer.Close()
		requireStatus(t, fakeRequest(t, client, http.MethodPost, server.URL+"/v1/realtime/calls", writer.FormDataContentType(), &body), http.StatusBadRequest)
	}
}

func TestProviderRealtimeAudioAndDisconnectScenarios(t *testing.T) {
	server := httptest.NewServer(HandlerWithCredential("test-provider-key"))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime"
	header := http.Header{"Authorization": []string{"Bearer test-provider-key"}}

	for _, scenario := range []string{"audio", "idle", "malformed", "disconnect"} {
		t.Run(scenario, func(t *testing.T) {
			connection, _, err := websocket.Dial(t.Context(), url, &websocket.DialOptions{HTTPHeader: header})
			if err != nil {
				t.Fatal(err)
			}
			defer connection.CloseNow()
			if _, _, err := connection.Read(t.Context()); err != nil {
				t.Fatal(err)
			}
			payload := map[string]string{
				"audio":      `{"type":"input_audio_buffer.append"}`,
				"idle":       `{"type":"test.idle"}`,
				"malformed":  `{`,
				"disconnect": `{"type":"test.provider_disconnect"}`,
			}[scenario]
			if err := connection.Write(t.Context(), websocket.MessageText, []byte(payload)); err != nil {
				t.Fatal(err)
			}
			if scenario != "disconnect" {
				_ = connection.Write(t.Context(), websocket.MessageText, []byte(`{"type":"test.provider_disconnect"}`))
			}
		})
	}
	events := requireStatus(t, fakeRequest(t, server.Client(), http.MethodGet, server.URL+"/events", "", nil), http.StatusOK)
	if !bytes.Contains(events, []byte(`"realtime_audio_events":1`)) {
		t.Fatalf("events=%s", events)
	}
}

func TestRealtimeDriverAndProviderCompletionCallbacks(t *testing.T) {
	provider := httptest.NewServer(HandlerWithCredential("test-provider-key", "callback-secret"))
	defer provider.Close()

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close(websocket.StatusNormalClosure, "done")
		for {
			_, raw, err := connection.Read(r.Context())
			if err != nil {
				return
			}
			if bytes.Contains(raw, []byte(`"type":"response.create"`)) {
				_ = connection.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.done"}`))
				return
			}
			if bytes.Contains(raw, []byte(`"type":"test.provider_disconnect"`)) {
				return
			}
		}
	}))
	defer gateway.Close()

	for _, scenario := range []string{"text_audio", "client_disconnect", "provider_disconnect"} {
		body := fmt.Sprintf(`{"api_url":%q,"secret":"client-secret","session_id":"session","scenario":%q}`, gateway.URL, scenario)
		requireStatus(t, fakeRequest(t, provider.Client(), http.MethodPost, provider.URL+"/drive-realtime", "application/json", strings.NewReader(body)), http.StatusOK)
	}
	requireStatus(t, fakeRequest(t, provider.Client(), http.MethodPost, provider.URL+"/drive-realtime", "application/json", strings.NewReader(fmt.Sprintf(`{"api_url":%q,"secret":"secret","session_id":"session","scenario":"unknown"}`, gateway.URL))), http.StatusBadRequest)
	requireStatus(t, fakeRequest(t, provider.Client(), http.MethodPost, provider.URL+"/drive-realtime", "application/json", strings.NewReader(`{"api_url":"http://127.0.0.1:1","secret":"secret","session_id":"session","scenario":"text_audio"}`)), http.StatusBadGateway)

	var callbacks atomic.Int32
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callbacks.Add(1)
		if !strings.HasPrefix(r.Header.Get("X-Gizway-Signature"), "v1=") {
			t.Error("callback omitted signature")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer callback.Close()

	completion := fmt.Sprintf(`{"callback_url":%q,"session_id":"compat-session","event_id":"event-1","input_tokens":21}`, callback.URL)
	requireStatus(t, fakeRequest(t, provider.Client(), http.MethodPost, provider.URL+"/complete-realtime", "application/json", strings.NewReader(completion)), http.StatusAccepted)

	var sdp bytes.Buffer
	writer := multipart.NewWriter(&sdp)
	_ = writer.WriteField("sdp", "v=0")
	configuration, _ := json.Marshal(map[string]any{
		"model":           "m",
		"gizway_callback": map[string]any{"callback_url": callback.URL, "session_id": "configured-session", "callback_token": "configured-token"},
	})
	_ = writer.WriteField("session", string(configuration))
	_ = writer.Close()
	requireStatus(t, fakeRequest(t, provider.Client(), http.MethodPost, provider.URL+"/v1/realtime/calls", writer.FormDataContentType(), &sdp), http.StatusOK)
	requireStatus(t, fakeRequest(t, provider.Client(), http.MethodPost, provider.URL+"/complete-realtime", "application/json", strings.NewReader(`{"session_id":"configured-session"}`)), http.StatusAccepted)

	badCallback := `{"callback_url":"http://127.0.0.1:1","session_id":"unreachable"}`
	requireStatus(t, fakeRequest(t, provider.Client(), http.MethodPost, provider.URL+"/complete-realtime", "application/json", strings.NewReader(badCallback)), http.StatusBadGateway)
	if callbacks.Load() != 2 {
		t.Fatalf("callbacks=%d", callbacks.Load())
	}
}
