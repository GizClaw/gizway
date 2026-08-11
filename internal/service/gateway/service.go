// Package gateway owns authorization-independent AI command orchestration,
// reservation, pricing, provider execution, and settlement.
package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/idy/gizway/internal/providerctx"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/timetext"
)

// Executor is the narrow Bifrost-owned provider boundary.
type Executor interface {
	ChatCompletionCandidates(context.Context, []store.ProviderExecutionTarget, []schemas.ChatMessage, *schemas.ChatParameters) (*schemas.BifrostChatResponse, error)
	ChatCompletionStreamCandidates(context.Context, []store.ProviderExecutionTarget, []schemas.ChatMessage, *schemas.ChatParameters) (<-chan *schemas.BifrostStreamChunk, context.CancelFunc, error)
	ResponsesCandidates(context.Context, []store.ProviderExecutionTarget, *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesResponse, error)
	ResponsesStreamCandidates(context.Context, []store.ProviderExecutionTarget, *schemas.BifrostResponsesRequest) (<-chan *schemas.BifrostStreamChunk, context.CancelFunc, error)
	EmbeddingCandidates(context.Context, []store.ProviderExecutionTarget, *schemas.BifrostEmbeddingRequest) (*schemas.BifrostEmbeddingResponse, error)
	SpeechCandidates(context.Context, []store.ProviderExecutionTarget, *schemas.BifrostSpeechRequest) (*schemas.BifrostSpeechResponse, error)
	TranscriptionCandidates(context.Context, []store.ProviderExecutionTarget, *schemas.BifrostTranscriptionRequest) (*schemas.BifrostTranscriptionResponse, error)
	ImageGenerationCandidates(context.Context, []store.ProviderExecutionTarget, *schemas.BifrostImageGenerationRequest) (*schemas.BifrostImageGenerationResponse, error)
	RealtimeWebSocketRoute(context.Context, store.ProviderExecutionTarget) (string, http.Header, error)
	RealtimeClientEvent(context.Context, store.ProviderExecutionTarget, []byte) ([]byte, error)
	RealtimeProviderEvent(context.Context, store.ProviderExecutionTarget, []byte) ([]byte, *schemas.BifrostLLMUsage, bool, error)
	ExchangeRealtimeWebRTCSDP(context.Context, store.ProviderExecutionTarget, string, json.RawMessage) (string, error)
}

// Service orchestrates one customer-visible request and charge.
type Service struct {
	store                  *store.Store
	executor               Executor
	realtimeCallbackSecret []byte
	realtimeCallbackURL    string
	realtimeCallbackMu     sync.RWMutex
	now                    func() time.Time
	executionLease         time.Duration
	realtimeSessionTimeout time.Duration
	afterProviderSuccess   func()
}

const defaultExecutionLease = 45 * time.Second
const defaultRealtimeSessionTimeout = 10 * time.Minute

// ErrInvalidRequest marks client-controlled validation failures that are
// discovered by the catalog-aware service layer rather than the HTTP codec.
var (
	ErrInvalidRequest       = errors.New("invalid Gateway request")
	ErrInvalidProviderEvent = errors.New("invalid Realtime provider event")
)

func New(repository *store.Store, executor Executor) *Service {
	return &Service{store: repository, executor: executor, now: time.Now, executionLease: defaultExecutionLease, realtimeSessionTimeout: defaultRealtimeSessionTimeout}
}

// NewWithRealtimeCallbackSecret additionally enables signed terminal usage
// events for direct WebRTC sessions.
func NewWithRealtimeCallbackSecret(repository *store.Store, executor Executor, callbackSecret string) *Service {
	return &Service{store: repository, executor: executor, realtimeCallbackSecret: []byte(callbackSecret), now: time.Now, executionLease: defaultExecutionLease, realtimeSessionTimeout: defaultRealtimeSessionTimeout}
}

// NewWithRealtimeProviderCallback configures the real provider-observable
// WebRTC settlement channel. The public callback URL and master key are never
// accepted from an API caller; a session-scoped token is derived below.
func NewWithRealtimeProviderCallback(repository *store.Store, executor Executor, callbackURL, callbackSecret string) *Service {
	return &Service{store: repository, executor: executor, realtimeCallbackURL: strings.TrimRight(callbackURL, "/"), realtimeCallbackSecret: []byte(callbackSecret), now: time.Now, executionLease: defaultExecutionLease, realtimeSessionTimeout: defaultRealtimeSessionTimeout}
}

// SetRealtimeProviderCallback is intended for httptest servers whose public
// URL is known only after Listen. Production wires the immutable URL at app
// construction time.
func (s *Service) SetRealtimeProviderCallback(callbackURL string) {
	s.realtimeCallbackMu.Lock()
	defer s.realtimeCallbackMu.Unlock()
	s.realtimeCallbackURL = strings.TrimRight(callbackURL, "/")
}

func (s *Service) realtimeProviderCallbackURL() string {
	s.realtimeCallbackMu.RLock()
	defer s.realtimeCallbackMu.RUnlock()
	return s.realtimeCallbackURL
}

func (s *Service) ConfigureRealtimeSessionTimeout(timeout time.Duration) {
	if timeout > 0 {
		s.realtimeSessionTimeout = timeout
	}
}

// ConfigureClock installs the composed business clock. Provider transport
// deadlines still use the real monotonic clock; catalog validity, leases,
// reservations and persisted timestamps use this clock.
func (s *Service) ConfigureClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// ConfigureStoryCrashRecovery installs deterministic failure injection used by
// the process-level crash/restart Hurl gate. Production startup never calls
// this method. The lease remains longer than Bifrost's request deadline in
// production; the story uses a short lease to keep acceptance tests fast.
func (s *Service) ConfigureStoryCrashRecovery(lease time.Duration, afterProviderSuccess func()) {
	if lease > 0 {
		s.executionLease = lease
	}
	s.afterProviderSuccess = afterProviderSuccess
}

