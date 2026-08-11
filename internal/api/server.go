// Package api implements Gizway-owned Account, Pay, and Admin HTTP surfaces.
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"crypto/sha256"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/idy/gizway/internal/providerctx"
	gatewayservice "github.com/idy/gizway/internal/service/gateway"
	merchantservice "github.com/idy/gizway/internal/service/merchant"
	paymentservice "github.com/idy/gizway/internal/service/payment"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/timetext"
)

type contextKey uint8

const (
	userIDKey contextKey = iota
	accountIDKey
	administratorIDKey
	apiKeyIDKey
	gatewayRecoveryPrincipalKey
)

// Server serves Gizway-owned APIs.
type Server struct {
	store     *store.Store
	gateway   *gatewayservice.Service
	payment   *paymentservice.Service
	merchant  *merchantservice.Service
	handler   http.Handler
	now       func() time.Time
	advance   func(time.Duration) time.Time
	powerSync powerSyncConfig

	realtimeMu      sync.Mutex
	realtimeClosing bool
	realtimeConns   map[*websocket.Conn]struct{}
	realtimeDone    sync.WaitGroup
}

// New registers the implemented API operations.
func New(repository *store.Store) *Server {
	return NewWithServices(repository, nil, nil, merchantservice.New(repository))
}

// NewWithGateway registers APIs with an owned AI orchestration service.
func NewWithGateway(repository *store.Store, gateway *gatewayservice.Service) *Server {
	return NewWithServices(repository, gateway, nil, merchantservice.New(repository))
}

// NewWithServices wires optional external orchestration services while all
// database-backed Account and Admin operations remain available.
func NewWithServices(repository *store.Store, gateway *gatewayservice.Service, payment *paymentservice.Service, merchant *merchantservice.Service) *Server {
	return NewWithServicesAndClock(repository, gateway, payment, merchant, time.Now, nil)
}

// NewWithServicesAndClock is the application composition seam for the
// controllable story clock. advance is nil in production, so the fixture-only
// clock route cannot accidentally be exposed by a normal deployment.
func NewWithServicesAndClock(repository *store.Store, gateway *gatewayservice.Service, payment *paymentservice.Service, merchant *merchantservice.Service, now func() time.Time, advance func(time.Duration) time.Time) *Server {
	if now == nil {
		now = time.Now
	}
	server := &Server{store: repository, gateway: gateway, payment: payment, merchant: merchant, now: now, advance: advance, realtimeConns: make(map[*websocket.Conn]struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	if advance != nil {
		mux.HandleFunc("POST /test/v1/clock/advance", server.advanceStoryClock)
		mux.HandleFunc("POST /test/v1/powersync/authorize", server.authorizePowerSyncFixture)
		mux.HandleFunc("POST /test/v1/gateway-recovery/poison", server.createGatewayRecoveryPoisonFixture)
	}

	mux.HandleFunc("POST /account/v1/auth/login", server.loginUser)
	mux.Handle("POST /account/v1/auth/refresh", server.requireUserSession(http.HandlerFunc(server.refreshUserSession)))
	mux.Handle("POST /account/v1/auth/logout", server.requireUserSession(http.HandlerFunc(server.logoutUser)))
	mux.Handle("POST /account/v1/powersync/credentials", server.requireUserSession(http.HandlerFunc(server.createPowerSyncCredentials)))
	mux.Handle("GET /account/v1/me", server.requireUserSession(http.HandlerFunc(server.getCurrentUser)))
	mux.Handle("PATCH /account/v1/me", server.requireUserSession(http.HandlerFunc(server.updateCurrentUser)))
	mux.Handle("GET /account/v1/accounts", server.requireUserSession(http.HandlerFunc(server.listAccounts)))
	mux.Handle("GET /account/v1/accounts/{account_id}/balance", server.requireUserOrGatewayScope("account:self", http.HandlerFunc(server.getBalance)))
	mux.Handle("GET /account/v1/accounts/{account_id}/models", server.requireUserOrGatewayScope("account:self", http.HandlerFunc(server.listAccountCatalog)))
	mux.Handle("GET /account/v1/accounts/{account_id}/api_keys", server.requireUserSession(http.HandlerFunc(server.listAPIKeys)))
	mux.Handle("POST /account/v1/accounts/{account_id}/api_keys", server.requireUserSession(http.HandlerFunc(server.createAPIKey)))
	mux.Handle("DELETE /account/v1/accounts/{account_id}/api_keys/{api_key_id}", server.requireUserSession(http.HandlerFunc(server.revokeAPIKey)))
	mux.Handle("GET /account/v1/accounts/{account_id}/usage", server.requireUserOrGatewayScope("gateway:usage:read", http.HandlerFunc(server.listGatewayUsage)))
	mux.Handle("GET /account/v1/accounts/{account_id}/transactions", server.requireUserSession(http.HandlerFunc(server.listAccountTransactions)))
	mux.Handle("GET /account/v1/accounts/{account_id}/invoices", server.requireUserSession(http.HandlerFunc(server.listInvoices)))
	mux.Handle("GET /account/v1/accounts/{account_id}/invoices/{invoice_id}", server.requireUserSession(http.HandlerFunc(server.getInvoice)))
	mux.Handle("GET /account/v1/accounts/{account_id}/transfers", server.requireUserSession(http.HandlerFunc(server.listCreditTransfers)))
	mux.Handle("POST /account/v1/accounts/{account_id}/transfers", server.requireUserSession(http.HandlerFunc(server.createCreditTransfer)))
	mux.Handle("GET /account/v1/accounts/{account_id}/topups", server.requireUserSession(http.HandlerFunc(server.listTopups)))
	mux.Handle("POST /account/v1/accounts/{account_id}/topups", server.requireUserSession(http.HandlerFunc(server.createTopup)))
	mux.Handle("POST /account/v1/accounts/{account_id}/topups/{topup_id}/refunds", server.requireUserSession(http.HandlerFunc(server.refundTopup)))
	mux.Handle("POST /account/v1/merchant_accounts", server.requireUserSession(http.HandlerFunc(server.createMerchantAccount)))
	mux.Handle("GET /account/v1/merchant_accounts/{account_id}/services", server.requireUserSession(http.HandlerFunc(server.listMerchantServices)))
	mux.Handle("POST /account/v1/merchant_accounts/{account_id}/services", server.requireUserSession(http.HandlerFunc(server.createMerchantService)))
	mux.HandleFunc("POST /callbacks/v1/payment_events", server.paymentProviderCallback)
	mux.Handle("POST /pay/v1/payment_intents", server.requirePaymentKey("pay:intents:write", http.HandlerFunc(server.createPaymentIntent)))
	mux.Handle("GET /pay/v1/payment_intents/{payment_intent_id}", server.requirePaymentKey("pay:intents:write", http.HandlerFunc(server.getPaymentIntent)))
	mux.Handle("GET /pay/v1/checkout/payment_intents/{payment_intent_id}", server.requireUserSession(http.HandlerFunc(server.getCheckoutPaymentIntent)))
	mux.Handle("POST /pay/v1/payment_intents/{payment_intent_id}/confirm", server.requireUserSession(http.HandlerFunc(server.confirmPaymentIntent)))
	mux.Handle("POST /pay/v1/payment_intents/{payment_intent_id}/cancel", server.requirePaymentKey("pay:intents:write", http.HandlerFunc(server.cancelPaymentIntent)))
	mux.Handle("POST /pay/v1/payment_intents/{payment_intent_id}/reversals", server.requirePaymentKey("pay:intents:write", http.HandlerFunc(server.reversePaymentIntent)))
	mux.Handle("GET /pay/v1/transactions", server.requirePaymentKey("pay:transactions:read", http.HandlerFunc(server.listMerchantTransactions)))
	mux.Handle("GET /pay/v1/webhook_endpoints", server.requirePaymentKey("pay:webhooks:write", http.HandlerFunc(server.listWebhookEndpoints)))
	mux.Handle("POST /pay/v1/webhook_endpoints", server.requirePaymentKey("pay:webhooks:write", http.HandlerFunc(server.createWebhookEndpoint)))
	mux.Handle("PATCH /pay/v1/webhook_endpoints/{endpoint_id}", server.requirePaymentKey("pay:webhooks:write", http.HandlerFunc(server.updateWebhookEndpoint)))
	mux.Handle("DELETE /pay/v1/webhook_endpoints/{endpoint_id}", server.requirePaymentKey("pay:webhooks:write", http.HandlerFunc(server.deleteWebhookEndpoint)))
	mux.Handle("POST /pay/v1/webhook_endpoints/{endpoint_id}/rotate_secret", server.requirePaymentKey("pay:webhooks:write", http.HandlerFunc(server.rotateWebhookEndpointSecret)))
	mux.Handle("GET /v1/models", server.requireGatewayKey("account:self|gateway:invoke", http.HandlerFunc(server.listPublicModels)))
	mux.Handle("GET /v1beta/models", server.requireGatewayKey("account:self|gateway:invoke", http.HandlerFunc(server.listGeminiModels)))
	mux.Handle("POST /v1/chat/completions", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.chatCompletions)))
	mux.Handle("POST /v1/responses", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.openAIResponses)))
	mux.Handle("POST /v1/embeddings", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.openAIEmbeddings)))
	mux.Handle("POST /v1/audio/speech", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.openAISpeech)))
	mux.Handle("POST /v1/audio/transcriptions", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.openAITranscription)))
	mux.Handle("POST /v1/images/generations", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.openAIImageGeneration)))
	mux.Handle("POST /v1/images/edits", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAICommand)))
	mux.Handle("POST /v1/messages", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.anthropicMessages)))
	mux.Handle("POST /v1/messages/batches", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAICommand)))
	mux.Handle("GET /v1/messages/batches", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAIRead)))
	mux.Handle("GET /v1/messages/batches/{batch_id}", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAIRead)))
	mux.Handle("POST /v1/messages/batches/{batch_id}/cancel", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAICommand)))
	mux.Handle("DELETE /v1/messages/batches/{batch_id}", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAICommand)))
	mux.Handle("GET /v1/messages/batches/{batch_id}/results", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAIRead)))
	mux.Handle("POST /v1/files", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAICommand)))
	mux.Handle("GET /v1/files", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAIRead)))
	mux.Handle("GET /v1/files/{file_id}", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAIRead)))
	mux.Handle("DELETE /v1/files/{file_id}", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAICommand)))
	mux.Handle("GET /v1/files/{file_id}/content", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAIRead)))
	mux.Handle("POST /upload/v1beta/files", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAICommand)))
	mux.Handle("GET /v1beta/files", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAIRead)))
	mux.Handle("GET /v1beta/files/{file_id}", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAIRead)))
	mux.Handle("DELETE /v1beta/files/{file_id}", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.unsupportedAICommand)))
	mux.Handle("POST /v1beta/models/{operation}", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.geminiGenerateContent)))
	mux.Handle("POST /v1/realtime/client_secrets", server.requireGatewayKey("gateway:invoke", http.HandlerFunc(server.createRealtimeClientSecret)))
	mux.HandleFunc("GET /v1/realtime", server.realtimeWebSocket)
	mux.HandleFunc("POST /v1/realtime/calls", server.realtimeWebRTCSDP)
	mux.HandleFunc("POST /callbacks/v1/realtime_events", server.realtimeProviderCallback)

	mux.Handle("GET /admin/v1/models", server.requireAdmin(http.HandlerFunc(server.listModels)))
	mux.Handle("POST /admin/v1/models", server.requireAdmin(http.HandlerFunc(server.createModel)))
	mux.Handle("PATCH /admin/v1/models/{model_id}", server.requireAdmin(http.HandlerFunc(server.updateModel)))
	mux.Handle("GET /admin/v1/models/{model_id}/variants", server.requireAdmin(http.HandlerFunc(server.listModelVariants)))
	mux.Handle("POST /admin/v1/models/{model_id}/variants", server.requireAdmin(http.HandlerFunc(server.createModelVariant)))
	mux.Handle("PATCH /admin/v1/model_variants/{variant_id}", server.requireAdmin(http.HandlerFunc(server.updateModelVariant)))
	mux.Handle("GET /admin/v1/model_variants/{variant_id}/prices", server.requireAdmin(http.HandlerFunc(server.listModelPrices)))
	mux.Handle("POST /admin/v1/model_variants/{variant_id}/prices", server.requireAdmin(http.HandlerFunc(server.createModelPrice)))
	server.registerAdminRoutes(mux)

	handler := recoverMiddleware(server.requestIDMiddleware(server.idempotencyMiddleware(mux)))
	if advance != nil {
		handler = server.storyClockTick(handler)
	}
	server.handler = handler
	return server
}

