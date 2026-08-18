package storage_test

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/testdb"
)

func TestPostgreSQLMigrateGizPayFirstRunAndReplay(t *testing.T) {
	dsn := testdb.NewDatabase(t)
	if err := storage.MigrateGizPayPostgreSQL(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}
	database := openMigrationDatabase(t, dsn)
	var appliedAt time.Time
	if err := database.GetContext(t.Context(), &appliedAt, `SELECT applied_at FROM schema_migrations WHERE service='gizpay' AND version=1`); err != nil {
		t.Fatal(err)
	}
	assertGizPaySystemRows(t, database)
	if err := storage.MigrateGizPayPostgreSQL(t.Context(), dsn); err != nil {
		t.Fatalf("replay: %v", err)
	}
	var replayedAt time.Time
	if err := database.GetContext(t.Context(), &replayedAt, `SELECT applied_at FROM schema_migrations WHERE service='gizpay' AND version=1`); err != nil {
		t.Fatal(err)
	}
	if !replayedAt.Equal(appliedAt) {
		t.Fatalf("migration timestamp changed from %s to %s", appliedAt, replayedAt)
	}
	assertGizPaySystemRows(t, database)
	var businessRows int
	if err := database.GetContext(t.Context(), &businessRows, `SELECT count(*) FROM products`); err != nil {
		t.Fatal(err)
	}
	if businessRows != 0 {
		t.Fatalf("migration inserted %d Product rows", businessRows)
	}
}

func TestPostgreSQLMigrateGizWayConcurrentReplay(t *testing.T) {
	baseDSN := testdb.NewDatabase(t)
	database := openMigrationDatabase(t, baseDSN)
	if _, err := database.ExecContext(t.Context(), `CREATE SCHEMA gizway`); err != nil {
		t.Fatal(err)
	}
	dsn := testdb.SearchPathDSN(t, baseDSN, "gizway")
	start := make(chan struct{})
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			errors <- storage.MigrateGizWayPostgreSQL(context.Background(), dsn)
		})
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent migration: %v", err)
		}
	}
	var versions []int64
	if err := database.SelectContext(t.Context(), &versions, `SELECT version FROM gizway.schema_migrations WHERE service='gizway' ORDER BY version`); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0] != 1 {
		t.Fatalf("migration versions = %v", versions)
	}
	for _, schema := range []string{"bifrost_config", "bifrost_logs"} {
		var exists bool
		if err := database.GetContext(t.Context(), &exists, `SELECT to_regnamespace($1) IS NOT NULL`, schema); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("migration unexpectedly created %s", schema)
		}
	}
}