func (s *Service) providerSucceeded() {
	if s.afterProviderSuccess != nil {
		s.afterProviderSuccess()
	}
}

func candidateTargets(candidates []store.GatewayCandidate) []store.ProviderExecutionTarget {
	targets := make([]store.ProviderExecutionTarget, 0, len(candidates))
	for _, candidate := range candidates {
		targets = append(targets, candidate.ExecutionTarget())
	}
	return targets
}

// resolvedCandidate uses Bifrost's private custom-provider identity first and
// falls back to the wire model only for legacy single-provider executors.
func resolvedCandidate(candidates []store.GatewayCandidate, provider schemas.ModelProvider, model string) (store.GatewayCandidate, error) {
	for _, candidate := range candidates {
		if string(provider) == "gizway-"+candidate.VariantID {
			if model != candidate.ProviderModel {
				return store.GatewayCandidate{}, errors.New("bifrost winner model is outside the authorized candidate")
			}
			return candidate, nil
		}
	}
	// Legacy/focused test executors may not expose winner metadata for a single
	// authorized candidate. This fallback is unambiguous only in that case;
	// multiple candidates always require Bifrost's private routing identity.
	if provider == "" && model == "" && len(candidates) == 1 {
		return candidates[0], nil
	}
	var matched *store.GatewayCandidate
	for index := range candidates {
		if candidates[index].ProviderModel != model {
			continue
		}
		if matched != nil {
			return store.GatewayCandidate{}, errors.New("bifrost winner is ambiguous across authorized candidates")
		}
		matched = &candidates[index]
	}
	if matched == nil {
		return store.GatewayCandidate{}, errors.New("bifrost resolved a provider outside the authorized candidate set")
	}
	return *matched, nil
}