func (s *Server) createGatewayRecoveryPoisonFixture(w http.ResponseWriter, r *http.Request) {
	requestID, err := s.store.CreateCorruptGatewayRecoveryFixture(r.Context(), timetext.Format(s.now()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "recovery fault fixture could not be created")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"gateway_request_id": requestID})
}

// storyClockTick gives every HTTP interaction a stable total ordering while
// remaining independent of host time. One nanosecond is enough to break
// database timestamp ties without materially advancing expiry windows.
func (s *Server) storyClockTick(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.advance(time.Nanosecond)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" || len(requestID) > 255 {
			requestID = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(store.WithAuditRequestID(r.Context(), requestID)))
	})
}

type bufferedResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *bufferedResponseWriter) Header() http.Header { return w.header }
func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *bufferedResponseWriter) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(value)
}

// Flush lets the recovery worker drive streaming protocol handlers through the
// same HTTP codecs. Bytes remain buffered and are intentionally discarded;
// the Gateway service persists canonical frames and exact terminal usage.
func (w *bufferedResponseWriter) Flush() {}

func (s *Server) idempotencyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || !journaledMutationPath(r.Method, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			writeError(w, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key is required for this mutation")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "request body could not be read")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		payloadHash := sha256.Sum256(body)
		// Scope by the credential value, not the raw header serialization.
		// HTTP auth schemes are case-insensitive, so `Bearer` and `bearer` must
		// resolve to one command identity rather than executing twice.
		credentialScope := strings.TrimSpace(r.Header.Get("Authorization"))
		if token, ok := bearerToken(r); ok {
			credentialScope = token
		}
		if strings.HasSuffix(r.URL.Path, "/auth/login") {
			var loginIdentity struct {
				Email string `json:"email"`
			}
			if json.Unmarshal(body, &loginIdentity) == nil {
				// Public login commands are scoped by normalized identity, never by
				// password or the otherwise-empty Authorization header.
				credentialScope = "login:" + r.URL.Path + ":" + strings.ToLower(strings.TrimSpace(loginIdentity.Email))
			}
		}
		credentialHash := sha256.Sum256([]byte(credentialScope))
		operation := r.Method + " " + r.URL.Path
		if r.URL.RawQuery != "" {
			operation += "?" + r.URL.RawQuery
		}
		response, _, err := s.store.ExecuteAPICommand(r.Context(), credentialHash[:], operation, key, payloadHash[:], func(commandContext context.Context) store.APICommandResponse {
			request := r.Clone(commandContext)
			request.Body = io.NopCloser(bytes.NewReader(body))
			buffered := &bufferedResponseWriter{header: make(http.Header)}
			next.ServeHTTP(buffered, request)
			if buffered.status == 0 {
				buffered.status = http.StatusOK
			}
			return store.APICommandResponse{StatusCode: buffered.status, ContentType: buffered.header.Get("Content-Type"), Body: append([]byte(nil), buffered.body.Bytes()...)}
		})
		if err != nil {
			if errors.Is(err, store.ErrIdempotencyConflict) {
				writeError(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used with a different request")
			} else if errors.Is(err, store.ErrCommandInProgress) {
				writeError(w, http.StatusConflict, "idempotency_in_progress", err.Error())
			} else {
				writeDataError(w, err)
			}
			return
		}
		if response.ContentType != "" {
			w.Header().Set("Content-Type", response.ContentType)
		}
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(response.Body)
	})
}

