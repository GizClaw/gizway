package powersync

import (
	"strings"
	"testing"
)

func TestEmbeddedAccountScopedProjectionContract(t *testing.T) {
	for _, view := range []string{
		"powersync_account_balances", "powersync_gateway_usage",
		"powersync_credit_transfers", "powersync_payments",
		"powersync_merchant_orders",
	} {
		if !strings.Contains(SyncRules, "FROM "+view+" WHERE account_id = bucket.account_id") {
			t.Errorf("sync rules do not bind %s to account bucket", view)
		}
	}
	for _, table := range []string{"gateway_requests", "credit_transfers", "topups", "refunds", "payment_intents", "ledger_entries"} {
		if !strings.Contains(PublicationSQL, table) {
			t.Errorf("publication omits %s", table)
		}
	}
	if strings.Contains(SyncRules, "credential_ref") || strings.Contains(SyncRules, "response_json") {
		t.Fatal("sync rules expose a secret-bearing source")
	}
	if strings.Contains(SyncRules, "request.parameters") || !strings.Contains(SyncRules, "owner_user_id = request.user_id()") {
		t.Fatal("sync rules trust a client account parameter instead of the signed JWT subject")
	}
	if strings.Contains(PublicationSQL, "api_keys") {
		t.Fatal("PowerSync publication includes secret-bearing API key rows")
	}
}