// ChatRequest is the canonical subset currently owned by the first vertical slice.
type ChatRequest struct {
	Model          string                  `json:"model"`
	Messages       []ChatMessage           `json:"messages"`
	Stream         bool                    `json:"stream"`
	MaxTokens      *int                    `json:"max_tokens,omitempty"`
	Tools          []schemas.ChatTool      `json:"tools,omitempty"`
	ToolChoice     *schemas.ChatToolChoice `json:"tool_choice,omitempty"`
	ResponseFormat *any                    `json:"response_format,omitempty"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func (request ChatRequest) providerParameters() *schemas.ChatParameters {
	if len(request.Tools) == 0 && request.ToolChoice == nil && request.ResponseFormat == nil && request.MaxTokens == nil {
		return nil
	}
	return &schemas.ChatParameters{Tools: request.Tools, ToolChoice: request.ToolChoice, ResponseFormat: request.ResponseFormat, MaxCompletionTokens: request.MaxTokens}
}

type RealtimeRequest struct {
	Model     string `json:"model"`
	Transport string `json:"transport"`
}

type RealtimeClientSecret struct {
	Session      store.RealtimeSession `json:"session"`
	ClientSecret string                `json:"client_secret"`
}

// CreateRealtimeSession reserves a complete-session upper bound and returns a
// one-purpose credential. Only its SHA-256 digest is persisted.
func (s *Service) CreateRealtimeSession(ctx context.Context, principal store.GatewayPrincipal, idempotencyKey string, request RealtimeRequest) (RealtimeClientSecret, error) {
	if request.Model == "" || (request.Transport != "websocket" && request.Transport != "webrtc") {
		return RealtimeClientSecret{}, errors.New("model and websocket/webrtc transport are required")
	}
	if request.Transport == "webrtc" && (s.realtimeProviderCallbackURL() == "" || len(s.realtimeCallbackSecret) == 0) {
		return RealtimeClientSecret{}, errors.New("WebRTC provider settlement callback is not configured")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return RealtimeClientSecret{}, err
	}
	fingerprint := sha256.Sum256(encoded)
	now := s.now().UTC()
	candidate, err := s.store.ResolveGatewayCandidateForAccount(ctx, principal.AccountID, request.Model, "realtime", timetext.Format(now))
	if err != nil {
		return RealtimeClientSecret{}, err
	}
	if request.Transport == "webrtc" {
		var capabilities map[string]bool
		if json.Unmarshal(candidate.Capabilities, &capabilities) != nil || !capabilities["realtime_webrtc_callback"] {
			return RealtimeClientSecret{}, errors.New("model provider does not declare durable WebRTC callback support")
		}
	}
	reserve, err := realtimeReservationUpperBound(candidate.Prices, candidate.ContextWindow, candidate.MaxOutputTokens)
	if err != nil {
		return RealtimeClientSecret{}, err
	}
	sessionID := uuid.NewString()
	protocol := request.Transport
	if protocol == "websocket" {
		protocol = "websocket"
	}
	rawSecret := make([]byte, 32)
	if _, err := rand.Read(rawSecret); err != nil {
		return RealtimeClientSecret{}, err
	}
	secret := "gizrt_" + base64.RawURLEncoding.EncodeToString(rawSecret)
	secretHash := sha256.Sum256([]byte(secret))
	expires := timetext.Format(now.Add(2 * time.Minute))
	session := store.RealtimeSession{ID: sessionID, GatewayRequestID: sessionID,
		AccountID: principal.AccountID, APIKeyID: principal.APIKeyID,
		ModelID: candidate.ModelID, VariantID: candidate.VariantID,
		PublicModel: candidate.PublicModel, ProviderModel: candidate.ProviderModel,
		Transport: request.Transport, Status: "created", ExpiresAt: expires,
		// The authoritative runtime deadline is replaced atomically when the
		// one-purpose credential is consumed. This initial value only satisfies
		// the non-null durable record while the session remains unconnected.
		DeadlineAt: timetext.Format(now.Add(2*time.Minute + s.realtimeSessionTimeout)),
		CreatedAt:  timetext.Format(now)}
	command := store.GatewayCommand{ID: sessionID, AccountID: principal.AccountID, APIKeyID: principal.APIKeyID,
		ModelID: candidate.ModelID, VariantID: candidate.VariantID, Operation: "realtime." + request.Transport,
		IdempotencyKey: idempotencyKey, PayloadHash: fingerprint[:], ReserveAmount: reserve,
		Protocol: protocol, StartedAt: timetext.Format(now)}
	if err := s.store.CreateRealtimeCommand(ctx, command, session, secretHash[:]); err != nil {
		return RealtimeClientSecret{}, err
	}
	return RealtimeClientSecret{Session: session, ClientSecret: secret}, nil
}

func (s *Service) ConnectRealtimeSession(ctx context.Context, secret, transport string) (store.RealtimeSession, error) {
	hash := sha256.Sum256([]byte(secret))
	now := s.now().UTC()
	return s.store.ConnectRealtimeSession(ctx, hash[:], transport, timetext.Format(now), timetext.Format(now.Add(s.realtimeSessionTimeout)))
}

// ProxyRealtimeWebSocket relays validated events bidirectionally and settles
// all terminal-turn usage once when the provider closes the session.
func (s *Service) ProxyRealtimeWebSocket(ctx context.Context, client *websocket.Conn, session store.RealtimeSession) error {
	target, err := s.store.ResolveVariantExecutionTarget(ctx, session.VariantID)
	if err != nil {
		s.releaseGatewayCommand(ctx, session.GatewayRequestID, "provider_configuration_error")
		return err
	}
	url, headers, err := s.executor.RealtimeWebSocketRoute(ctx, target)
	if err != nil {
		_ = s.store.ReleaseGatewayCommand(context.WithoutCancel(ctx), session.GatewayRequestID, "provider_error")
		return err
	}
	upstream, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: headers})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		_ = s.store.ReleaseGatewayCommand(context.WithoutCancel(ctx), session.GatewayRequestID, "provider_error")
		return err
	}
	defer upstream.Close(websocket.StatusNormalClosure, "session complete")
	deadline, err := timetext.Parse(session.DeadlineAt)
	if err != nil {
		_ = s.store.ReleaseGatewayCommand(context.WithoutCancel(ctx), session.GatewayRequestID, "invalid_session_deadline")
		return err
	}
	// Use the exact deadline committed by ConnectRealtimeSession. The durable
	// sweeper and this live socket therefore cannot disagree after a late
	// connection consumes an almost-expired client secret.
	remaining := deadline.Sub(s.now())
	if remaining <= 0 {
		_ = s.store.ReleaseGatewayCommand(context.WithoutCancel(ctx), session.GatewayRequestID, "session_timeout")
		return context.DeadlineExceeded
	}
	// Convert the business-clock deadline to a monotonic runtime duration.
	// Story clocks can be fixed/advanced independently of the host wall clock;
	// production still observes the exact same configured duration.
	proxyCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	clientErrors := make(chan error, 1)
	go func() {
		report := func(err error) {
			clientErrors <- err
			// A client-side close must interrupt a blocked upstream Read. Without
			// this cancellation, an idle provider can strand the connection and
			// its active Credit reservation indefinitely.
			cancel()
		}
		for {
			kind, raw, err := client.Read(proxyCtx)
			if err != nil {
				report(err)
				return
			}
			translated, err := s.executor.RealtimeClientEvent(proxyCtx, target, raw)
			if err != nil {
				report(err)
				return
			}
			if err := upstream.Write(proxyCtx, kind, translated); err != nil {
				report(err)
				return
			}
		}
	}()
	var inputTokens, outputTokens, cachedInputTokens, inputAudioTokens, outputAudioTokens int64
	terminalUsage := false
	for {
		select {
		case err := <-clientErrors:
			if terminalUsage {
				return s.settleRealtime(context.WithoutCancel(ctx), session, inputTokens, outputTokens, cachedInputTokens, inputAudioTokens, outputAudioTokens)
			}
			_ = s.store.ReleaseGatewayCommand(context.WithoutCancel(ctx), session.GatewayRequestID, "client_disconnect")
			return err
		default:
		}
		kind, raw, err := upstream.Read(proxyCtx)
		if err != nil {
			if terminalUsage {
				return s.settleRealtime(context.WithoutCancel(ctx), session, inputTokens, outputTokens, cachedInputTokens, inputAudioTokens, outputAudioTokens)
			}
			releaseCode := "provider_disconnect"
			deadlineExpired := errors.Is(proxyCtx.Err(), context.DeadlineExceeded)
			if deadlineExpired {
				releaseCode = "session_timeout"
			} else {
				// At the shared deadline both proxy legs unblock. The client reader
				// reporting that same context cancellation must not overwrite the
				// authoritative timeout classification.
				select {
				case <-clientErrors:
					releaseCode = "client_disconnect"
				default:
				}
			}
			_ = s.store.ReleaseGatewayCommand(context.WithoutCancel(ctx), session.GatewayRequestID, releaseCode)
			return err
		}
		public, usage, terminal, err := s.executor.RealtimeProviderEvent(proxyCtx, target, raw)
		if err != nil {
			_ = s.store.ReleaseGatewayCommand(context.WithoutCancel(ctx), session.GatewayRequestID, "invalid_provider_event")
			return err
		}
		if usage != nil {
			inputTokens += int64(usage.PromptTokens)
			outputTokens += int64(usage.CompletionTokens)
			if usage.PromptTokensDetails != nil {
				cachedInputTokens += int64(usage.PromptTokensDetails.CachedReadTokens)
				inputAudioTokens += int64(usage.PromptTokensDetails.AudioTokens)
			}
			if usage.CompletionTokensDetails != nil {
				outputAudioTokens += int64(usage.CompletionTokensDetails.AudioTokens)
			}
			terminalUsage = terminalUsage || terminal
		}
		if err := client.Write(proxyCtx, kind, public); err != nil {
			if terminalUsage {
				return s.settleRealtime(context.WithoutCancel(ctx), session, inputTokens, outputTokens, cachedInputTokens, inputAudioTokens, outputAudioTokens)
			}
			_ = s.store.ReleaseGatewayCommand(context.WithoutCancel(ctx), session.GatewayRequestID, "client_disconnect")
			return err
		}
	}
}

func (s *Service) settleRealtime(ctx context.Context, session store.RealtimeSession, inputTokens, outputTokens, cachedInputTokens, inputAudioTokens, outputAudioTokens int64) error {
	prices, err := s.store.GatewayPricesForVariant(ctx, session.VariantID, session.CreatedAt)
	if err != nil {
		return err
	}
	metrics, err := pricedRealtimeMetrics(prices, inputTokens, outputTokens, cachedInputTokens, inputAudioTokens, outputAudioTokens)
	if err != nil {
		return err
	}
	summary, _ := json.Marshal(map[string]any{"session_id": session.ID, "status": "succeeded", "usage": map[string]int64{"input_tokens": inputTokens, "output_tokens": outputTokens, "cached_input_tokens": cachedInputTokens, "input_audio_tokens": inputAudioTokens, "output_audio_tokens": outputAudioTokens}})
	return s.settleGatewayCommand(ctx, session.GatewayRequestID, session.ID, metrics, summary, timetext.Format(s.now()))
}

func (s *Service) ExchangeRealtimeWebRTCSDP(ctx context.Context, session store.RealtimeSession, offer string, _ json.RawMessage) (string, error) {
	target, err := s.store.ResolveVariantExecutionTarget(ctx, session.VariantID)
	if err != nil {
		s.releaseGatewayCommand(ctx, session.GatewayRequestID, "provider_configuration_error")
		return "", err
	}
	callbackConfig, err := json.Marshal(map[string]any{
		"model": target.Model,
		// This extension is sent only to catalog variants that explicitly
		// declare realtime_webrtc_callback. Standard OpenAI endpoints are not
		// falsely treated as supporting a terminal-usage webhook.
		"gizway_callback": map[string]string{
			"callback_url":   s.realtimeProviderCallbackURL() + "/callbacks/v1/realtime_events",
			"session_id":     session.ID,
			"callback_token": s.realtimeSessionCallbackToken(session),
		},
	})
	if err != nil {
		s.releaseGatewayCommand(ctx, session.GatewayRequestID, "provider_configuration_error")
		return "", err
	}
	answer, err := s.executor.ExchangeRealtimeWebRTCSDP(ctx, target, offer, callbackConfig)
	if err != nil {
		_ = s.store.ReleaseGatewayCommand(context.WithoutCancel(ctx), session.GatewayRequestID, "provider_error")
		return "", err
	}
	return answer, nil
}

type RealtimeProviderEvent struct {
	EventID           string `json:"event_id"`
	Type              string `json:"type"`
	SessionID         string `json:"session_id"`
	InputTokens       int64  `json:"input_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
	CachedInputTokens int64  `json:"cached_input_tokens"`
	InputAudioTokens  int64  `json:"input_audio_tokens"`
	OutputAudioTokens int64  `json:"output_audio_tokens"`
}

