package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

// TestRetainedMutationRoutesRejectMalformedCommands keeps validation branches
// attached to the final split surfaces. It is intentionally table-driven so a
// retained operation cannot silently lose its Bad Request behavior when the
// old monolith tests are removed.
func TestRetainedMutationRoutesRejectMalformedCommands(t *testing.T) {
	payDatabase := testdb.OpenGizPayStory(t)
	defer payDatabase.Close()
	wayDatabase := testdb.OpenGizWayStory(t)
	defer wayDatabase.Close()
	pay := testGizPayServer(store.New(payDatabase.SQL)).Handler()
	way := testGizWayServer(store.New(wayDatabase.SQL), nil).Handler()

	type invalidRoute struct {
		name, method, path, token, body string
		way                             bool
	}
	cases := []invalidRoute{
		{"user login", http.MethodPost, "/account/v1/auth/login", "", `{`, false},
		{"user profile", http.MethodPatch, "/account/v1/me", storyUserOneSession, `{}`, false},
		{"API key", http.MethodPost, "/account/v1/accounts/" + storyUserOneAccount + "/api_keys", storyUserOneSession, `{}`, false},
		{"merchant account", http.MethodPost, "/account/v1/merchant_accounts", storyUserOneSession, `{}`, false},
		{"merchant service", http.MethodPost, "/account/v1/merchant_accounts/" + storyMerchantAccount + "/services", storyUserTwoSession, `{}`, false},
		{"credit transfer", http.MethodPost, "/account/v1/accounts/" + storyUserOneAccount + "/transfers", storyUserOneSession, `{}`, false},
		{"topup", http.MethodPost, "/account/v1/accounts/" + storyUserOneAccount + "/topups", storyUserOneSession, `{}`, false},
		{"refund", http.MethodPost, "/account/v1/accounts/" + storyUserOneAccount + "/topups/missing/refunds", storyUserOneSession, `{}`, false},
		{"payment intent", http.MethodPost, "/pay/v1/payment_intents", "invalid-payment-key", `{}`, false},
		{"payment reversal", http.MethodPost, "/pay/v1/payment_intents/missing/reversals", "invalid-payment-key", `{}`, false},
		{"webhook endpoint", http.MethodPost, "/pay/v1/webhook_endpoints", "invalid-payment-key", `{}`, false},
		{"admin login", http.MethodPost, "/admin/v1/auth/login", "", `{`, false},
		{"administrator", http.MethodPost, "/admin/v1/administrators", "gizadm_story_admin", `{}`, false},
		{"administrator update", http.MethodPatch, "/admin/v1/administrators/missing", "gizadm_story_admin", `{`, false},
		{"administrator key", http.MethodPost, "/admin/v1/administrators/missing/api_keys", "gizadm_story_admin", `{}`, false},
		{"user status", http.MethodPost, "/admin/v1/users/missing/status", "gizadm_story_admin", `{}`, false},
		{"balance status", http.MethodPost, "/admin/v1/accounts/missing/balance_status", "gizadm_story_admin", `{}`, false},
		{"merchant review", http.MethodPost, "/admin/v1/merchants/missing/decision", "gizadm_story_admin", `{}`, false},
		{"merchant service decision", http.MethodPost, "/admin/v1/merchant_services/missing/decision", "gizadm_story_admin", `{}`, false},
		{"ledger adjustment", http.MethodPost, "/admin/v1/ledger/adjustments", "gizadm_story_admin", `{}`, false},
		{"ledger reversal", http.MethodPost, "/admin/v1/ledger/transactions/missing/reverse", "gizadm_story_admin", `{}`, false},
		{"gateway node", http.MethodPost, "/admin/v1/gateway_nodes", "gizadm_story_admin", `{}`, false},
		{"node certificate", http.MethodPost, "/admin/v1/gateway_nodes/missing/certificates", "gizadm_story_admin", `{}`, false},
		{"provider", http.MethodPost, "/admin/v1/providers", "gizadm_story_admin", `{}`, true},
		{"provider update", http.MethodPatch, "/admin/v1/providers/missing", "gizadm_story_admin", `{`, true},
		{"endpoint", http.MethodPost, "/admin/v1/providers/missing/endpoints", "gizadm_story_admin", `{}`, true},
		{"endpoint update", http.MethodPatch, "/admin/v1/provider_endpoints/missing", "gizadm_story_admin", `{`, true},
		{"credential rotation", http.MethodPost, "/admin/v1/provider_endpoints/missing/rotate_credential", "gizadm_story_admin", `{}`, true},
		{"model", http.MethodPost, "/admin/v1/models", "gizadm_story_admin", `{}`, true},
		{"model update", http.MethodPatch, "/admin/v1/models/missing", "gizadm_story_admin", `{`, true},
		{"variant", http.MethodPost, "/admin/v1/models/missing/variants", "gizadm_story_admin", `{}`, true},
		{"variant update", http.MethodPatch, "/admin/v1/model_variants/missing", "gizadm_story_admin", `{`, true},
		{"price", http.MethodPost, "/admin/v1/model_variants/missing/prices", "gizadm_story_admin", `{}`, true},
		{"rate publication", http.MethodPost, "/admin/v1/rate_publications", "gizadm_story_admin", `{}`, true},
		{"responses", http.MethodPost, "/v1/responses", "giz_story_user_active_1", `{}`, true},
		{"chat", http.MethodPost, "/v1/chat/completions", "giz_story_user_active_1", `{}`, true},
		{"embedding", http.MethodPost, "/v1/embeddings", "giz_story_user_active_1", `{}`, true},
		{"speech", http.MethodPost, "/v1/audio/speech", "giz_story_user_active_1", `{}`, true},
		{"image", http.MethodPost, "/v1/images/generations", "giz_story_user_active_1", `{}`, true},
		{"anthropic", http.MethodPost, "/v1/messages", "giz_story_user_active_1", `{}`, true},
		{"gemini", http.MethodPost, "/v1beta/models/story-text:generateContent", "giz_story_user_active_1", `{}`, true},
		{"Realtime secret", http.MethodPost, "/v1/realtime/client_secrets", "giz_story_user_active_1", `{}`, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "invalid-"+strings.ReplaceAll(test.name, " ", "-"))
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			handler := pay
			if test.way {
				handler = way
			}
			handler.ServeHTTP(response, request)
			if response.Code < http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
