// Package gateway owns authorization-independent AI command orchestration,
// local quota admission, pricing, provider execution, and Usage measurement.
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

	"github.com/idy/gizway/internal/service/gatewayquota"
	"github.com/idy/gizway/internal/service/quotaexchange"
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
	regionalQuota          *gatewayquota.Runtime
	regionalMu             sync.Mutex
	regionalExecutions     map[string]regionalExecution
	regionalRealtimeSecret map[[32]byte]string
	regionalRealtime       map[string]store.RealtimeSession
	regionalRealtimeEvents map[string][32]byte
	realtimeCallbackSecret []byte
	realtimeCallbackURL    string
	realtimeCallbackMu     sync.RWMutex
	now                    func() time.Time
	realtimeSessionTimeout time.Duration
}

type regionalExecution struct {
	rawAPIKey     string
	committed     int64
	startedAt     string
	publicModel   string
	publicationID string
	variantID     string
	prices        map[string]store.GatewayPrice
}

const defaultRealtimeSessionTimeout = 10 * time.Minute

// ErrInvalidRequest marks client-controlled validation failures that are
// discovered by the catalog-aware service layer rather than the HTTP codec.
var (
	ErrInvalidRequest       = errors.New("invalid Gateway request")
	ErrInvalidProviderEvent = errors.New("invalid Realtime provider event")
	ErrQuotaDenied          = errors.New("GizPay denied current quota")
)

// CustomerCredential is the entire customer identity visible to GizWay.
// It deliberately has no Account ID or API Key ID; GizPay alone resolves the
// raw key while atomically processing Usage and calculating current quota.
type CustomerCredential struct {
	RawAPIKey string
}

// NewWithRealtimeProviderCallback configures the real provider-observable
// WebRTC settlement channel. The public callback URL and master key are never
// accepted from an API caller; a session-scoped token is derived below.
func NewWithRealtimeProviderCallback(repository *store.Store, executor Executor, callbackURL, callbackSecret string) *Service {
	service := newService(repository, executor)
	service.realtimeCallbackURL = strings.TrimRight(callbackURL, "/")
	service.realtimeCallbackSecret = []byte(callbackSecret)
	return service
}

func newService(repository *store.Store, executor Executor) *Service {
	return &Service{
		store: repository, executor: executor, now: time.Now,
		realtimeSessionTimeout: defaultRealtimeSessionTimeout,
		regionalExecutions:     make(map[string]regionalExecution),
		regionalRealtimeSecret: make(map[[32]byte]string),
		regionalRealtime:       make(map[string]store.RealtimeSession),
		regionalRealtimeEvents: make(map[string][32]byte),
	}
}

// ConfigureRegionalQuota supplies the Refactor 01 admission and Usage exchange
// runtime. Gateway execution has no central-database fallback.
func (s *Service) ConfigureRegionalQuota(runtime *gatewayquota.Runtime) {
	s.regionalQuota = runtime
}