func journaledMutationPath(method, path string) bool {
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch && method != http.MethodDelete {
		return false
	}
	// Signed callbacks carry their provider event ID as the intrinsic command
	// identity. AI/Realtime and top-up/refund flows own provider idempotency and
	// durable domain command tables; wrapping them in an outer HTTP transaction
	// would hide the reservation before the network call and reopen crash gaps.
	if strings.HasPrefix(path, "/callbacks/") || strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/upload/") || strings.HasPrefix(path, "/v1beta/") {
		return false
	}
	if strings.HasPrefix(path, "/admin/v1/") {
		if strings.Contains(path, "/webhook_deliveries/") || strings.HasSuffix(path, "/api_keys") {
			return false
		}
		return true
	}
	if strings.HasPrefix(path, "/account/v1/") {
		// PowerSync credentials are short-lived, stateless connection material.
		// Replaying an old JWT from a long-lived command journal is less correct
		// than issuing a fresh token, so this read-like POST intentionally has no
		// Idempotency-Key contract.
		if path == "/account/v1/powersync/credentials" {
			return false
		}
		if strings.Contains(path, "/topups") || strings.Contains(path, "/services") || strings.Contains(path, "/transfers") || strings.HasSuffix(path, "/api_keys") {
			return false
		}
		return true
	}
	if strings.HasPrefix(path, "/pay/v1/") {
		if strings.Contains(path, "/webhook_endpoints") || (strings.Contains(path, "/payment_intents") && !strings.HasSuffix(path, "/cancel")) {
			return false
		}
		return true
	}
	return path == "/admin/v1/auth/login"
}

func (s *Server) createRealtimeClientSecret(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	if s.gateway == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "AI execution is not configured")
		return
	}
	var request gatewayservice.RealtimeRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	created, err := s.gateway.CreateRealtimeSession(r.Context(), store.GatewayPrincipal{UserID: contextString(r.Context(), userIDKey), AccountID: contextString(r.Context(), accountIDKey), APIKeyID: contextString(r.Context(), apiKeyIDKey)}, r.Header.Get("Idempotency-Key"), request)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrInsufficientBalance):
			writeError(w, http.StatusPaymentRequired, "insufficient_balance", "available balance is insufficient")
		case errors.Is(err, store.ErrAccountFrozen):
			writeError(w, http.StatusLocked, "account_frozen", "account balance is frozen")
		case errors.Is(err, store.ErrIdempotencyConflict), errors.Is(err, store.ErrCredentialConsumed):
			writeError(w, http.StatusConflict, "idempotency_conflict", "Realtime credential was already issued")
		case errors.Is(err, store.ErrRealtimeSessionLimit):
			writeError(w, http.StatusTooManyRequests, "session_limit", "Realtime concurrent session limit reached")
		default:
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"client_secret": map[string]any{"value": created.ClientSecret, "expires_at": created.Session.ExpiresAt}, "session": created.Session})
}

func (s *Server) realtimeWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.gateway == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "AI execution is not configured")
		return
	}
	secret, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing Realtime client secret")
		return
	}
	session, err := s.gateway.ConnectRealtimeSession(r.Context(), secret, "websocket")
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid, expired, or consumed Realtime client secret")
		return
	}
	if requested := r.URL.Query().Get("session_id"); requested != "" && requested != session.ID {
		_ = s.store.ReleaseGatewayCommand(context.WithoutCancel(r.Context()), session.GatewayRequestID, "session_mismatch")
		writeError(w, http.StatusUnauthorized, "unauthorized", "Realtime session mismatch")
		return
	}
	client, err := websocket.Accept(w, r, nil)
	if err != nil {
		_ = s.store.ReleaseGatewayCommand(context.WithoutCancel(r.Context()), session.GatewayRequestID, "upgrade_error")
		return
	}
	if !s.trackRealtimeConnection(client) {
		_ = client.CloseNow()
		_ = s.store.ReleaseGatewayCommand(context.WithoutCancel(r.Context()), session.GatewayRequestID, "server_shutdown")
		return
	}
	defer s.untrackRealtimeConnection(client)
	// Once Accept hijacks the connection, the outer HTTP recovery middleware
	// can no longer write a JSON error safely. Contain unexpected executor
	// panics here and terminate the WebSocket with the protocol-level status.
	defer func() {
		if recover() != nil {
			_ = client.Close(websocket.StatusInternalError, "internal server error")
		}
	}()
	defer client.Close(websocket.StatusNormalClosure, "session complete")
	if err := s.gateway.ProxyRealtimeWebSocket(r.Context(), client, session); err != nil {
		return
	}
}

func (s *Server) trackRealtimeConnection(client *websocket.Conn) bool {
	s.realtimeMu.Lock()
	defer s.realtimeMu.Unlock()
	if s.realtimeClosing {
		return false
	}
	s.realtimeConns[client] = struct{}{}
	s.realtimeDone.Add(1)
	return true
}

func (s *Server) untrackRealtimeConnection(client *websocket.Conn) {
	s.realtimeMu.Lock()
	if _, ok := s.realtimeConns[client]; ok {
		delete(s.realtimeConns, client)
		s.realtimeDone.Done()
	}
	s.realtimeMu.Unlock()
}

// CloseRealtimeConnections owns the shutdown boundary for hijacked
// WebSockets. net/http no longer tracks them after Accept, so Server.Shutdown
// alone can return while a proxy is still settling or releasing Credit. Once
// closing is set, no new connection can increment the wait group; all current
// sockets are force-closed and their handlers must finish before dependencies
// such as Bifrost and sqlx are torn down.
func (s *Server) CloseRealtimeConnections(ctx context.Context) error {
	s.realtimeMu.Lock()
	s.realtimeClosing = true
	connections := make([]*websocket.Conn, 0, len(s.realtimeConns))
	for connection := range s.realtimeConns {
		connections = append(connections, connection)
	}
	s.realtimeMu.Unlock()
	for _, connection := range connections {
		_ = connection.CloseNow()
	}
	done := make(chan struct{})
	go func() {
		s.realtimeDone.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) realtimeWebRTCSDP(w http.ResponseWriter, r *http.Request) {
	if s.gateway == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "AI execution is not configured")
		return
	}
	secret, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing Realtime client secret")
		return
	}
	offer, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "could not read SDP offer")
		return
	}
	if len(offer) > 1<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "SDP offer exceeds 1 MiB")
		return
	}
	if len(strings.TrimSpace(string(offer))) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "SDP offer is required")
		return
	}
	// Consume the one-purpose credential only after the complete signaling body
	// has passed its transport bound. A rejected oversized request must not burn
	// the caller's otherwise-valid session.
	session, err := s.gateway.ConnectRealtimeSession(r.Context(), secret, "webrtc")
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid, expired, or consumed Realtime client secret")
		return
	}
	if requested := r.URL.Query().Get("session_id"); requested == "" || requested != session.ID {
		_ = s.store.ReleaseGatewayCommand(context.WithoutCancel(r.Context()), session.GatewayRequestID, "session_mismatch")
		writeError(w, http.StatusUnauthorized, "unauthorized", "Realtime session mismatch")
		return
	}
	answer, err := s.gateway.ExchangeRealtimeWebRTCSDP(r.Context(), session, string(offer), nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "provider_error", "WebRTC signaling failed")
		return
	}
	w.Header().Set("Content-Type", "application/sdp")
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, answer)
}

