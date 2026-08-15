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
	"sort"
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
	credentials := map[string]bool{}
	for credential := range strings.SplitSeq(providerCredential, ",") {
		credentials[strings.TrimSpace(credential)] = true
	}
	authorized := func(r *http.Request) bool {
		return credentials[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	}
	callbackSecret := ""
	if len(callbackSecrets) > 0 {
		callbackSecret = callbackSecrets[0]
	}
	mux := http.NewServeMux()
	var totalRequests atomic.Int64
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
	var lastMaxTokens atomic.Int64
	var lastStreamIncludeUsage atomic.Bool
	var lastStreamIncludeObfuscation atomic.Bool
	type heldChatJob struct {
		reached     chan struct{}
		release     chan struct{}
		done        chan struct{}
		reachedOnce sync.Once
		releaseOnce sync.Once
		mu          sync.Mutex
		status      int
		body        string
		err         string
	}
	type concurrentBarrier struct {
		expected int64
		arrived  atomic.Int64
		ready    chan struct{}
		once     sync.Once
	}
	var heldChatSequence atomic.Int64
	var heldChatMu sync.Mutex
	heldChatJobs := map[string]*heldChatJob{}
	var activeHeldChat *heldChatJob
	var activeConcurrentBarrier *concurrentBarrier
	type realtimeCallbackConfig struct {
		CallbackURL   string `json:"callback_url"`
		SessionID     string `json:"session_id"`
		CallbackToken string `json:"callback_token"`
	}
	var realtimeConfigMu sync.Mutex
	realtimeConfigs := map[string]realtimeCallbackConfig{}
	writeStats := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_requests": totalRequests.Load(),
			"chat_calls":     calls.Load(), "stream_calls": streamCalls.Load(),
			"responses_calls": responsesCalls.Load(), "embedding_calls": embeddingCalls.Load(),
			"speech_calls": speechCalls.Load(), "transcription_calls": transcriptionCalls.Load(),
			"image_calls": imageCalls.Load(), "realtime_calls": realtimeCalls.Load(),
			"webrtc_calls":                    webrtcCalls.Load(),
			"realtime_audio_events":           realtimeAudioEvents.Load(),
			"last_max_tokens":                 lastMaxTokens.Load(),
			"last_stream_include_usage":       map[bool]int64{false: 0, true: 1}[lastStreamIncludeUsage.Load()],
			"last_stream_include_obfuscation": map[bool]int64{false: 0, true: 1}[lastStreamIncludeObfuscation.Load()],
		})
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /test/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, _ *http.Request) { writeStats(w) })
	mux.HandleFunc("GET /test/stats", func(w http.ResponseWriter, _ *http.Request) { writeStats(w) })
	mux.HandleFunc("POST /test/reset", func(w http.ResponseWriter, _ *http.Request) {
		totalRequests.Store(0)
		w.WriteHeader(http.StatusNoContent)
	})
	// These three endpoints are test orchestration only. They let a Hurl story
	// start a real GizWay request, wait until it reaches the Provider, mutate
	// prices, then release the response. That proves request-start pricing is a
	// snapshot without adding timing sleeps or hooks to production services.
	mux.HandleFunc("POST /drive-held-chat", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			APIURL string `json:"api_url"`
			APIKey string `json:"api_key"`
			Model  string `json:"model"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.APIURL == "" || request.APIKey == "" || request.Model == "" {
			http.Error(w, "invalid held chat driver request", http.StatusBadRequest)
			return
		}
		jobID := fmt.Sprintf("held-chat-%d", heldChatSequence.Add(1))
		job := &heldChatJob{reached: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
		heldChatMu.Lock()
		heldChatJobs[jobID] = job
		activeHeldChat = job
		heldChatMu.Unlock()
		go func() {
			defer close(job.done)
			payload := strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hold-for-price-snapshot"}],"stream":false}`, request.Model))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			upstreamRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(request.APIURL, "/")+"/v1/chat/completions", payload)
			if err == nil {
				upstreamRequest.Header.Set("Authorization", "Bearer "+request.APIKey)
				upstreamRequest.Header.Set("Content-Type", "application/json")
				var response *http.Response
				response, err = http.DefaultClient.Do(upstreamRequest)
				if err == nil {
					defer response.Body.Close()
					raw, readErr := io.ReadAll(response.Body)
					job.mu.Lock()
					job.status = response.StatusCode
					job.body = string(raw)
					if readErr != nil {
						job.err = readErr.Error()
					}
					job.mu.Unlock()
					return
				}
			}
			job.mu.Lock()
			job.err = err.Error()
			job.mu.Unlock()
		}()
		select {
		case <-job.reached:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprintf(w, `{"job_id":%q,"provider_reached":true}`, jobID)
		case <-time.After(5 * time.Second):
			job.releaseOnce.Do(func() { close(job.release) })
			http.Error(w, "held chat did not reach provider", http.StatusGatewayTimeout)
		}
	})
	mux.HandleFunc("POST /release-held-chat/{job_id}", func(w http.ResponseWriter, r *http.Request) {
		heldChatMu.Lock()
		job := heldChatJobs[r.PathValue("job_id")]
		heldChatMu.Unlock()
		if job == nil {
			http.NotFound(w, r)
			return
		}
		job.releaseOnce.Do(func() { close(job.release) })
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /held-chat/{job_id}", func(w http.ResponseWriter, r *http.Request) {
		heldChatMu.Lock()
		job := heldChatJobs[r.PathValue("job_id")]
		heldChatMu.Unlock()
		if job == nil {
			http.NotFound(w, r)
			return
		}
		select {
		case <-job.done:
			job.mu.Lock()
			defer job.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"done": true, "status": job.status, "body": job.body, "error": job.err})
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"done":false}`)
		}
	})
	// Hurl is sequential, so this driver originates simultaneous customer calls
	// to verify per-HMAC Credit Check singleflight and atomic local deduction.
	mux.HandleFunc("POST /drive-concurrent-chat", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			APIURL  string `json:"api_url"`
			APIKey  string `json:"api_key"`
			Model   string `json:"model"`
			Message string `json:"message"`
			Count   int    `json:"count"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.APIURL == "" || request.APIKey == "" || request.Model == "" || request.Count < 2 || request.Count > 64 {
			http.Error(w, "invalid concurrent chat driver request", http.StatusBadRequest)
			return
		}
		var successes atomic.Int64
		var failures atomic.Int64
		var group sync.WaitGroup
		start := make(chan struct{})
		if request.Message == "concurrent-barrier" {
			heldChatMu.Lock()
			activeConcurrentBarrier = &concurrentBarrier{expected: int64(request.Count), ready: make(chan struct{})}
			heldChatMu.Unlock()
		}
		for range request.Count {
			group.Go(func() {
				<-start
				payload := strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":%q}],"stream":false}`, request.Model, request.Message))
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				customerRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(request.APIURL, "/")+"/v1/chat/completions", payload)
				if err == nil {
					customerRequest.Header.Set("Authorization", "Bearer "+request.APIKey)
					customerRequest.Header.Set("Content-Type", "application/json")
					var response *http.Response
					response, err = http.DefaultClient.Do(customerRequest)
					if err == nil {
						response.Body.Close()
						if response.StatusCode == http.StatusOK {
							successes.Add(1)
							return
						}
					}
				}
				failures.Add(1)
			})
		}
		close(start)
		group.Wait()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"requested": request.Count, "succeeded": successes.Load(), "failed": failures.Load(),
		})
	})
	// This driver starts simultaneous first authenticated requests for the same
	// Human identity. Every response must expose the same single Personal
	// Account and its single GIZ_CREDIT Ledger Account.
	mux.HandleFunc("POST /drive-concurrent-account-login", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			PayURL     string `json:"pay_url"`
			HumanToken string `json:"human_token"`
			Count      int    `json:"count"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.PayURL == "" || request.HumanToken == "" || request.Count < 2 || request.Count > 64 {
			http.Error(w, "invalid concurrent account login driver request", http.StatusBadRequest)
			return
		}
		type account struct {
			ID    string `json:"id"`
			Kind  string `json:"kind"`
			Asset string `json:"asset"`
		}
		type accountResponse struct {
			Data []account `json:"data"`
		}
		var successes atomic.Int64
		var failures atomic.Int64
		var personal atomic.Int64
		var creditLedger atomic.Int64
		accountIDs := map[string]bool{}
		var accountIDsMu sync.Mutex
		var group sync.WaitGroup
		start := make(chan struct{})
		for range request.Count {
			group.Go(func() {
				<-start
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				loginRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(request.PayURL, "/")+"/account/v1/accounts", nil)
				if err != nil {
					failures.Add(1)
					return
				}
				loginRequest.Header.Set("Authorization", "Bearer "+request.HumanToken)
				response, err := http.DefaultClient.Do(loginRequest)
				if err != nil {
					failures.Add(1)
					return
				}
				defer response.Body.Close()
				var body accountResponse
				if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&body) != nil || len(body.Data) != 1 || body.Data[0].ID == "" {
					failures.Add(1)
					return
				}
				successes.Add(1)
				if body.Data[0].Kind == "personal" {
					personal.Add(1)
				}
				if body.Data[0].Asset == "GIZ_CREDIT" {
					creditLedger.Add(1)
				}
				accountIDsMu.Lock()
				accountIDs[body.Data[0].ID] = true
				accountIDsMu.Unlock()
			})
		}
		close(start)
		group.Wait()
		ids := make([]string, 0, len(accountIDs))
		for id := range accountIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"requested": request.Count, "succeeded": successes.Load(), "failed": failures.Load(),
			"account_ids":                          ids,
			"responses_with_one_personal_account":  personal.Load(),
			"responses_with_one_giz_credit_ledger": creditLedger.Load(),
		})
	})
	checkAuthorization := func(w http.ResponseWriter, r *http.Request) bool {
		if authorized(r) {
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
		if !authorized(r) {
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
		if !authorized(r) {
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
		if !authorized(r) {
			http.Error(w, `{"error":{"message":"unauthorized"}}`, http.StatusUnauthorized)
			return
		}
		calls.Add(1)
		var request struct {
			Model         string `json:"model"`
			Stream        bool   `json:"stream"`
			MaxTokens     int    `json:"max_tokens"`
			MaxCompletion int    `json:"max_completion_tokens"`
			StreamOptions struct {
				IncludeUsage       bool `json:"include_usage"`
				IncludeObfuscation bool `json:"include_obfuscation"`
			} `json:"stream_options"`
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
		lastMaxTokens.Store(int64(max(request.MaxTokens, request.MaxCompletion)))
		lastStreamIncludeUsage.Store(request.StreamOptions.IncludeUsage)
		lastStreamIncludeObfuscation.Store(request.StreamOptions.IncludeObfuscation)
		cachedUsage := false
		actualGrossSeven := false
		invalidConversion := false
		streamFailureAfterFirstDelta := false
		var heldJob *heldChatJob
		var barrier *concurrentBarrier
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
			if strings.Contains(text, "actual gross seven") {
				actualGrossSeven = true
			}
			if text == "provider-error" {
				http.Error(w, `{"error":{"message":"fixture failure"}}`, http.StatusInternalServerError)
				return
			}
			if text == "invalid-conversion" {
				invalidConversion = true
			}
			if text == "stream-error-after-first-delta" {
				streamFailureAfterFirstDelta = true
			}
			if text == "hold-for-price-snapshot" {
				heldChatMu.Lock()
				heldJob = activeHeldChat
				heldChatMu.Unlock()
			}
			if text == "concurrent-barrier" {
				heldChatMu.Lock()
				barrier = activeConcurrentBarrier
				heldChatMu.Unlock()
			}
			if text == "fallback-required" && request.Model == "fake-text-v1" {
				http.Error(w, `{"error":{"message":"primary fixture variant unavailable"}}`, http.StatusInternalServerError)
				return
			}
		}
		if heldJob != nil {
			heldJob.reachedOnce.Do(func() { close(heldJob.reached) })
			select {
			case <-heldJob.release:
			case <-r.Context().Done():
				return
			}
		}
		if barrier != nil {
			if barrier.arrived.Add(1) == barrier.expected {
				barrier.once.Do(func() { close(barrier.ready) })
			}
			select {
			case <-barrier.ready:
			case <-r.Context().Done():
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
			if streamFailureAfterFirstDelta {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", chunks[0])
				if flusher != nil {
					flusher.Flush()
				}
				_, _ = io.WriteString(w, `data: {"error":{"message":"fixture failed after first delta"}}`+"\n\n")
				return
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
		if invalidConversion {
			message = map[string]any{"role": "assistant", "content": nil}
		} else if len(request.Tools) > 0 {
			message = map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
				"id": "call_weather_001", "type": "function", "function": map[string]any{"name": "get_weather", "arguments": `{"city":"Shanghai"}`},
			}}}
			finishReason = "tool_calls"
		} else if len(request.ResponseFormat) > 0 && string(request.ResponseFormat) != "null" {
			message["content"] = `{"answer":"deterministic"}`
		}
		usage := map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		if actualGrossSeven {
			usage = map[string]any{"prompt_tokens": 12, "completion_tokens": 7, "total_tokens": 19}
		}
		if cachedUsage {
			usage["prompt_tokens_details"] = map[string]any{"cached_tokens": 4}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "fake-chat-001", "object": "chat.completion", "created": 1786320000, "model": request.Model,
			"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finishReason}},
			"usage":   usage,
		})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isProviderTestControlRequest(r.URL.Path) {
			totalRequests.Add(1)
		}
		mux.ServeHTTP(w, r)
	})
}

func isProviderTestControlRequest(path string) bool {
	return path == "/events" || strings.HasPrefix(path, "/test/") ||
		strings.HasPrefix(path, "/drive-") || strings.HasPrefix(path, "/held-chat/") ||
		strings.HasPrefix(path, "/release-held-chat/") || path == "/complete-realtime"
}
