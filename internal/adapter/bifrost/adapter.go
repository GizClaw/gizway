// Package bifrost owns the lifecycle and protocol boundary to the pinned
// Bifrost execution engine. It has no access to Gizway identity or ledger data.
package bifrost

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	bf "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/GizClaw/gizway/internal/store"
)

func bifrostContext(ctx context.Context, deadline time.Time) *schemas.BifrostContext {
	return schemas.NewBifrostContext(ctx, deadline)
}

func (a *Adapter) requestContext(ctx context.Context) *schemas.BifrostContext {
	deadline := time.Now().Add(a.requestTimeout)
	if parentDeadline, ok := ctx.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	return bifrostContext(ctx, deadline)
}

func (a *Adapter) requestContextWithHeaders(ctx context.Context, headers map[string][]string) *schemas.BifrostContext {
	bfctx := a.requestContext(ctx)
	// GizWay populates ExtraParams only from explicitly allowlisted public
	// fields. Bifrost currently rebuilds OpenAI stream_options internally, so
	// the controlled extra-parameter merge preserves options it does not copy.
	bfctx.SetValue(schemas.BifrostContextKeyPassthroughExtraParams, true)
	if len(headers) != 0 {
		bfctx.SetValue(schemas.BifrostContextKeyPassthroughHeaders, headers)
	}
	return bfctx
}

// ExecutionError retains the private Bifrost routing identity for a failed
// Provider attempt so GizWay can write an accurate execution log without
// exposing the Provider Key to the public response.
type ExecutionError struct {
	SelectedKeyID string
	Cause         error
}

func (e *ExecutionError) Error() string { return e.Cause.Error() }
func (e *ExecutionError) Unwrap() error { return e.Cause }

// SelectedKeyID returns the Bifrost-selected Provider Key carried by an error.
func SelectedKeyID(err error) string {
	if executionError, ok := errors.AsType[*ExecutionError](err); ok {
		return executionError.SelectedKeyID
	}
	return ""
}

type account struct {
	providers []schemas.ModelProvider
	keys      map[schemas.ModelProvider][]schemas.Key
	configs   map[schemas.ModelProvider]*schemas.ProviderConfig
}

func (a *Adapter) realtimeProvider(ctx context.Context, target store.ProviderExecutionTarget) (schemas.RealtimeProvider, schemas.Key, *schemas.BifrostContext, func(), error) {
	client, target, release, err := a.clientFor(ctx, target)
	if err != nil {
		return nil, schemas.Key{}, nil, nil, err
	}
	bfctx := a.requestContext(ctx)
	key, err := client.SelectKeyForProviderRequestType(bfctx, schemas.RealtimeRequest, schemas.OpenAI, target.Model)
	if err != nil {
		bfctx.Cancel()
		release()
		return nil, schemas.Key{}, nil, nil, fmt.Errorf("select Bifrost Realtime key: %w", err)
	}
	provider, ok := client.GetProviderByKey(schemas.OpenAI).(schemas.RealtimeProvider)
	if !ok || !provider.SupportsRealtimeAPI() {
		bfctx.Cancel()
		release()
		return nil, schemas.Key{}, nil, nil, fmt.Errorf("bifrost OpenAI provider does not support realtime")
	}
	return provider, key, bfctx, release, nil
}

// RealtimeWebSocketRoute resolves the provider-owned WebSocket URL and
// authorization headers through the pinned Bifrost provider implementation.
func (a *Adapter) RealtimeWebSocketRoute(ctx context.Context, target store.ProviderExecutionTarget) (string, http.Header, error) {
	provider, key, bfctx, release, err := a.realtimeProvider(ctx, target)
	if err != nil {
		return "", nil, err
	}
	defer release()
	defer bfctx.Cancel()
	headers, bfErr := provider.RealtimeHeaders(bfctx, key)
	if bfErr != nil {
		return "", nil, fmt.Errorf("bifrost realtime headers: %s", bfErr.Error.Message)
	}
	httpHeaders := make(http.Header, len(headers))
	for name, value := range headers {
		httpHeaders.Set(name, value)
	}
	return provider.RealtimeWebSocketURL(key, target.Model), httpHeaders, nil
}

