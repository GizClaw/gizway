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
	"math/bits"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"

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
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Stream        bool            `json:"stream"`
	MaxTokens     *int            `json:"max_tokens,omitempty"`
	StreamOptions json.RawMessage `json:"stream_options,omitempty"`
}

type realtimeSession struct {
	Secret, KeyHMAC, ProviderURL string
	ProviderHeader               http.Header
	Call                         resolvedCall
}

func (h *Handler) protocol(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/v1/realtime" {
		h.realtimeSocket(w, r)
		return
	}
	rawKey := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if googleKey := r.Header.Get("x-goog-api-key"); googleKey != "" {
		rawKey = googleKey
	}
	if rawKey == "" {
		errJSON(w, http.StatusUnauthorized, "invalid_subscription_key", "Subscription API Key is required")
		return
	}
	keyHMAC := subscriptionkey.HMAC(h.config.HMACSecret, rawKey)
	allowed, productID, admissionErr := h.admit(r.Context(), keyHMAC)
	if admissionErr != nil {
		errJSON(w, http.StatusServiceUnavailable, "credit_check_unavailable", "Credit Check unavailable")
		return
	}
	if !allowed {
		errJSON(w, http.StatusPaymentRequired, "credit_denied", "Credit denied")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		rows, err := h.many(`SELECT name id FROM models WHERE status='active' ORDER BY name`)
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
	if r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions" {
		h.chat(w, r, keyHMAC, productID, "openai")
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/v1/messages" {
		h.chat(w, r, keyHMAC, productID, "anthropic")
		return
	}
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
		h.chat(w, r, keyHMAC, productID, "gemini")
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/v1/realtime/client_secrets" {
		h.createRealtimeSecret(w, r, keyHMAC, productID)
		return
	}
	errJSON(w, http.StatusNotImplemented, "not_implemented", "protocol handler not implemented")
}

func (h *Handler) createRealtimeSecret(w http.ResponseWriter, r *http.Request, keyHMAC, productID string) {
	var body struct {
		Model     string `json:"model"`
		Transport string `json:"transport"`
	}
	if decode(r, &body) != nil || body.Model == "" || body.Transport != "websocket" {
		invalid(w)
		return
	}
	call, err := h.resolveCall(r.Context(), body.Model, productID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) && call.ModelID == "" {
			errJSON(w, http.StatusNotFound, "model_not_found", "model not found")
			return
		}
		errJSON(w, http.StatusServiceUnavailable, "no_active_provider_key", "no active Provider Key")
		return
	}
	providerURL, providerHeader, selectedKeyID, err := h.engine.RealtimeWebSocketRouteCandidates(r.Context(), call.Targets)
	h.observeDependency("provider", err)
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
	h.realtimeMu.Lock()
	h.realtime[sessionID] = realtimeSession{Secret: secret, KeyHMAC: keyHMAC, Call: call, ProviderURL: providerURL, ProviderHeader: providerHeader}
	h.realtimeMu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_secret": map[string]any{"value": secret},
		"session":       map[string]any{"session_id": sessionID, "transport": "websocket", "model": body.Model},
	})
}

func (h *Handler) realtimeSocket(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	h.realtimeMu.Lock()
	session, ok := h.realtime[sessionID]
	if ok && hmac.Equal([]byte(secret), []byte(session.Secret)) {
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
	h.observeDependency("provider", err)
	if err != nil {
		_ = client.Close(websocket.StatusInternalError, "provider unavailable")
		return
	}
	defer provider.Close(websocket.StatusNormalClosure, "session complete")

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
		case <-clientToProvider:
			return
		default:
		}
		kind, raw, readErr := provider.Read(ctx)
		if readErr != nil {
			return
		}
		public, usage, terminal, translateErr := h.engine.RealtimeProviderEvent(ctx, session.Call.Target, raw)
		if translateErr != nil {
			return
		}
		if writeErr := client.Write(ctx, kind, public); writeErr != nil {
			return
		}
		if terminal && usage != nil {
			gross, commission, rateErr := ratedUsage(usage, session.Call.CustomerPrices, session.Call.CommissionPrices)
			if rateErr != nil {
				return
			}
			h.consumeLocalCredit(session.KeyHMAC, gross)
			_ = h.completeCall(context.Background(), session.KeyHMAC, session.Call, gross, commission, usage, true)
			return
		}
	}
}

