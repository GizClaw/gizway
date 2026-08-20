package gizway

import (
	"bytes"
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"

	bifrostadapter "github.com/idy/gizway/internal/adapter/bifrost"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/subscriptionkey"
)

type priceRow struct {
	Metric string `db:"metric" json:"metric"`
	Unit   int64  `db:"unit_size" json:"unit_size"`
	Amount int64  `db:"amount" json:"amount_microcredits"`
}

type resolvedCall struct {
	ProductID, ModelID, PublicModel, ProviderID, ProviderModel string
	KeyID, MerchantID                                          string
	AccountID, SubscriptionID, SubscriptionKeyID               string
	OwnerIssuer, OwnerSubject                                  string
	Target                                                     store.ProviderExecutionTarget
	Targets                                                    []store.ProviderExecutionTarget
	CustomerPrices, CommissionPrices                           []priceRow
	Payments                                                   map[string]keyPayment
	StartedAt                                                  time.Time
}

type keyPayment struct {
	MerchantID string
	Prices     []priceRow
}

type chatRequest struct {
	Model         string          `json:"model"`
	Messages      []chatMessage   `json:"messages"`
	Stream        bool            `json:"stream"`
	MaxTokens     *int            `json:"max_tokens,omitempty"`
	StreamOptions json.RawMessage `json:"stream_options,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	TopK          *int            `json:"top_k,omitempty"`
	Stop          []string        `json:"stop,omitempty"`
}

func (request chatRequest) parameters() (*schemas.ChatParameters, error) {
	parameters := &schemas.ChatParameters{
		MaxCompletionTokens: request.MaxTokens,
		Temperature:         request.Temperature,
		TopP:                request.TopP,
		TopK:                request.TopK,
		Stop:                request.Stop,
	}
	if len(request.StreamOptions) != 0 && string(request.StreamOptions) != "null" {
		var streamOptions schemas.ChatStreamOptions
		if err := json.Unmarshal(request.StreamOptions, &streamOptions); err != nil {
			return nil, fmt.Errorf("invalid stream_options: %w", err)
		}
		parameters.StreamOptions = &streamOptions
		if streamOptions.IncludeObfuscation != nil {
			parameters.ExtraParams = map[string]any{
				"stream_options": map[string]any{"include_obfuscation": *streamOptions.IncludeObfuscation},
			}
		}
	}
	return parameters, nil
}

func protocolHeaders(r *http.Request, protocol string) (map[string][]string, error) {
	if protocol != "anthropic" {
		return nil, nil
	}
	version := strings.TrimSpace(r.Header.Get("anthropic-version"))
	if version == "" {
		return nil, errors.New("anthropic-version is required")
	}
	return map[string][]string{"anthropic-version": {version}}, nil
}

type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (message chatMessage) text() (string, error) {
	var text string
	if json.Unmarshal(message.Content, &text) == nil {
		return text, nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(message.Content, &blocks); err != nil {
		return "", err
	}
	var result strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			result.WriteString(block.Text)
		}
	}
	return result.String(), nil
}

func stringMessage(role, content string) chatMessage {
	raw, _ := json.Marshal(content)
	return chatMessage{Role: role, Content: raw}
}

type realtimeSession struct {
	Secret, KeyHMAC, ProviderURL string
	ProviderHeader               http.Header
	Call                         resolvedCall
	ExpiresAt                    time.Time
}

func (h *Handler) protocol(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/openai/v1/realtime" {
		h.realtimeSocket(w, r)
		return
	}
	rawKey, keyErr := protocolSubscriptionKey(r)
	if keyErr != nil {
		errJSON(w, http.StatusUnauthorized, "invalid_subscription_key", "Subscription Key is required")
		return
	}
	keyHMAC := subscriptionkey.HMAC(h.config.HMACSecret, rawKey)
	admission, admissionErr := h.admit(r.Context(), keyHMAC)
	if admissionErr != nil {
		h.diagnostic("Credit Check failed", admissionErr)
		errJSON(w, http.StatusServiceUnavailable, "credit_check_unavailable", "Credit Check unavailable")
		return
	}
	if !admission.allowed {
		errJSON(w, http.StatusPaymentRequired, "credit_denied", "Credit denied")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/openai/v1/models" {
		rows, err := h.many(`SELECT name id FROM client_sync.models WHERE status='active' ORDER BY name`)
		if err != nil {
			internal(w)
			return
		}
		for _, model := range rows {
			model["object"] = "model"
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": rows})
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/openai/v1/chat/completions" {
		h.chat(w, r, keyHMAC, admission, "openai")
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/anthropic/v1/messages" {
		h.chat(w, r, keyHMAC, admission, "anthropic")
		return
	}
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/genai/v1beta/models/") {
		h.chat(w, r, keyHMAC, admission, "gemini")
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/openai/v1/realtime/client_secrets" {
		h.createRealtimeSecret(w, r, keyHMAC, admission)
		return
	}
	errJSON(w, http.StatusNotImplemented, "not_implemented", "protocol handler not implemented")
}

func protocolSubscriptionKey(r *http.Request) (string, error) {
	values := []string{}
	if raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && raw != "" {
		values = append(values, raw)
	}
	for _, header := range []string{"x-api-key", "x-goog-api-key"} {
		if value := r.Header.Get(header); value != "" {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return "", errors.New("missing Subscription Key")
	}
	for _, value := range values[1:] {
		if !hmac.Equal([]byte(values[0]), []byte(value)) {
			return "", errors.New("conflicting Subscription Keys")
		}
	}
	return values[0], nil
}

func (h *Handler) createRealtimeSecret(w http.ResponseWriter, r *http.Request, keyHMAC string, admission creditAdmission) {
	var body struct {
		Model     string `json:"model"`
		Transport string `json:"transport"`
	}
	if decode(r, &body) != nil || body.Model == "" || body.Transport != "websocket" {
		invalid(w)
		return
	}
	call, err := h.resolveCall(r.Context(), body.Model, admission.productID)
	admission.applyTo(&call)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) && call.ModelID == "" {
			errJSON(w, http.StatusNotFound, "model_not_found", "model not found")
			return
		}
		errJSON(w, http.StatusServiceUnavailable, "no_active_provider_key", "no active Provider Key")
		return
	}
	providerURL, providerHeader, selectedKeyID, err := h.engine.RealtimeWebSocketRouteCandidates(r.Context(), call.Targets)
	if err != nil {
		errJSON(w, http.StatusBadGateway, "provider_error", "Provider realtime route unavailable")
		return
	}
	call, err = call.selected(selectedKeyID)
	if err != nil {
		internal(w)
		return
	}
	sessionID := "rts_" + uuid.NewString()
	secret := "rtk_" + uuid.NewString() + uuid.NewString()
	expiresAt := h.config.Now().UTC().Add(h.config.RealtimeSessionTTL)
	h.pruneRealtimeSessions()
	h.realtimeMu.Lock()
	h.realtime[sessionID] = realtimeSession{Secret: secret, KeyHMAC: keyHMAC, Call: call, ProviderURL: providerURL, ProviderHeader: providerHeader, ExpiresAt: expiresAt}
	h.realtimeMu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_secret": map[string]any{"value": secret, "expires_at": expiresAt.Unix()},
		"session":       map[string]any{"session_id": sessionID, "transport": "websocket", "model": body.Model},
	})
}

func (h *Handler) pruneRealtimeSessions() {
	now := h.config.Now().UTC()
	h.realtimeMu.Lock()
	defer h.realtimeMu.Unlock()
	for sessionID, session := range h.realtime {
		if !now.Before(session.ExpiresAt) {
			delete(h.realtime, sessionID)
		}
	}
}

func (h *Handler) realtimeSocket(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	h.realtimeMu.Lock()
	session, ok := h.realtime[sessionID]
	if ok && h.config.Now().UTC().Before(session.ExpiresAt) && hmac.Equal([]byte(secret), []byte(session.Secret)) {
		delete(h.realtime, sessionID)
	} else {
		ok = false
	}
	h.realtimeMu.Unlock()
	if !ok {
		errJSON(w, http.StatusUnauthorized, "invalid_realtime_secret", "invalid realtime secret")
		return
	}
	client, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer client.Close(websocket.StatusNormalClosure, "session complete")
	provider, _, err := websocket.Dial(r.Context(), session.ProviderURL, &websocket.DialOptions{HTTPHeader: session.ProviderHeader})
	if err != nil {
		h.recordExecution(context.Background(), session.Call, nil, 0, 0, "realtime", "", "error", err, "")
		_ = client.Close(websocket.StatusInternalError, "provider unavailable")
		return
	}
	defer provider.Close(websocket.StatusNormalClosure, "session complete")
	executionLogged := false
	executionErr := errors.New("realtime session ended before terminal usage")
	defer func() {
		if !executionLogged {
			h.recordExecution(context.Background(), session.Call, nil, 0, 0, "realtime", "", "error", executionErr, "")
		}
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	clientToProvider := make(chan error, 1)
	go func() {
		for {
			kind, raw, readErr := client.Read(ctx)
			if readErr != nil {
				cancel()
				clientToProvider <- readErr
				return
			}
			translated, translateErr := h.engine.RealtimeClientEvent(ctx, session.Call.Target, raw)
			if translateErr != nil {
				cancel()
				clientToProvider <- translateErr
				return
			}
			if writeErr := provider.Write(ctx, kind, translated); writeErr != nil {
				cancel()
				clientToProvider <- writeErr
				return
			}
		}
	}()
	for {
		select {
		case executionErr = <-clientToProvider:
			return
		default:
		}
		kind, raw, readErr := provider.Read(ctx)
		if readErr != nil {
			executionErr = readErr
			return
		}
		public, usage, terminal, translateErr := h.engine.RealtimeProviderEvent(ctx, session.Call.Target, raw)
		if translateErr != nil {
			executionErr = translateErr
			return
		}
		if writeErr := client.Write(ctx, kind, public); writeErr != nil {
			executionErr = writeErr
			return
		}
		if terminal {
			if usage == nil {
				executionErr = errors.New("realtime terminal event lacks usage")
				return
			}
			if usedErr := h.markProviderKeyUsed(context.Background(), session.Call.KeyID, h.config.Now().UTC()); usedErr != nil {
				h.diagnostic("update Provider Key last_used_at", usedErr, "selected_key_id", session.Call.KeyID)
			}
			gross, commission, rateErr := ratedUsage(usage, session.Call.CustomerPrices, session.Call.CommissionPrices)
			if rateErr != nil {
				executionErr = rateErr
				return
			}
			h.consumeLocalCredit(session.KeyHMAC, gross)
			if completeErr := h.completeCall(context.Background(), session.KeyHMAC, session.Call, gross, commission, usage, "realtime"); completeErr != nil {
				h.diagnostic("Realtime Provider result returned without billing", completeErr, "selected_key_id", session.Call.KeyID)
			}
			executionLogged = true
			return
		}
	}
}

func (h *Handler) admit(ctx context.Context, keyHMAC string) (creditAdmission, error) {
	for {
		h.creditMu.Lock()
		state := h.credits[keyHMAC]
		now := h.config.Now().UTC()
		if state != nil && state.loading {
			wait := state.wait
			h.creditMu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return creditAdmission{}, ctx.Err()
			}
		}
		if state != nil && now.Before(state.expires) {
			admission := state.snapshot()
			h.creditMu.Unlock()
			return admission, nil
		}
		if state == nil {
			state = &creditState{}
			h.credits[keyHMAC] = state
		}
		state.loading = true
		state.wait = make(chan struct{})
		h.creditMu.Unlock()

		result, err := h.checkCredit(ctx, keyHMAC)
		h.creditMu.Lock()
		state.loading = false
		close(state.wait)
		if err != nil {
			if h.credits[keyHMAC] == state {
				delete(h.credits, keyHMAC)
			}
			h.creditMu.Unlock()
			return creditAdmission{}, err
		}
		state.available, state.admission = result.available, result.admission
		state.expires = result.expires
		admission := state.snapshot()
		h.creditMu.Unlock()
		return admission, nil
	}
}

func (state *creditState) snapshot() creditAdmission {
	admission := state.admission
	admission.allowed = admission.allowed && state.available > 0
	return admission
}

func (admission creditAdmission) applyTo(call *resolvedCall) {
	call.ProductID = admission.productID
	call.AccountID, call.SubscriptionID = admission.accountID, admission.subscriptionID
	call.SubscriptionKeyID = admission.subscriptionKeyID
	call.OwnerIssuer, call.OwnerSubject = admission.ownerIssuer, admission.ownerSubject
}

type creditCheckResult struct {
	admission creditAdmission
	available int64
	expires   time.Time
}

func (h *Handler) checkCredit(ctx context.Context, keyHMAC string) (creditCheckResult, error) {
	if h.config.GizPayURL == "" || h.config.ServiceToken == nil {
		return creditCheckResult{}, errors.New("credit check is not configured")
	}
	token, err := h.config.ServiceToken(ctx)
	if err != nil {
		return creditCheckResult{}, err
	}
	body, _ := json.Marshal(map[string]any{"subscription_key_hmac": keyHMAC})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.config.GizPayURL, "/")+"/service/v1/subscription-credit-checks", bytes.NewReader(body))
	if err != nil {
		return creditCheckResult{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := h.config.HTTPClient.Do(request)
	if err != nil {
		return creditCheckResult{}, err
	}
	defer response.Body.Close()
	var result struct {
		Status              string    `json:"status"`
		ProductID           string    `json:"product_id"`
		BillingMode         string    `json:"billing_mode"`
		Available           int64     `json:"available_microcredits"`
		RecheckAfterSeconds int64     `json:"recheck_after_seconds"`
		AccountID           string    `json:"account_id"`
		SubscriptionID      string    `json:"subscription_id"`
		SubscriptionKeyID   string    `json:"subscription_key_id"`
		OwnerIssuer         string    `json:"owner_identity_issuer"`
		OwnerSubject        string    `json:"owner_identity_subject"`
		CheckedAt           time.Time `json:"checked_at"`
	}
	if response.StatusCode != http.StatusOK {
		return creditCheckResult{}, fmt.Errorf("credit check returned %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return creditCheckResult{}, err
	}
	if result.RecheckAfterSeconds <= 0 || result.CheckedAt.IsZero() || result.Status != "allowed" && result.Status != "denied" {
		return creditCheckResult{}, errors.New("credit check returned invalid cache metadata")
	}
	return creditCheckResult{
		admission: creditAdmission{
			accountID: result.AccountID, subscriptionID: result.SubscriptionID,
			subscriptionKeyID: result.SubscriptionKeyID,
			productID:         result.ProductID, billing: result.BillingMode,
			ownerIssuer: result.OwnerIssuer, ownerSubject: result.OwnerSubject,
			allowed: result.Status == "allowed",
		},
		available: result.Available,
		expires:   result.CheckedAt.Add(time.Duration(result.RecheckAfterSeconds) * time.Second),
	}, nil
}

func (h *Handler) resolveCall(ctx context.Context, modelName, productID string) (resolvedCall, error) {
	var call resolvedCall
	call.ProductID, call.PublicModel = productID, modelName
	call.StartedAt = h.config.Now().UTC()
	err := h.config.DB.QueryRowx(`SELECT id,provider_id,provider_model FROM client_sync.models WHERE name=$1 AND status='active'`, modelName).Scan(&call.ModelID, &call.ProviderID, &call.ProviderModel)
	if err != nil {
		return call, err
	}
	if err := h.config.DB.Select(&call.CustomerPrices, `SELECT metric,unit_size,price_microcredits amount FROM model_customer_prices WHERE model_id=$1 ORDER BY metric`, call.ModelID); err != nil {
		return call, err
	}
	provider, err := h.stores.Provider(ctx, call.ProviderID)
	if err != nil || provider.Status != "active" {
		return call, sql.ErrNoRows
	}
	keys, err := h.stores.Keys(ctx, call.ProviderID)
	if err != nil {
		return call, err
	}
	call.Payments = map[string]keyPayment{}
	for _, key := range keys {
		if !key.Enabled || key.Status != "active" {
			continue
		}
		var merchantID, billingStatus string
		if err := h.config.DB.QueryRowx(`SELECT merchant_id,status FROM provider_key_billing WHERE provider_key_id=$1`, key.ID).Scan(&merchantID, &billingStatus); err != nil || billingStatus != "active" {
			continue
		}
		target := store.ProviderExecutionTarget{Provider: provider.Kind, Endpoint: provider.BaseURL, Credential: key.APIKey, RouteKey: key.ID, Weight: key.Weight}
		target.Model = call.ProviderModel
		var prices []priceRow
		if err := h.config.DB.Select(&prices, `SELECT metric,unit_size,microcredits_per_unit amount FROM provider_key_prices WHERE model_id=$1 AND provider_key_id=$2 ORDER BY metric`, call.ModelID, target.RouteKey); err != nil {
			return call, err
		}
		if !pricesCoverMetrics(prices, call.CustomerPrices) {
			continue
		}
		call.Targets = append(call.Targets, target)
		call.Payments[target.RouteKey] = keyPayment{MerchantID: merchantID, Prices: prices}
	}
	if len(call.Targets) == 0 {
		return call, sql.ErrNoRows
	}
	call.Targets = cheapestTargets(call.Targets, call.Payments)
	return call, nil
}

func pricesCoverMetrics(providerPrices, customerPrices []priceRow) bool {
	missing := make(map[string]struct{}, len(customerPrices))
	for _, price := range customerPrices {
		missing[price.Metric] = struct{}{}
	}
	if len(missing) == 0 {
		return false
	}
	for _, price := range providerPrices {
		delete(missing, price.Metric)
	}
	return len(missing) == 0
}

// cheapestTargets preserves Bifrost's load balancing only among Provider Keys
// with the same lowest normalized procurement-price vector.
func cheapestTargets(targets []store.ProviderExecutionTarget, payments map[string]keyPayment) []store.ProviderExecutionTarget {
	if len(targets) < 2 {
		return targets
	}
	sort.SliceStable(targets, func(i, j int) bool {
		return comparePrices(payments[targets[i].RouteKey].Prices, payments[targets[j].RouteKey].Prices) < 0
	})
	cheapest := payments[targets[0].RouteKey].Prices
	end := 1
	for end < len(targets) && comparePrices(cheapest, payments[targets[end].RouteKey].Prices) == 0 {
		end++
	}
	return targets[:end]
}

func comparePrices(left, right []priceRow) int {
	l, r := append([]priceRow(nil), left...), append([]priceRow(nil), right...)
	sort.Slice(l, func(i, j int) bool { return l[i].Metric < l[j].Metric })
	sort.Slice(r, func(i, j int) bool { return r[i].Metric < r[j].Metric })
	for index := 0; index < len(l) && index < len(r); index++ {
		if l[index].Metric != r[index].Metric {
			return strings.Compare(l[index].Metric, r[index].Metric)
		}
		comparison := new(big.Rat).SetFrac64(l[index].Amount, l[index].Unit).Cmp(new(big.Rat).SetFrac64(r[index].Amount, r[index].Unit))
		if comparison != 0 {
			return comparison
		}
	}
	if len(l) < len(r) {
		return -1
	}
	if len(l) > len(r) {
		return 1
	}
	return 0
}

func (call resolvedCall) selected(keyID string) (resolvedCall, error) {
	payment, ok := call.Payments[keyID]
	if !ok {
		return call, fmt.Errorf("bifrost selected unknown key %q", keyID)
	}
	for _, target := range call.Targets {
		if target.RouteKey == keyID {
			call.KeyID, call.MerchantID, call.Target = keyID, payment.MerchantID, target
			call.CommissionPrices = payment.Prices
			return call, nil
		}
	}
	return call, fmt.Errorf("bifrost selected missing target %q", keyID)
}

func (h *Handler) chat(w http.ResponseWriter, r *http.Request, keyHMAC string, admission creditAdmission, protocol string) {
	var body chatRequest
	if protocol == "gemini" {
		modelOperation := strings.TrimPrefix(r.URL.Path, "/genai/v1beta/models/")
		body.Stream = strings.HasSuffix(modelOperation, ":streamGenerateContent")
		body.Model = strings.TrimSuffix(strings.TrimSuffix(modelOperation, ":generateContent"), ":streamGenerateContent")
		var gemini struct {
			Contents []struct {
				Role  string `json:"role,omitempty"`
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
			GenerationConfig struct {
				MaxOutputTokens *int     `json:"maxOutputTokens"`
				Temperature     *float64 `json:"temperature"`
				TopP            *float64 `json:"topP"`
				TopK            *int     `json:"topK"`
				StopSequences   []string `json:"stopSequences"`
			} `json:"generationConfig"`
		}
		if decode(r, &gemini) != nil {
			invalid(w)
			return
		}
		if len(gemini.Contents) == 0 {
			invalid(w)
			return
		}
		for _, content := range gemini.Contents {
			for _, part := range content.Parts {
				body.Messages = append(body.Messages, stringMessage("user", part.Text))
			}
		}
		body.MaxTokens = gemini.GenerationConfig.MaxOutputTokens
		body.Temperature = gemini.GenerationConfig.Temperature
		body.TopP = gemini.GenerationConfig.TopP
		body.TopK = gemini.GenerationConfig.TopK
		body.Stop = gemini.GenerationConfig.StopSequences
	} else if decode(r, &body) != nil {
		invalid(w)
		return
	}
	if !nonBlank(body.Model) || len(body.Messages) == 0 || body.MaxTokens != nil && *body.MaxTokens < 1 {
		invalid(w)
		return
	}
	for _, message := range body.Messages {
		if !nonBlank(message.Role) {
			invalid(w)
			return
		}
	}
	parameters, err := body.parameters()
	if err != nil {
		invalid(w)
		return
	}
	passthroughHeaders, err := protocolHeaders(r, protocol)
	if err != nil {
		invalid(w)
		return
	}
	call, err := h.resolveCall(r.Context(), body.Model, admission.productID)
	admission.applyTo(&call)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) && call.ModelID == "" {
			errJSON(w, http.StatusNotFound, "model_not_found", "model not found")
			return
		}
		errJSON(w, http.StatusServiceUnavailable, "no_active_provider_key", "no active Provider Key")
		return
	}
	messages := make([]schemas.ChatMessage, len(body.Messages))
	for index, message := range body.Messages {
		text, textErr := message.text()
		if textErr != nil || !nonBlank(text) {
			invalid(w)
			return
		}
		messages[index] = schemas.ChatMessage{Role: schemas.ChatMessageRole(message.Role), Content: &schemas.ChatMessageContent{ContentStr: &text}}
	}
	if body.Stream {
		h.streamChat(w, r, keyHMAC, call, messages, parameters, passthroughHeaders, protocol)
		return
	}
	response, err := h.engine.ChatCompletionCandidatesWithHeaders(r.Context(), call.Targets, messages, parameters, passthroughHeaders)
	if err != nil {
		call = h.callForExecutionLog(call, bifrostadapter.SelectedKeyID(err))
		h.recordExecution(r.Context(), call, nil, 0, 0, "non_streaming", "", "error", err, "")
		errJSON(w, http.StatusBadGateway, "provider_error", "Provider call failed")
		return
	}
	failpoint := r.Header.Get("X-Gizway-Test-Failpoint")
	actualSelectedKeyID := response.ExtraFields.RoutingInfo.Key
	if actualSelectedKeyID != "" {
		if usedErr := h.markProviderKeyUsed(context.WithoutCancel(r.Context()), actualSelectedKeyID, h.config.Now().UTC()); usedErr != nil {
			h.diagnostic("update Provider Key last_used_at", usedErr, "selected_key_id", actualSelectedKeyID)
		}
	}
	response.Model = body.Model
	publicResponse, err := publicChatResponse(protocol, response, body.Model)
	if err != nil {
		logCall := h.callForExecutionLog(call, actualSelectedKeyID)
		h.recordExecution(r.Context(), logCall, response.Usage, 0, 0, "non_streaming", "", "error", err, failpoint)
		h.diagnostic("Provider response conversion failed", err, "protocol", protocol, "model", body.Model)
		errJSON(w, http.StatusBadGateway, "invalid_provider_response", "Provider response cannot be converted to the public protocol")
		return
	}
	selectedKeyID := actualSelectedKeyID
	if failpoint == "selected_key_mapping_failure" {
		selectedKeyID = "bfk_missing_billing_mapping"
	}
	call, err = call.selected(selectedKeyID)
	if err != nil {
		logCall := h.callForExecutionLog(call, actualSelectedKeyID)
		h.recordExecution(r.Context(), logCall, response.Usage, 0, 0, "non_streaming", "", "error", err, failpoint)
		h.diagnostic("Provider result returned without billing", err, "protocol", protocol, "model", body.Model)
		writeJSON(w, http.StatusOK, publicResponse)
		return
	}
	var gross, commission int64
	completedCall := false
	if failpoint == "rating_failure" {
		err = errors.New("injected rating failure")
	} else {
		gross, commission, err = ratedUsage(response.Usage, call.CustomerPrices, call.CommissionPrices)
		if err == nil {
			h.consumeLocalCredit(keyHMAC, gross)
			completedCall = true
			err = h.completeCall(r.Context(), keyHMAC, call, gross, commission, response.Usage, "non_streaming", failpoint)
		}
	}
	if err != nil {
		if !completedCall {
			h.recordExecution(r.Context(), call, response.Usage, gross, commission, "non_streaming", "", "error", err, failpoint)
		}
		h.diagnostic("Provider result returned without billing", err, "protocol", protocol, "model", body.Model, "selected_key_id", call.KeyID)
	}
	writeJSON(w, http.StatusOK, publicResponse)
}

func publicChatResponse(protocol string, response *schemas.BifrostChatResponse, model string) (any, error) {
	if response == nil {
		return nil, errors.New("provider returned an empty response")
	}
	if protocol == "openai" {
		return response, nil
	}
	text, ok := responseText(response)
	if !ok {
		return nil, errors.New("provider returned no message content")
	}
	usage := response.Usage
	if usage == nil {
		usage = &schemas.BifrostLLMUsage{}
	}
	if protocol == "anthropic" {
		return map[string]any{
			"id": response.ID, "type": "message", "role": "assistant", "model": model,
			"content": []any{map[string]any{"type": "text", "text": text}},
			"usage":   map[string]any{"input_tokens": usage.PromptTokens, "output_tokens": usage.CompletionTokens},
		}, nil
	}
	if protocol == "gemini" {
		return map[string]any{
			"candidates":    []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}}},
			"usageMetadata": map[string]any{"promptTokenCount": usage.PromptTokens, "candidatesTokenCount": usage.CompletionTokens, "totalTokenCount": usage.TotalTokens},
		}, nil
	}
	return nil, fmt.Errorf("unsupported public protocol %q", protocol)
}

func responseText(response *schemas.BifrostChatResponse) (string, bool) {
	if response == nil {
		return "", false
	}
	for _, choice := range response.Choices {
		if choice.ChatNonStreamResponseChoice == nil || choice.Message == nil || choice.Message.Content == nil {
			continue
		}
		if text := choice.Message.Content.ContentStr; text != nil {
			return *text, true
		}
		var builder strings.Builder
		for _, block := range choice.Message.Content.ContentBlocks {
			if block.Text != nil {
				builder.WriteString(*block.Text)
			}
		}
		if builder.Len() != 0 {
			return builder.String(), true
		}
	}
	return "", false
}

func (h *Handler) streamChat(w http.ResponseWriter, r *http.Request, keyHMAC string, call resolvedCall, messages []schemas.ChatMessage, parameters *schemas.ChatParameters, passthroughHeaders map[string][]string, protocol string) {
	chunks, cancel, err := h.engine.ChatCompletionStreamCandidatesWithHeaders(r.Context(), call.Targets, messages, parameters, passthroughHeaders)
	if err != nil {
		call = h.callForExecutionLog(call, bifrostadapter.SelectedKeyID(err))
		h.recordExecution(r.Context(), call, nil, 0, 0, "streaming", "", "error", err, "")
		errJSON(w, 502, "provider_error", "Provider stream failed")
		return
	}
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	writeEvent := func(event string, value any) {
		raw, _ := json.Marshal(value)
		if event != "" {
			_, _ = fmt.Fprintf(w, "event: %s\n", event)
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		if flusher != nil {
			flusher.Flush()
		}
	}
	if protocol == "anthropic" {
		writeEvent("message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": "msg_" + uuid.NewString(), "type": "message", "role": "assistant", "model": call.PublicModel,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		}})
		writeEvent("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
	}
	var usage *schemas.BifrostLLMUsage
	selectedKeyID := ""
	var streamErr error
	for chunk := range chunks {
		if chunk == nil {
			continue
		}
		if chunk.BifrostChatResponse != nil {
			text := streamResponseText(chunk.BifrostChatResponse)
			switch protocol {
			case "openai":
				writeEvent("", chunk)
			case "anthropic":
				if text != "" {
					writeEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}})
				}
			case "gemini":
				if text != "" {
					writeEvent("", map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}}}})
				}
			}
		}
		if chunk.BifrostChatResponse != nil && chunk.BifrostChatResponse.Usage != nil {
			usage = chunk.BifrostChatResponse.Usage
		}
		if chunk.BifrostChatResponse != nil && chunk.BifrostChatResponse.ExtraFields.RoutingInfo.Key != "" {
			selectedKeyID = chunk.BifrostChatResponse.ExtraFields.RoutingInfo.Key
		}
		if chunk.BifrostError != nil {
			if chunk.BifrostError.ExtraFields.RoutingInfo.Key != "" {
				selectedKeyID = chunk.BifrostError.ExtraFields.RoutingInfo.Key
			}
			streamErr = errors.New(chunk.BifrostError.GetErrorString())
		}
	}
	if streamErr != nil || usage == nil || selectedKeyID == "" {
		if streamErr == nil {
			streamErr = errors.New("provider stream ended without usage or selected Provider Key")
		}
		logCall := h.callForExecutionLog(call, selectedKeyID)
		h.recordExecution(context.Background(), logCall, usage, 0, 0, "streaming", "", "error", streamErr, "")
		return
	}
	if usedErr := h.markProviderKeyUsed(context.Background(), selectedKeyID, h.config.Now().UTC()); usedErr != nil {
		h.diagnostic("update Provider Key last_used_at", usedErr, "selected_key_id", selectedKeyID)
	}
	switch protocol {
	case "openai":
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	case "anthropic":
		writeEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		writeEvent("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": usage.CompletionTokens}})
		writeEvent("message_stop", map[string]any{"type": "message_stop"})
	case "gemini":
		writeEvent("", map[string]any{"candidates": []any{}, "usageMetadata": map[string]any{"promptTokenCount": usage.PromptTokens, "candidatesTokenCount": usage.CompletionTokens, "totalTokenCount": usage.TotalTokens}})
	}
	call, err = call.selected(selectedKeyID)
	if err != nil {
		h.recordExecution(context.Background(), h.callForExecutionLog(call, selectedKeyID), usage, 0, 0, "streaming", "", "error", err, "")
		return
	}
	gross, commission, err := ratedUsage(usage, call.CustomerPrices, call.CommissionPrices)
	if err != nil {
		h.recordExecution(context.Background(), call, usage, 0, 0, "streaming", "", "error", err, "")
		return
	}
	h.consumeLocalCredit(keyHMAC, gross)
	if completeErr := h.completeCall(context.Background(), keyHMAC, call, gross, commission, usage, "streaming"); completeErr != nil {
		h.diagnostic("Streaming Provider result returned without billing", completeErr, "selected_key_id", call.KeyID)
	}
}

func streamResponseText(response *schemas.BifrostChatResponse) string {
	if response == nil {
		return ""
	}
	for _, choice := range response.Choices {
		if choice.ChatStreamResponseChoice != nil && choice.Delta != nil && choice.Delta.Content != nil {
			return *choice.Delta.Content
		}
	}
	return ""
}

// consumeLocalCredit applies the completed Provider usage independently of
// durable billing. The caller invokes it exactly once after Gross is known, so
// a later AI Order, Outbox, Log Store, or Charge failure cannot restore quota.
func (h *Handler) consumeLocalCredit(keyHMAC string, gross int64) {
	if gross <= 0 {
		return
	}
	h.creditMu.Lock()
	defer h.creditMu.Unlock()
	if state := h.credits[keyHMAC]; state != nil {
		if state.available < math.MinInt64+gross {
			state.available = math.MinInt64
		} else {
			state.available -= gross
		}
		if state.available <= 0 {
			state.admission.allowed = false
		}
	}
}

func ratedUsage(usage *schemas.BifrostLLMUsage, customer, provider []priceRow) (int64, int64, error) {
	if usage == nil {
		usage = &schemas.BifrostLLMUsage{}
	}
	gross, err := rate(usage, customer)
	if err != nil {
		return 0, 0, err
	}
	commission, err := rate(usage, provider)
	return gross, commission, err
}

func rate(usage *schemas.BifrostLLMUsage, prices []priceRow) (int64, error) {
	total := int64(0)
	for _, price := range prices {
		var units int64
		switch price.Metric {
		case "input_tokens":
			units = int64(usage.PromptTokens)
		case "output_tokens":
			units = int64(usage.CompletionTokens)
		default:
			return 0, fmt.Errorf("unsupported AI metric %q", price.Metric)
		}
		if price.Unit > 0 && units > 0 && price.Amount > 0 {
			component := ceilMulDiv(units, price.Amount, price.Unit)
			if component > math.MaxInt64-total {
				return math.MaxInt64, nil
			}
			total += component
		}
	}
	return total, nil
}

// ceilMulDiv calculates ceil(left*right/divisor) without overflowing the
// intermediate product. Unrepresentable results saturate at MaxInt64.
func ceilMulDiv(left, right, divisor int64) int64 {
	hi, lo := bits.Mul64(uint64(left), uint64(right))
	unsignedDivisor := uint64(divisor)
	if hi >= unsignedDivisor {
		return math.MaxInt64
	}
	quotient, remainder := bits.Div64(hi, lo, unsignedDivisor)
	if remainder != 0 {
		quotient++
	}
	if quotient > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(quotient)
}

func (h *Handler) recordExecution(ctx context.Context, call resolvedCall, usage *schemas.BifrostLLMUsage, gross, commission int64, executionMode, externalID, status string, executionErr error, failpoint string) {
	if usage == nil {
		usage = &schemas.BifrostLLMUsage{}
	}
	latency := h.config.Now().UTC().Sub(call.StartedAt).Seconds() * 1000
	if call.StartedAt.IsZero() || latency < 0 {
		latency = 0
	}
	record := map[string]any{
		"id": "log_" + uuid.NewString(), "selected_key_id": call.KeyID,
		"provider_id": call.ProviderID, "model_id": call.ModelID,
		"beneficiary_merchant_id": call.MerchantID,
		"gross_microcredits":      gross, "commission_microcredits": commission,
		"usage": usage, "metrics": []any{
			map[string]any{"metric": "input_tokens", "quantity": usage.PromptTokens},
			map[string]any{"metric": "output_tokens", "quantity": usage.CompletionTokens},
		},
		"latency_ms": latency, "execution_mode": executionMode,
		"status": status, "external_order_id": externalID, "created_at": h.config.Now().UTC(),
	}
	if executionErr != nil {
		record["error"] = executionErr.Error()
	}
	var err error
	if failpoint == "bifrost_log_store_write" {
		err = errors.New("injected Bifrost Log Store write failure")
	} else {
		err = h.stores.WriteLog(ctx, record)
	}
	if err != nil {
		h.diagnostic("Bifrost Log Store write failed", err, "external_order_id", externalID)
	}
}

func (h *Handler) callForExecutionLog(call resolvedCall, selectedKeyID string) resolvedCall {
	if selectedKeyID == "" && len(call.Targets) == 1 {
		selectedKeyID = call.Targets[0].RouteKey
	}
	if selectedKeyID != "" {
		if selected, err := call.selected(selectedKeyID); err == nil {
			return selected
		}
		call.KeyID = selectedKeyID
	}
	return call
}

func (h *Handler) completeCall(ctx context.Context, keyHMAC string, call resolvedCall, gross, commission int64, usage *schemas.BifrostLLMUsage, executionMode string, failpoints ...string) error {
	if usage == nil {
		usage = &schemas.BifrostLLMUsage{}
	}
	failpoint := ""
	if len(failpoints) != 0 {
		failpoint = failpoints[0]
	}
	externalID := ""
	defer func() {
		h.recordExecution(context.WithoutCancel(ctx), call, usage, gross, commission, executionMode, externalID, "success", nil, failpoint)
	}()
	if gross == 0 {
		return nil
	}
	tx, err := h.config.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	orderID := "aio_" + uuid.NewString()
	externalID = "ord_" + uuid.NewString()
	pricingSnapshot, _ := json.Marshal(map[string]any{"customer_prices": call.CustomerPrices, "commission_prices": call.CommissionPrices, "usage": usage})
	providerSnapshot, _ := json.Marshal(map[string]any{"provider_id": call.ProviderID, "provider_key_id": call.KeyID})
	now := h.config.Now().UTC()
	startedAt := call.StartedAt
	if startedAt.IsZero() {
		startedAt = now
	}
	status := "pending"
	var billingError []byte
	if commission > gross {
		status = "billing_failed"
		billingError, _ = json.Marshal(map[string]any{"code": "commission_exceeds_gross"})
	}
	var billingErrorValue any
	if len(billingError) != 0 {
		billingErrorValue = string(billingError)
	}
	if failpoint == "ai_order_write" {
		return errors.New("injected AI Order write failure")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_orders(id,external_order_id,provider_key_id,subscription_key_hmac,subscription_key_id,account_id,subscription_id,product_id,owner_identity_issuer,owner_identity_subject,model_id,provider_id,gross_microcredits,commission_microcredits,pricing_snapshot,provider_snapshot,billing_error,status,created_at,completed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`, orderID, externalID, call.KeyID, keyHMAC, call.SubscriptionKeyID, call.AccountID, call.SubscriptionID, call.ProductID, call.OwnerIssuer, call.OwnerSubject, call.ModelID, call.ProviderID, gross, commission, string(pricingSnapshot), string(providerSnapshot), billingErrorValue, status, now, now)
	if err != nil {
		return err
	}
	usageRows := []struct {
		metric   string
		quantity int
	}{{"input_tokens", usage.PromptTokens}, {"output_tokens", usage.CompletionTokens}}
	for _, usageRow := range usageRows {
		if _, err = tx.ExecContext(ctx, `INSERT INTO client_sync.ai_usage(id,account_id,order_id,model_id,metric,quantity,owner_identity_issuer,owner_identity_subject,status,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'completed',$9)`, "use_"+uuid.NewString(), call.AccountID, orderID, call.ModelID, usageRow.metric, usageRow.quantity, call.OwnerIssuer, call.OwnerSubject, now); err != nil {
			return err
		}
	}
	if gross > 0 && commission <= gross {
		if failpoint == "charge_outbox_write" {
			return errors.New("injected Charge Outbox write failure")
		}
		payload, _ := json.Marshal(map[string]any{"external_order_id": externalID, "subscription_key_hmac": keyHMAC, "gross_microcredits": gross, "commissions": []any{map[string]any{"merchant_id": call.MerchantID, "amount_microcredits": commission}}, "order": map[string]any{"type": "ai_call", "model": call.PublicModel, "provider": call.ProviderID, "execution_mode": executionMode}, "service_started_at": startedAt, "service_completed_at": now})
		_, err = tx.ExecContext(ctx, `INSERT INTO charge_outbox(id,external_order_id,ai_order_id,payload,status,created_at,updated_at) VALUES($1,$2,$3,$4,'pending',$5,$5)`, "out_"+uuid.NewString(), externalID, orderID, string(payload), now)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// The single Outbox worker owns every retry for this logical task.
	return nil
}