// RealtimeWebSocketRouteCandidates lets Bifrost select one key from the
// Model's unique Provider pool and returns the stable selected key ID needed by
// GizWay's payment mapping.
func (a *Adapter) RealtimeWebSocketRouteCandidates(ctx context.Context, targets []store.ProviderExecutionTarget) (string, http.Header, string, error) {
	client, targets, release, err := a.clientForCandidates(ctx, targets)
	if err != nil {
		return "", nil, "", err
	}
	defer release()
	bfctx := a.requestContext(ctx)
	defer bfctx.Cancel()
	key, err := client.SelectKeyForProviderRequestType(bfctx, schemas.RealtimeRequest, schemas.OpenAI, targets[0].Model)
	if err != nil {
		return "", nil, "", fmt.Errorf("select Bifrost Realtime key: %w", err)
	}
	provider, ok := client.GetProviderByKey(schemas.OpenAI).(schemas.RealtimeProvider)
	if !ok || !provider.SupportsRealtimeAPI() {
		return "", nil, "", errors.New("bifrost provider does not support realtime")
	}
	headers, bfErr := provider.RealtimeHeaders(bfctx, key)
	if bfErr != nil {
		return "", nil, "", fmt.Errorf("bifrost realtime headers: %s", bfErr.Error.Message)
	}
	httpHeaders := make(http.Header, len(headers))
	for name, value := range headers {
		httpHeaders.Set(name, value)
	}
	return provider.RealtimeWebSocketURL(key, targets[0].Model), httpHeaders, key.ID, nil
}

// RealtimeClientEvent validates and translates one client event with the
// Bifrost canonical Realtime codec before it reaches the provider.
func (a *Adapter) RealtimeClientEvent(ctx context.Context, target store.ProviderExecutionTarget, raw []byte) ([]byte, error) {
	provider, _, bfctx, release, err := a.realtimeProvider(ctx, target)
	if err != nil {
		return nil, err
	}
	defer release()
	defer bfctx.Cancel()
	event, err := schemas.ParseRealtimeEvent(raw)
	if err != nil {
		return nil, fmt.Errorf("parse Realtime client event: %w", err)
	}
	translated, err := provider.ToProviderRealtimeEvent(event)
	if err != nil {
		return nil, fmt.Errorf("translate Realtime client event: %w", err)
	}
	return translated, nil
}

// RealtimeProviderEvent validates a provider event and extracts terminal usage
// through Bifrost. The original provider-compatible bytes remain public.
func (a *Adapter) RealtimeProviderEvent(ctx context.Context, target store.ProviderExecutionTarget, raw []byte) ([]byte, *schemas.BifrostLLMUsage, bool, error) {
	provider, _, bfctx, release, err := a.realtimeProvider(ctx, target)
	if err != nil {
		return nil, nil, false, err
	}
	defer release()
	defer bfctx.Cancel()
	event, err := provider.ToBifrostRealtimeEvent(json.RawMessage(raw))
	if err != nil {
		return nil, nil, false, fmt.Errorf("parse Realtime provider event: %w", err)
	}
	terminal := event.Type == provider.RealtimeTurnFinalEvent()
	var usage *schemas.BifrostLLMUsage
	if terminal {
		if extractor, ok := provider.(schemas.RealtimeUsageExtractor); ok {
			usage = extractor.ExtractRealtimeTurnUsage(raw)
		}
	}
	return append([]byte(nil), raw...), usage, terminal, nil
}