func (h *Handler) admit(ctx context.Context, keyHMAC string) (bool, string, error) {
	for {
		h.creditMu.Lock()
		state := h.credits[keyHMAC]
		now := h.config.Now()
		if state != nil && state.loading {
			wait := state.wait
			h.creditMu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return false, "", ctx.Err()
			}
		}
		if state != nil && now.Before(state.expires) && state.available > 0 {
			productID := state.productID
			h.creditMu.Unlock()
			return true, productID, nil
		}
		state = &creditState{loading: true, wait: make(chan struct{})}
		h.credits[keyHMAC] = state
		h.creditMu.Unlock()

		available, productID, billingMode, recheckAfter, ok, err := h.checkCredit(ctx, keyHMAC)
		h.creditMu.Lock()
		state.available = available
		state.productID = productID
		state.billing = billingMode
		state.expires = now.Add(recheckAfter)
		state.loading = false
		close(state.wait)
		h.creditMu.Unlock()
		return ok, productID, err
	}
}

func (h *Handler) checkCredit(ctx context.Context, keyHMAC string) (int64, string, string, time.Duration, bool, error) {
	if h.config.GizPayURL == "" || h.config.ServiceToken == nil {
		return 0, "", "", 0, false, errors.New("credit check is not configured")
	}
	token, err := h.config.ServiceToken(ctx)
	if err != nil {
		return 0, "", "", 0, false, err
	}
	body, _ := json.Marshal(map[string]any{"api_key_hmac": keyHMAC})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.config.GizPayURL, "/")+"/service/v1/subscription-credit-checks", bytes.NewReader(body))
	if err != nil {
		return 0, "", "", 0, false, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := h.config.HTTPClient.Do(request)
	if err != nil {
		return 0, "", "", 0, false, err
	}
	defer response.Body.Close()
	var result struct {
		Status              string `json:"status"`
		ProductID           string `json:"product_id"`
		BillingMode         string `json:"billing_mode"`
		Available           int64  `json:"available_microcredits"`
		RecheckAfterSeconds int64  `json:"recheck_after_seconds"`
	}
	if response.StatusCode != http.StatusOK {
		return 0, "", "", 0, false, fmt.Errorf("credit check returned %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return 0, "", "", 0, false, err
	}
	if result.RecheckAfterSeconds <= 0 {
		return 0, "", "", 0, false, errors.New("credit check returned invalid recheck interval")
	}
	return result.Available, result.ProductID, result.BillingMode, time.Duration(result.RecheckAfterSeconds) * time.Second, result.Status == "allowed", nil
}

func (h *Handler) resolveCall(ctx context.Context, modelName, productID string) (resolvedCall, error) {
	var call resolvedCall
	call.ProductID, call.PublicModel = productID, modelName
	call.StartedAt = h.config.Now().UTC()
	err := h.config.DB.QueryRowx(`SELECT id,provider_id,provider_model FROM models WHERE name=$1 AND status='active'`, modelName).Scan(&call.ModelID, &call.ProviderID, &call.ProviderModel)
	if err != nil {
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
		if err := h.config.DB.QueryRowx(`SELECT beneficiary_merchant_id,status FROM provider_key_billing WHERE bifrost_key_id=$1`, key.ID).Scan(&merchantID, &billingStatus); err != nil || billingStatus != "active" {
			continue
		}
		target := store.ProviderExecutionTarget{Provider: provider.Kind, Endpoint: provider.BaseURL, Credential: key.APIKey, RouteKey: key.ID, Weight: key.Weight}
		target.Model = call.ProviderModel
		var prices []priceRow
		if err := h.config.DB.Select(&prices, `SELECT metric,unit_size,commission_microcredits amount FROM provider_key_prices WHERE model_id=$1 AND bifrost_key_id=$2 ORDER BY metric`, call.ModelID, target.RouteKey); err != nil {
			return call, err
		}
		call.Targets = append(call.Targets, target)
		call.Payments[target.RouteKey] = keyPayment{MerchantID: merchantID, Prices: prices}
	}
	if len(call.Targets) == 0 {
		return call, sql.ErrNoRows
	}
	if err := h.config.DB.Select(&call.CustomerPrices, `SELECT metric,unit_size,price_microcredits amount FROM model_customer_prices WHERE model_id=$1 ORDER BY metric`, call.ModelID); err != nil {
		return call, err
	}
	return call, nil
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

func (h *Handler) chat(w http.ResponseWriter, r *http.Request, keyHMAC, productID, protocol string) {
	var body chatRequest
	if protocol == "gemini" {
		body.Model = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1beta/models/"), ":generateContent")
		var gemini struct {
			Contents []struct {
				Role  string `json:"role,omitempty"`
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
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
				body.Messages = append(body.Messages, struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				}{Role: "user", Content: part.Text})
			}
		}
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
	call, err := h.resolveCall(r.Context(), body.Model, productID)
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
		text := message.Content
		messages[index] = schemas.ChatMessage{Role: schemas.ChatMessageRole(message.Role), Content: &schemas.ChatMessageContent{ContentStr: &text}}
	}
	if body.Stream {
		h.streamChat(w, r, keyHMAC, call, messages)
		return
	}
	response, err := h.engine.ChatCompletionCandidates(r.Context(), call.Targets, messages, &schemas.ChatParameters{})
	h.observeDependency("provider", err)
	if err != nil {
		errJSON(w, http.StatusBadGateway, "provider_error", "Provider call failed")
		return
	}
	response.Model = body.Model
	publicResponse, err := publicChatResponse(protocol, response, body.Model)
	if err != nil {
		h.diagnostic("Provider response conversion failed", err, "protocol", protocol, "model", body.Model)
		errJSON(w, http.StatusBadGateway, "invalid_provider_response", "Provider response cannot be converted to the public protocol")
		return
	}
	failpoint := r.Header.Get("X-Gizway-Test-Failpoint")
	selectedKeyID := response.ExtraFields.RoutingInfo.Key
	if failpoint == "selected_key_mapping_failure" {
		selectedKeyID = "bfk_missing_billing_mapping"
	}
	call, err = call.selected(selectedKeyID)
	if err != nil {
		h.diagnostic("Provider result returned without billing", err, "protocol", protocol, "model", body.Model)
		writeJSON(w, http.StatusOK, publicResponse)
		return
	}
	var gross, commission int64
	if failpoint == "rating_failure" {
		err = errors.New("injected rating failure")
	} else {
		gross, commission, err = ratedUsage(response.Usage, call.CustomerPrices, call.CommissionPrices)
		if err == nil {
			h.consumeLocalCredit(keyHMAC, gross)
			err = h.completeCall(r.Context(), keyHMAC, call, gross, commission, response.Usage, true, failpoint)
		}
	}
	if err != nil {
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

func (h *Handler) streamChat(w http.ResponseWriter, r *http.Request, keyHMAC string, call resolvedCall, messages []schemas.ChatMessage) {
	chunks, cancel, err := h.engine.ChatCompletionStreamCandidates(r.Context(), call.Targets, messages, &schemas.ChatParameters{})
	h.observeDependency("provider", err)
	if err != nil {
		errJSON(w, 502, "provider_error", "Provider stream failed")
		return
	}
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	var usage *schemas.BifrostLLMUsage
	selectedKeyID := ""
	failed := false
	for chunk := range chunks {
		if chunk == nil {
			continue
		}
		raw, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		if flusher != nil {
			flusher.Flush()
		}
		if chunk.BifrostChatResponse != nil && chunk.BifrostChatResponse.Usage != nil {
			usage = chunk.BifrostChatResponse.Usage
		}
		if chunk.BifrostChatResponse != nil && chunk.BifrostChatResponse.ExtraFields.RoutingInfo.Key != "" {
			selectedKeyID = chunk.BifrostChatResponse.ExtraFields.RoutingInfo.Key
		}
		if chunk.BifrostError != nil {
			failed = true
		}
	}
	if failed || usage == nil || selectedKeyID == "" {
		return
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	call, err = call.selected(selectedKeyID)
	if err != nil {
		return
	}
	gross, commission, err := ratedUsage(usage, call.CustomerPrices, call.CommissionPrices)
	if err != nil {
		return
	}
	h.consumeLocalCredit(keyHMAC, gross)
	_ = h.completeCall(context.Background(), keyHMAC, call, gross, commission, usage, true)
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
		case "input_token":
			units = int64(usage.PromptTokens)
		case "output_token":
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

func (h *Handler) completeCall(ctx context.Context, keyHMAC string, call resolvedCall, gross, commission int64, usage *schemas.BifrostLLMUsage, writeLog bool, failpoints ...string) error {
	tx, err := h.config.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	orderID, externalID := "aio_"+uuid.NewString(), "ord_"+uuid.NewString()
	pricingSnapshot, _ := json.Marshal(map[string]any{"customer_prices": call.CustomerPrices, "commission_prices": call.CommissionPrices, "usage": usage})
	providerSnapshot, _ := json.Marshal(map[string]any{"provider_id": call.ProviderID, "bifrost_key_id": call.KeyID})
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
	failpoint := ""
	if len(failpoints) != 0 {
		failpoint = failpoints[0]
	}
	if failpoint == "ai_order_write" {
		return errors.New("injected AI Order write failure")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_orders(id,external_order_id,key_hmac,product_id,model_id,provider_id,bifrost_key_id,gross_microcredits,commission_microcredits,pricing_snapshot,provider_snapshot,billing_error,status,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, orderID, externalID, keyHMAC, call.ProductID, call.ModelID, call.ProviderID, call.KeyID, gross, commission, string(pricingSnapshot), string(providerSnapshot), billingErrorValue, status, now)
	if err != nil {
		return err
	}
	if gross > 0 && commission <= gross {
		if failpoint == "charge_outbox_write" {
			return errors.New("injected Charge Outbox write failure")
		}
		payload, _ := json.Marshal(map[string]any{"external_order_id": externalID, "api_key_hmac": keyHMAC, "gross_microcredits": gross, "commissions": []any{map[string]any{"merchant_id": call.MerchantID, "amount_microcredits": commission}}, "order": map[string]any{"type": "ai_call", "model": call.PublicModel, "provider": call.ProviderID}, "service_started_at": startedAt, "service_completed_at": now})
		_, err = tx.ExecContext(ctx, `INSERT INTO charge_outbox(id,external_order_id,ai_order_id,payload,status,created_at,updated_at) VALUES($1,$2,$3,$4,'pending',$5,$5)`, "out_"+uuid.NewString(), externalID, orderID, string(payload), now)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if writeLog {
		var logErr error
		if failpoint == "bifrost_log_store_write" {
			logErr = errors.New("injected Bifrost Log Store write failure")
		} else {
			logErr = h.stores.WriteLog(ctx, map[string]any{
				"id": "log_" + uuid.NewString(), "selected_key_id": call.KeyID,
				"provider_id": call.ProviderID, "model_id": call.ModelID,
				"beneficiary_merchant_id": call.MerchantID,
				"gross_microcredits":      gross, "commission_microcredits": commission, "created_at": now,
			})
		}
		h.observeDependency("bifrost_log_store", logErr)
		if logErr != nil {
			h.diagnostic("Bifrost Log Store write failed", logErr, "external_order_id", externalID)
		}
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
		result, updateErr := h.config.DB.Exec(`WITH reported AS (
			UPDATE charge_outbox SET status='reported',recover_duplicate=false,updated_at=$1
			WHERE id=$2 AND ai_order_id=$3
			RETURNING ai_order_id
		) UPDATE ai_orders SET status='charged'
		  WHERE id=$3 AND id IN (SELECT ai_order_id FROM reported)`, h.config.Now().UTC(), outboxID, orderID)
		if updateErr == nil {
			var updated int64
			updated, updateErr = result.RowsAffected()
			if updateErr == nil && updated != 1 {
				updateErr = errors.New("outbox completion updated no AI Order")
			}
		}
		if updateErr != nil {
			// GizPay has accepted this logical Charge (either the POST returned
			// 201 or recovery GET found it). If the local completion write fails,
			// the next POST will necessarily return 409, so keep the recovery bit
			// until that retry can confirm the original Charge again.
			_, _ = h.config.DB.Exec(`UPDATE charge_outbox SET status='pending',recover_duplicate=true,updated_at=$1 WHERE id=$2`, h.config.Now().UTC(), outboxID)
		}
	} else {
		// Once a POST outcome is uncertain, a temporary POST/GET failure cannot
		// prove that GizPay did not commit it. Preserve that state until recovery
		// succeeds. A fresh, definitive non-409 failure remains a normal retry.
		_, _ = h.config.DB.Exec(`UPDATE charge_outbox SET status='pending',recover_duplicate=$1,updated_at=$2 WHERE id=$3`, recoverDuplicate, h.config.Now().UTC(), outboxID)
	}
}
