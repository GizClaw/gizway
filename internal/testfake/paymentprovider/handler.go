// Package paymentprovider is a deterministic external payment and merchant
// webhook fixture. It runs outside Gizway and talks only over HTTP.
package paymentprovider

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

func Handler(callbackSecret string) http.Handler {
	return HandlerWithClock(callbackSecret, time.Now)
}

func HandlerWithClock(callbackSecret string, now func() time.Time) http.Handler {
	return HandlerWithClockAndClient(callbackSecret, now, http.DefaultClient)
}

func HandlerWithClockAndClient(callbackSecret string, now func() time.Time, callbackClient *http.Client) http.Handler {
	if callbackClient == nil {
		callbackClient = http.DefaultClient
	}
	mux := http.NewServeMux()
	var callbacks atomic.Int64
	var checkouts atomic.Int64
	var refunds atomic.Int64
	var webhooks atomic.Int64
	var failNextCheckout atomic.Bool
	var failNextRefund atomic.Bool
	var failNextRefundDefinitively atomic.Bool
	var pendNextRefund atomic.Bool
	var lastMu sync.Mutex
	lastWebhook := map[string]any{}
	var checkoutKeys sync.Map
	var refundKeys sync.Map
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, _ *http.Request) {
		lastMu.Lock()
		defer lastMu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"callbacks": callbacks.Load(), "checkouts": checkouts.Load(), "refunds": refunds.Load(),
			"webhooks": webhooks.Load(), "last_webhook": lastWebhook,
		})
	})
	mux.HandleFunc("POST /v1/test/fail_next_checkout_after_commit", func(w http.ResponseWriter, _ *http.Request) {
		failNextCheckout.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/test/fail_next_refund_after_commit", func(w http.ResponseWriter, _ *http.Request) {
		failNextRefund.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/test/fail_next_refund", func(w http.ResponseWriter, _ *http.Request) {
		failNextRefundDefinitively.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/test/pend_next_refund", func(w http.ResponseWriter, _ *http.Request) {
		pendNextRefund.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/checkouts", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer story-payment-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var request struct {
			TopupID string `json:"topup_id"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.TopupID == "" {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Idempotency-Key") != request.TopupID {
			http.Error(w, "invalid idempotency key", http.StatusBadRequest)
			return
		}
		if _, loaded := checkoutKeys.LoadOrStore(request.TopupID, struct{}{}); !loaded {
			checkouts.Add(1)
		}
		if failNextCheckout.CompareAndSwap(true, false) {
			http.Error(w, "ambiguous response after committed checkout", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"provider_reference": "pay_" + request.TopupID,
			"checkout_url":       "https://checkout.gizway.test/" + request.TopupID,
		})
	})
	mux.HandleFunc("POST /v1/refunds", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer story-payment-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var request struct {
			RefundID string `json:"refund_id"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.RefundID == "" {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Idempotency-Key") != request.RefundID {
			http.Error(w, "invalid idempotency key", http.StatusBadRequest)
			return
		}
		if _, loaded := refundKeys.LoadOrStore(request.RefundID, struct{}{}); !loaded {
			refunds.Add(1)
		}
		if failNextRefund.CompareAndSwap(true, false) {
			http.Error(w, "ambiguous response after committed refund", http.StatusInternalServerError)
			return
		}
		if failNextRefundDefinitively.CompareAndSwap(true, false) {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "failed"})
			return
		}
		if pendNextRefund.CompareAndSwap(true, false) {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"provider_refund_id": "refund_" + request.RefundID, "status": "succeeded"})
	})
	// Test control endpoint: the fake provider, not Hurl, constructs the signed
	// callback. This exercises Gizway's real signature verification boundary.
	mux.HandleFunc("POST /v1/test/confirm", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			CallbackURL       string `json:"callback_url"`
			EventID           string `json:"event_id"`
			ProviderReference string `json:"provider_reference"`
			Currency          string `json:"currency"`
			AmountMinor       int64  `json:"amount_minor"`
			TimestampOffset   int64  `json:"timestamp_offset_seconds,omitempty"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		payload, _ := json.Marshal(map[string]any{
			"event_id": request.EventID, "type": "topup.succeeded",
			"provider_reference": request.ProviderReference,
			"currency":           request.Currency, "amount_minor": request.AmountMinor,
		})
		mac := hmac.New(sha256.New, []byte(callbackSecret))
		timestamp := now().Add(time.Duration(request.TimestampOffset) * time.Second).Unix()
		_, _ = fmt.Fprintf(mac, "%d.", timestamp)
		_, _ = mac.Write(payload)
		callback, err := http.NewRequest(http.MethodPost, request.CallbackURL, bytes.NewReader(payload))
		if err != nil {
			http.Error(w, "invalid callback URL", http.StatusBadGateway)
			return
		}
		callback.Header.Set("Content-Type", "application/json")
		callback.Header.Set("X-Gizway-Signature", fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil))))
		resp, err := callbackClient.Do(callback)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		callbacks.Add(1)
		w.WriteHeader(resp.StatusCode)
		_, _ = fmt.Fprintf(w, `{"callback_status":%d}`, resp.StatusCode)
	})
	mux.HandleFunc("POST /merchant-webhook", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		lastMu.Lock()
		lastWebhook = map[string]any{"payload": payload, "signature": r.Header.Get("X-Gizway-Signature")}
		lastMu.Unlock()
		webhooks.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}