// CheckQuota authorizes an unmetered compatible-protocol read without giving
// GizWay any customer identity projection. GizPay still validates the raw key
// and returns the current quota through the same Exchange contract.
func (s *Service) CheckQuota(ctx context.Context, credential CustomerCredential) error {
	if s.regionalQuota == nil {
		return errors.New("regional quota runtime is not configured")
	}
	allowed, err := s.regionalQuota.Check(ctx, credential.RawAPIKey)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrQuotaDenied
	}
	return nil
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
// deadlines still use the real monotonic clock; catalog validity and persisted
// execution timestamps use this clock.
func (s *Service) ConfigureClock(now func() time.Time) {
	if now != nil {
		s.now = now
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
func (s *Service) CreateRealtimeSession(ctx context.Context, principal CustomerCredential, request RealtimeRequest) (RealtimeClientSecret, error) {
	if request.Model == "" || (request.Transport != "websocket" && request.Transport != "webrtc") {
		return RealtimeClientSecret{}, errors.New("model and websocket/webrtc transport are required")
	}
	if request.Transport == "webrtc" && (s.realtimeProviderCallbackURL() == "" || len(s.realtimeCallbackSecret) == 0) {
		return RealtimeClientSecret{}, errors.New("WebRTC provider settlement callback is not configured")
	}
	if s.regionalQuota == nil {
		return RealtimeClientSecret{}, errors.New("regional quota runtime is not configured")
	}
	return s.createRegionalRealtimeSession(ctx, principal, request)
}

func (s *Service) createRegionalRealtimeSession(ctx context.Context, principal CustomerCredential, request RealtimeRequest) (RealtimeClientSecret, error) {
	if principal.RawAPIKey == "" {
		return RealtimeClientSecret{}, store.ErrNotFound
	}
	now := s.now().UTC()
	candidates, err := s.store.ResolveRegionalGatewayCandidates(ctx, request.Model, "realtime", timetext.Format(now))
	if err != nil {
		return RealtimeClientSecret{}, err
	}
	candidate := candidates[0]
	if request.Transport == "webrtc" {
		var capabilities map[string]bool
		if json.Unmarshal(candidate.Capabilities, &capabilities) != nil || !capabilities["realtime_webrtc_callback"] {
			return RealtimeClientSecret{}, errors.New("model provider does not declare durable WebRTC callback support")
		}
	}
	commitment, err := realtimeCommitmentUpperBound(candidate.Prices, candidate.ContextWindow, candidate.MaxOutputTokens)
	if err != nil {
		return RealtimeClientSecret{}, err
	}
	var allowed bool
	if commitment == 0 {
		// A complete zero-price publication still requires a valid, funded Quota
		// answer, but it must not manufacture a one-microcredit charge merely to
		// pass through the positive-commitment local admission primitive.
		allowed, err = s.regionalQuota.Check(ctx, principal.RawAPIKey)
	} else {
		allowed, err = s.regionalQuota.Admit(ctx, principal.RawAPIKey, commitment)
	}
	if err != nil {
		return RealtimeClientSecret{}, err
	}
	if !allowed {
		return RealtimeClientSecret{}, ErrQuotaDenied
	}
	sessionID := uuid.NewString()
	startedAt := timetext.Format(now)
	if err := s.store.BeginRegionalExecution(ctx, sessionID, candidate.PublicModel, candidate.VariantID,
		candidate.LocalRatePublicationID, request.Transport, "realtime", commitment, startedAt); err != nil {
		s.regionalQuota.Release(principal.RawAPIKey, commitment)
		return RealtimeClientSecret{}, err
	}
	rawSecret := make([]byte, 32)
	if _, err := rand.Read(rawSecret); err != nil {
		s.regionalQuota.Release(principal.RawAPIKey, commitment)
		_ = s.store.FailRegionalExecution(context.WithoutCancel(ctx), sessionID, timetext.Format(s.now()))
		return RealtimeClientSecret{}, err
	}
	secret := "gizrt_" + base64.RawURLEncoding.EncodeToString(rawSecret)
	secretHash := sha256.Sum256([]byte(secret))
	session := store.RealtimeSession{
		ID: sessionID, GatewayRequestID: sessionID, ModelID: candidate.ModelID,
		VariantID: candidate.VariantID, PublicModel: candidate.PublicModel,
		ProviderModel: candidate.ProviderModel, Transport: request.Transport,
		Status: "created", ExpiresAt: timetext.Format(now.Add(2 * time.Minute)),
		DeadlineAt: timetext.Format(now.Add(2*time.Minute + s.realtimeSessionTimeout)), CreatedAt: startedAt,
	}
	s.regionalMu.Lock()
	s.regionalExecutions[sessionID] = regionalExecution{
		rawAPIKey: principal.RawAPIKey, committed: commitment, startedAt: startedAt,
		publicModel: candidate.PublicModel, publicationID: candidate.RatePublicationID, variantID: candidate.VariantID,
		prices: candidate.Prices,
	}
	s.regionalRealtimeSecret[secretHash] = sessionID
	s.regionalRealtime[sessionID] = session
	s.regionalMu.Unlock()
	return RealtimeClientSecret{Session: session, ClientSecret: secret}, nil
}

func (s *Service) ConnectRealtimeSession(ctx context.Context, secret, transport string) (store.RealtimeSession, error) {
	hash := sha256.Sum256([]byte(secret))
	now := s.now().UTC()
	if s.regionalQuota == nil {
		return store.RealtimeSession{}, errors.New("regional quota runtime is not configured")
	}
	s.regionalMu.Lock()
	defer s.regionalMu.Unlock()
	sessionID, ok := s.regionalRealtimeSecret[hash]
	if !ok {
		return store.RealtimeSession{}, store.ErrNotFound
	}
	session := s.regionalRealtime[sessionID]
	expires, err := timetext.Parse(session.ExpiresAt)
	if err != nil || session.Status != "created" || session.Transport != transport || !now.Before(expires) {
		return store.RealtimeSession{}, store.ErrNotFound
	}
	delete(s.regionalRealtimeSecret, hash)
	session.Status = "connected"
	connectedAt := timetext.Format(now)
	session.ConnectedAt = &connectedAt
	session.DeadlineAt = timetext.Format(now.Add(s.realtimeSessionTimeout))
	s.regionalRealtime[sessionID] = session
	return session, nil
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
		s.releaseGatewayCommand(ctx, session.GatewayRequestID, "provider_error")
		return err
	}
	upstream, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: headers})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		s.releaseGatewayCommand(ctx, session.GatewayRequestID, "provider_error")
		return err
	}
	defer upstream.Close(websocket.StatusNormalClosure, "session complete")
	deadline, err := timetext.Parse(session.DeadlineAt)
	if err != nil {
		s.releaseGatewayCommand(ctx, session.GatewayRequestID, "invalid_session_deadline")
		return err
	}
	// Use the exact deadline committed by ConnectRealtimeSession. The durable
	// sweeper and this live socket therefore cannot disagree after a late
	// connection consumes an almost-expired client secret.
	remaining := deadline.Sub(s.now())
	if remaining <= 0 {
		s.releaseGatewayCommand(ctx, session.GatewayRequestID, "session_timeout")
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
			// its process-local Quota commitment indefinitely.
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
			s.releaseGatewayCommand(ctx, session.GatewayRequestID, "client_disconnect")
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
			s.releaseGatewayCommand(ctx, session.GatewayRequestID, releaseCode)
			return err
		}
		public, usage, terminal, err := s.executor.RealtimeProviderEvent(proxyCtx, target, raw)
		if err != nil {
			s.releaseGatewayCommand(ctx, session.GatewayRequestID, "invalid_provider_event")
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
			s.releaseGatewayCommand(ctx, session.GatewayRequestID, "client_disconnect")
			return err
		}
	}
}

