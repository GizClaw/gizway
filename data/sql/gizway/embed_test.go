package gizwaysql

import (
	"strings"
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

func TestMigrationsParse(t *testing.T) {
	for index, migration := range Migrations {
		if _, err := pg_query.Parse(migration); err != nil {
			t.Fatalf("parse GizWay migration %d: %v", index+1, err)
		}
	}
}

func TestMilestone02MigrationDoesNotOwnBifrostSchemas(t *testing.T) {
	if len(Migrations) != 1 {
		t.Fatalf("Milestone 02 requires one empty-database migration, got %d", len(Migrations))
	}
	lower := strings.ToLower(Migrations[0])
	for _, forbidden := range []string{
		"bifrost_config", "bifrost_logs", "create table providers", "create table provider_api_keys",
		"create table provider_endpoints", "create table subscription_key_states",
	} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("GizWay migration contains Bifrost-owned or forbidden object %q", forbidden)
		}
	}
}
