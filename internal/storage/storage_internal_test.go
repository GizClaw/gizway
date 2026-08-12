package storage

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestEmptyStorageClose(t *testing.T) {
	if err := (&Storage{}).Close(); err != nil {
		t.Fatalf("empty Close: %v", err)
	}
}

func TestPostgreSQLSetupRollsBackSchemaWhenSeedFails(t *testing.T) {
	dsn := os.Getenv("GIZWAY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("GIZWAY_TEST_POSTGRES_DSN is required; run tests through scripts/test-unit")
	}
	database, err := sqlx.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	schema := "gizway_setup_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := database.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	defer database.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)

	if _, err := database.Exec(`SET search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	err = applyServiceMigrations(context.Background(), database, "probe",
		[]string{`CREATE TABLE setup_probe (id INTEGER PRIMARY KEY)`},
		`INSERT INTO missing_table(id) VALUES (1)`)
	if err == nil || !strings.Contains(err.Error(), "apply probe story fixture") {
		t.Fatalf("setup error = %v, want seed failure", err)
	}
	var tableName *string
	if err := database.Get(&tableName, `SELECT to_regclass($1)::TEXT`, schema+`.setup_probe`); err != nil {
		t.Fatal(err)
	}
	if tableName != nil {
		t.Fatalf("schema change survived failed seed: %q", *tableName)
	}
}
