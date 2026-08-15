package gizwaysql

import (
	"strings"
	"testing"
)

func TestMilestone03MigrationOwnsOnlyGizWayExtensions(t *testing.T) {
	if len(Migrations) != 1 {
		t.Fatalf("Milestone 03 requires one empty-database migration, got %d", len(Migrations))
	}
	lower := strings.ToLower(Migrations[0])
	for _, forbidden := range []string{
		"create table administrators",
		"create table models",
		"create table providers",
		"create table provider_keys",
		"bifrost_key_id",
		"beneficiary_merchant_id",
		" key_hmac ",
	} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("GizWay migration retains pre-Milestone-03 or Bifrost-owned object %q", forbidden)
		}
	}
	for _, required := range []string{
		"create table gizway_user_merchants",
		"create table model_customer_prices",
		"create table provider_key_billing",
		"create table provider_key_prices",
		"create table ai_orders",
		"create table charge_outbox",
		"create schema client_sync",
	} {
		if !strings.Contains(lower, required) {
			t.Errorf("GizWay migration lacks Milestone 03 declaration %q", required)
		}
	}
}
