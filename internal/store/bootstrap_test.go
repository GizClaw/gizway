package store_test

import (
	"errors"
	"testing"

	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

func TestPostgreSQLGizPayBootstrapIsExactRetryOnlyAndCreatesSystemLedgers(t *testing.T) {
	database, err := storage.OpenGizPayPostgreSQL(testdb.NewSchema(t), true)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := store.New(database.SQL)
	at := "2026-08-12T00:00:00.000000000Z"
	created, replayed, err := repository.BootstrapAdministrator(t.Context(), "ADMIN@EXAMPLE.COM", "Initial Admin", "secret-password", at)
	if err != nil || replayed || created.Email != "admin@example.com" {
		t.Fatalf("initial bootstrap=(%+v,%t,%v)", created, replayed, err)
	}
	retried, replayed, err := repository.BootstrapAdministrator(t.Context(), "admin@example.com", "Initial Admin", "secret-password", at)
	if err != nil || !replayed || retried.ID != created.ID {
		t.Fatalf("bootstrap replay=(%+v,%t,%v)", retried, replayed, err)
	}
	if _, _, err := repository.BootstrapAdministrator(t.Context(), "other@example.com", "Other", "secret-password", at); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("conflicting bootstrap error=%v", err)
	}
	var administrators, ledgers int
	if err := database.SQL.Get(&administrators, `SELECT COUNT(*) FROM administrators`); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.Get(&ledgers, `SELECT COUNT(*) FROM ledger_accounts WHERE code IN ('SYSTEM:CREDIT_LIABILITY','SYSTEM:PLATFORM_FEE_REVENUE')`); err != nil {
		t.Fatal(err)
	}
	if administrators != 1 || ledgers != 2 {
		t.Fatalf("bootstrap rows administrators=%d ledgers=%d", administrators, ledgers)
	}
}

func TestPostgreSQLGizWayBootstrapCreatesAnIndependentRegionalAdministrator(t *testing.T) {
	database, err := storage.OpenGizWayPostgreSQL(testdb.NewSchema(t), true)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := store.New(database.SQL)
	at := "2026-08-12T00:00:00.000000000Z"
	created, replayed, err := repository.BootstrapRegionalAdministrator(t.Context(), "CN-OPS@EXAMPLE.COM", "CN Operator", "regional-password", at)
	if err != nil || replayed || created.Email != "cn-ops@example.com" {
		t.Fatalf("initial regional bootstrap=(%+v,%t,%v)", created, replayed, err)
	}
	retried, replayed, err := repository.BootstrapRegionalAdministrator(t.Context(), "cn-ops@example.com", "CN Operator", "regional-password", at)
	if err != nil || !replayed || retried.ID != created.ID {
		t.Fatalf("regional bootstrap replay=(%+v,%t,%v)", retried, replayed, err)
	}
	if _, _, err := repository.BootstrapRegionalAdministrator(t.Context(), "global-ops@example.com", "Global Operator", "regional-password", at); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("conflicting regional bootstrap error=%v", err)
	}
	var auditReason string
	if err := database.SQL.Get(&auditReason, `SELECT reason FROM audit_events WHERE action='administrator.bootstrapped'`); err != nil || auditReason != "initial GizWay bootstrap" {
		t.Fatalf("regional bootstrap audit reason=%q err=%v", auditReason, err)
	}
}