// ExchangeRealtimeWebRTCSDP delegates provider-specific multipart signaling to
// Bifrost, keeping credentials and upstream wire details out of the API layer.
func (a *Adapter) ExchangeRealtimeWebRTCSDP(ctx context.Context, target store.ProviderExecutionTarget, sdp string, session json.RawMessage) (string, error) {
	provider, key, bfctx, release, err := a.realtimeProvider(ctx, target)
	if err != nil {
		return "", err
	}
	defer release()
	defer bfctx.Cancel()
	if !provider.SupportsRealtimeWebRTC() {
		return "", errors.New("provider does not support WebRTC")
	}
	answer, bfErr := provider.ExchangeRealtimeWebRTCSDP(bfctx, key, target.Model, sdp, session)
	if bfErr != nil {
		return "", fmt.Errorf("bifrost WebRTC SDP exchange: %s", bfErr.Error.Message)
	}
	return answer, nil
}

func (a *account) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return append([]schemas.ModelProvider(nil), a.providers...), nil
}

func (a *account) GetKeysForProvider(_ context.Context, provider schemas.ModelProvider) ([]schemas.Key, error) {
	keys, ok := a.keys[provider]
	if !ok {
		return nil, fmt.Errorf("unsupported provider %q", provider)
	}
	return append([]schemas.Key(nil), keys...), nil
}

func (a *account) GetConfigForProvider(provider schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	config, ok := a.configs[provider]
	if !ok {
		return nil, fmt.Errorf("unsupported provider %q", provider)
	}
	copy := *config
	return &copy, nil
}

// Adapter executes already-authorized requests through Bifrost. Clients are
// cached per endpoint+credential pair so database catalog changes drive real
// routing without rebuilding Bifrost on every request.
type Adapter struct {
	mu              sync.Mutex
	defaultTarget   store.ProviderExecutionTarget
	clients         map[string]cachedClient
	clientClock     uint64
	maxRetries      int
	requestTimeout  time.Duration
	providerHeaders map[string]string
}

type cachedClient struct {
	client   *bf.Bifrost
	lastUsed uint64
	pinned   bool
	active   int
}

const maxCachedBifrostClients = 32

