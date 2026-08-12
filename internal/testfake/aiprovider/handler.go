// Package aiprovider implements a deterministic OpenAI-compatible upstream.
package aiprovider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

func Handler(callbackSecrets ...string) http.Handler {
	return HandlerWithCredential("story-provider-key", callbackSecrets...)
}

// HandlerWithCredential lets routing stories give a catalog endpoint a
// credential different from the process bootstrap provider. This prevents a
// false-positive test where only the URL or only the credential is dynamic.
func HandlerWithCredential(providerCredential string, callbackSecrets ...string) http.Handler {
	callbackSecret := ""
	if len(callbackSecrets) > 0 {
		callbackSecret = callbackSecrets[0]
	}
	mux := http.NewServeMux()
	var calls atomic.Int64
	var streamCalls atomic.Int64
	var responsesCalls atomic.Int64
	var embeddingCalls atomic.Int64
	var speechCalls atomic.Int64
	var transcriptionCalls atomic.Int64
	var imageCalls atomic.Int64
	var realtimeCalls atomic.Int64
	var webrtcCalls atomic.Int64
	var realtimeAudioEvents atomic.Int64
	type realtimeCallbackConfig struct {
		CallbackURL   string `json:"callback_url"`
		SessionID     string `json:"session_id"`
		CallbackToken string `json:"callback_token"`
	}
	var realtimeConfigMu sync.Mutex
	realtimeConfigs := map[string]realtimeCallbackConfig{}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"chat_calls": calls.Load(), "stream_calls": streamCalls.Load(),
			"responses_calls": responsesCalls.Load(), "embedding_calls": embeddingCalls.Load(),
			"speech_calls": speechCalls.Load(), "transcription_calls": transcriptionCalls.Load(),
			"image_calls": imageCalls.Load(), "realtime_calls": realtimeCalls.Load(),
			"webrtc_calls":          webrtcCalls.Load(),
			"realtime_audio_events": realtimeAudioEvents.Load(),
		})
	})
	checkAuthorization := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") == "Bearer "+providerCredential {
			return true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"unauthorized"}}`)
		return false
	}
	// The fixture speaks the actual OpenAI wire protocols consumed by Bifrost.
	// Stable IDs and usage values make tests deterministic without adding an
	// upstream response-replay contract that GizWay no longer requires.
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuthorization(w, r) {
			return
		}
		var request struct {
			Model  string            `json:"model"`
			Stream bool              `json:"stream"`
			Tools  []json.RawMessage `json:"tools"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model == "" {
			http.Error(w, `{"error":{"message":"invalid request"}}`, http.StatusBadRequest)
			return
		}
		responsesCalls.Add(1)
		response := map[string]any{
			"id": "fake-response-001", "object": "response", "created_at": int64(1786320000),
			"status": "completed", "model": request.Model,
			"output": []any{map[string]any{
				"id": "msg_fake_001", "type": "message", "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": "deterministic responses output", "annotations": []any{}}},
			}},
			"usage": map[string]any{
				"input_tokens": 11, "input_tokens_details": map[string]any{"cached_tokens": 2},
				"output_tokens": 6, "output_tokens_details": map[string]any{"reasoning_tokens": 0}, "total_tokens": 17,
			},
		}
		if len(request.Tools) > 0 {
			response["output"] = []any{map[string]any{
				"id": "call_weather_001", "type": "function_call", "status": "completed",
				"call_id": "call_weather_001", "name": "get_weather", "arguments": `{"city":"Shanghai"}`,
			}}
		}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			created := map[string]any{"type": "response.created", "sequence_number": 0, "response": map[string]any{
				"id": "fake-response-001", "object": "response", "created_at": int64(1786320000),
				"status": "in_progress", "model": request.Model, "output": []any{},
			}}
			events := []any{
				created,
				map[string]any{"type": "response.output_text.delta", "sequence_number": 1, "output_index": 0, "content_index": 0, "item_id": "msg_fake_001", "delta": "deterministic responses output"},
				map[string]any{"type": "response.output_text.done", "sequence_number": 2, "output_index": 0, "content_index": 0, "item_id": "msg_fake_001", "text": "deterministic responses output"},
				map[string]any{"type": "response.completed", "sequence_number": 3, "response": response},
			}
			for _, event := range events {
				encoded, _ := json.Marshal(event)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
				if flusher != nil {
					flusher.Flush()
				}
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})
	mux.HandleFunc("POST /v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuthorization(w, r) {
			return
		}
		var request struct {
			Model      string `json:"model"`
			Input      any    `json:"input"`
			Dimensions *int   `json:"dimensions"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model == "" || request.Input == nil {
			http.Error(w, `{"error":{"message":"invalid request"}}`, http.StatusBadRequest)
			return
		}
		embeddingCalls.Add(1)
		if request.Model == "fake-fallback-primary" {
			http.Error(w, `{"error":{"message":"primary fixture variant unavailable"}}`, http.StatusInternalServerError)
			return
		}
		count := 1
		if values, ok := request.Input.([]any); ok {
			count = len(values)
		}
		dimensions := 3
		if request.Dimensions != nil && *request.Dimensions > 0 {
			dimensions = *request.Dimensions
		}
		data := make([]any, 0, count)
		for index := 0; index < count; index++ {
			vector := make([]float64, dimensions)
			for dimension := range vector {
				vector[dimension] = float64(index+dimension+1) / 8
			}
			data = append(data, map[string]any{"object": "embedding", "index": index, "embedding": vector})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list", "model": request.Model,
			"data":  data,
			"usage": map[string]any{"prompt_tokens": 8, "total_tokens": 8},
		})
	})
	mux.HandleFunc("POST /v1/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuthorization(w, r) {
			return
		}
		var request struct {
			Model string `json:"model"`
			Input string `json:"input"`
			Voice any    `json:"voice"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model == "" || request.Input == "" || request.Voice == nil {
			http.Error(w, `{"error":{"message":"invalid request"}}`, http.StatusBadRequest)
			return
		}
		speechCalls.Add(1)
		if request.Model == "fake-fallback-primary" {
			http.Error(w, `{"error":{"message":"primary fixture variant unavailable"}}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("GIZWAY-DETERMINISTIC-MP3"))
	})
	mux.HandleFunc("POST /v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuthorization(w, r) {
			return
		}
		if err := r.ParseMultipartForm(16 << 20); err != nil || r.FormValue("model") == "" {
			http.Error(w, `{"error":{"message":"invalid request"}}`, http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, `{"error":{"message":"file required"}}`, http.StatusBadRequest)
			return
		}
		defer file.Close()
		if contents, err := io.ReadAll(file); err != nil || len(contents) == 0 {
			http.Error(w, `{"error":{"message":"file required"}}`, http.StatusBadRequest)
			return
		}
		transcriptionCalls.Add(1)
		if r.FormValue("model") == "fake-fallback-primary" {
			http.Error(w, `{"error":{"message":"primary fixture variant unavailable"}}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"text": "deterministic transcript", "duration": 2.4, "language": "english",
			"usage": map[string]any{"type": "duration", "seconds": 2.4},
		})
	})
	mux.HandleFunc("POST /v1/images/generations", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuthorization(w, r) {
			return
		}
		var request struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model == "" || request.Prompt == "" {
			http.Error(w, `{"error":{"message":"invalid request"}}`, http.StatusBadRequest)
			return
		}
		imageCalls.Add(1)
		if request.Model == "fake-fallback-primary" {
			http.Error(w, `{"error":{"message":"primary fixture variant unavailable"}}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "fake-image-001", "created": int64(1786320000), "model": request.Model,
			"data": []any{map[string]any{"b64_json": "R0laV0FZLUlNQUdF", "revised_prompt": "deterministic story image"}},
			"usage": map[string]any{
				"input_tokens": 4, "output_tokens": 3, "total_tokens": 7,
				"output_tokens_details": map[string]any{"image_tokens": 3},
			},
		})
	})
	mux.HandleFunc("GET /v1/realtime", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+providerCredential {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "fixture complete")
		realtimeCalls.Add(1)
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"event_id":"rt-session","type":"session.created","session":{"id":"provider-realtime-001","model":"fake-text-v1"}}`))
		for {
			_, raw, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var event struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &event) != nil {
				continue
			}
			switch event.Type {
			case "input_audio_buffer.append":
				realtimeAudioEvents.Add(1)
			case "test.provider_disconnect":
				return
			case "test.idle":
				continue
			case "response.create":
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"event_id":"rt-delta","type":"response.text.delta","response_id":"rt-response-001","delta":"deterministic realtime response"}`))
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"event_id":"rt-done","type":"response.done","response":{"id":"rt-response-001","status":"completed","output":[],"usage":{"input_tokens":12,"output_tokens":7,"total_tokens":19,"input_token_details":{"cached_tokens":2,"audio_tokens":4,"text_tokens":6},"output_token_details":{"audio_tokens":2,"text_tokens":5}}}}`))
				return
			}
		}
	})
	// Hurl cannot speak WebSocket frames directly. This HTTP fixture is only a
	// protocol driver: it dials Gizway's real public WebSocket route, presents
	// the real one-purpose secret, sends actual frames, and reports no synthetic
	// business result. All assertions still read Gizway's SQL-backed APIs.
	mux.HandleFunc("POST /drive-realtime", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			APIURL    string `json:"api_url"`
			Secret    string `json:"secret"`
			SessionID string `json:"session_id"`
			Scenario  string `json:"scenario"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.APIURL == "" || request.Secret == "" || request.SessionID == "" {
			http.Error(w, "invalid realtime driver request", http.StatusBadRequest)
			return
		}
		wsURL := "ws" + strings.TrimPrefix(request.APIURL, "http") + "/v1/realtime?session_id=" + request.SessionID
		dialCtx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + request.Secret}}})
		if err != nil {
			http.Error(w, "Gizway realtime dial failed", http.StatusBadGateway)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "fixture done")
		write := func(payload string) bool {
			return conn.Write(dialCtx, websocket.MessageText, []byte(payload)) == nil
		}
		switch request.Scenario {
		case "text_audio":
			if !write(`{"type":"input_audio_buffer.append","audio":"AQIDBA=="}`) || !write(`{"type":"response.create","response":{"modalities":["text","audio"]}}`) {
				http.Error(w, "write failed", http.StatusBadGateway)
				return
			}
			terminal := false
			for {
				_, raw, readErr := conn.Read(dialCtx)
				if readErr != nil {
					break
				}
				if bytes.Contains(raw, []byte(`"type":"response.done"`)) {
					terminal = true
				}
			}
			if !terminal {
				http.Error(w, "terminal realtime usage was not relayed", http.StatusBadGateway)
				return
			}
		case "client_disconnect":
			_ = write(`{"type":"input_audio_buffer.append","audio":"AQIDBA=="}`)
			_ = conn.Close(websocket.StatusNormalClosure, "client disconnected")
		case "provider_disconnect":
			_ = write(`{"type":"test.provider_disconnect"}`)
			for {
				if _, _, readErr := conn.Read(dialCtx); readErr != nil {
					break
				}
			}
		case "timeout":
			_ = write(`{"type":"test.idle"}`)
			// The provider emits session.created immediately. Reading only one
			// frame would close the client side before Gizway's committed session
			// deadline and misclassify the result as client_disconnect. Remain on
			// the real socket until Gizway closes it at the shared deadline.
			for {
				if _, _, readErr := conn.Read(dialCtx); readErr != nil {
					break
				}
			}
		default:
			http.Error(w, "unsupported realtime scenario", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"driven":true}`)
	})
	mux.HandleFunc("POST /v1/realtime/calls", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+providerCredential {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("sdp") == "" {
			http.Error(w, "invalid SDP", http.StatusBadRequest)
			return
		}
		var callbackConfig realtimeCallbackConfig
		if rawSession := r.FormValue("session"); rawSession != "" {
			var sessionConfig struct {
				Model          string                 `json:"model"`
				GizwayCallback realtimeCallbackConfig `json:"gizway_callback"`
			}
			if json.Unmarshal([]byte(rawSession), &sessionConfig) != nil {
				http.Error(w, "invalid settlement callback config", http.StatusBadRequest)
				return
			}
			callbackConfig = sessionConfig.GizwayCallback
			if sessionConfig.Model == "" || callbackConfig.CallbackURL == "" || callbackConfig.SessionID == "" || callbackConfig.CallbackToken == "" {
				http.Error(w, "invalid settlement callback config", http.StatusBadRequest)
				return
			}
			realtimeConfigMu.Lock()
			realtimeConfigs[callbackConfig.SessionID] = callbackConfig
			realtimeConfigMu.Unlock()
		}
		webrtcCalls.Add(1)
		w.Header().Set("Content-Type", "application/sdp")
		_, _ = fmt.Fprint(w, "v=0\r\no=story-answer 0 0 IN IP4 127.0.0.1\r\ns=Gizway Fake Answer\r\nt=0 0\r\n")
	})
	// The real WebRTC media channel bypasses Gizway. This fixture endpoint
	// models the provider's independent, signed terminal-usage webhook so the
	// API story can prove exact settlement without inventing client-reported
	// usage.
	mux.HandleFunc("POST /complete-realtime", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			CallbackURL string `json:"callback_url"`
			SessionID   string `json:"session_id"`
			EventID     string `json:"event_id,omitempty"`
			InputTokens *int64 `json:"input_tokens,omitempty"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.SessionID == "" {
			http.Error(w, "invalid completion request", http.StatusBadRequest)
			return
		}
		realtimeConfigMu.Lock()
		callbackConfig, configured := realtimeConfigs[request.SessionID]
		realtimeConfigMu.Unlock()
		callbackKey := callbackConfig.CallbackToken
		if configured {
			request.CallbackURL = callbackConfig.CallbackURL
		} else {
			// Compatibility path for focused fake-provider unit tests. The Hurl
			// business suite deliberately omits callback_url and therefore proves
			// signaling supplied the real provider configuration.
			callbackKey = callbackSecret
		}
		if request.CallbackURL == "" || callbackKey == "" {
			http.Error(w, "missing provider callback configuration", http.StatusConflict)
			return
		}
		inputTokens := int64(12)
		if request.InputTokens != nil {
			inputTokens = *request.InputTokens
		}
		eventID := request.EventID
		if eventID == "" {
			eventID = "provider-webrtc-" + request.SessionID
		}
		payload, _ := json.Marshal(map[string]any{
			"event_id": eventID,
			"type":     "realtime.session.completed", "session_id": request.SessionID,
			"input_tokens": inputTokens, "output_tokens": 7,
		})
		mac := hmac.New(sha256.New, []byte(callbackKey))
		_, _ = mac.Write(payload)
		callback, err := http.NewRequestWithContext(r.Context(), http.MethodPost, request.CallbackURL, bytes.NewReader(payload))
		if err != nil {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
		callback.Header.Set("Content-Type", "application/json")
		callback.Header.Set("X-Gizway-Signature", "v1="+hex.EncodeToString(mac.Sum(nil)))
		response, err := http.DefaultClient.Do(callback)
		if err != nil {
			http.Error(w, "callback failed", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = fmt.Fprintf(w, `{"callback_status":%d}`, response.StatusCode)
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+providerCredential {
			http.Error(w, `{"error":{"message":"unauthorized"}}`, http.StatusUnauthorized)
			return
		}
		calls.Add(1)
		var request struct {
			Model          string            `json:"model"`
			Stream         bool              `json:"stream"`
			Tools          []json.RawMessage `json:"tools"`
			ResponseFormat json.RawMessage   `json:"response_format"`
			Messages       []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, `{"error":{"message":"invalid request"}}`, http.StatusBadRequest)
			return
		}
		cachedUsage := false
		if request.Model == "fake-fallback-primary" {
			http.Error(w, `{"error":{"message":"primary fixture variant unavailable"}}`, http.StatusInternalServerError)
			return
		}
		for _, message := range request.Messages {
			var text string
			_ = json.Unmarshal(message.Content, &text)
			if text == "cached chat" || text == "cached stream" {
				cachedUsage = true
			}
			if text == "provider-error" {
				http.Error(w, `{"error":{"message":"fixture failure"}}`, http.StatusInternalServerError)
				return
			}
			if text == "fallback-required" && request.Model == "fake-text-v1" {
				http.Error(w, `{"error":{"message":"primary fixture variant unavailable"}}`, http.StatusInternalServerError)
				return
			}
		}
		if request.Stream {
			streamCalls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			usage := `{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}`
			if cachedUsage {
				usage = `{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19,"prompt_tokens_details":{"cached_tokens":4}}`
			}
			chunks := []string{
				`{"id":"fake-stream-001","object":"chat.completion.chunk","created":1786320000,"model":"` + request.Model + `","choices":[{"index":0,"delta":{"role":"assistant","content":"deterministic "},"finish_reason":null}]}`,
				`{"id":"fake-stream-001","object":"chat.completion.chunk","created":1786320000,"model":"` + request.Model + `","choices":[{"index":0,"delta":{"content":"stream response"},"finish_reason":"stop"}]}`,
				`{"id":"fake-stream-001","object":"chat.completion.chunk","created":1786320000,"model":"` + request.Model + `","choices":[],"usage":` + usage + `}`,
			}
			for _, chunk := range chunks {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
				if flusher != nil {
					flusher.Flush()
				}
			}
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		message := map[string]any{"role": "assistant", "content": "deterministic story response"}
		finishReason := "stop"
		if len(request.Tools) > 0 {
			message = map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
				"id": "call_weather_001", "type": "function", "function": map[string]any{"name": "get_weather", "arguments": `{"city":"Shanghai"}`},
			}}}
			finishReason = "tool_calls"
		} else if len(request.ResponseFormat) > 0 && string(request.ResponseFormat) != "null" {
			message["content"] = `{"answer":"deterministic"}`
		}
		usage := map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		if cachedUsage {
			usage["prompt_tokens_details"] = map[string]any{"cached_tokens": 4}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "fake-chat-001", "object": "chat.completion", "created": 1786320000, "model": request.Model,
			"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finishReason}},
			"usage":   usage,
		})
	})
	return mux
}