func (h *Handler) reportOutbox(externalID string) {
	if h.config.ServiceToken == nil || h.config.GizPayURL == "" {
		return
	}
	var payload []byte
	var outboxID, orderID string
	var recoverDuplicate bool
	if h.config.DB.QueryRowx(`SELECT id,ai_order_id,payload,recover_duplicate FROM charge_outbox WHERE external_order_id=$1 AND status IN('pending','sending')`, externalID).Scan(&outboxID, &orderID, &payload, &recoverDuplicate) != nil {
		return
	}
	token, err := h.config.ServiceToken(context.Background())
	if err != nil {
		h.diagnostic("obtain GizPay Charge service token", err, "external_order_id", externalID)
		return
	}
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(h.config.GizPayURL, "/")+"/service/v1/payg-charges", bytes.NewReader(payload))
	if err != nil {
		h.diagnostic("build GizPay Charge request", err, "external_order_id", externalID)
		return
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	// Persist uncertainty before making the irreversible remote call. Keep the
	// value read above in recoverDuplicate: it distinguishes a fresh business
	// conflict from a retry whose earlier POST may already have committed.
	sending, err := h.config.DB.Exec(`UPDATE charge_outbox SET status='sending',recover_duplicate=true,updated_at=$1 WHERE id=$2 AND status IN('pending','sending')`, h.config.Now().UTC(), outboxID)
	if err != nil {
		return
	}
	updated, err := sending.RowsAffected()
	if err != nil || updated != 1 {
		return
	}
	response, err := h.config.HTTPClient.Do(request)
	if err != nil {
		h.diagnostic("send GizPay Charge request", err, "external_order_id", externalID)
		_, _ = h.config.DB.Exec(`UPDATE charge_outbox SET status='pending',recover_duplicate=true,updated_at=$1 WHERE id=$2`, h.config.Now().UTC(), outboxID)
		return
	}
	defer response.Body.Close()
	success := response.StatusCode == http.StatusCreated
	if response.StatusCode == http.StatusConflict && !recoverDuplicate {
		result, conflictErr := h.config.DB.Exec(`WITH conflict AS (
			UPDATE charge_outbox SET status='abandoned',recover_duplicate=false,updated_at=$1
			WHERE id=$2 AND ai_order_id=$3
			RETURNING ai_order_id
		) UPDATE ai_orders SET status='billing_failed',billing_error='{"code":"duplicate_external_order_id"}'::jsonb
		  WHERE id=$3 AND id IN (SELECT ai_order_id FROM conflict)`, h.config.Now().UTC(), outboxID, orderID)
		if conflictErr == nil {
			var updated int64
			updated, conflictErr = result.RowsAffected()
			if conflictErr == nil && updated != 1 {
				conflictErr = errors.New("outbox conflict updated no AI Order")
			}
		}
		if conflictErr != nil {
			_, _ = h.config.DB.Exec(`UPDATE charge_outbox SET status='pending',recover_duplicate=false,updated_at=$1 WHERE id=$2`, h.config.Now().UTC(), outboxID)
		}
		return
	}
	if response.StatusCode == http.StatusConflict && recoverDuplicate {
		getRequest, requestErr := http.NewRequest(http.MethodGet, strings.TrimRight(h.config.GizPayURL, "/")+"/service/v1/payg-charges/"+externalID, nil)
		if requestErr != nil {
			h.diagnostic("build GizPay Charge recovery request", requestErr, "external_order_id", externalID)
			_, _ = h.config.DB.Exec(`UPDATE charge_outbox SET status='pending',recover_duplicate=true,updated_at=$1 WHERE id=$2`, h.config.Now().UTC(), outboxID)
			return
		}
		getRequest.Header.Set("Authorization", "Bearer "+token)
		getResponse, getErr := h.config.HTTPClient.Do(getRequest)
		if getErr != nil {
			_, _ = h.config.DB.Exec(`UPDATE charge_outbox SET status='pending',recover_duplicate=true,updated_at=$1 WHERE id=$2`, h.config.Now().UTC(), outboxID)
			return
		}
		success = getResponse.StatusCode == http.StatusOK
		getResponse.Body.Close()
	}
	if success {
		updateErr := h.markOutboxReported(outboxID, orderID)
		if updateErr != nil {
			// GizPay has accepted this logical Charge (either the POST returned
			// 201 or recovery GET found it). If the local completion write fails,
			// the next POST will necessarily return 409, so keep the recovery bit
			// until that retry can confirm the original Charge again.
			_, _ = h.config.DB.Exec(`UPDATE charge_outbox SET status='pending',recover_duplicate=true,updated_at=$1 WHERE id=$2`, h.config.Now().UTC(), outboxID)
		}
	} else {
		h.diagnostic("GizPay Charge request was rejected", fmt.Errorf("status %d", response.StatusCode), "external_order_id", externalID)
		// Once a POST outcome is uncertain, a temporary POST/GET failure cannot
		// prove that GizPay did not commit it. Preserve that state until recovery
		// succeeds. A fresh, definitive non-409 failure remains a normal retry.
		_, _ = h.config.DB.Exec(`UPDATE charge_outbox SET status='pending',recover_duplicate=$1,updated_at=$2 WHERE id=$3`, recoverDuplicate, h.config.Now().UTC(), outboxID)
	}
}