// NewOpenAI constructs the pinned OpenAI execution slice.
func NewOpenAI(ctx context.Context, baseURL, credential string) (*Adapter, error) {
	target := store.ProviderExecutionTarget{Endpoint: baseURL, Credential: credential}
	client, err := newOpenAIClient(ctx, target, 1, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return &Adapter{defaultTarget: target, clientClock: 1, maxRetries: 1, requestTimeout: 10 * time.Second, clients: map[string]cachedClient{targetKey(target): {client: client, lastUsed: 1, pinned: true}}}, nil
}

// NewLazy creates an execution engine with no process-global provider. The
// first authorized database candidate initializes its own cached Bifrost
// client, making provider_endpoints and encrypted credentials the production
// source of truth.
func NewLazy() *Adapter {
	return NewLazyWithExecution(1, 10*time.Second)
}

// NewLazyWithExecution applies the configured retry and request timeout to
// every Bifrost client created from database candidates.
func NewLazyWithExecution(maxRetries int, requestTimeout time.Duration) *Adapter {
	if maxRetries < 0 {
		maxRetries = 0
	}
	if requestTimeout <= 0 {
		requestTimeout = 10 * time.Second
	}
	return &Adapter{clients: make(map[string]cachedClient), maxRetries: maxRetries, requestTimeout: requestTimeout}
}

// ConfigureProviderCallbacks attaches the configured public callback identity
// to subsequent Provider requests without exposing the callback Secret.
func (a *Adapter) ConfigureProviderCallbacks(publicBaseURL string, secret []byte) {
	a.providerHeaders = nil
	if publicBaseURL == "" || len(secret) == 0 {
		return
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(publicBaseURL))
	a.providerHeaders = map[string]string{
		"X-Gizway-Public-Base-URL":    publicBaseURL,
		"X-Gizway-Callback-Signature": "v1=" + hex.EncodeToString(mac.Sum(nil)),
	}
}

func newOpenAIClient(ctx context.Context, target store.ProviderExecutionTarget, maxRetries int, requestTimeout time.Duration) (*bf.Bifrost, error) {
	target.Provider = string(schemas.OpenAI)
	return newProviderClient(ctx, target, maxRetries, requestTimeout)
}

func providerForTarget(target store.ProviderExecutionTarget) (schemas.ModelProvider, error) {
	provider := schemas.ModelProvider(target.Provider)
	if provider == "" {
		provider = schemas.OpenAI
	}
	switch provider {
	case schemas.OpenAI, schemas.Anthropic, schemas.Gemini:
		return provider, nil
	default:
		return "", fmt.Errorf("unsupported Milestone 03 Provider kind %q", target.Provider)
	}
}

func newProviderClient(ctx context.Context, target store.ProviderExecutionTarget, maxRetries int, requestTimeout time.Duration, extraHeaders ...map[string]string) (*bf.Bifrost, error) {
	provider, err := providerForTarget(target)
	if err != nil {
		return nil, err
	}
	enabled := true
	config := &schemas.ProviderConfig{NetworkConfig: schemas.NetworkConfig{
		BaseURL: target.Endpoint, DefaultRequestTimeoutInSeconds: max(1, int(requestTimeout.Seconds())),
		MaxRetries: maxRetries, AllowPrivateNetwork: true,
	}}
	if len(extraHeaders) != 0 {
		config.NetworkConfig.ExtraHeaders = extraHeaders[0]
	}
	client, err := bf.Init(ctx, schemas.BifrostConfig{
		Account: &account{providers: []schemas.ModelProvider{provider}, keys: map[schemas.ModelProvider][]schemas.Key{provider: {{
			ID: "gizway-openai", Name: "Gizway OpenAI endpoint",
			Value: *schemas.NewSecretVar(target.Credential), Models: schemas.WhiteList{"*"},
			Weight: 1, Enabled: &enabled,
		}}}, configs: map[schemas.ModelProvider]*schemas.ProviderConfig{provider: config}},
		Logger: bf.NewNoOpLogger(), InitialPoolSize: 8,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Bifrost: %w", err)
	}
	return client, nil
}

func normalizeTarget(target, fallback store.ProviderExecutionTarget) store.ProviderExecutionTarget {
	if target.Credential == "" {
		target.Endpoint, target.Credential = fallback.Endpoint, fallback.Credential
	} else if target.Endpoint == "" {
		target.Endpoint = fallback.Endpoint
	}
	return target
}

// newCandidateClient represents all active keys of one logical Provider as one
// native Bifrost key pool. Mixed endpoints or models are rejected so rotation
// cannot silently become cross-Provider or cross-Model fallback.
func newCandidateClient(ctx context.Context, targets []store.ProviderExecutionTarget, maxRetries int, requestTimeout time.Duration, extraHeaders ...map[string]string) (*bf.Bifrost, []store.ProviderExecutionTarget, error) {
	if len(targets) == 0 {
		return nil, nil, errors.New("no provider execution candidates")
	}
	provider, err := providerForTarget(targets[0])
	if err != nil {
		return nil, nil, err
	}
	keys := make([]schemas.Key, 0, len(targets))
	enabled := true
	for index, target := range targets {
		candidateProvider, providerErr := providerForTarget(target)
		if providerErr != nil {
			return nil, nil, providerErr
		}
		if candidateProvider != provider || target.Endpoint != targets[0].Endpoint || target.Model != targets[0].Model {
			return nil, nil, errors.New("bifrost key pool cannot cross Provider endpoint or Model")
		}
		keyID := target.RouteKey
		if keyID == "" {
			keyID = fmt.Sprintf("gizway-key-%d", index)
		}
		weight := target.Weight
		if weight <= 0 {
			weight = 1
		}
		keys = append(keys, schemas.Key{
			ID: keyID, Name: keyID,
			Value: *schemas.NewSecretVar(target.Credential), Models: schemas.WhiteList{"*"},
			Weight: float64(weight), Enabled: &enabled,
		})
	}
	config := &schemas.ProviderConfig{NetworkConfig: schemas.NetworkConfig{
		BaseURL: targets[0].Endpoint, DefaultRequestTimeoutInSeconds: max(1, int(requestTimeout.Seconds())),
		MaxRetries: maxRetries, AllowPrivateNetwork: true,
	}}
	if len(extraHeaders) != 0 {
		config.NetworkConfig.ExtraHeaders = extraHeaders[0]
	}
	client, err := bf.Init(ctx, schemas.BifrostConfig{
		Account: &account{
			providers: []schemas.ModelProvider{provider},
			keys:      map[schemas.ModelProvider][]schemas.Key{provider: keys},
			configs:   map[schemas.ModelProvider]*schemas.ProviderConfig{provider: config},
		},
		Logger: bf.NewNoOpLogger(), InitialPoolSize: 8,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("initialize Bifrost candidates: %w", err)
	}
	return client, targets, nil
}

func targetKey(target store.ProviderExecutionTarget) string {
	hash := sha256.Sum256([]byte(target.Credential))
	return target.Provider + "\x00" + target.Endpoint + "\x00" + hex.EncodeToString(hash[:])
}

func targetsKey(targets []store.ProviderExecutionTarget) string {
	hash := sha256.New()
	for _, target := range targets {
		_, _ = hash.Write([]byte(target.Provider))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(target.Endpoint))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(target.Credential))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(target.Model))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(target.RouteKey))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(fmt.Sprintf("%d", target.Weight)))
		_, _ = hash.Write([]byte{0})
	}
	return "candidates:" + hex.EncodeToString(hash.Sum(nil))
}

