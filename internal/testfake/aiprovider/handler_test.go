package aiprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestProviderSuccessIsReplayedByIdempotencyKey(t *testing.T) {
	server := httptest.NewServer(HandlerWithCredential("test-provider-key"))
	defer server.Close()
	call := func(body string) []byte {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer test-provider-key")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "durable-request")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return requireStatus(t, response, http.StatusOK)
	}
	first := call(`{"model":"m","messages":[{"content":"first"}]}`)
	second := call(`{"model":"m","messages":[{"content":"changed but same provider command"}]}`)
	if !bytes.Equal(first, second) {
		t.Fatalf("provider replay changed\nfirst=%s\nsecond=%s", first, second)
	}
	events := requireStatus(t, fakeRequest(t, server.Client(), http.MethodGet, server.URL+"/events", "", nil), http.StatusOK)
	var counts map[string]int64
	if err := json.Unmarshal(events, &counts); err != nil || counts["chat_calls"] != 1 {
		t.Fatalf("events=%s err=%v", events, err)
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