func TestPostgreSQLMigrateRollsBackAndRetries(t *testing.T) {
	dsn := testdb.NewDatabase(t)
	database := openMigrationDatabase(t, dsn)
	if _, err := database.ExecContext(t.Context(), `CREATE TABLE users (id text primary key)`); err != nil {
		t.Fatal(err)
	}
	if err := storage.MigrateGizPayPostgreSQL(t.Context(), dsn); err == nil {
		t.Fatal("migration unexpectedly succeeded with a conflicting table")
	}
	var historyExists bool
	if err := database.GetContext(t.Context(), &historyExists, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if historyExists {
		t.Fatal("failed migration left schema_migrations behind")
	}
	if _, err := database.ExecContext(t.Context(), `DROP TABLE users`); err != nil {
		t.Fatal(err)
	}
	if err := storage.MigrateGizPayPostgreSQL(t.Context(), dsn); err != nil {
		t.Fatalf("retry: %v", err)
	}
	assertGizPaySystemRows(t, database)
}

func TestPostgreSQLMigrateRejectsWrongOwnerAndInvalidHistory(t *testing.T) {
	t.Run("wrong owner", func(t *testing.T) {
		dsn := testdb.NewDatabase(t)
		if err := storage.MigrateGizWayPostgreSQL(t.Context(), dsn); err != nil {
			t.Fatal(err)
		}
		err := storage.MigrateGizPayPostgreSQL(t.Context(), dsn)
		if err == nil || !strings.Contains(err.Error(), "belongs to gizway, not gizpay") {
			t.Fatalf("owner error = %v", err)
		}
	})

	for name, versions := range map[string][]int64{
		"gap":   {2},
		"newer": {1, 2},
	} {
		t.Run(name, func(t *testing.T) {
			dsn := testdb.NewDatabase(t)
			database := openMigrationDatabase(t, dsn)
			if _, err := database.ExecContext(t.Context(), `CREATE TABLE schema_migrations (
				service TEXT NOT NULL,
				version BIGINT NOT NULL CHECK (version > 0),
				applied_at TIMESTAMPTZ NOT NULL,
				PRIMARY KEY (service,version)
			)`); err != nil {
				t.Fatal(err)
			}
			for _, version := range versions {
				if _, err := database.ExecContext(t.Context(), `INSERT INTO schema_migrations(service,version,applied_at) VALUES ('gizpay',$1,now())`, version); err != nil {
					t.Fatal(err)
				}
			}
			err := storage.MigrateGizPayPostgreSQL(t.Context(), dsn)
			if err == nil || !strings.Contains(err.Error(), "migration history") {
				t.Fatalf("history error = %v", err)
			}
		})
	}
}

func TestPostgreSQLMigrateRejectsConflictingGizPaySystemRows(t *testing.T) {
	dsn := testdb.NewDatabase(t)
	if err := storage.MigrateGizPayPostgreSQL(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}
	database := openMigrationDatabase(t, dsn)
	if _, err := database.ExecContext(t.Context(), `UPDATE users SET display_name='Conflicting Platform' WHERE id=$1`, storage.PlatformUserID); err != nil {
		t.Fatal(err)
	}
	err := storage.MigrateGizPayPostgreSQL(t.Context(), dsn)
	if err == nil || !strings.Contains(err.Error(), "conflicting GizPay system row") {
		t.Fatalf("system row error = %v", err)
	}
	var displayName string
	if err := database.GetContext(t.Context(), &displayName, `SELECT display_name FROM users WHERE id=$1`, storage.PlatformUserID); err != nil {
		t.Fatal(err)
	}
	if displayName != "Conflicting Platform" {
		t.Fatalf("migration overwrote conflicting display name with %q", displayName)
	}
}

func TestPostgreSQLMigrateRestoresMissingGizPaySystemRow(t *testing.T) {
	dsn := testdb.NewDatabase(t)
	if err := storage.MigrateGizPayPostgreSQL(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}
	database := openMigrationDatabase(t, dsn)
	if _, err := database.ExecContext(t.Context(), `DELETE FROM ledger_accounts WHERE id=$1`, storage.PlatformClearingID); err != nil {
		t.Fatal(err)
	}
	if err := storage.MigrateGizPayPostgreSQL(t.Context(), dsn); err != nil {
		t.Fatalf("restore missing system row: %v", err)
	}
	assertGizPaySystemRows(t, database)
}

func TestPostgreSQLMigrationClosesConnectionsOnFailure(t *testing.T) {
	baseDSN := testdb.NewDatabase(t)
	parsed, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	applicationName := "gizway_migration_connection_test"
	query := parsed.Query()
	query.Set("application_name", applicationName)
	parsed.RawQuery = query.Encode()
	dsn := parsed.String()
	database := openMigrationDatabase(t, baseDSN)
	if _, err := database.ExecContext(t.Context(), `CREATE TABLE users (id text primary key)`); err != nil {
		t.Fatal(err)
	}
	if err := storage.MigrateGizPayPostgreSQL(t.Context(), dsn); err == nil {
		t.Fatal("migration unexpectedly succeeded")
	}
	var connections int
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := database.GetContext(t.Context(), &connections, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name=$1`, applicationName); err != nil {
			t.Fatal(err)
		}
		if connections == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if connections != 0 {
		t.Fatalf("migration left %d PostgreSQL connections open", connections)
	}
}

func openMigrationDatabase(t *testing.T, dsn string) *sqlx.DB {
	t.Helper()
	database, err := sqlx.ConnectContext(t.Context(), "postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func assertGizPaySystemRows(t *testing.T, database *sqlx.DB) {
	t.Helper()
	var user struct {
		Issuer      string `db:"issuer"`
		Subject     string `db:"subject"`
		Email       string `db:"email"`
		DisplayName string `db:"display_name"`
		Status      string `db:"status"`
	}
	if err := database.GetContext(t.Context(), &user, `SELECT identity_issuer AS issuer,identity_subject AS subject,email,display_name,status FROM users WHERE id=$1`, storage.PlatformUserID); err != nil {
		t.Fatal(err)
	}
	if user.Issuer != "urn:gizpay:system" || user.Subject != "platform" || user.Email != "" || user.DisplayName != "GizPay Platform" || user.Status != "active" {
		t.Fatalf("platform user = %+v", user)
	}
	var owner, accountStatus string
	if err := database.QueryRowxContext(t.Context(), `SELECT owner_user_id,status FROM accounts WHERE id=$1`, storage.PlatformAccountID).Scan(&owner, &accountStatus); err != nil {
		t.Fatal(err)
	}
	if owner != storage.PlatformUserID || accountStatus != "active" {
		t.Fatalf("platform account = owner %q status %q", owner, accountStatus)
	}
	for id, expected := range map[string]struct {
		Owner, Asset, Status string
	}{
		storage.PlatformCreditID:   {Owner: storage.PlatformAccountID, Asset: "credit", Status: "active"},
		storage.PlatformClearingID: {Owner: "", Asset: "clearing", Status: "active"},
	} {
		var owner *string
		var asset, status string
		if err := database.QueryRowxContext(t.Context(), `SELECT owner_account_id,asset_code,status FROM ledger_accounts WHERE id=$1`, id).Scan(&owner, &asset, &status); err != nil {
			t.Fatal(err)
		}
		ownerValue := ""
		if owner != nil {
			ownerValue = *owner
		}
		if ownerValue != expected.Owner || asset != expected.Asset || status != expected.Status {
			t.Fatalf("ledger account %s = owner %q asset %q status %q", id, ownerValue, asset, status)
		}
	}
}
