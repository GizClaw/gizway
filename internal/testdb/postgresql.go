// Package testdb provides isolated PostgreSQL schemas for local tests.
package testdb

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"github.com/idy/gizway/internal/storage"
)

// OpenStory creates, migrates, and seeds one schema owned by the test.
func OpenStory(t testing.TB) *storage.Storage {
	t.Helper()
	return open(t, true)
}

// OpenEmpty creates and migrates one schema without fixture rows.
func OpenEmpty(t testing.TB) *storage.Storage {
	t.Helper()
	return open(t, false)
}

func open(t testing.TB, storySeed bool) *storage.Storage {
	t.Helper()
	scopedDSN := NewSchema(t)
	var database *storage.Storage
	var err error
	if storySeed {
		database, err = storage.OpenStoryPostgreSQL(scopedDSN)
	} else {
		database, err = storage.OpenPostgreSQL(scopedDSN, true)
	}
	if err != nil {
		t.Fatalf("open isolated PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// NewSchema creates one empty schema and returns a scoped DSN. The schema is
// dropped automatically when the test finishes.
func NewSchema(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv("GIZWAY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("GIZWAY_TEST_POSTGRES_DSN is required; run tests through scripts/test-unit")
	}
	schema := "gizway_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	bootstrap, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.ExecContext(context.Background(), `CREATE SCHEMA `+schema); err != nil {
		_ = bootstrap.Close()
		t.Fatalf("create PostgreSQL test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = bootstrap.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		_ = bootstrap.Close()
	})

	return SearchPathDSN(t, dsn, schema)
}

// SearchPathDSN scopes a PostgreSQL connection to one test-owned schema.
func SearchPathDSN(t testing.TB, dsn, schema string) string {
	t.Helper()
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return dsn + " search_path=" + schema
}
