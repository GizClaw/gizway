package gizwaysql

import (
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