// CompleteRealtimeProviderEvent verifies the provider boundary and settles a
// direct WebRTC session from measured terminal usage. Settlement itself is
// guarded by the started Gateway request, so concurrent retries cannot charge
// twice.
func (s *Service) CompleteRealtimeProviderEvent(ctx context.Context, raw []byte, signature string) (store.RealtimeSession, bool, error) {
	if len(s.realtimeCallbackSecret) == 0 || !strings.HasPrefix(signature, "v1=") {
		return store.RealtimeSession{}, false, ErrInvalidProviderEvent
	}
	var event RealtimeProviderEvent
	if json.Unmarshal(raw, &event) != nil || event.EventID == "" || event.Type != "realtime.session.completed" || event.SessionID == "" || event.InputTokens < 0 || event.OutputTokens < 0 || event.CachedInputTokens < 0 || event.InputAudioTokens < 0 || event.OutputAudioTokens < 0 || event.CachedInputTokens+event.InputAudioTokens > event.InputTokens || event.OutputAudioTokens > event.OutputTokens {
		return store.RealtimeSession{}, false, ErrInvalidProviderEvent
	}
	signedSession, err := s.store.GetRealtimeSession(ctx, event.SessionID)
	if err != nil || signedSession.VariantID == "" {
		return store.RealtimeSession{}, false, ErrInvalidProviderEvent
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signature, "v1="))
	if err != nil {
		return store.RealtimeSession{}, false, ErrInvalidProviderEvent
	}
	mac := hmac.New(sha256.New, []byte(s.realtimeSessionCallbackToken(signedSession)))
	_, _ = mac.Write(raw)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return store.RealtimeSession{}, false, ErrInvalidProviderEvent
	}
	now := timetext.Format(s.now())
	payloadHash := sha256.Sum256(raw)
	replayed, err := s.store.RecordRealtimeProviderEvent(ctx, event.EventID, event.SessionID, payloadHash[:], event.InputTokens, event.OutputTokens, event.CachedInputTokens, event.InputAudioTokens, event.OutputAudioTokens, now)
	if err != nil {
		if errors.Is(err, store.ErrInvalidRealtimeProviderState) {
			return store.RealtimeSession{}, false, ErrInvalidProviderEvent
		}
		return store.RealtimeSession{}, false, err
	}
	session, err := s.completeRecordedRealtimeProviderEvent(ctx, store.RealtimeProviderUsageEvent{
		EventID: event.EventID, SessionID: event.SessionID, InputTokens: event.InputTokens, OutputTokens: event.OutputTokens, CachedInputTokens: event.CachedInputTokens, InputAudioTokens: event.InputAudioTokens, OutputAudioTokens: event.OutputAudioTokens,
	}, now)
	return session, replayed, err
}