func (s *Service) settleRealtime(ctx context.Context, session store.RealtimeSession, inputTokens, outputTokens, cachedInputTokens, inputAudioTokens, outputAudioTokens int64) error {
	// Realtime may outlive a pricing edit. Its metering uses the immutable
	// publication captured at admission, never mutable draft prices.
	s.regionalMu.Lock()
	execution, ok := s.regionalExecutions[session.GatewayRequestID]
	s.regionalMu.Unlock()
	if !ok || len(execution.prices) == 0 {
		return errors.New("regional Realtime pricing snapshot is unavailable")
	}
	prices := execution.prices
	metrics, err := pricedRealtimeMetrics(prices, inputTokens, outputTokens, cachedInputTokens, inputAudioTokens, outputAudioTokens)
	if err != nil {
		return err
	}
	return s.settleGatewayCommand(ctx, session.GatewayRequestID, session.ID, metrics, timetext.Format(s.now()))
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
		s.releaseGatewayCommand(ctx, session.GatewayRequestID, "provider_error")
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
	if s.regionalQuota == nil {
		return store.RealtimeSession{}, false, errors.New("regional quota runtime is not configured")
	}
	return s.completeRegionalRealtimeProviderEvent(ctx, event, raw, signature)
}

func (s *Service) completeRegionalRealtimeProviderEvent(ctx context.Context, event RealtimeProviderEvent, raw []byte, signature string) (store.RealtimeSession, bool, error) {
	s.regionalMu.Lock()
	session, ok := s.regionalRealtime[event.SessionID]
	existingHash, replayed := s.regionalRealtimeEvents[event.EventID]
	s.regionalMu.Unlock()
	if !ok || session.Transport != "webrtc" {
		return store.RealtimeSession{}, false, ErrInvalidProviderEvent
	}
	payloadHash := sha256.Sum256(raw)
	if replayed {
		if existingHash != payloadHash {
			return store.RealtimeSession{}, false, ErrInvalidProviderEvent
		}
		return session, true, nil
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signature, "v1="))
	if err != nil {
		return store.RealtimeSession{}, false, ErrInvalidProviderEvent
	}
	mac := hmac.New(sha256.New, []byte(s.realtimeSessionCallbackToken(session)))
	_, _ = mac.Write(raw)
	if !hmac.Equal(provided, mac.Sum(nil)) || session.Status != "connected" {
		return store.RealtimeSession{}, false, ErrInvalidProviderEvent
	}
	if err := s.settleRealtime(ctx, session, event.InputTokens, event.OutputTokens, event.CachedInputTokens, event.InputAudioTokens, event.OutputAudioTokens); err != nil {
		return store.RealtimeSession{}, false, err
	}
	s.regionalMu.Lock()
	s.regionalRealtimeEvents[event.EventID] = payloadHash
	session = s.regionalRealtime[event.SessionID]
	s.regionalMu.Unlock()
	return session, false, nil
}