func (a *Adapter) clientFor(ctx context.Context, target store.ProviderExecutionTarget) (*bf.Bifrost, store.ProviderExecutionTarget, func(), error) {
	target = normalizeTarget(target, a.defaultTarget)
	if target.Endpoint == "" || target.Credential == "" {
		return nil, target, nil, errors.New("database provider endpoint and credential are required")
	}
	key := targetKey(target)
	a.mu.Lock()
	if entry, ok := a.clients[key]; ok {
		a.clientClock++
		entry.lastUsed = a.clientClock
		entry.active++
		a.clients[key] = entry
		a.mu.Unlock()
		return entry.client, target, a.releaseClient(key, entry.client), nil
	}
	client, err := newProviderClient(ctx, target, a.maxRetries, a.requestTimeout, a.providerHeaders)
	if err != nil {
		a.mu.Unlock()
		return nil, target, nil, err
	}
	victim := a.cacheClientLocked(key, client, false, 1)
	a.mu.Unlock()
	shutdownClient(victim)
	return client, target, a.releaseClient(key, client), nil
}

func (a *Adapter) clientForCandidates(ctx context.Context, targets []store.ProviderExecutionTarget) (*bf.Bifrost, []store.ProviderExecutionTarget, func(), error) {
	if len(targets) == 0 {
		return nil, nil, nil, errors.New("no provider execution candidates")
	}
	normalized := make([]store.ProviderExecutionTarget, len(targets))
	for index, target := range targets {
		normalized[index] = normalizeTarget(target, a.defaultTarget)
		if normalized[index].Endpoint == "" || normalized[index].Credential == "" {
			return nil, nil, nil, errors.New("database provider endpoint and credential are required")
		}
	}
	key := targetsKey(normalized)
	a.mu.Lock()
	if entry, ok := a.clients[key]; ok {
		a.clientClock++
		entry.lastUsed = a.clientClock
		entry.active++
		a.clients[key] = entry
		a.mu.Unlock()
		return entry.client, normalized, a.releaseClient(key, entry.client), nil
	}
	client, normalized, err := newCandidateClient(ctx, normalized, a.maxRetries, a.requestTimeout, a.providerHeaders)
	if err != nil {
		a.mu.Unlock()
		return nil, nil, nil, err
	}
	victim := a.cacheClientLocked(key, client, false, 1)
	a.mu.Unlock()
	shutdownClient(victim)
	return client, normalized, a.releaseClient(key, client), nil
}