func (s *Service) realtimeSessionCallbackToken(session store.RealtimeSession) string {
	mac := hmac.New(sha256.New, s.realtimeCallbackSecret)
	_, _ = mac.Write([]byte(session.VariantID))
	_, _ = mac.Write([]byte("\n"))
	_, _ = mac.Write([]byte(session.ID))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) completeRecordedRealtimeProviderEvent(ctx context.Context, event store.RealtimeProviderUsageEvent, completedAt string) (store.RealtimeSession, error) {
	session, err := s.store.GetRealtimeSession(ctx, event.SessionID)
	if err != nil || session.Transport != "webrtc" {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return store.RealtimeSession{}, err
		}
		return store.RealtimeSession{}, ErrInvalidProviderEvent
	}
	if session.Status == "succeeded" {
		return session, s.store.MarkRealtimeProviderEventProcessed(ctx, event.EventID, completedAt)
	}
	if session.Status != "connected" {
		return store.RealtimeSession{}, ErrInvalidProviderEvent
	}
	if err := s.settleRealtime(ctx, session, event.InputTokens, event.OutputTokens, event.CachedInputTokens, event.InputAudioTokens, event.OutputAudioTokens); err != nil {
		// A concurrent duplicate may have completed between the status read and
		// settlement. Re-read before surfacing an internal error.
		latest, readErr := s.store.GetRealtimeSession(ctx, event.SessionID)
		if readErr == nil && latest.Status == "succeeded" {
			return latest, s.store.MarkRealtimeProviderEventProcessed(ctx, event.EventID, completedAt)
		}
		return store.RealtimeSession{}, err
	}
	if err := s.store.MarkRealtimeProviderEventProcessed(ctx, event.EventID, completedAt); err != nil {
		return store.RealtimeSession{}, err
	}
	return s.store.GetRealtimeSession(ctx, event.SessionID)
}