func (s *Service) realtimeSessionCallbackToken(session store.RealtimeSession) string {
	mac := hmac.New(sha256.New, s.realtimeCallbackSecret)
	_, _ = mac.Write([]byte(session.VariantID))
	_, _ = mac.Write([]byte("\n"))
	_, _ = mac.Write([]byte(session.ID))
	return hex.EncodeToString(mac.Sum(nil))
}

type gatewayExecutionPlan struct {
	requestID  string
	candidates []store.GatewayCandidate
}

// beginGatewayExecution admits a regional provider execution against the
// current in-memory Quota window. Refactor 01 intentionally has no central
// reservation, response replay, or cross-restart recovery path.
func (s *Service) beginGatewayExecution(
	ctx context.Context,
	principal CustomerCredential,
	operation, publicModel string,
	now time.Time,
	requestedMaxOutput int64,
	reserveFor func(store.GatewayCandidate) (int64, error),
) (gatewayExecutionPlan, error) {
	if s.regionalQuota == nil {
		return gatewayExecutionPlan{}, errors.New("regional quota runtime is not configured")
	}
	return s.beginRegionalGatewayExecution(ctx, principal, operation, publicModel, now, requestedMaxOutput, reserveFor)
}

func (s *Service) beginRegionalGatewayExecution(
	ctx context.Context,
	principal CustomerCredential,
	operation, publicModel string,
	now time.Time,
	requestedMaxOutput int64,
	reserveFor func(store.GatewayCandidate) (int64, error),
) (gatewayExecutionPlan, error) {
	if principal.RawAPIKey == "" {
		return gatewayExecutionPlan{}, store.ErrNotFound
	}
	candidates, err := s.store.ResolveRegionalGatewayCandidates(ctx, publicModel, operation, timetext.Format(now))
	if err != nil {
		return gatewayExecutionPlan{}, err
	}
	candidates, err = candidatesWithinOutputLimit(candidates, requestedMaxOutput)
	if err != nil {
		return gatewayExecutionPlan{}, err
	}
	var commitment int64
	for _, candidate := range candidates {
		candidateCommitment, err := reserveFor(candidate)
		if err != nil {
			return gatewayExecutionPlan{}, err
		}
		if candidateCommitment > commitment {
			commitment = candidateCommitment
		}
	}
	var allowed bool
	if commitment == 0 {
		// A complete zero-price publication still requires a valid, funded Quota
		// answer, but it must not manufacture a one-microcredit charge merely to
		// pass through the positive-commitment local admission primitive.
		allowed, err = s.regionalQuota.Check(ctx, principal.RawAPIKey)
	} else {
		allowed, err = s.regionalQuota.Admit(ctx, principal.RawAPIKey, commitment)
	}
	if err != nil {
		return gatewayExecutionPlan{}, err
	}
	if !allowed {
		return gatewayExecutionPlan{}, ErrQuotaDenied
	}
	requestID := uuid.NewString()
	primary := candidates[0]
	if err := s.store.BeginRegionalExecution(ctx, requestID, primary.PublicModel, primary.VariantID,
		primary.LocalRatePublicationID, "https", "buffered", commitment, timetext.Format(now)); err != nil {
		s.regionalQuota.Release(principal.RawAPIKey, commitment)
		return gatewayExecutionPlan{}, err
	}
	s.regionalMu.Lock()
	s.regionalExecutions[requestID] = regionalExecution{
		rawAPIKey: principal.RawAPIKey, committed: commitment, startedAt: timetext.Format(now),
		publicModel: primary.PublicModel, publicationID: primary.RatePublicationID, variantID: primary.VariantID,
		prices: primary.Prices,
	}
	s.regionalMu.Unlock()
	return gatewayExecutionPlan{requestID: requestID, candidates: candidates}, nil
}

