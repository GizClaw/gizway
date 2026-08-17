package gizwaysql

import (
	"strings"
	"testing"
)

func TestMilestone04GizWaySchemaDeclaresWebReadModels(t *testing.T) {
	lower := strings.ToLower(Migrations[0])
	for _, required := range []string{
		"name text not null",
		"last_used_at timestamptz",
		"earned_microcredits bigint",
		"subscription_key_id text not null",
		"create table model_listings",
		"id text generated always as (model_id || ':' || metric) stored primary key",
		"unique (model_id, metric)",
	} {
		if !strings.Contains(lower, required) {
			t.Errorf("GizWay migration lacks M04 declaration %q", required)
		}
	}
}
