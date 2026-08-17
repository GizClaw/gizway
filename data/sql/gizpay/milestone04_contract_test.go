package gizpaysql

import (
	"strings"
	"testing"
)

func TestMilestone04GizPaySchemaDeclaresWebReadModels(t *testing.T) {
	lower := strings.ToLower(Migrations[0])
	for _, required := range []string{
		"email text not null",
		"display_name text not null",
		"unique (account_id, product_id)",
		"name text not null",
		"last_used_at timestamptz",
		"create table product_listings",
		"create table client_sync.user_profiles",
		"merchant_id text not null",
	} {
		if !strings.Contains(lower, required) {
			t.Errorf("GizPay migration lacks M04 declaration %q", required)
		}
	}
}
