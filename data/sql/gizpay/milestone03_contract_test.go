package gizpaysql

import (
	"strings"
	"testing"
)

func TestMilestone03MigrationIsSingleBreakingSchema(t *testing.T) {
	if len(Migrations) != 1 {
		t.Fatalf("Milestone 03 requires one empty-database migration, got %d", len(Migrations))
	}
	lower := strings.ToLower(Migrations[0])
	for _, forbidden := range []string{
		"create table subscription_api_keys",
		"encrypted_key",
		"encryption_version",
		"create table payment_intents",
		"create table refunds",
		"create table transfers",
	} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("GizPay migration retains pre-Milestone-03 object %q", forbidden)
		}
	}
	for _, required := range []string{
		"create table users",
		"create table accounts",
		"create table ledger_accounts",
		"create table merchants",
		"create table products",
		"create table subscriptions",
		"create table subscription_keys",
		"create table payg_charges",
		"create table charge_commissions",
		"create table topups",
		"create schema client_sync",
	} {
		if !strings.Contains(lower, required) {
			t.Errorf("GizPay migration lacks Milestone 03 declaration %q", required)
		}
	}
}