// StreamChat executes a provider SSE stream, forwards canonical chunks through
// emit, and settles exactly once from the provider's terminal usage record.
func (s *Service) StreamChat(ctx context.Context, principal CustomerCredential, request ChatRequest, emit func([]byte) error) error {
	if s.executor == nil {
		return errors.New("AI executor is not configured")
	}
	now := s.now().UTC()
	maxOutput := int64(0)
	if request.MaxTokens != nil {
		if *request.MaxTokens <= 0 {
			return errors.New("max_tokens must be positive")
		}
		maxOutput = int64(*request.MaxTokens)
	}
	plan, err := s.beginGatewayExecution(ctx, principal, "chat.completions", request.Model, now, maxOutput, func(allowed store.GatewayCandidate) (int64, error) {
		outputLimit := maxOutput
		if outputLimit == 0 {
			outputLimit = allowed.MaxOutputTokens
		}
		return quotaCommitmentUpperBound(allowed.Prices, allowed.ContextWindow, outputLimit)
	})
	if err != nil {
		return err
	}
	requestID := plan.requestID
	candidates := plan.candidates
	candidate := candidates[0]
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
	if err := s.settleGatewayCommandForVariant(ctx, requestID, providerRequestID, resolved.VariantID, metrics, timetext.Format(s.now())); err != nil {
		return err
	}
	if clientWriteErr != nil {
		return clientWriteErr
	}
	return nil
}

// Chat executes one provider request and returns its public response directly.
func (s *Service) Chat(ctx context.Context, principal CustomerCredential, request ChatRequest) ([]byte, error) {
	if s.executor == nil {
		return nil, errors.New("AI executor is not configured")
	}
	now := s.now().UTC()
	maxOutput := int64(0)
	if request.MaxTokens != nil {
		if *request.MaxTokens <= 0 {
			return nil, fmt.Errorf("%w: max_tokens must be positive", ErrInvalidRequest)
		}
		maxOutput = int64(*request.MaxTokens)
	}
	plan, err := s.beginGatewayExecution(ctx, principal, "chat.completions", request.Model, now, maxOutput, func(allowed store.GatewayCandidate) (int64, error) {
		outputLimit := maxOutput
		if outputLimit == 0 {
			outputLimit = allowed.MaxOutputTokens
		}
		return quotaCommitmentUpperBound(allowed.Prices, allowed.ContextWindow, outputLimit)
	})
	if err != nil {
		return nil, err
	}
	requestID := plan.requestID
	candidates := plan.candidates
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
	if err := s.settleGatewayCommandForVariant(ctx, requestID, response.ID, resolved.VariantID, metrics, completed); err != nil {
		return nil, err
	}
	return publicJSON, nil
}

// Regional cleanup releases the in-memory commitment and marks the local
// identity-free execution failed.
func (s *Service) releaseGatewayCommand(ctx context.Context, requestID, reason string) {
	s.regionalMu.Lock()
	execution, ok := s.regionalExecutions[requestID]
	if ok {
		delete(s.regionalExecutions, requestID)
		if session, exists := s.regionalRealtime[requestID]; exists {
			completedAt := timetext.Format(s.now())
			session.Status = "failed"
			session.CompletedAt = &completedAt
			s.regionalRealtime[requestID] = session
		}
	}
	s.regionalMu.Unlock()
	if !ok {
		return
	}
	s.regionalQuota.Release(execution.rawAPIKey, execution.committed)
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = s.store.FailRegionalExecution(cleanup, requestID, timetext.Format(s.now()))
}