func (h *Handler) markOutboxReported(outboxID, orderID string) error {
	tx, err := h.config.DB.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var providerKeyID string
	var commission int64
	err = tx.QueryRowx(`SELECT provider_key_id,commission_microcredits FROM ai_orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&providerKeyID, &commission)
	if err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE charge_outbox SET status='reported',recover_duplicate=false,updated_at=$1 WHERE id=$2 AND ai_order_id=$3 AND status IN('pending','sending')`, h.config.Now().UTC(), outboxID, orderID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("outbox completion updated no Outbox row")
	}
	if _, err = tx.Exec(`UPDATE ai_orders SET status='charged' WHERE id=$1`, orderID); err != nil {
		return err
	}
	now := h.config.Now().UTC()
	if _, err = tx.Exec(`UPDATE provider_key_billing SET earned_microcredits=earned_microcredits+$1,updated_at=$2 WHERE provider_key_id=$3`, commission, now, providerKeyID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE client_sync.provider_keys SET earned_microcredits=earned_microcredits+$1,updated_at=$2 WHERE id=$3`, commission, now, providerKeyID); err != nil {
		return err
	}
	return tx.Commit()
}

func (h *Handler) markProviderKeyUsed(ctx context.Context, providerKeyID string, usedAt time.Time) error {
	tx, err := h.config.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE provider_key_billing SET last_used_at=CASE WHEN last_used_at IS NULL OR last_used_at < $1 THEN $1 ELSE last_used_at END,updated_at=CASE WHEN updated_at < $1 THEN $1 ELSE updated_at END WHERE provider_key_id=$2`, usedAt, providerKeyID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("provider key billing row was not updated")
	}
	result, err = tx.ExecContext(ctx, `UPDATE client_sync.provider_keys SET last_used_at=CASE WHEN last_used_at IS NULL OR last_used_at < $1 THEN $1 ELSE last_used_at END,updated_at=CASE WHEN updated_at < $1 THEN $1 ELSE updated_at END WHERE id=$2`, usedAt, providerKeyID)
	if err != nil {
		return err
	}
	updated, err = result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("provider key sync row was not updated")
	}
	return tx.Commit()
}
