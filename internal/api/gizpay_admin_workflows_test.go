package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	merchantservice "github.com/idy/gizway/internal/service/merchant"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

// TestGizPayAdministratorWorkflows exercises the central operator surface on
// the GizPay-only schema. Regional Catalog and execution administration are
// deliberately absent and are tested against the separate GizWay database.
func TestGizPayAdministratorWorkflows(t *testing.T) {
	database := testdb.OpenGizPayStory(t)
	defer database.Close()
	now := func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) }
	repository, err := store.NewWithSecretKey(database.SQL, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	repository.ConfigureClock(now)
	merchant := merchantservice.NewConfigured(repository, nil, true, "https://pay.gizway.test")
	merchant.ConfigureClock(now)
	server := NewWithServicesAndClockSurface(repository, nil, nil, merchant, now, nil, SurfaceGizPay)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	login := apiJSON(t, httpServer, http.MethodPost, "/admin/v1/auth/login", "", "go-admin-login", map[string]any{
		"email": "admin@gizway.test", "password": "story-admin-password",
	}, http.StatusOK)
	adminSession := requiredString(t, login, "access_token")
	apiJSON(t, httpServer, http.MethodGet, "/admin/v1/me", adminSession, "", nil, http.StatusOK)

	created := apiJSON(t, httpServer, http.MethodPost, "/admin/v1/administrators", adminSession, "go-create-admin", map[string]any{
		"email": "go-admin@gizway.test", "display_name": "Go Administrator", "password": "go-admin-password",
	}, http.StatusCreated)
	administratorID := requiredString(t, created, "id")
	apiJSON(t, httpServer, http.MethodGet, "/admin/v1/administrators", "gizadm_story_admin", "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodGet, "/admin/v1/administrators/"+administratorID, "gizadm_story_admin", "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPatch, "/admin/v1/administrators/"+administratorID, "gizadm_story_admin", "go-update-admin", map[string]any{
		"display_name": "Updated Go Administrator", "password": "go-admin-password-rotated", "reason": "coverage rotation",
	}, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/auth/login", "", "go-admin-new-login", map[string]any{
		"email": "go-admin@gizway.test", "password": "go-admin-password-rotated",
	}, http.StatusOK)
	adminKey := apiJSON(t, httpServer, http.MethodPost, "/admin/v1/administrators/"+administratorID+"/api_keys", "gizadm_story_admin", "go-admin-key", map[string]any{
		"name": "Go automation",
	}, http.StatusCreated)
	adminKeyID := requiredString(t, adminKey, "id")
	apiJSON(t, httpServer, http.MethodGet, "/admin/v1/administrators/"+administratorID+"/api_keys", "gizadm_story_admin", "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/administrators/"+administratorID+"/api_keys/"+adminKeyID+"/revoke", "gizadm_story_admin", "go-revoke-admin-key", map[string]any{
		"reason": "coverage rotation",
	}, http.StatusNoContent)

	apiJSON(t, httpServer, http.MethodGet, "/admin/v1/users", "gizadm_story_admin", "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodGet, "/admin/v1/users/11000000-0000-4000-8000-000000000003", "gizadm_story_admin", "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/users/11000000-0000-4000-8000-000000000003/status", "gizadm_story_admin", "go-activate-user", map[string]any{
		"status": "active", "reason": "coverage review",
	}, http.StatusOK)

	merchantApplication := apiJSON(t, httpServer, http.MethodPost, "/account/v1/merchant_accounts", storyUserOneSession, "go-admin-review-merchant", map[string]any{
		"name": "Go Review Merchant", "legal_name": "Go Review Merchant LLC", "public_name": "Go Review", "country_code": "US",
	}, http.StatusCreated)
	account, _ := merchantApplication["account"].(map[string]any)
	merchantID := requiredString(t, account, "id")
	apiJSON(t, httpServer, http.MethodGet, "/admin/v1/merchants/"+merchantID, "gizadm_story_admin", "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/merchants/"+merchantID+"/decision", "gizadm_story_admin", "go-approve-merchant", map[string]any{
		"decision": "approve", "review_level": "enhanced", "reason": "coverage approval",
	}, http.StatusOK)
	apiJSON(t, httpServer, http.MethodGet, "/admin/v1/merchants", "gizadm_story_admin", "", nil, http.StatusOK)

	apiJSON(t, httpServer, http.MethodGet, "/admin/v1/api_keys", "gizadm_story_admin", "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/api_keys/31000000-0000-4000-8000-000000000003/revoke", "gizadm_story_admin", "go-revoke-user-key", map[string]any{
		"reason": "coverage revocation",
	}, http.StatusNoContent)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/accounts/"+storyUserOneAccount+"/balance_status", "gizadm_story_admin", "go-freeze-balance", map[string]any{
		"status": "frozen", "reason": "coverage freeze",
	}, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/accounts/"+storyUserOneAccount+"/balance_status", "gizadm_story_admin", "go-unfreeze-balance", map[string]any{
		"status": "active", "reason": "coverage unfreeze",
	}, http.StatusOK)

	adjustment := apiJSON(t, httpServer, http.MethodPost, "/admin/v1/ledger/adjustments", "gizadm_story_admin", "go-adjustment", map[string]any{
		"description": "Go support adjustment", "reason": "coverage correction",
		"entries": []map[string]any{
			{"ledger_account_id": "b1000000-0000-4000-8000-000000000004", "direction": "debit", "amount_microcredits": 10},
			{"ledger_account_id": "b1000000-0000-4000-8000-000000000001", "direction": "credit", "amount_microcredits": 10},
		},
	}, http.StatusCreated)
	adjustmentID := requiredString(t, adjustment, "id")
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/ledger/transactions/"+adjustmentID+"/reverse", "gizadm_story_admin", "go-reverse-adjustment", map[string]any{
		"reason": "coverage reversal",
	}, http.StatusCreated)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/ledger/transactions/"+adjustmentID+"/reverse", "gizadm_story_admin", "go-reverse-adjustment", map[string]any{
		"reason": "coverage reversal",
	}, http.StatusCreated)
	apiJSON(t, httpServer, http.MethodGet, "/admin/v1/ledger/accounts", "gizadm_story_admin", "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodGet, "/admin/v1/ledger/transactions?transaction_type=reversal", "gizadm_story_admin", "", nil, http.StatusOK)

	// A terminal delivery has no automatic successor and is therefore eligible
	// for an explicit, idempotent administrator retry command.
	for _, fixture := range []struct {
		statement string
		args      []any
	}{
		{`INSERT INTO webhook_endpoints(id,merchant_account_id,url,events,signing_secret,status,created_at,updated_at) VALUES ('go-endpoint',$1,'https://example.invalid/hook','["payment_intent.succeeded"]','unused','active',$2,$2)`, []any{storyMerchantAccount, "2026-08-12T00:00:00.000000000Z"}},
		{`INSERT INTO webhook_events(id,merchant_account_id,event_type,resource_id,payload,created_at) VALUES ('go-event',$1,'payment_intent.succeeded','go-intent','{}',$2)`, []any{storyMerchantAccount, "2026-08-12T00:00:00.000000000Z"}},
		{`INSERT INTO webhook_deliveries(id,event_id,endpoint_id,attempt,status,error,created_at,completed_at) VALUES ('go-delivery','go-event','go-endpoint',5,'exhausted','coverage failure',$1,$1)`, []any{"2026-08-12T00:00:00.000000000Z"}},
	} {
		if _, err := database.SQL.Exec(fixture.statement, fixture.args...); err != nil {
			t.Fatal(err)
		}
	}
	apiJSON(t, httpServer, http.MethodGet, "/admin/v1/webhook_deliveries?status=exhausted", "gizadm_story_admin", "", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/webhook_deliveries/go-delivery/retry", "gizadm_story_admin", "go-retry-delivery", nil, http.StatusAccepted)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/webhook_deliveries/go-delivery/retry", "gizadm_story_admin", "go-retry-delivery", nil, http.StatusAccepted)

	for _, path := range []string{
		"/admin/v1/overview", "/admin/v1/payments", "/admin/v1/audit_events",
		"/admin/v1/received_usage", "/admin/v1/rate_publications",
	} {
		apiJSON(t, httpServer, http.MethodGet, path, "gizadm_story_admin", "", nil, http.StatusOK)
	}
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/rate_publications/ratepub_story_global_1/disable", "gizadm_story_admin", "go-disable-publication", map[string]any{
		"reason": "coverage publication retirement",
	}, http.StatusNoContent)

	// Well-formed administrator failures exercise final GizPay store policy;
	// they are distinct from malformed-request coverage at the HTTP decoder.
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/administrators", "gizadm_story_admin", "go-duplicate-admin", map[string]any{
		"email": "go-admin@gizway.test", "display_name": "Duplicate", "password": "go-admin-password",
	}, http.StatusConflict)
	apiJSON(t, httpServer, http.MethodPatch, "/admin/v1/administrators/missing", "gizadm_story_admin", "go-update-missing-admin", map[string]any{
		"display_name": "Missing", "password": "go-admin-password", "reason": "coverage",
	}, http.StatusNotFound)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/administrators/missing/api_keys", "gizadm_story_admin", "go-key-missing-admin", map[string]any{
		"name": "Missing owner",
	}, http.StatusConflict)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/administrators/"+administratorID+"/api_keys/missing/revoke", "gizadm_story_admin", "go-revoke-missing-admin-key", map[string]any{
		"reason": "coverage",
	}, http.StatusNotFound)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/users/missing/status", "gizadm_story_admin", "go-status-missing-user", map[string]any{
		"status": "suspended", "reason": "coverage",
	}, http.StatusNotFound)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/accounts/missing/balance_status", "gizadm_story_admin", "go-status-missing-account", map[string]any{
		"status": "frozen", "reason": "coverage",
	}, http.StatusNotFound)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/merchants/missing/decision", "gizadm_story_admin", "go-decide-missing-merchant", map[string]any{
		"decision": "approve", "review_level": "standard", "reason": "coverage",
	}, http.StatusNotFound)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/merchant_services/missing/decision", "gizadm_story_admin", "go-decide-missing-service", map[string]any{
		"decision": "approve", "reason": "coverage",
	}, http.StatusConflict)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/api_keys/missing/revoke", "gizadm_story_admin", "go-revoke-missing-customer-key", map[string]any{
		"reason": "coverage",
	}, http.StatusNotFound)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/ledger/adjustments", "gizadm_story_admin", "go-unbalanced-adjustment", map[string]any{
		"description": "Unbalanced", "reason": "coverage",
		"entries": []map[string]any{{"ledger_account_id": "b1000000-0000-4000-8000-000000000001", "direction": "credit", "amount_microcredits": 1}},
	}, http.StatusBadRequest)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/ledger/transactions/missing/reverse", "gizadm_story_admin", "go-reverse-missing-ledger", map[string]any{
		"reason": "coverage",
	}, http.StatusNotFound)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/webhook_deliveries/missing/retry", "gizadm_story_admin", "go-retry-missing-delivery", nil, http.StatusNotFound)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/rate_publications/missing/disable", "gizadm_story_admin", "go-disable-missing-publication", map[string]any{
		"reason": "coverage",
	}, http.StatusNotFound)
	refreshed := apiJSON(t, httpServer, http.MethodPost, "/admin/v1/auth/refresh", adminSession, "go-admin-refresh", nil, http.StatusOK)
	apiJSON(t, httpServer, http.MethodPost, "/admin/v1/auth/logout", requiredString(t, refreshed, "access_token"), "go-admin-logout", nil, http.StatusNoContent)
}