// RecoverRealtimeProviderEvents finishes authenticated terminal usage after a
// process/database interruption between callback receipt and ledger settlement.
func (s *Service) RecoverRealtimeProviderEvents(ctx context.Context, limit int) error {
	events, err := s.store.RecoverableRealtimeProviderEvents(ctx, limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, event := range events {
		if _, err := s.completeRecordedRealtimeProviderEvent(ctx, event, timetext.Format(s.now())); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) RecoverExpiredRealtimeSessions(ctx context.Context, limit int) error {
	_, err := s.store.ExpireRealtimeSessions(ctx, timetext.Format(s.now()), limit)
	return err
}

type gatewayExecutionPlan struct {
	requestID  string
	candidates []store.GatewayCandidate
	replayJSON []byte
}

// beginGatewayExecution establishes one immutable provider execution plan.
// It deliberately checks the durable idempotency record before consulting the
// live catalog. After a crash, a reclaimed lease therefore uses the exact
// endpoint, credential, fallback order and prices authorized on attempt one;
// catalog rotation can affect new requests only.
func (s *Service) beginGatewayExecution(
	ctx context.Context,
	principal store.GatewayPrincipal,
	operation, idempotencyKey, publicModel string,
	payloadHash []byte,
	now time.Time,
	requestedMaxOutput int64,
	reserveFor func(store.GatewayCandidate) (int64, error),
) (gatewayExecutionPlan, error) {
	leaseUntil := timetext.Format(now.Add(s.executionLease))
	existing, err := s.store.ResumeGatewayCommand(ctx, principal.APIKeyID, operation, idempotencyKey, payloadHash, timetext.Format(now), leaseUntil)
	if err != nil {
		return gatewayExecutionPlan{}, err
	}
	if existing.Existing {
		if len(existing.ReplayJSON) > 0 {
			return gatewayExecutionPlan{requestID: existing.RequestID, replayJSON: existing.ReplayJSON}, nil
		}
		candidates, err := decodeGatewayExecutionSnapshot(existing.ExecutionSnapshot)
		if err != nil {
			return gatewayExecutionPlan{}, err
		}
		return gatewayExecutionPlan{requestID: existing.RequestID, candidates: candidates}, nil
	}

	candidates, err := s.store.ResolveGatewayCandidatesForAccount(ctx, principal.AccountID, publicModel, operation, timetext.Format(now))
	if err != nil {
		return gatewayExecutionPlan{}, err
	}
	candidates, err = candidatesWithinOutputLimit(candidates, requestedMaxOutput)
	if err != nil {
		return gatewayExecutionPlan{}, err
	}
	var reserve int64
	for _, candidate := range candidates {
		candidateReserve, err := reserveFor(candidate)
		if err != nil {
			return gatewayExecutionPlan{}, err
		}
		if candidateReserve > reserve {
			reserve = candidateReserve
		}
	}
	snapshot, err := json.Marshal(candidates)
	if err != nil {
		return gatewayExecutionPlan{}, fmt.Errorf("encode Gateway execution snapshot: %w", err)
	}
	var recoveryRequest []byte
	if request, ok := providerctx.RecoveryRequestFrom(ctx); ok {
		recoveryRequest, err = json.Marshal(request)
		if err != nil {
			return gatewayExecutionPlan{}, fmt.Errorf("encode Gateway recovery request: %w", err)
		}
	}
	primary := candidates[0]
	begin, err := s.store.BeginGatewayCommand(ctx, store.GatewayCommand{
		ID: uuid.NewString(), AccountID: principal.AccountID, APIKeyID: principal.APIKeyID,
		ModelID: primary.ModelID, VariantID: primary.VariantID, Protocol: "https",
		Operation: operation, IdempotencyKey: idempotencyKey, PayloadHash: payloadHash,
		ReserveAmount: reserve, StartedAt: timetext.Format(now), ExecutionLeaseUntil: leaseUntil,
		ExecutionSnapshot: snapshot, RecoveryRequest: recoveryRequest,
	})
	if err != nil {
		return gatewayExecutionPlan{}, err
	}
	if len(begin.ReplayJSON) > 0 {
		return gatewayExecutionPlan{requestID: begin.RequestID, replayJSON: begin.ReplayJSON}, nil
	}
	if len(begin.ExecutionSnapshot) > 0 {
		candidates, err = decodeGatewayExecutionSnapshot(begin.ExecutionSnapshot)
		if err != nil {
			return gatewayExecutionPlan{}, err
		}
	}
	return gatewayExecutionPlan{requestID: begin.RequestID, candidates: candidates}, nil
}

func decodeGatewayExecutionSnapshot(snapshot []byte) ([]store.GatewayCandidate, error) {
	var candidates []store.GatewayCandidate
	if len(snapshot) == 0 {
		return nil, errors.New("stored Gateway execution snapshot is invalid")
	}
	if err := json.Unmarshal(snapshot, &candidates); err != nil || len(candidates) == 0 {
		return nil, errors.New("stored Gateway execution snapshot is invalid")
	}
	for _, candidate := range candidates {
		if candidate.ModelID == "" || candidate.VariantID == "" || candidate.ProviderEndpoint == "" || candidate.ProviderModel == "" || len(candidate.Prices) == 0 {
			return nil, errors.New("stored Gateway execution snapshot is incomplete")
		}
	}
	return candidates, nil
}

// StreamChat executes a provider SSE stream, forwards canonical chunks through
// emit, and settles exactly once from the provider's terminal usage record.
// Stored chunks are replayed without another provider call for the same
// idempotency key.
func (s *Service) StreamChat(ctx context.Context, principal store.GatewayPrincipal, idempotencyKey string, request ChatRequest, emit func([]byte) error) error {
	if s.executor == nil {
		return errors.New("AI executor is not configured")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode Gateway request: %w", err)
	}
	fingerprint := sha256.Sum256(encoded)
	now := s.now().UTC()
	maxOutput := int64(0)
	if request.MaxTokens != nil {
		if *request.MaxTokens <= 0 {
			return errors.New("max_tokens must be positive")
		}
		maxOutput = int64(*request.MaxTokens)
	}
	plan, err := s.beginGatewayExecution(ctx, principal, "chat.completions", idempotencyKey, request.Model, fingerprint[:], now, maxOutput, func(allowed store.GatewayCandidate) (int64, error) {
		outputLimit := maxOutput
		if outputLimit == 0 {
			outputLimit = allowed.MaxOutputTokens
		}
		return reservationUpperBound(allowed.Prices, allowed.ContextWindow, outputLimit)
	})
	if err != nil {
		return err
	}
	if len(plan.replayJSON) > 0 {
		var chunks []json.RawMessage
		if err := json.Unmarshal(plan.replayJSON, &chunks); err != nil {
			return fmt.Errorf("decode stored stream: %w", err)
		}
		for _, chunk := range chunks {
			if err := emit(chunk); err != nil {
				return err
			}
		}
		return nil
	}
	requestID := plan.requestID
	candidates := plan.candidates
	candidate := candidates[0]
	ctx = providerctx.WithIdempotencyKey(ctx, requestID)

	messages, err := providerMessages(request.Messages)
	if err != nil {
		s.releaseGatewayCommand(ctx, requestID, "invalid_request")
		return err
	}
	chunks, cancel, err := s.executor.ChatCompletionStreamCandidates(ctx, candidateTargets(candidates), messages, request.providerParameters())
	if err != nil {
		s.releaseGatewayCommand(ctx, requestID, "provider_error")
		return err
	}
	defer cancel()
	stored := make([]json.RawMessage, 0, 4)
	var usage *schemas.BifrostLLMUsage
	var clientWriteErr error
	providerRequestID := ""
	resolved := candidate
	for chunk := range chunks {
		if chunk == nil {
			continue
		}
		if chunk.BifrostError != nil {
			s.releaseGatewayCommand(ctx, requestID, "provider_error")
			return fmt.Errorf("provider stream: %s", chunk.BifrostError.Error.Message)
		}
		response := chunk.BifrostChatResponse
		if response == nil {
			continue
		}
		winnerModel := resolvedResponseModel(response.ExtraFields, response.Model)
		winner, winnerErr := resolvedCandidate(candidates, response.ExtraFields.RoutingInfo.Provider, winnerModel)
		if winnerErr != nil {
			s.releaseGatewayCommand(ctx, requestID, "unapproved_fallback")
			return winnerErr
		}
		resolved = winner
		response.Model = resolved.PublicModel
		if response.ID != "" {
			providerRequestID = response.ID
		}
		if response.Usage != nil {
			usage = response.Usage
		}
		publicChunk, err := MarshalPublicJSON(response)
		if err != nil {
			s.releaseGatewayCommand(ctx, requestID, "response_error")
			return err
		}
		stored = append(stored, append(json.RawMessage(nil), publicChunk...))
		if clientWriteErr == nil {
			if err := emit(publicChunk); err != nil {
				// A terminal frame may carry authoritative usage. Once that
				// usage exists, a broken client connection must not turn paid
				// provider work into a free request. Drain the provider stream,
				// persist the exact settlement, then return the write error.
				if usage == nil {
					s.releaseGatewayCommand(ctx, requestID, "client_disconnect")
					return err
				}
				clientWriteErr = err
			}
		}
	}
	s.providerSucceeded()
	if usage == nil {
		s.releaseGatewayCommand(ctx, requestID, "invalid_usage")
		return errors.New("provider stream did not include terminal usage")
	}
	quantities, err := llmUsageQuantities(usage)
	if err != nil {
		s.releaseGatewayCommand(ctx, requestID, "invalid_usage")
		return err
	}
	metrics, err := pricedQuantities(resolved.Prices, quantities)
	if err != nil {
		s.releaseGatewayCommand(ctx, requestID, "pricing_error")
		return err
	}
	if err := validateCandidateUsage("chat.completions", resolved, quantities); err != nil {
		s.releaseGatewayCommand(ctx, requestID, "invalid_usage")
		return err
	}
	storedJSON, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	if err := s.settleGatewayCommandForVariant(ctx, requestID, providerRequestID, resolved.VariantID, metrics, storedJSON, timetext.Format(s.now())); err != nil {
		return err
	}
	if clientWriteErr != nil {
		return clientWriteErr
	}
	return nil
}

// Chat executes or replays a settled request. Returned bytes are the exact
// public response persisted with the command.
func (s *Service) Chat(ctx context.Context, principal store.GatewayPrincipal, idempotencyKey string, request ChatRequest) ([]byte, error) {
	if s.executor == nil {
		return nil, errors.New("AI executor is not configured")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode Gateway request: %w", err)
	}
	fingerprint := sha256.Sum256(encoded)
	now := s.now().UTC()
	maxOutput := int64(0)
	if request.MaxTokens != nil {
		if *request.MaxTokens <= 0 {
			return nil, fmt.Errorf("%w: max_tokens must be positive", ErrInvalidRequest)
		}
		maxOutput = int64(*request.MaxTokens)
	}
	plan, err := s.beginGatewayExecution(ctx, principal, "chat.completions", idempotencyKey, request.Model, fingerprint[:], now, maxOutput, func(allowed store.GatewayCandidate) (int64, error) {
		outputLimit := maxOutput
		if outputLimit == 0 {
			outputLimit = allowed.MaxOutputTokens
		}
		return reservationUpperBound(allowed.Prices, allowed.ContextWindow, outputLimit)
	})
	if err != nil {
		return nil, err
	}
	if len(plan.replayJSON) > 0 {
		return plan.replayJSON, nil
	}
	requestID := plan.requestID
	candidates := plan.candidates
	ctx = providerctx.WithIdempotencyKey(ctx, requestID)

	messages, err := providerMessages(request.Messages)
	if err != nil {
		s.releaseGatewayCommand(ctx, requestID, "invalid_request")
		return nil, err
	}
	response, err := s.executor.ChatCompletionCandidates(ctx, candidateTargets(candidates), messages, request.providerParameters())
	if err != nil {
		s.releaseGatewayCommand(ctx, requestID, "provider_error")
		return nil, err
	}
	s.providerSucceeded()
	if response.Usage == nil {
		s.releaseGatewayCommand(ctx, requestID, "invalid_usage")
		return nil, errors.New("provider response did not include usage")
	}

	resolvedModel := resolvedResponseModel(response.ExtraFields, response.Model)
	resolved, err := resolvedCandidate(candidates, response.ExtraFields.RoutingInfo.Provider, resolvedModel)
	if err != nil {
		s.releaseGatewayCommand(ctx, requestID, "unapproved_fallback")
		return nil, err
	}
	quantities, err := llmUsageQuantities(response.Usage)
	if err != nil {
		s.releaseGatewayCommand(ctx, requestID, "invalid_usage")
		return nil, err
	}
	metrics, err := pricedQuantities(resolved.Prices, quantities)
	if err != nil {
		s.releaseGatewayCommand(ctx, requestID, "pricing_error")
		return nil, err
	}
	if err := validateCandidateUsage("chat.completions", resolved, quantities); err != nil {
		s.releaseGatewayCommand(ctx, requestID, "invalid_usage")
		return nil, err
	}
	response.Model = resolved.PublicModel
	publicJSON, err := MarshalPublicJSON(response)
	if err != nil {
		s.releaseGatewayCommand(ctx, requestID, "response_error")
		return nil, err
	}
	completed := timetext.Format(s.now())
	if err := s.settleGatewayCommandForVariant(ctx, requestID, response.ID, resolved.VariantID, metrics, publicJSON, completed); err != nil {
		return nil, err
	}
	return publicJSON, nil
}

// Reservation cleanup and settlement are durable state transitions. They must
// survive a caller disconnect, while retaining a short independent deadline
// so a failed database cannot strand a request goroutine indefinitely.
func (s *Service) releaseGatewayCommand(ctx context.Context, requestID, reason string) {
	if providerctx.IsRecoveryExecution(ctx) {
		// The original process may have died after the provider committed. A
		// replay-side error cannot prove that no external effect exists, so keep
		// the reservation and let the renewed lease expire for another durable
		// replay/reconciliation attempt.
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = s.store.ReleaseGatewayCommand(cleanup, requestID, reason)
}

func (s *Service) settleGatewayCommand(ctx context.Context, requestID, providerRequestID string, metrics []store.GatewayMetric, response []byte, completedAt string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.store.SettleGatewayCommand(cleanup, requestID, providerRequestID, metrics, response, completedAt)
}

func (s *Service) settleGatewayCommandForVariant(ctx context.Context, requestID, providerRequestID, variantID string, metrics []store.GatewayMetric, response []byte, completedAt string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.store.SettleGatewayCommandForVariant(cleanup, requestID, providerRequestID, variantID, metrics, response, completedAt)
}

func providerMessages(input []ChatMessage) ([]schemas.ChatMessage, error) {
	messages := make([]schemas.ChatMessage, 0, len(input))
	for _, message := range input {
		role, err := chatRole(message.Role)
		if err != nil {
			return nil, err
		}
		var content schemas.ChatMessageContent
		switch value := message.Content.(type) {
		case string:
			content.ContentStr = &value
		default:
			encoded, marshalErr := json.Marshal(value)
			if marshalErr != nil || json.Unmarshal(encoded, &content) != nil {
				return nil, errors.New("chat message content must be a string or valid multimodal content array")
			}
		}
		messages = append(messages, schemas.ChatMessage{Role: role, Content: &content})
	}
	return messages, nil
}

func reservationUpperBound(prices map[string]store.GatewayPrice, input, output int64) (int64, error) {
	inputReserve, err := inputTokenReservationUpperBound(prices, input)
	if err != nil {
		return 0, err
	}
	outputPrice, ok := prices["output_token"]
	if !ok {
		return 0, errors.New("missing active output_token price")
	}
	outputCharge, err := store.CheckedCharge(output, outputPrice.EffectivePrice, outputPrice.UnitSize)
	if err != nil {
		return 0, err
	}
	if outputCharge > math.MaxInt64-inputReserve {
		return 0, errors.New("reservation overflow")
	}
	total := inputReserve + outputCharge
	return total, nil
}

func realtimeReservationUpperBound(prices map[string]store.GatewayPrice, input, output int64) (int64, error) {
	// Audio tokens are subsets of the provider's aggregate prompt/completion
	// totals. Reserve the most expensive possible distribution on each side,
	// plus one integer ceiling allowance for every additional non-empty metric;
	// summing full text+audio maxima would reject customers who can afford every
	// valid request even though one token cannot occupy both categories.
	inputReserve, err := tokenDistributionUpperBound(prices, input, "input_token", "cached_input_token", "input_audio_token")
	if err != nil {
		return 0, err
	}
	outputReserve, err := tokenDistributionUpperBound(prices, output, "output_token", "output_audio_token")
	if err != nil {
		return 0, err
	}
	if outputReserve > math.MaxInt64-inputReserve {
		return 0, errors.New("reservation overflow")
	}
	return inputReserve + outputReserve, nil
}

func tokenDistributionUpperBound(prices map[string]store.GatewayPrice, quantity int64, metrics ...string) (int64, error) {
	var upper int64
	for _, metric := range metrics {
		price, ok := prices[metric]
		if !ok {
			return 0, fmt.Errorf("missing active %s price", metric)
		}
		charge, err := store.CheckedCharge(quantity, price.EffectivePrice, price.UnitSize)
		if err != nil {
			return 0, err
		}
		upper = max(upper, charge)
	}
	allowance := int64(len(metrics) - 1)
	if quantity > 1 && upper > 0 {
		if upper > math.MaxInt64-allowance {
			return 0, errors.New("reservation overflow")
		}
		upper += allowance
	}
	return upper, nil
}

func pricedMetrics(prices map[string]store.GatewayPrice, input, output int64) ([]store.GatewayMetric, error) {
	metrics := make([]store.GatewayMetric, 0, 2)
	for _, item := range []struct {
		name     string
		quantity int64
	}{{"input_token", input}, {"output_token", output}} {
		price, ok := prices[item.name]
		if !ok {
			return nil, fmt.Errorf("missing active %s price", item.name)
		}
		charge, err := store.CheckedCharge(item.quantity, price.EffectivePrice, price.UnitSize)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, store.GatewayMetric{Metric: item.name, Quantity: item.quantity, Price: price, Charge: charge})
	}
	return metrics, nil
}

func pricedRealtimeMetrics(prices map[string]store.GatewayPrice, input, output, cachedInput, inputAudio, outputAudio int64) ([]store.GatewayMetric, error) {
	// The supported provider callback contract reports cached and audio input as
	// disjoint subsets. Without that invariant a single aggregate callback
	// cannot reconstruct whether a cached audio token belongs to one or both
	// price buckets, so ambiguous usage is rejected rather than double-billed.
	if cachedInput < 0 || inputAudio < 0 || outputAudio < 0 || cachedInput+inputAudio > input || outputAudio > output {
		return nil, errors.New("provider Realtime usage subsets exceed aggregate token usage")
	}
	quantities := []struct {
		name     string
		quantity int64
	}{
		{"input_token", input - cachedInput - inputAudio},
		{"cached_input_token", cachedInput},
		{"input_audio_token", inputAudio},
		{"output_token", output - outputAudio},
		{"output_audio_token", outputAudio},
	}
	metrics := make([]store.GatewayMetric, 0, len(quantities))
	for _, item := range quantities {
		price, ok := prices[item.name]
		if !ok {
			return nil, fmt.Errorf("missing active %s price", item.name)
		}
		charge, err := store.CheckedCharge(item.quantity, price.EffectivePrice, price.UnitSize)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, store.GatewayMetric{Metric: item.name, Quantity: item.quantity, Price: price, Charge: charge})
	}
	return metrics, nil
}

func chatRole(value string) (schemas.ChatMessageRole, error) {
	switch value {
	case "system":
		return schemas.ChatMessageRoleSystem, nil
	case "user":
		return schemas.ChatMessageRoleUser, nil
	case "assistant":
		return schemas.ChatMessageRoleAssistant, nil
	default:
		return "", fmt.Errorf("unsupported chat role %q", value)
	}
}
