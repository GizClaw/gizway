package gizpaysql

import (
	"strings"
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

func TestMigrationsParse(t *testing.T) {
	for index, migration := range Migrations {
		if _, err := pg_query.Parse(migration); err != nil {
			t.Fatalf("parse GizPay migration %d: %v", index+1, err)
		}
	}
}

func TestMilestone02MigrationIsSingleBreakingSchema(t *testing.T) {
	if len(Migrations) != 1 {
		t.Fatalf("Milestone 02 requires one empty-database migration, got %d", len(Migrations))
	}
	lower := strings.ToLower(Migrations[0])
	for _, forbidden := range []string{
		"create table payment_intents", "create table topups", "create table refunds",
		"create table gateway_nodes", "create table credit_holds", "create table api_keys",
	} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("GizPay migration retains pre-Milestone-02 table %q", forbidden)
		}
	}
	for _, required := range []string{
		"create table service_principals", "create table merchants", "create table products",
		"create table subscriptions", "create table subscription_api_keys", "create table payg_charges",
		"create table charge_commissions",
	} {
		if !strings.Contains(lower, required) {
			t.Errorf("GizPay migration lacks target table declaration %q", required)
		}
	}
}