// ReleaseExecution is the transport-facing cleanup boundary for Realtime
// handshake and connection failures. The service, rather than the HTTP layer,
// releases its local Quota commitment.
func (s *Service) ReleaseExecution(ctx context.Context, requestID, reason string) {
	s.releaseGatewayCommand(ctx, requestID, reason)
}

func (s *Service) settleGatewayCommand(ctx context.Context, requestID, providerRequestID string, metrics []store.GatewayMetric, completedAt string) error {
	return s.completeRegionalGatewayExecution(ctx, requestID, providerRequestID, "", metrics, completedAt)
}

func (s *Service) settleGatewayCommandForVariant(ctx context.Context, requestID, providerRequestID, variantID string, metrics []store.GatewayMetric, completedAt string) error {
	return s.completeRegionalGatewayExecution(ctx, requestID, providerRequestID, variantID, metrics, completedAt)
}

func (s *Service) completeRegionalGatewayExecution(ctx context.Context, requestID, providerRequestID, variantID string, metrics []store.GatewayMetric, completedAt string) error {
	s.regionalMu.Lock()
	execution, ok := s.regionalExecutions[requestID]
	s.regionalMu.Unlock()
	if !ok {
		return errors.New("regional execution context is unavailable")
	}
	if variantID == "" {
		variantID = execution.variantID
	}
	if variantID == "" {
		return errors.New("regional execution winner is unavailable")
	}
	quantities := make(map[string]int64, len(metrics))
	var actual int64
	for _, metric := range metrics {
		if metric.Charge > math.MaxInt64-actual {
			return errors.New("regional Usage charge overflow")
		}
		actual += metric.Charge
		quantities[metric.Metric] = metric.Quantity
	}
	usage := quotaexchange.UsageRecord{
		UCGID: "ucg_" + uuid.NewString(), OperationID: requestID,
		PublicModel: execution.publicModel, ModelVariantID: variantID,
		RatePublicationID: execution.publicationID, Metrics: quantities,
		StartedAt: execution.startedAt, CompletedAt: completedAt,
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.regionalQuota.Complete(cleanup, execution.rawAPIKey, execution.committed, actual, providerRequestID, usage, metrics); err != nil {
		return err
	}
	s.regionalMu.Lock()
	delete(s.regionalExecutions, requestID)
	if session, exists := s.regionalRealtime[requestID]; exists {
		session.Status = "succeeded"
		session.CompletedAt = &completedAt
		s.regionalRealtime[requestID] = session
	}
	s.regionalMu.Unlock()
	return nil
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

func quotaCommitmentUpperBound(prices map[string]store.GatewayPrice, input, output int64) (int64, error) {
	inputCommitment, err := inputTokenCommitmentUpperBound(prices, input)
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
	if outputCharge > math.MaxInt64-inputCommitment {
		return 0, errors.New("quota commitment overflow")
	}
	total := inputCommitment + outputCharge
	return total, nil
}

func realtimeCommitmentUpperBound(prices map[string]store.GatewayPrice, input, output int64) (int64, error) {
	// Audio tokens are subsets of the provider's aggregate prompt/completion
	// totals. Reserve the most expensive possible distribution on each side,
	// plus one integer ceiling allowance for every additional non-empty metric;
	// summing full text+audio maxima would reject customers who can afford every
	// valid request even though one token cannot occupy both categories.
	inputCommitment, err := tokenDistributionUpperBound(prices, input, "input_token", "cached_input_token", "input_audio_token")
	if err != nil {
		return 0, err
	}
	outputCommitment, err := tokenDistributionUpperBound(prices, output, "output_token", "output_audio_token")
	if err != nil {
		return 0, err
	}
	if outputCommitment > math.MaxInt64-inputCommitment {
		return 0, errors.New("quota commitment overflow")
	}
	return inputCommitment + outputCommitment, nil
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
			return 0, errors.New("quota commitment overflow")
		}
		upper += allowance
	}
	return upper, nil
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
