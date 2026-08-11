package sqlassets

import (
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

func TestPostgreSQLSchemaParses(t *testing.T) {
	if _, err := pg_query.Parse(PostgreSQLSchema); err != nil {
		t.Fatalf("parse PostgreSQL migration: %v", err)
	}
}