func (s *Server) realtimeProviderCallback(w http.ResponseWriter, r *http.Request) {
	if s.gateway == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "AI execution is not configured")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "could not read Realtime provider event")
		return
	}
	if len(raw) > 1<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Realtime provider event exceeds 1 MiB")
		return
	}
	session, replayed, err := s.gateway.CompleteRealtimeProviderEvent(r.Context(), raw, r.Header.Get("X-Gizway-Signature"))
	if err != nil {
		if errors.Is(err, store.ErrIdempotencyConflict) {
			writeError(w, http.StatusConflict, "event_conflict", "event id was reused with a different payload")
			return
		}
		if errors.Is(err, gatewayservice.ErrInvalidProviderEvent) {
			writeError(w, http.StatusUnauthorized, "invalid_provider_event", "Realtime provider event authentication or state is invalid")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Realtime provider event could not be settled")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"received": true, "duplicate": replayed, "session": session})
}

func (s *Server) requirePaymentKey(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, ok := bearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		principal, err := s.store.AuthenticatePaymentKey(r.Context(), secret, timetext.Format(s.now()))
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer token")
			} else {
				writeError(w, http.StatusInternalServerError, "internal_error", "authentication failed")
			}
			return
		}
		var scopes []string
		if json.Unmarshal(principal.Scopes, &scopes) != nil || !contains(scopes, scope) {
			writeError(w, http.StatusForbidden, "forbidden", "Payment key lacks required scope")
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, principal.UserID)
		ctx = context.WithValue(ctx, accountIDKey, principal.AccountID)
		ctx = context.WithValue(ctx, apiKeyIDKey, principal.APIKeyID)
		ctx = store.WithAuditActor(ctx, "api_key", principal.APIKeyID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) createPaymentIntent(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request merchantservice.CreateIntentRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	intent, replayed, err := s.merchant.CreateIntent(r.Context(), contextString(r.Context(), accountIDKey), contextString(r.Context(), apiKeyIDKey), r.Header.Get("Idempotency-Key"), request)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrIdempotencyConflict):
			writeError(w, http.StatusConflict, "idempotency_conflict", err.Error())
		case errors.Is(err, merchantservice.ErrInvalidRequest):
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "payment intent could not be created")
		}
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, intent)
}
func (s *Server) getPaymentIntent(w http.ResponseWriter, r *http.Request) {
	intent, err := s.store.GetMerchantPaymentIntent(r.Context(), contextString(r.Context(), accountIDKey), r.PathValue("payment_intent_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, intent)
}
func (s *Server) getCheckoutPaymentIntent(w http.ResponseWriter, r *http.Request) {
	intent, err := s.store.GetCheckoutPaymentIntent(r.Context(), r.PathValue("payment_intent_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, intent)
}
func (s *Server) confirmPaymentIntent(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	if !s.consumeRateLimit(w, r, "user:"+contextString(r.Context(), userIDKey), "payment.confirm", 60, 5) {
		return
	}
	intent, replayed, err := s.merchant.Confirm(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("payment_intent_id"), r.Header.Get("Idempotency-Key"))
	if err != nil {
		switch {
		case errors.Is(err, store.ErrInsufficientBalance):
			writeError(w, http.StatusConflict, "insufficient_balance", "available balance is insufficient")
		case errors.Is(err, store.ErrAccountFrozen):
			writeError(w, http.StatusLocked, "account_frozen", "account balance is frozen")
		case errors.Is(err, store.ErrNotFound):
			writeDataError(w, err)
		default:
			writeError(w, http.StatusConflict, "payment_not_confirmable", "payment intent cannot be confirmed")
		}
		return
	}
	_ = replayed
	writeJSON(w, http.StatusOK, intent)
}
func (s *Server) cancelPaymentIntent(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	intent, err := s.store.CancelPaymentIntent(r.Context(), contextString(r.Context(), accountIDKey), r.PathValue("payment_intent_id"), timetext.Format(s.now()))
	if err != nil {
		writeError(w, http.StatusConflict, "payment_not_cancelable", "payment intent cannot be cancelled")
		return
	}
	writeJSON(w, http.StatusOK, intent)
}

func (s *Server) reversePaymentIntent(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	reversal, replayed, err := s.merchant.Reverse(r.Context(), contextString(r.Context(), accountIDKey), r.PathValue("payment_intent_id"), r.Header.Get("Idempotency-Key"), request.Reason)
	if err != nil {
		switch {
		case errors.Is(err, merchantservice.ErrInvalidRequest):
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		case errors.Is(err, store.ErrNotFound):
			writeDataError(w, err)
		case errors.Is(err, store.ErrInsufficientBalance):
			writeError(w, http.StatusConflict, "insufficient_balance", "merchant has already committed the Credit required for reversal")
		case errors.Is(err, store.ErrAccountFrozen):
			writeError(w, http.StatusLocked, "account_frozen", "merchant balance is frozen")
		case errors.Is(err, store.ErrIdempotencyConflict):
			writeError(w, http.StatusConflict, "reversal_conflict", "payment cannot be reversed by this command")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "payment reversal could not be completed")
		}
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, reversal)
}
func (s *Server) listMerchantTransactions(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListMerchantTransactions(r.Context(), contextString(r.Context(), accountIDKey))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page(items))
}
func (s *Server) listWebhookEndpoints(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListWebhookEndpoints(r.Context(), contextString(r.Context(), accountIDKey))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}
func (s *Server) createWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	endpoint, secret, replayed, err := s.merchant.CreateWebhookEndpoint(r.Context(), contextString(r.Context(), accountIDKey), r.Header.Get("Idempotency-Key"), request.URL, request.Events)
	if err != nil {
		if errors.Is(err, store.ErrIdempotencyConflict) {
			writeError(w, http.StatusConflict, "idempotency_conflict", err.Error())
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		}
		return
	}
	response := map[string]any{"id": endpoint.ID, "url": endpoint.URL, "events": endpoint.Events, "status": endpoint.Status, "created_at": endpoint.CreatedAt, "updated_at": endpoint.UpdatedAt}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	} else {
		response["signing_secret"] = secret
	}
	writeJSON(w, status, response)
}

func (s *Server) listMerchantServices(w http.ResponseWriter, r *http.Request) {
	services, err := s.store.ListMerchantServices(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("account_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page(services))
}

func (s *Server) createMerchantService(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request merchantservice.CreateMerchantServiceRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	service, risk, replayed, err := s.merchant.CreateMerchantService(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("account_id"), r.Header.Get("Idempotency-Key"), request)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeDataError(w, err)
		case errors.Is(err, store.ErrIdempotencyConflict):
			writeError(w, http.StatusConflict, "idempotency_conflict", err.Error())
		case errors.Is(err, merchantservice.ErrRiskUnavailable):
			writeError(w, http.StatusServiceUnavailable, "risk_unavailable", "mandatory risk checks are unavailable")
		case errors.Is(err, merchantservice.ErrInvalidRequest):
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "merchant service could not be created")
		}
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"service": service, "risk_decision": risk})
}

func (s *Server) updateWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	endpoint, _, err := s.merchant.SetWebhookEndpointStatus(r.Context(), contextString(r.Context(), accountIDKey), r.PathValue("endpoint_id"), r.Header.Get("Idempotency-Key"), request.Status)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeDataError(w, err)
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, endpoint)
}

func (s *Server) rotateWebhookEndpointSecret(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	secret, _, err := s.merchant.RotateWebhookEndpointSecret(r.Context(), contextString(r.Context(), accountIDKey), r.PathValue("endpoint_id"), r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signing_secret": secret})
}

func (s *Server) deleteWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	if _, err := s.merchant.DeleteWebhookEndpoint(r.Context(), contextString(r.Context(), accountIDKey), r.PathValue("endpoint_id"), r.Header.Get("Idempotency-Key")); err != nil {
		writeDataError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listTopups(w http.ResponseWriter, r *http.Request) {
	query, err := accountListQuery(r, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	topups, err := s.store.ListTopupsPage(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("account_id"), query)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeAccountPage(w, topups)
}

func (s *Server) createTopup(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	if s.payment == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "payment execution is not configured")
		return
	}
	var request paymentservice.CreateTopupRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	topup, replayed, err := s.payment.CreateTopup(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("account_id"), r.Header.Get("Idempotency-Key"), request)
	if err != nil {
		if errors.Is(err, store.ErrIdempotencyConflict) {
			writeError(w, http.StatusConflict, "idempotency_conflict", err.Error())
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeDataError(w, err)
			return
		}
		if errors.Is(err, paymentservice.ErrInvalidRequest) {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		} else if errors.Is(err, paymentservice.ErrProviderUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "provider_unavailable", "payment provider is unavailable")
		} else {
			writeError(w, http.StatusInternalServerError, "internal_error", "top-up could not be created")
		}
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, topup)
}