// cacheClientLocked bounds both connection pools and decrypted credential
// lifetime. Catalog rotations generate new cache keys; least-recently-used
// non-default clients are shut down immediately instead of surviving until
// process exit. The optional process default remains pinned for legacy rows.
func (a *Adapter) cacheClientLocked(key string, client *bf.Bifrost, pinned bool, active int) *bf.Bifrost {
	var evicted *bf.Bifrost
	if len(a.clients) >= maxCachedBifrostClients {
		victimKey := ""
		var victim cachedClient
		for candidateKey, candidate := range a.clients {
			if candidate.pinned || candidate.active != 0 || (victimKey != "" && candidate.lastUsed >= victim.lastUsed) {
				continue
			}
			victimKey, victim = candidateKey, candidate
		}
		if victimKey != "" {
			delete(a.clients, victimKey)
			evicted = victim.client
		}
	}
	a.clientClock++
	a.clients[key] = cachedClient{client: client, lastUsed: a.clientClock, pinned: pinned, active: active}
	return evicted
}

// releaseClient keeps eviction from shutting down a Bifrost worker pool while
// an HTTP/Realtime call still owns it. Temporary overflow is intentional when
// every cached client is active; the first release trims one idle LRU entry.
func (a *Adapter) releaseClient(key string, client *bf.Bifrost) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			entry, ok := a.clients[key]
			if ok && entry.client == client && entry.active > 0 {
				entry.active--
				a.clients[key] = entry
			}
			var victim *bf.Bifrost
			if len(a.clients) > maxCachedBifrostClients {
				victim = a.evictIdleClientLocked()
			}
			a.mu.Unlock()
			shutdownClient(victim)
		})
	}
}

func (a *Adapter) evictIdleClientLocked() *bf.Bifrost {
	victimKey := ""
	var victim cachedClient
	for key, candidate := range a.clients {
		if candidate.pinned || candidate.active != 0 || (victimKey != "" && candidate.lastUsed >= victim.lastUsed) {
			continue
		}
		victimKey, victim = key, candidate
	}
	if victimKey == "" {
		return nil
	}
	delete(a.clients, victimKey)
	return victim.client
}

func shutdownClient(client *bf.Bifrost) {
	if client != nil {
		client.Shutdown()
	}
}

// ChatCompletionCandidates gives Bifrost the ordered database-authorized
// models as native fallbacks. Each candidate becomes its own custom provider,
// so candidates may use different database-selected endpoints and credentials.
func (a *Adapter) ChatCompletionCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, messages []schemas.ChatMessage, params *schemas.ChatParameters) (*schemas.BifrostChatResponse, error) {
	return a.ChatCompletionCandidatesWithHeaders(ctx, targets, messages, params, nil)
}

// ChatCompletionCandidatesWithHeaders forwards only the public-protocol
// headers explicitly allowlisted by the caller.
func (a *Adapter) ChatCompletionCandidatesWithHeaders(ctx context.Context, targets []store.ProviderExecutionTarget, messages []schemas.ChatMessage, params *schemas.ChatParameters, headers map[string][]string) (*schemas.BifrostChatResponse, error) {
	client, targets, release, err := a.clientForCandidates(ctx, targets)
	if err != nil {
		return nil, err
	}
	defer release()
	bfctx := a.requestContextWithHeaders(ctx, headers)
	defer bfctx.Cancel()
	provider, _ := providerForTarget(targets[0])
	request := &schemas.BifrostChatRequest{
		Provider: provider, Model: targets[0].Model, Input: messages, Params: params,
	}
	response, bfErr := client.ChatCompletionRequest(bfctx, request)
	if bfErr != nil {
		return nil, &ExecutionError{SelectedKeyID: bfErr.ExtraFields.RoutingInfo.Key, Cause: fmt.Errorf("bifrost chat completion: %s", bfErr.Error.Message)}
	}
	return response, nil
}

