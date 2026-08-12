package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	paymentadapter "github.com/idy/gizway/internal/adapter/payment"
	riskadapter "github.com/idy/gizway/internal/adapter/risk"
	merchantservice "github.com/idy/gizway/internal/service/merchant"
	paymentservice "github.com/idy/gizway/internal/service/payment"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
	"github.com/idy/gizway/internal/testfake/paymentprovider"
	"github.com/idy/gizway/internal/testfake/riskprovider"
)

const (
	storyUserOneSession  = "gzs_story_user_active_1"
	storyUserTwoSession  = "gzs_story_user_active_2"
	storyUserOneAccount  = "21000000-0000-4000-8000-000000000001"
	storyUserTwoAccount  = "21000000-0000-4000-8000-000000000002"
	storyMerchantAccount = "22000000-0000-4000-8000-000000000002"
)

// TestGizPayPublicWorkflows is intentionally a Go-side integration test, not a
// wrapper around the Hurl suite. It drives the retained public API through the
// final GizPay-only schema so statement coverage represents Go behavior while
// the black-box Hurl suite remains an independent protocol acceptance layer.
func TestGizPayPublicWorkflows(t *testing.T) {
	database := testdb.OpenGizPayStory(t)
	defer database.Close()
	current := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { return current }
	advance := func(duration time.Duration) time.Time {
		current = current.Add(duration)
		return current
	}
	repository, err := store.NewWithSecretKey(database.SQL, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	repository.ConfigureClock(now)
	fakePayment := httptest.NewServer(paymentprovider.HandlerWithClock("story-callback-secret", now))
	defer fakePayment.Close()
	fakeRisk := httptest.NewServer(riskprovider.Handler("story-risk-key"))
	defer fakeRisk.Close()
	payments := paymentservice.New(repository, paymentadapter.New(fakePayment.URL, "story-payment-key"), "story-callback-secret")
	payments.ConfigureClock(now)
	merchant := merchantservice.NewConfigured(repository, riskadapter.New(fakeRisk.URL, "story-risk-key"), true, "https://pay.gizway.test")
	merchant.ConfigureClock(now)
	server := NewWithServicesAndClockSurface(repository, nil, payments, merchant, now, advance, SurfaceGizPay)
	server.ConfigurePowerSync("https://sync.gizway.test", "powersync-story", "gizway-story-hs256", []byte("gizway-story-powersync-signing-key"))
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	loginOne := apiJSON(t, httpServer, http.MethodPost, "/account/v1/auth/login", "", "go-login-one", map[string]any{
		"email": "active-one@gizway.test", "password": "story-user-password",
	}, http.StatusOK)
	loginToken := requiredString(t, loginOne, "access_token")
	apiJSON(t, httpServer, http.MethodPost, "/account/v1/auth/login", "", "go-login-one", map[string]any{
		"email": "active-one@gizway.test", "password": "story-user-password",
	}, http.StatusOK)
	refreshed := apiJSON(t, httpServer, http.MethodPost, "/account/v1/auth/refresh", loginToken, "go-refresh-one", nil, http.StatusOK)
	refreshedToken := requiredString(t, refreshed, "access_token")
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/me", refreshedToken, "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/account/v1/auth/logout", refreshedToken, "go-logout-one", nil, http.StatusNoContent)
	apiJSON(t, httpServer, http.MethodPost, "/account/v1/auth/login", "", "go-suspended", map[string]any{
		"email": "suspended@gizway.test", "password": "story-user-password",
	}, http.StatusUnauthorized)

	apiJSON(t, httpServer, http.MethodPatch, "/account/v1/me", storyUserOneSession, "go-profile", map[string]any{
		"display_name": "Go Integration User",
	}, http.StatusOK)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/me", storyUserOneSession, "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts", storyUserOneSession, "", nil, http.StatusOK)

	createdKey := apiJSON(t, httpServer, http.MethodPost, "/account/v1/accounts/"+storyUserOneAccount+"/api_keys", storyUserOneSession, "go-gateway-key", map[string]any{
		"name": "Go coverage key", "kind": "gateway", "scopes": []string{"gateway:invoke", "gateway:usage:read"},
	}, http.StatusCreated)
	createdKeyID := requiredString(t, createdKey, "id")
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts/"+storyUserOneAccount+"/api_keys", storyUserOneSession, "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodDelete, "/account/v1/accounts/"+storyUserOneAccount+"/api_keys/"+createdKeyID, storyUserOneSession, "go-revoke-key", nil, http.StatusNoContent)

	apiJSON(t, httpServer, http.MethodPost, "/account/v1/accounts/"+storyUserOneAccount+"/transfers", storyUserOneSession, "go-transfer", map[string]any{
		"recipient_account_id": storyUserTwoAccount,
		"amount":               map[string]any{"asset": "GIZ_CREDIT", "microcredits": 1000},
		"note":                 "Go coverage transfer",
	}, http.StatusCreated)
	apiJSON(t, httpServer, http.MethodPost, "/account/v1/accounts/"+storyUserOneAccount+"/transfers", storyUserOneSession, "go-transfer", map[string]any{
		"recipient_account_id": storyUserTwoAccount,
		"amount":               map[string]any{"asset": "GIZ_CREDIT", "microcredits": 1000},
		"note":                 "Go coverage transfer",
	}, http.StatusOK)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts/"+storyUserOneAccount+"/transfers?limit=1", storyUserOneSession, "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts/"+storyUserOneAccount+"/balance", storyUserOneSession, "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/account/v1/merchant_accounts/"+storyMerchantAccount+"/services", storyUserTwoSession, "go-merchant-service", map[string]any{
		"service_code": "coverage", "name": "Go Coverage Service", "description": "Go service workflow",
		"interface_set": []string{"checkout", "webhook"},
	}, http.StatusCreated)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/merchant_accounts/"+storyMerchantAccount+"/services", storyUserTwoSession, "", nil, http.StatusOK)

	topup := apiJSON(t, httpServer, http.MethodPost, "/account/v1/accounts/"+storyUserOneAccount+"/topups", storyUserOneSession, "go-topup", map[string]any{
		"fiat_currency": "USD", "fiat_amount_minor": 900,
	}, http.StatusCreated)
	topupID := requiredString(t, topup, "id")
	providerReference := requiredString(t, topup, "provider_reference")
	apiJSON(t, httpServer, http.MethodPost, "/account/v1/accounts/"+storyUserOneAccount+"/topups", storyUserOneSession, "go-topup", map[string]any{
		"fiat_currency": "USD", "fiat_amount_minor": 900,
	}, http.StatusOK)
	apiJSON(t, fakePayment, http.MethodPost, "/v1/test/confirm", "", "", map[string]any{
		"callback_url": httpServer.URL + "/callbacks/v1/payment_events", "event_id": "go-provider-event",
		"provider_reference": providerReference, "currency": "USD", "amount_minor": 900,
	}, http.StatusOK)
	invoicePage := apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts/"+storyUserOneAccount+"/invoices", storyUserOneSession, "", nil, http.StatusOK)
	invoiceID := firstPageID(t, invoicePage)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts/"+storyUserOneAccount+"/invoices/"+invoiceID, storyUserOneSession, "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts/"+storyUserOneAccount+"/topups", storyUserOneSession, "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/account/v1/accounts/"+storyUserOneAccount+"/topups/"+topupID+"/refunds", storyUserOneSession, "go-refund", map[string]any{
		"amount": map[string]any{"asset": "GIZ_CREDIT", "microcredits": 1_000_000},
	}, http.StatusAccepted)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts/"+storyUserOneAccount+"/transactions", storyUserOneSession, "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts/"+storyUserOneAccount+"/usage?from=2026-08-10T00%3A00%3A00Z&to=2026-08-13T00%3A00%3A00Z", storyUserOneSession, "", nil, http.StatusOK)

	paymentKeyResult := apiJSON(t, httpServer, http.MethodPost, "/account/v1/accounts/"+storyMerchantAccount+"/api_keys", storyUserTwoSession, "go-payment-key", map[string]any{
		"name": "Go merchant payment key", "kind": "payment",
		"scopes": []string{"pay:intents:write", "pay:transactions:read", "pay:webhooks:write"},
	}, http.StatusCreated)
	paymentKey := requiredString(t, paymentKeyResult, "secret")
	webhook := apiJSON(t, httpServer, http.MethodPost, "/pay/v1/webhook_endpoints", paymentKey, "go-webhook", map[string]any{
		"url": fakePayment.URL + "/merchant-webhook", "events": []string{"payment_intent.succeeded", "transaction.reversed"},
	}, http.StatusCreated)
	webhookID := requiredString(t, webhook, "id")
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/webhook_endpoints", paymentKey, "go-webhook", map[string]any{
		"url": fakePayment.URL + "/merchant-webhook", "events": []string{"payment_intent.succeeded", "transaction.reversed"},
	}, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/webhook_endpoints/"+webhookID+"/rotate_secret", paymentKey, "go-webhook-rotate", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPatch, "/pay/v1/webhook_endpoints/"+webhookID, paymentKey, "go-webhook-disable", map[string]any{"status": "disabled"}, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPatch, "/pay/v1/webhook_endpoints/"+webhookID, paymentKey, "go-webhook-enable", map[string]any{"status": "active"}, http.StatusOK)
	apiJSON(t, httpServer, http.MethodGet, "/pay/v1/webhook_endpoints", paymentKey, "", nil, http.StatusOK)

	intent := apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents", paymentKey, "go-intent", map[string]any{
		"service_id": "23000000-0000-4000-8000-000000000001", "external_order_id": "GO-ORDER-1",
		"amount":      map[string]any{"asset": "GIZ_CREDIT", "microcredits": 100_001},
		"description": "Go payment", "expires_at": "2027-08-12T00:00:00Z", "metadata": map[string]any{"source": "go"},
	}, http.StatusCreated)
	intentID := requiredString(t, intent, "id")
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents", paymentKey, "go-intent", map[string]any{
		"service_id": "23000000-0000-4000-8000-000000000001", "external_order_id": "GO-ORDER-1",
		"amount":      map[string]any{"asset": "GIZ_CREDIT", "microcredits": 100_001},
		"description": "Go payment", "expires_at": "2027-08-12T00:00:00Z", "metadata": map[string]any{"source": "go"},
	}, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents", paymentKey, "go-intent", map[string]any{
		"service_id": "23000000-0000-4000-8000-000000000001", "external_order_id": "GO-ORDER-1",
		"amount":      map[string]any{"asset": "GIZ_CREDIT", "microcredits": 100_002},
		"description": "Conflicting Go payment", "expires_at": "2027-08-12T00:00:00Z", "metadata": map[string]any{"source": "go"},
	}, http.StatusConflict)
	apiJSON(t, httpServer, http.MethodGet, "/pay/v1/payment_intents/"+intentID, paymentKey, "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodGet, "/pay/v1/checkout/payment_intents/"+intentID, storyUserOneSession, "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents/"+intentID+"/confirm", storyUserOneSession, "go-confirm", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodGet, "/pay/v1/transactions", paymentKey, "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents/"+intentID+"/reversals", paymentKey, "go-reverse", map[string]any{"reason": "customer requested reversal"}, http.StatusCreated)
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents/"+intentID+"/reversals", paymentKey, "go-reverse", map[string]any{"reason": "customer requested reversal"}, http.StatusOK)

	cancelIntent := apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents", paymentKey, "go-cancel-intent", map[string]any{
		"service_id": "23000000-0000-4000-8000-000000000001", "external_order_id": "GO-ORDER-2",
		"amount":      map[string]any{"asset": "GIZ_CREDIT", "microcredits": 10_000},
		"description": "Go cancellation", "expires_at": "2027-08-12T00:00:00Z", "metadata": map[string]any{},
	}, http.StatusCreated)
	cancelIntentID := requiredString(t, cancelIntent, "id")
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents/"+cancelIntentID+"/cancel", paymentKey, "go-cancel", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents/"+cancelIntentID+"/cancel", paymentKey, "go-cancel-again", nil, http.StatusConflict)
	apiJSON(t, httpServer, http.MethodDelete, "/pay/v1/webhook_endpoints/"+webhookID, paymentKey, "go-webhook-delete", nil, http.StatusNoContent)
	powerSync := apiJSON(t, httpServer, http.MethodPost, "/account/v1/powersync/credentials", storyUserOneSession, "go-powersync", nil, http.StatusOK)
	powerSyncToken := requiredString(t, powerSync, "token")
	apiJSON(t, httpServer, http.MethodPost, "/test/v1/powersync/authorize", powerSyncToken, "", map[string]any{
		"account_id": storyUserOneAccount,
	}, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/test/v1/powersync/authorize", powerSyncToken, "", map[string]any{
		"account_id": storyUserTwoAccount,
	}, http.StatusForbidden)
	apiJSON(t, httpServer, http.MethodPost, "/test/v1/powersync/authorize", "invalid-token", "", map[string]any{
		"account_id": storyUserOneAccount,
	}, http.StatusUnauthorized)

	// Semantic failure stories deliberately use well-formed JSON and valid
	// credentials so they exercise the service/store policy paths rather than
	// stopping at the transport decoder.
	apiJSON(t, httpServer, http.MethodPost, "/account/v1/accounts/"+storyUserOneAccount+"/topups", storyUserOneSession, "go-invalid-topup", map[string]any{
		"fiat_currency": "", "fiat_amount_minor": 0,
	}, http.StatusBadRequest)
	apiJSON(t, httpServer, http.MethodPost, "/account/v1/accounts/"+storyUserOneAccount+"/topups/missing/refunds", storyUserOneSession, "go-missing-refund", map[string]any{
		"amount": map[string]any{"asset": "GIZ_CREDIT", "microcredits": 100},
	}, http.StatusNotFound)
	apiJSON(t, httpServer, http.MethodPost, "/account/v1/merchant_accounts/"+storyMerchantAccount+"/services", storyUserTwoSession, "go-invalid-service-interface", map[string]any{
		"service_code": "invalid-interface", "name": "Invalid Interface", "interface_set": []string{"direct-database"},
	}, http.StatusBadRequest)
	apiJSON(t, httpServer, http.MethodPost, "/account/v1/merchant_accounts/missing/services", storyUserTwoSession, "go-missing-merchant-service", map[string]any{
		"service_code": "missing", "name": "Missing Merchant", "interface_set": []string{"checkout"},
	}, http.StatusNotFound)
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents", paymentKey, "go-invalid-intent", map[string]any{
		"service_id": "", "external_order_id": "", "amount": map[string]any{"asset": "GIZ_CREDIT", "microcredits": 0}, "expires_at": "",
	}, http.StatusBadRequest)
	apiJSON(t, httpServer, http.MethodGet, "/pay/v1/payment_intents/missing", paymentKey, "", nil, http.StatusNotFound)
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents/missing/confirm", storyUserOneSession, "go-confirm-missing", nil, http.StatusNotFound)
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents/missing/cancel", paymentKey, "go-cancel-missing", nil, http.StatusConflict)
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents/missing/reversals", paymentKey, "go-reverse-missing-reason", map[string]any{
		"reason": "",
	}, http.StatusBadRequest)
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents/missing/reversals", paymentKey, "go-reverse-missing", map[string]any{
		"reason": "valid support reason",
	}, http.StatusNotFound)
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/webhook_endpoints", paymentKey, "go-invalid-webhook-event", map[string]any{
		"url": fakePayment.URL + "/merchant-webhook", "events": []string{"unknown.event"},
	}, http.StatusBadRequest)

	limitedGateway := apiJSON(t, httpServer, http.MethodPost, "/account/v1/accounts/"+storyUserOneAccount+"/api_keys", storyUserOneSession, "go-limited-gateway-key", map[string]any{
		"name": "Limited Go gateway key", "kind": "gateway", "scopes": []string{"account:self"},
	}, http.StatusCreated)
	limitedGatewaySecret := requiredString(t, limitedGateway, "secret")
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts/"+storyUserOneAccount+"/balance", limitedGatewaySecret, "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts/"+storyUserTwoAccount+"/balance", limitedGatewaySecret, "", nil, http.StatusForbidden)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts/"+storyUserOneAccount+"/usage", limitedGatewaySecret, "", nil, http.StatusForbidden)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts/"+storyUserOneAccount+"/balance", "invalid-gateway-key", "", nil, http.StatusUnauthorized)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/me", "invalid-user-session", "", nil, http.StatusUnauthorized)

	limitedPayment := apiJSON(t, httpServer, http.MethodPost, "/account/v1/accounts/"+storyMerchantAccount+"/api_keys", storyUserTwoSession, "go-limited-payment-key", map[string]any{
		"name": "Limited Go payment key", "kind": "payment", "scopes": []string{"pay:intents:write"},
	}, http.StatusCreated)
	apiJSON(t, httpServer, http.MethodGet, "/pay/v1/transactions", requiredString(t, limitedPayment, "secret"), "", nil, http.StatusForbidden)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts/"+storyUserOneAccount+"/topups?limit=0", storyUserOneSession, "", nil, http.StatusBadRequest)
	apiJSON(t, httpServer, http.MethodGet, "/account/v1/accounts/"+storyUserOneAccount+"/transactions?cursor=invalid", storyUserOneSession, "", nil, http.StatusBadRequest)

	expiresDuringConfirm := apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents", paymentKey, "go-expire-during-confirm", map[string]any{
		"service_id": "23000000-0000-4000-8000-000000000001", "external_order_id": "GO-EXPIRE-CONFIRM",
		"amount": map[string]any{"asset": "GIZ_CREDIT", "microcredits": 10_000}, "description": "Expires during confirmation",
		"expires_at": "2026-08-12T00:00:01Z", "metadata": map[string]any{},
	}, http.StatusCreated)
	current = current.Add(2 * time.Second)
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents/"+requiredString(t, expiresDuringConfirm, "id")+"/confirm", storyUserOneSession, "go-confirm-expired", nil, http.StatusConflict)

	expiresDuringSweep := apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents", paymentKey, "go-expire-during-sweep", map[string]any{
		"service_id": "23000000-0000-4000-8000-000000000001", "external_order_id": "GO-EXPIRE-SWEEP",
		"amount": map[string]any{"asset": "GIZ_CREDIT", "microcredits": 10_000}, "description": "Expires during sweep",
		"expires_at": "2026-08-12T00:00:03Z", "metadata": map[string]any{},
	}, http.StatusCreated)
	apiJSON(t, httpServer, http.MethodPost, "/test/v1/clock/advance", "", "", map[string]any{"by": "2s"}, http.StatusOK)
	expired := apiJSON(t, httpServer, http.MethodGet, "/pay/v1/payment_intents/"+requiredString(t, expiresDuringSweep, "id"), paymentKey, "", nil, http.StatusOK)
	if expired["status"] != "expired" {
		t.Fatalf("swept intent status=%v", expired["status"])
	}

	frozenIntent := apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents", paymentKey, "go-frozen-payer-intent", map[string]any{
		"service_id": "23000000-0000-4000-8000-000000000001", "external_order_id": "GO-FROZEN-PAYER",
		"amount": map[string]any{"asset": "GIZ_CREDIT", "microcredits": 10_000}, "description": "Frozen payer",
		"expires_at": "2027-08-12T00:00:00Z", "metadata": map[string]any{},
	}, http.StatusCreated)
	if _, err := database.SQL.Exec(`UPDATE ledger_accounts SET status='frozen' WHERE owner_account_id=$1`, storyUserOneAccount); err != nil {
		t.Fatal(err)
	}
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents/"+requiredString(t, frozenIntent, "id")+"/confirm", storyUserOneSession, "go-confirm-frozen-payer", nil, http.StatusLocked)
	if _, err := database.SQL.Exec(`UPDATE ledger_accounts SET status='active' WHERE owner_account_id=$1`, storyUserOneAccount); err != nil {
		t.Fatal(err)
	}

	var remaining int64
	if err := database.SQL.Get(&remaining, `SELECT balance_microcredits FROM account_balances WHERE account_id=$1`, storyUserOneAccount); err != nil {
		t.Fatal(err)
	}
	apiJSON(t, httpServer, http.MethodPost, "/account/v1/accounts/"+storyUserOneAccount+"/transfers", storyUserOneSession, "go-drain-for-insufficient-payment", map[string]any{
		"recipient_account_id": storyUserTwoAccount,
		"amount":               map[string]any{"asset": "GIZ_CREDIT", "microcredits": remaining - 1_000},
		"note":                 "Leave too little for payment",
	}, http.StatusCreated)
	insufficientIntent := apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents", paymentKey, "go-insufficient-payer-intent", map[string]any{
		"service_id": "23000000-0000-4000-8000-000000000001", "external_order_id": "GO-INSUFFICIENT-PAYER",
		"amount": map[string]any{"asset": "GIZ_CREDIT", "microcredits": 10_000}, "description": "Insufficient payer",
		"expires_at": "2027-08-12T00:00:00Z", "metadata": map[string]any{},
	}, http.StatusCreated)
	apiJSON(t, httpServer, http.MethodPost, "/pay/v1/payment_intents/"+requiredString(t, insufficientIntent, "id")+"/confirm", storyUserOneSession, "go-confirm-insufficient-payer", nil, http.StatusConflict)
}

func apiJSON(t *testing.T, server *httptest.Server, method, path, token, idempotencyKey string, body any, wantStatus int) map[string]any {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if response.ContentLength != 0 {
		_ = json.NewDecoder(response.Body).Decode(&result)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status=%d want=%d body=%+v", method, path, response.StatusCode, wantStatus, result)
	}
	return result
}

func firstPageID(t *testing.T, page map[string]any) string {
	t.Helper()
	items, _ := page["data"].([]any)
	if len(items) == 0 {
		t.Fatalf("page has no rows: %+v", page)
	}
	item, _ := items[0].(map[string]any)
	return requiredString(t, item, "id")
}