func (s *Server) refundTopup(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	if s.payment == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "payment execution is not configured")
		return
	}
	var request struct {
		Amount store.CreditAmount `json:"amount"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	refund, replayed, err := s.payment.Refund(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("account_id"), r.PathValue("topup_id"), r.Header.Get("Idempotency-Key"), request.Amount)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeDataError(w, err)
		case errors.Is(err, store.ErrIdempotencyConflict):
			writeError(w, http.StatusConflict, "idempotency_conflict", err.Error())
		case errors.Is(err, store.ErrInsufficientBalance):
			writeError(w, http.StatusConflict, "not_refundable", "amount exceeds unused purchased Credit")
		case errors.Is(err, store.ErrAccountFrozen):
			writeError(w, http.StatusLocked, "account_frozen", "account balance is frozen")
		case errors.Is(err, paymentservice.ErrInvalidRequest):
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		case errors.Is(err, paymentservice.ErrProviderUnavailable):
			writeError(w, http.StatusServiceUnavailable, "provider_unavailable", "payment provider is unavailable")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "refund could not be completed")
		}
		return
	}
	status := http.StatusAccepted
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, refund)
}

func (s *Server) paymentProviderCallback(w http.ResponseWriter, r *http.Request) {
	if s.payment == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "payment execution is not configured")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "could not read payment event")
		return
	}
	if len(raw) > 1<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "payment provider event exceeds 1 MiB")
		return
	}
	topup, replayed, err := s.payment.CompleteProviderEvent(r.Context(), raw, r.Header.Get("X-Gizway-Signature"))
	if err != nil {
		switch {
		case errors.Is(err, store.ErrPaymentMismatch):
			writeError(w, http.StatusConflict, "event_quarantined", "payment amount or currency does not match")
		case errors.Is(err, store.ErrIdempotencyConflict):
			writeError(w, http.StatusConflict, "event_conflict", "event id was reused with a different payload")
		case errors.Is(err, paymentservice.ErrInvalidProviderEvent):
			writeError(w, http.StatusUnauthorized, "invalid_signature", "payment event authentication failed")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "payment event could not be processed")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"received": true, "duplicate": replayed, "topup": topup})
}

func (s *Server) advanceStoryClock(w http.ResponseWriter, r *http.Request) {
	if s.advance == nil {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	var request struct {
		By string `json:"by"`
	}
	if decodeJSON(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "by must be a positive duration")
		return
	}
	duration, err := time.ParseDuration(request.By)
	if err != nil || duration <= 0 || duration > 365*24*time.Hour {
		writeError(w, http.StatusBadRequest, "invalid_request", "by must be between 1ns and 8760h")
		return
	}
	now := s.advance(duration)
	if _, err := s.store.ExpirePaymentIntents(r.Context(), timetext.Format(now), 1024); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "clock-dependent state could not be advanced")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"now": timetext.Format(now)})
}

// Handler returns the registered HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) requireUserSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, ok := bearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		userID, accountID, err := s.store.AuthenticateUserSession(r.Context(), secret)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusInternalServerError, "internal_error", "authentication failed")
				return
			}
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer token")
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, userID)
		ctx = context.WithValue(ctx, accountIDKey, accountID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireGatewayKey(requiredScope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal, ok := r.Context().Value(gatewayRecoveryPrincipalKey).(store.GatewayPrincipal); ok {
			ctx := context.WithValue(r.Context(), userIDKey, principal.UserID)
			ctx = context.WithValue(ctx, accountIDKey, principal.AccountID)
			ctx = context.WithValue(ctx, apiKeyIDKey, principal.APIKeyID)
			ctx = store.WithAuditActor(ctx, "api_key", principal.APIKeyID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		secret, ok := gatewayToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		hash := sha256.Sum256([]byte(secret))
		principal, err := s.store.AuthenticateGatewayKey(r.Context(), hash[:], timetext.Format(s.now()))
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusInternalServerError, "internal_error", "authentication failed")
				return
			}
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer token")
			return
		}
		if !gatewayPrincipalHasScope(principal, requiredScope) {
			writeError(w, http.StatusForbidden, "forbidden", "Gateway key lacks "+requiredScope+" scope")
			return
		}
		if requiredScope == "gateway:invoke" && !s.consumeRateLimit(w, r, "api-key:"+principal.APIKeyID, "gateway.invoke", 600, 10) {
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, principal.UserID)
		ctx = context.WithValue(ctx, accountIDKey, principal.AccountID)
		ctx = context.WithValue(ctx, apiKeyIDKey, principal.APIKeyID)
		ctx = store.WithAuditActor(ctx, "api_key", principal.APIKeyID)
		if requiredScope == "gateway:invoke" && r.Method == http.MethodPost {
			const maximumRecoverableGatewayBody = 17 << 20
			body, readErr := io.ReadAll(io.LimitReader(r.Body, maximumRecoverableGatewayBody+1))
			if readErr != nil {
				writeError(w, http.StatusBadRequest, "invalid_request", "request body could not be read")
				return
			}
			if len(body) > maximumRecoverableGatewayBody {
				writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Gateway request exceeds the recoverable transport limit")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			requestURI := *r.URL
			query := requestURI.Query()
			query.Del("key") // Gemini query authentication is never persisted.
			requestURI.RawQuery = query.Encode()
			ctx = providerctx.WithRecoveryRequest(ctx, providerctx.RecoveryRequest{
				Method: r.Method, RequestURI: requestURI.RequestURI(),
				ContentType: r.Header.Get("Content-Type"), Body: body,
			})
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RecoverGatewayCommands replays expired HTTPS leases through the real public
// codecs and service orchestration. The persisted envelope contains no bearer
// secret; a private context value restores only the immutable principal that
// authorized the original database reservation. ResumeGatewayCommand remains
// the atomic claim, so client retries and workers across replicas cannot both
// invoke the provider concurrently.
func (s *Server) RecoverGatewayCommands(ctx context.Context, limit int) error {
	commands, listErr := s.store.RecoverableGatewayCommands(ctx, timetext.Format(s.now()), limit)
	if len(commands) == 0 {
		return listErr
	}
	// Provider calls are potentially slow. A small fixed pool bounds memory and
	// upstream concurrency while allowing one stuck endpoint to stop neither a
	// later Gateway command nor the independent payment/Realtime workers.
	workerCount := min(4, len(commands))
	jobs := make(chan store.RecoverableGatewayCommand)
	errorsFound := make(chan error, len(commands))
	var workers sync.WaitGroup
	for range workerCount {
		workers.Go(func() {
			for command := range jobs {
				if err := s.recoverGatewayCommand(ctx, command); err != nil {
					errorsFound <- err
				}
			}
		})
	}
	for _, command := range commands {
		jobs <- command
	}
	close(jobs)
	workers.Wait()
	close(errorsFound)
	if listErr != nil {
		return listErr
	}
	for err := range errorsFound {
		return err
	}
	return nil
}

func (s *Server) recoverGatewayCommand(ctx context.Context, command store.RecoverableGatewayCommand) error {
	now := timetext.Format(s.now())
	fail := func(message string, permanent bool) error {
		if err := s.store.RecordGatewayRecoveryFailure(ctx, command.RequestID, message, now, permanent); err != nil {
			return fmt.Errorf("record Gateway recovery failure %s: %w", command.RequestID, err)
		}
		return errors.New(message)
	}
	var envelope providerctx.RecoveryRequest
	if err := json.Unmarshal(command.RecoveryRequest, &envelope); err != nil || envelope.Method != http.MethodPost || len(envelope.Body) == 0 || (!strings.HasPrefix(envelope.RequestURI, "/v1/") && !strings.HasPrefix(envelope.RequestURI, "/v1beta/")) {
		return fail("Gateway recovery request "+command.RequestID+" is invalid", true)
	}
	replayContext := providerctx.WithRecoveryExecution(ctx)
	replayContext = context.WithValue(replayContext, gatewayRecoveryPrincipalKey, command.Principal)
	request, err := http.NewRequestWithContext(replayContext, envelope.Method, "http://gateway-recovery.invalid"+envelope.RequestURI, bytes.NewReader(envelope.Body))
	if err != nil {
		return fail("construct Gateway recovery request "+command.RequestID+": "+err.Error(), true)
	}
	request.Header.Set("Idempotency-Key", command.IdempotencyKey)
	request.Header.Set("X-Request-ID", "recovery-"+command.RequestID)
	if envelope.ContentType != "" {
		request.Header.Set("Content-Type", envelope.ContentType)
	}
	response := &bufferedResponseWriter{header: make(http.Header)}
	s.handler.ServeHTTP(response, request)
	status, statusErr := s.store.GatewayCommandStatus(ctx, command.RequestID)
	if statusErr != nil {
		return fail("read Gateway recovery state "+command.RequestID+": "+statusErr.Error(), false)
	}
	if status == "succeeded" || status == "failed" || status == "cancelled" {
		return nil
	}
	if response.status == http.StatusConflict {
		var responseError struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(response.body.Bytes(), &responseError)
		if responseError.Error.Code == "idempotency_in_progress" {
			// A client or another replica won the atomic lease claim. Its
			// execution owns the next transition; the losing observer is not a
			// recovery failure and must not advance the failure counter.
			return nil
		}
		// A payload mismatch is not lease contention. The persisted envelope
		// can never reclaim this command and requires explicit reconciliation.
		return fail(fmt.Sprintf("Gateway recovery request %s returned conflict %s", command.RequestID, responseError.Error.Code), true)
	}
	permanent := response.status >= 400 && response.status < 500
	return fail(fmt.Sprintf("Gateway recovery request %s returned HTTP %d without a terminal state", command.RequestID, response.status), permanent)
}

// requireUserOrGatewayScope preserves the full User-session Account surface
// while exposing only explicitly scoped, own-account read projections to a
// Gateway key. A Gateway credential is bound to one account; sharing the same
// owner user with another account never broadens that key's tenant boundary.
func (s *Server) requireUserOrGatewayScope(requiredScope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, ok := bearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		userID, accountID, userErr := s.store.AuthenticateUserSession(r.Context(), secret)
		if userErr == nil {
			ctx := context.WithValue(r.Context(), userIDKey, userID)
			ctx = context.WithValue(ctx, accountIDKey, accountID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if !errors.Is(userErr, store.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, "internal_error", "authentication failed")
			return
		}
		hash := sha256.Sum256([]byte(secret))
		principal, gatewayErr := s.store.AuthenticateGatewayKey(r.Context(), hash[:], timetext.Format(s.now()))
		if gatewayErr != nil {
			if !errors.Is(gatewayErr, store.ErrNotFound) {
				writeError(w, http.StatusInternalServerError, "internal_error", "authentication failed")
				return
			}
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer token")
			return
		}
		if !gatewayPrincipalHasScope(principal, requiredScope) {
			writeError(w, http.StatusForbidden, "forbidden", "Gateway key lacks "+requiredScope+" scope")
			return
		}
		if target := r.PathValue("account_id"); target != "" && target != principal.AccountID {
			writeError(w, http.StatusForbidden, "forbidden", "Gateway key is bound to a different account")
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, principal.UserID)
		ctx = context.WithValue(ctx, accountIDKey, principal.AccountID)
		ctx = context.WithValue(ctx, apiKeyIDKey, principal.APIKeyID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func gatewayPrincipalHasScope(principal store.GatewayPrincipal, required string) bool {
	var scopes []string
	if json.Unmarshal(principal.Scopes, &scopes) != nil {
		return false
	}
	for alternative := range strings.SplitSeq(required, "|") {
		if contains(scopes, alternative) {
			return true
		}
	}
	return false
}

func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	if s.gateway == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "AI execution is not configured")
		return
	}
	var request gatewayservice.ChatRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Model == "" || len(request.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "model and messages are required")
		return
	}
	// Validate the explicit boundary before either the non-streaming provider
	// call or streaming response headers. A present zero is not the same as an
	// omitted optional limit and must never reserve the catalog default.
	if request.MaxTokens != nil && *request.MaxTokens <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "max_tokens must be positive")
		return
	}
	if request.Stream {
		s.streamChatCompletions(w, r, request)
		return
	}
	response, err := s.gateway.Chat(r.Context(), store.GatewayPrincipal{
		UserID: contextString(r.Context(), userIDKey), AccountID: contextString(r.Context(), accountIDKey),
		APIKeyID: contextString(r.Context(), apiKeyIDKey),
	}, r.Header.Get("Idempotency-Key"), request)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "model_not_found", "model is not available")
		case errors.Is(err, store.ErrInsufficientBalance):
			writeError(w, http.StatusPaymentRequired, "insufficient_balance", "available balance is insufficient")
		case errors.Is(err, store.ErrAccountFrozen):
			writeError(w, http.StatusLocked, "account_frozen", "account balance is frozen")
		case errors.Is(err, store.ErrIdempotencyConflict):
			writeError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was used with a different payload")
		case errors.Is(err, store.ErrCommandInProgress):
			writeError(w, http.StatusConflict, "idempotency_in_progress", "idempotent command is already being executed")
		default:
			writeError(w, http.StatusBadGateway, "provider_error", "AI request failed")
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response)
}

func (s *Server) streamChatCompletions(w http.ResponseWriter, r *http.Request, request gatewayservice.ChatRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unavailable", "streaming is unavailable")
		return
	}
	wroteHeader := false
	err := s.gateway.StreamChat(r.Context(), store.GatewayPrincipal{
		UserID: contextString(r.Context(), userIDKey), AccountID: contextString(r.Context(), accountIDKey),
		APIKeyID: contextString(r.Context(), apiKeyIDKey),
	}, r.Header.Get("Idempotency-Key"), request, func(chunk []byte) error {
		if !wroteHeader {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			wroteHeader = true
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", chunk); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err != nil {
		if !wroteHeader {
			// Lease contention and payload mismatch happen before the first
			// provider frame. Keeping headers lazy preserves their typed 409
			// outcomes for clients and the internal recovery worker.
			s.writeGatewayExecutionError(w, err)
			return
		}
		// Once frames have begun, a terminal SSE error keeps the wire format
		// valid without leaking provider or database details.
		_, _ = io.WriteString(w, "event: error\ndata: {\"error\":{\"code\":\"stream_failed\"}}\n\n")
		flusher.Flush()
		return
	}
	if !wroteHeader {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, ok := bearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		administratorID, err := s.store.AuthenticateAdminKey(r.Context(), secret)
		if errors.Is(err, store.ErrNotFound) {
			administratorID, err = s.store.AuthenticateAdminSession(r.Context(), secret, timetext.Format(s.now()))
		}
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusInternalServerError, "internal_error", "authentication failed")
				return
			}
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer token")
			return
		}
		ctx := context.WithValue(r.Context(), administratorIDKey, administratorID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) getCurrentUser(w http.ResponseWriter, r *http.Request) {
	user, err := s.store.GetUser(r.Context(), contextString(r.Context(), userIDKey))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) updateCurrentUser(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DisplayName string `json:"display_name"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	if request.DisplayName == "" || len(request.DisplayName) > 120 {
		writeError(w, http.StatusBadRequest, "invalid_request", "display_name must contain 1 to 120 characters")
		return
	}
	user, err := s.store.UpdateUser(r.Context(), contextString(r.Context(), userIDKey), request.DisplayName)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.store.ListAccounts(r.Context(), contextString(r.Context(), userIDKey))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": accounts})
}