// ChatCompletionStreamCandidates is the streaming equivalent of
// ChatCompletionCandidates. Bifrost performs retry/fallback before emitting a
// successful stream; each chunk retains the winning private routing identity.
func (a *Adapter) ChatCompletionStreamCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, messages []schemas.ChatMessage, params *schemas.ChatParameters) (<-chan *schemas.BifrostStreamChunk, context.CancelFunc, error) {
	return a.ChatCompletionStreamCandidatesWithHeaders(ctx, targets, messages, params, nil)
}

// ChatCompletionStreamCandidatesWithHeaders is the streaming equivalent of
// ChatCompletionCandidatesWithHeaders.
func (a *Adapter) ChatCompletionStreamCandidatesWithHeaders(ctx context.Context, targets []store.ProviderExecutionTarget, messages []schemas.ChatMessage, params *schemas.ChatParameters, headers map[string][]string) (<-chan *schemas.BifrostStreamChunk, context.CancelFunc, error) {
	client, targets, release, err := a.clientForCandidates(ctx, targets)
	if err != nil {
		return nil, nil, err
	}
	provider, _ := providerForTarget(targets[0])
	request := &schemas.BifrostChatRequest{
		Provider: provider, Model: targets[0].Model, Input: messages, Params: params,
	}
	streamCtx, cancel := context.WithCancel(ctx)
	bfctx := a.requestContextWithHeaders(streamCtx, headers)
	chunks, bfErr := client.ChatCompletionStreamRequest(bfctx, request)
	if bfErr != nil {
		cancel()
		bfctx.Cancel()
		release()
		return nil, nil, &ExecutionError{SelectedKeyID: bfErr.ExtraFields.RoutingInfo.Key, Cause: fmt.Errorf("bifrost chat completion stream: %s", bfErr.Error.Message)}
	}
	return chunks, func() {
		cancel()
		bfctx.Cancel()
		release()
	}, nil
}

// ResponsesCandidates executes canonical Responses with the complete ordered
// database candidate set. Anthropic and Gemini compatibility routes translate
// through this same method, so their fallback semantics cannot drift.
func (a *Adapter) ResponsesCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, request *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesResponse, error) {
	client, targets, release, err := a.clientForCandidates(ctx, targets)
	if err != nil {
		return nil, err
	}
	defer release()
	requestCopy := *request
	requestCopy.Provider, _ = providerForTarget(targets[0])
	requestCopy.Model = targets[0].Model
	// Caller-provided fallbacks are untrusted protocol input. Only the ordered
	// candidates resolved from Gizway's database may reach Bifrost.
	requestCopy.Fallbacks = nil
	bfctx := a.requestContext(ctx)
	defer bfctx.Cancel()
	response, bfErr := client.ResponsesRequest(bfctx, &requestCopy)
	if bfErr != nil {
		return nil, fmt.Errorf("bifrost responses: %s", bfErr.Error.Message)
	}
	return response, nil
}

// ResponsesStreamCandidates applies the same candidate contract to OpenAI,
// Anthropic and Gemini streaming compatibility routes.
func (a *Adapter) ResponsesStreamCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, request *schemas.BifrostResponsesRequest) (<-chan *schemas.BifrostStreamChunk, context.CancelFunc, error) {
	client, targets, release, err := a.clientForCandidates(ctx, targets)
	if err != nil {
		return nil, nil, err
	}
	requestCopy := *request
	requestCopy.Provider, _ = providerForTarget(targets[0])
	requestCopy.Model = targets[0].Model
	requestCopy.Fallbacks = nil
	streamCtx, cancel := context.WithCancel(ctx)
	bfctx := a.requestContext(streamCtx)
	chunks, bfErr := client.ResponsesStreamRequest(bfctx, &requestCopy)
	if bfErr != nil {
		cancel()
		bfctx.Cancel()
		release()
		return nil, nil, fmt.Errorf("bifrost responses stream: %s", bfErr.Error.Message)
	}
	return chunks, func() {
		cancel()
		bfctx.Cancel()
		release()
	}, nil
}