func (s *Server) getBalance(w http.ResponseWriter, r *http.Request) {
	balance, err := s.store.GetBalance(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("account_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, balance)
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.ListAPIKeys(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("account_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": keys})
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Name      string   `json:"name"`
		Kind      string   `json:"kind"`
		Scopes    []string `json:"scopes"`
		ExpiresAt *string  `json:"expires_at"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateAPIKeyRequest(request.Name, request.Kind, request.Scopes, request.ExpiresAt, s.now()); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	payload, err := json.Marshal(request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not fingerprint request")
		return
	}
	payloadHash := sha256.Sum256(payload)
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not generate credential")
		return
	}
	secret := "giz_" + base64.RawURLEncoding.EncodeToString(secretBytes)
	hash := sha256.Sum256([]byte(secret))
	now := timetext.Format(s.now())
	key := store.APIKey{
		ID: uuid.NewString(), AccountID: r.PathValue("account_id"), Name: request.Name,
		Kind: request.Kind, KeyPrefix: secret[:12], Scopes: mustJSON(request.Scopes),
		Status: "active", ExpiresAt: request.ExpiresAt, CreatedAt: now,
	}
	created, replayed, err := s.store.CreateAPIKey(r.Context(), contextString(r.Context(), userIDKey), r.Header.Get("Idempotency-Key"), payloadHash[:], hash[:], key)
	if err != nil {
		if errors.Is(err, store.ErrIdempotencyConflict) {
			writeError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was used with a different payload")
			return
		}
		writeConflictOrDataError(w, err)
		return
	}
	if replayed {
		writeJSON(w, http.StatusOK, created)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		store.APIKey
		Secret string `json:"secret"`
	}{APIKey: created, Secret: secret})
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	err := s.store.RevokeAPIKey(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("account_id"), r.PathValue("api_key_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createMerchantAccount(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Name        string  `json:"name"`
		LegalName   string  `json:"legal_name"`
		PublicName  string  `json:"public_name"`
		CountryCode string  `json:"country_code"`
		WebsiteURL  *string `json:"website_url"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.LegalName) == "" ||
		strings.TrimSpace(request.PublicName) == "" || len(request.CountryCode) != 2 {
		writeError(w, http.StatusBadRequest, "invalid_request", "name, legal_name, public_name, and two-letter country_code are required")
		return
	}
	now := timetext.Format(s.now())
	merchant := store.MerchantAccount{
		Account:   store.Account{ID: uuid.NewString(), Kind: "merchant", Name: request.Name, Status: "active", CreatedAt: now},
		LegalName: request.LegalName, PublicName: request.PublicName,
		ReviewLevel: "basic", MerchantStatus: "pending",
		CountryCode: &request.CountryCode, WebsiteURL: request.WebsiteURL,
	}
	created, err := s.store.CreateMerchantAccount(r.Context(), contextString(r.Context(), userIDKey), merchant)
	if err != nil {
		writeConflictOrDataError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) listGatewayUsage(w http.ResponseWriter, r *http.Request) {
	query, err := accountListQuery(r, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	from, err := timetext.Normalize(r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "from must be an RFC3339 timestamp")
		return
	}
	to, err := timetext.Normalize(r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "to must be an RFC3339 timestamp")
		return
	}
	usage, err := s.store.ListGatewayUsagePage(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("account_id"), from, to, query)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeAccountPage(w, usage)
}

func (s *Server) listAccountTransactions(w http.ResponseWriter, r *http.Request) {
	query, err := accountListQuery(r, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	transactions, err := s.store.ListAccountTransactionsPage(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("account_id"), query)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeAccountPage(w, transactions)
}

func (s *Server) listInvoices(w http.ResponseWriter, r *http.Request) {
	invoices, err := s.store.ListInvoices(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("account_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page(invoices))
}

func (s *Server) listAccountCatalog(w http.ResponseWriter, r *http.Request) {
	models, err := s.store.ListAccountCatalog(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("account_id"), timetext.Format(s.now()))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page(models))
}

func (s *Server) getInvoice(w http.ResponseWriter, r *http.Request) {
	invoice, err := s.store.GetInvoice(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("account_id"), r.PathValue("invoice_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, invoice)
}

func (s *Server) createCreditTransfer(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		RecipientAccountID string             `json:"recipient_account_id"`
		Amount             store.CreditAmount `json:"amount"`
		Note               string             `json:"note"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	senderAccountID := r.PathValue("account_id")
	if request.RecipientAccountID == "" || request.RecipientAccountID == senderAccountID ||
		request.Amount.Asset != "GIZ_CREDIT" || request.Amount.Microcredits <= 0 || len(request.Note) > 500 {
		writeError(w, http.StatusBadRequest, "invalid_request", "recipient, positive GIZ_CREDIT amount, and a distinct sender are required")
		return
	}
	if !s.consumeRateLimit(w, r, "account:"+senderAccountID, "credit.transfer", 60, 5) {
		return
	}
	payload, err := json.Marshal(request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not fingerprint request")
		return
	}
	payloadHash := sha256.Sum256(payload)
	now := timetext.Format(s.now())
	transfer := store.CreditTransfer{
		ID: uuid.NewString(), SenderAccountID: senderAccountID,
		RecipientAccountID: request.RecipientAccountID, Amount: request.Amount,
		Status: "succeeded", Note: request.Note, CreatedAt: now, CompletedAt: &now,
	}
	created, replayed, err := s.store.CreateCreditTransfer(r.Context(), contextString(r.Context(), userIDKey), r.Header.Get("Idempotency-Key"), payloadHash[:], transfer)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "resource not found")
		case errors.Is(err, store.ErrInsufficientBalance):
			writeError(w, http.StatusConflict, "insufficient_balance", "available balance is insufficient")
		case errors.Is(err, store.ErrAccountFrozen):
			writeError(w, http.StatusLocked, "account_frozen", "account balance is frozen")
		case errors.Is(err, store.ErrIdempotencyConflict):
			writeError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was used with a different payload")
		default:
			writeDataError(w, err)
		}
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, created)
}

func (s *Server) listCreditTransfers(w http.ResponseWriter, r *http.Request) {
	query, err := accountListQuery(r, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	transfers, err := s.store.ListCreditTransfersPage(r.Context(), contextString(r.Context(), userIDKey), r.PathValue("account_id"), query)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeAccountPage(w, transfers)
}

func validateAPIKeyRequest(name, kind string, scopes []string, expiresAt *string, now time.Time) error {
	if strings.TrimSpace(name) == "" || len(name) > 120 {
		return errors.New("name must contain 1 to 120 characters")
	}
	allowed := map[string]map[string]bool{
		"gateway": {"gateway:invoke": true, "gateway:usage:read": true, "account:self": true},
		"payment": {"pay:intents:write": true, "pay:transactions:read": true, "pay:webhooks:write": true},
	}
	kindScopes, ok := allowed[kind]
	if !ok || len(scopes) == 0 {
		return errors.New("kind and at least one scope are required")
	}
	seen := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		if !kindScopes[scope] || seen[scope] {
			return errors.New("scope is not allowed for credential kind")
		}
		seen[scope] = true
	}
	if expiresAt != nil {
		expiry, err := timetext.Parse(*expiresAt)
		if err != nil || !expiry.After(now) {
			return errors.New("expires_at must be a future RFC3339 timestamp")
		}
		*expiresAt = timetext.Format(expiry)
	}
	return nil
}

func mustJSON(value any) store.JSON {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return store.JSON(encoded)
}

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	query, err := parseAdminListQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	models, err := s.store.ListModelsPage(r.Context(), query)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeAdminPage(w, models)
}

func (s *Server) listPublicModels(w http.ResponseWriter, r *http.Request) {
	protocol := "openai"
	if r.Header.Get("x-api-key") != "" && r.Header.Get("Authorization") == "" {
		protocol = "anthropic"
	}
	models, err := s.store.ListPublicModelsForAccount(r.Context(), contextString(r.Context(), accountIDKey), protocol, s.now())
	if err != nil {
		writeDataError(w, err)
		return
	}
	if r.Header.Get("x-api-key") != "" && r.Header.Get("Authorization") == "" {
		data := make([]map[string]any, 0, len(models))
		for _, model := range models {
			data = append(data, map[string]any{
				"id": model.ID, "type": "model", "display_name": model.ID,
				"created_at": time.Unix(model.Created, 0).UTC().Format(time.RFC3339),
			})
		}
		response := map[string]any{"data": data, "has_more": false}
		if len(models) > 0 {
			response["first_id"], response["last_id"] = models[0].ID, models[len(models)-1].ID
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": models})
}

func (s *Server) listGeminiModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.store.ListPublicModelsForAccount(r.Context(), contextString(r.Context(), accountIDKey), "gemini", s.now())
	if err != nil {
		writeDataError(w, err)
		return
	}
	data := make([]map[string]any, 0, len(models))
	for _, model := range models {
		data = append(data, map[string]any{
			"name": "models/" + model.ID, "baseModelId": model.ID, "version": model.ID,
			"displayName":                model.ID,
			"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": data})
}

func (s *Server) createModel(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var model store.Model
	if err := decodeJSON(r, &model); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if model.Slug == "" || model.Name == "" || !json.Valid(model.Modality) {
		writeError(w, http.StatusBadRequest, "invalid_request", "slug, name, and modality are required")
		return
	}
	created, err := s.store.CreateModel(r.Context(), contextString(r.Context(), administratorIDKey), model)
	if err != nil {
		writeConflictOrDataError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) updateModel(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Name == "" || !oneOf(request.Status, "active", "deprecated", "disabled") {
		writeError(w, http.StatusBadRequest, "invalid_request", "name and a valid status are required")
		return
	}
	model, err := s.store.UpdateModel(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("model_id"), request.Name, request.Status)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model)
}

func (s *Server) listModelVariants(w http.ResponseWriter, r *http.Request) {
	variants, err := s.store.ListModelVariants(r.Context(), r.PathValue("model_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": variants})
}

func (s *Server) createModelVariant(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var variant store.ModelVariant
	if err := decodeJSON(r, &variant); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	variant.ModelID = r.PathValue("model_id")
	if variant.ProviderEndpointID == "" || variant.ProviderModelName == "" || variant.VariantSlug == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "provider endpoint, provider model name, and variant slug are required")
		return
	}
	created, err := s.store.CreateModelVariant(r.Context(), contextString(r.Context(), administratorIDKey), variant)
	if err != nil {
		writeConflictOrDataError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) updateModelVariant(w http.ResponseWriter, r *http.Request) {
	var variant store.ModelVariant
	if err := decodeJSON(r, &variant); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	variant.ID = r.PathValue("variant_id")
	if variant.ProviderModelName == "" || !json.Valid(variant.Capabilities) ||
		!oneOf(variant.Status, "active", "degraded", "disabled") {
		writeError(w, http.StatusBadRequest, "invalid_request", "provider_model_name, capabilities, and valid status are required")
		return
	}
	updated, err := s.store.UpdateModelVariant(r.Context(), contextString(r.Context(), administratorIDKey), variant)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) listModelPrices(w http.ResponseWriter, r *http.Request) {
	prices, err := s.store.ListModelPrices(r.Context(), r.PathValue("variant_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": prices})
}

func (s *Server) createModelPrice(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var price store.ModelPrice
	if err := decodeJSON(r, &price); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	price.ModelVariantID = r.PathValue("variant_id")
	if err := validatePrice(&price); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	created, err := s.store.CreateModelPrice(r.Context(), contextString(r.Context(), administratorIDKey), price)
	if err != nil {
		writeConflictOrDataError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func validatePrice(price *store.ModelPrice) error {
	if price.ModelVariantID == "" || price.Metric == "" || price.UnitSize <= 0 || price.ValidFrom == "" {
		return errors.New("variant, metric, positive unit size, and valid_from are required")
	}
	if price.DiscountBPS < 0 || price.DiscountBPS > 10_000 {
		return errors.New("discount_bps must be between 0 and 10000")
	}
	allowedMetrics := map[string]bool{
		"input_token": true, "output_token": true, "cached_input_token": true,
		"input_audio_token": true, "output_audio_token": true,
		"audio_second": true, "image": true, "video_second": true, "request": true,
	}
	if !allowedMetrics[price.Metric] {
		return errors.New("unsupported price metric")
	}
	if price.UpstreamCostMicrocredits < 0 || price.BaseCustomerPriceMicrocredits < 0 || price.CustomerPriceMicrocredits < 0 {
		return errors.New("price values cannot be negative")
	}
	if price.CustomerPriceMicrocredits > price.BaseCustomerPriceMicrocredits {
		return errors.New("effective customer price cannot exceed the baseline")
	}
	factor := int64(10_000 - price.DiscountBPS)
	if factor != 0 && price.BaseCustomerPriceMicrocredits > math.MaxInt64/factor {
		return errors.New("baseline price is too large")
	}
	expected := price.BaseCustomerPriceMicrocredits * factor / 10_000
	if expected != price.CustomerPriceMicrocredits {
		return fmt.Errorf("effective customer price must equal baseline after discount; expected %d", expected)
	}
	validFrom, err := timetext.Parse(price.ValidFrom)
	if err != nil {
		return errors.New("valid_from must be an RFC3339 timestamp")
	}
	if price.ValidUntil != nil {
		validUntil, err := timetext.Parse(*price.ValidUntil)
		if err != nil || !validUntil.After(validFrom) {
			return errors.New("valid_until must be an RFC3339 timestamp after valid_from")
		}
		canonical := timetext.Format(validUntil)
		price.ValidUntil = &canonical
	}
	price.ValidFrom = timetext.Format(validFrom)
	return nil
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, secret, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	return secret, ok && strings.EqualFold(scheme, "Bearer") && secret != ""
}

// gatewayToken accepts the native credential location used by each supported
// compatible SDK while resolving every value as the same Gizway Gateway key.
// Provider credentials are never accepted on the public API.
func gatewayToken(r *http.Request) (string, bool) {
	if secret, ok := bearerToken(r); ok {
		return secret, true
	}
	if secret := r.Header.Get("x-api-key"); secret != "" {
		return secret, true
	}
	if secret := r.Header.Get("x-goog-api-key"); secret != "" {
		return secret, true
	}
	// Only Gemini SDK routes define a query-string key. Accepting it on the
	// OpenAI/Anthropic surfaces would put a reusable secret into access logs,
	// proxy traces and browser history for no compatibility benefit.
	if strings.HasPrefix(r.URL.Path, "/v1beta/") || strings.HasPrefix(r.URL.Path, "/upload/v1beta/") {
		if secret := r.URL.Query().Get("key"); secret != "" {
			return secret, true
		}
	}
	return "", false
}

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) bool {
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 255 {
		writeError(w, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key must contain 8 to 255 characters")
		return false
	}
	return true
}

func contextString(ctx context.Context, key contextKey) string {
	value, _ := ctx.Value(key).(string)
	return value
}

func decodeJSON(r *http.Request, destination any) error {
	const maximumJSONBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(r.Body, maximumJSONBody+1))
	if err != nil {
		return fmt.Errorf("read JSON: %w", err)
	}
	if len(body) > maximumJSONBody {
		return errors.New("decode JSON: request body exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode JSON: request body must contain exactly one JSON value")
		}
		return fmt.Errorf("decode JSON: %w", err)
	}
	return nil
}

func page[T any](items []T) map[string]any {
	return map[string]any{
		"data": items,
		"page": map[string]any{"next_cursor": nil, "has_more": false},
	}
}

func accountListQuery(r *http.Request, allowAPIKeyFilter bool) (store.AccountListQuery, error) {
	values := r.URL.Query()
	query := store.AccountListQuery{Cursor: values.Get("cursor")}
	if len(query.Cursor) > 255 {
		return query, errors.New("cursor is too long")
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			return query, errors.New("limit must be between 1 and 100")
		}
		query.Limit = limit
	}
	if query.Cursor != "" {
		offset, err := strconv.Atoi(query.Cursor)
		if err != nil || offset < 0 {
			return query, errors.New("cursor is invalid")
		}
	}
	if allowAPIKeyFilter {
		query.APIKeyID = strings.TrimSpace(values.Get("api_key_id"))
	}
	return query, nil
}

func writeAccountPage[T any](w http.ResponseWriter, result store.AccountPage[T]) {
	writeJSON(w, http.StatusOK, map[string]any{
		"data": result.Items,
		"page": map[string]any{"next_cursor": result.NextCursor, "has_more": result.HasMore},
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeDataError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeConflictOrDataError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrIdempotencyConflict) || errors.Is(err, store.ErrCredentialConsumed) {
		writeError(w, http.StatusConflict, "idempotency_conflict", "command conflicts with existing state")
		return
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "unique") || strings.Contains(message, "constraint") {
		writeError(w, http.StatusConflict, "conflict", "resource conflicts with existing data")
		return
	}
	writeDataError(w, err)
}

func oneOf(value string, choices ...string) bool {
	return slices.Contains(choices, value)
}

func contains(values []string, target string) bool {
	return slices.Contains(values, target)
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