// EmbeddingCandidates delegates retry and cross-endpoint fallback to Bifrost.
// The winning custom-provider identity is retained in response ExtraFields so
// Gizway can settle against the exact database variant that served the call.
func (a *Adapter) EmbeddingCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, request *schemas.BifrostEmbeddingRequest) (*schemas.BifrostEmbeddingResponse, error) {
	client, targets, release, err := a.clientForCandidates(ctx, targets)
	if err != nil {
		return nil, err
	}
	defer release()
	requestCopy := *request
	requestCopy.Provider, _ = providerForTarget(targets[0])
	requestCopy.Model = targets[0].Model
	requestCopy.Fallbacks = nil
	bfctx := a.requestContext(ctx)
	defer bfctx.Cancel()
	response, bfErr := client.EmbeddingRequest(bfctx, &requestCopy)
	if bfErr != nil {
		return nil, fmt.Errorf("bifrost embedding: %s", bfErr.Error.Message)
	}
	return response, nil
}

// SpeechCandidates keeps text-to-speech retries and fallback policy inside
// Bifrost, including candidates with different endpoints and credentials.
func (a *Adapter) SpeechCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, request *schemas.BifrostSpeechRequest) (*schemas.BifrostSpeechResponse, error) {
	client, targets, release, err := a.clientForCandidates(ctx, targets)
	if err != nil {
		return nil, err
	}
	defer release()
	requestCopy := *request
	requestCopy.Provider, _ = providerForTarget(targets[0])
	requestCopy.Model = targets[0].Model
	requestCopy.Fallbacks = nil
	bfctx := a.requestContext(ctx)
	defer bfctx.Cancel()
	response, bfErr := client.SpeechRequest(bfctx, &requestCopy)
	if bfErr != nil {
		return nil, fmt.Errorf("bifrost speech: %s", bfErr.Error.Message)
	}
	return response, nil
}

// TranscriptionCandidates executes one Bifrost-owned fallback chain for the
// uploaded audio rather than duplicating provider retry logic in Gizway.
func (a *Adapter) TranscriptionCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, request *schemas.BifrostTranscriptionRequest) (*schemas.BifrostTranscriptionResponse, error) {
	client, targets, release, err := a.clientForCandidates(ctx, targets)
	if err != nil {
		return nil, err
	}
	defer release()
	requestCopy := *request
	requestCopy.Provider, _ = providerForTarget(targets[0])
	requestCopy.Model = targets[0].Model
	requestCopy.Fallbacks = nil
	bfctx := a.requestContext(ctx)
	defer bfctx.Cancel()
	response, bfErr := client.TranscriptionRequest(bfctx, &requestCopy)
	if bfErr != nil {
		return nil, fmt.Errorf("bifrost transcription: %s", bfErr.Error.Message)
	}
	return response, nil
}

// ImageGenerationCandidates gives Bifrost the complete ordered database
// candidate set and preserves its resolved winner for exact price settlement.
func (a *Adapter) ImageGenerationCandidates(ctx context.Context, targets []store.ProviderExecutionTarget, request *schemas.BifrostImageGenerationRequest) (*schemas.BifrostImageGenerationResponse, error) {
	client, targets, release, err := a.clientForCandidates(ctx, targets)
	if err != nil {
		return nil, err
	}
	defer release()
	requestCopy := *request
	requestCopy.Provider, _ = providerForTarget(targets[0])
	requestCopy.Model = targets[0].Model
	requestCopy.Fallbacks = nil
	bfctx := a.requestContext(ctx)
	defer bfctx.Cancel()
	response, bfErr := client.ImageGenerationRequest(bfctx, &requestCopy)
	if bfErr != nil {
		return nil, fmt.Errorf("bifrost image generation: %s", bfErr.Error.Message)
	}
	return response, nil
}

// Shutdown releases every cached Bifrost client exactly once.
func (a *Adapter) Shutdown() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, entry := range a.clients {
		entry.client.Shutdown()
		delete(a.clients, key)
	}
}
