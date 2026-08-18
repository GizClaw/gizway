// Package storage owns Gizway's direct PostgreSQL connection and schema setup.
package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	gizpaysql "github.com/idy/gizway/data/sql/gizpay"
	gizwaysql "github.com/idy/gizway/data/sql/gizway"
)

// Storage owns the PostgreSQL connection pool.
type Storage struct {
	SQL *sqlx.DB
}

// OpenGizPayPostgreSQL initializes a fresh control-plane-only database.
func OpenGizPayPostgreSQL(dsn string, initialize bool) (*Storage, error) {
	return openServicePostgreSQL(dsn, initialize, "gizpay", gizpaysql.Migrations, "", true)
}

// OpenGizWayPostgreSQL initializes one fresh regional data-plane database.
func OpenGizWayPostgreSQL(dsn string, initialize bool) (*Storage, error) {
	return openServicePostgreSQL(dsn, initialize, "gizway", gizwaysql.Migrations, "", false)
}

// OpenGizPayStoryPostgreSQL creates a fresh GizPay schema plus its own test
// fixture. It never imports or executes regional SQL.
func OpenGizPayStoryPostgreSQL(dsn string) (*Storage, error) {
	return openServicePostgreSQL(dsn, true, "gizpay", gizpaysql.Migrations, gizpaysql.StoryBaseSeed, true)
}

const (
	PlatformUserID     = "usr_platform"
	PlatformAccountID  = "acct_platform"
	PlatformCreditID   = "led_acct_platform"
	PlatformClearingID = "led_clearing"
)

// initializeGizPaySystem creates the ledger principals required for the first
// real Charge. This is application bootstrap, not story seed data, and is
// deliberately idempotent for retrying an empty-environment initialization.
func initializeGizPaySystem(ctx context.Context, tx *sqlx.Tx) error {
	statements := []string{
		`INSERT INTO users(id,identity_issuer,identity_subject,email,display_name,status) VALUES
		 ('usr_platform','urn:gizpay:system','platform','','GizPay Platform','active') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO accounts(id,owner_user_id,status) VALUES
		 ('acct_platform','usr_platform','active') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO ledger_accounts(id,owner_account_id,asset_code,status) VALUES
		 ('led_acct_platform','acct_platform','credit','active'),
		 ('led_clearing',NULL,'clearing','active') ON CONFLICT (id) DO NOTHING`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize GizPay system ledger: %w", err)
		}
	}
	return validateGizPaySystem(ctx, tx)
}

// OpenGizWayStoryPostgreSQL creates a fresh regional schema plus its own test
// fixture. It never imports or executes GizPay SQL.
func OpenGizWayStoryPostgreSQL(dsn string) (*Storage, error) {
	return openServicePostgreSQL(dsn, true, "gizway", gizwaysql.Migrations, gizwaysql.StoryBaseSeed, false)
}

// OpenExistingPostgreSQL reopens an initialized schema after an intentional
// process crash without reapplying migration or fixture data.
func OpenExistingPostgreSQL(dsn string) (*Storage, error) {
	database, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	result := &Storage{SQL: database}
	if err := database.Ping(); err != nil {
		_ = result.Close()
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	return result, nil
}

func openServicePostgreSQL(dsn string, initialize bool, service string, migrations []string, seedSQL string, initializeSystem bool) (*Storage, error) {
	database, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	result := &Storage{SQL: database}
	if err := database.Ping(); err != nil {
		_ = result.Close()
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	if initialize {
		if err := applyServiceMigrationsWithSystem(context.Background(), database, service, migrations, seedSQL, initializeSystem); err != nil {
			_ = result.Close()
			return nil, err
		}
	}
	return result, nil
}

// applyServiceMigrations gives each binary an explicit, independently
// versioned schema history. Initialization is retry-safe for deployment jobs,
// but a database already owned by the other service is rejected instead of
// being silently reshaped into a cross-plane database.
func applyServiceMigrations(ctx context.Context, database *sqlx.DB, service string, migrations []string, seedSQL string) error {
	return applyServiceMigrationsWithSystem(ctx, database, service, migrations, seedSQL, false)
}

func applyServiceMigrationsWithSystem(ctx context.Context, database *sqlx.DB, service string, migrations []string, seedSQL string, initializeSystem bool) error {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s migrations: %w", service, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_database() || ':' || current_schema(), 0))`); err != nil {
		return fmt.Errorf("lock %s migrations: %w", service, err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		service TEXT NOT NULL,
		version BIGINT NOT NULL CHECK (version > 0),
		applied_at TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (service,version)
	)`); err != nil {
		return fmt.Errorf("create %s migration history: %w", service, err)
	}
	var owners []string
	if err := tx.SelectContext(ctx, &owners, `SELECT DISTINCT service FROM schema_migrations WHERE service<>$1`, service); err != nil {
		return fmt.Errorf("inspect %s migration owner: %w", service, err)
	}
	if len(owners) != 0 {
		return fmt.Errorf("database belongs to %s, not %s", owners[0], service)
	}
	var appliedVersions []int64
	if err := tx.SelectContext(ctx, &appliedVersions, `SELECT version FROM schema_migrations WHERE service=$1 ORDER BY version`, service); err != nil {
		return fmt.Errorf("inspect %s migration history: %w", service, err)
	}
	for index, version := range appliedVersions {
		expected := int64(index + 1)
		if version != expected || version > int64(len(migrations)) {
			return fmt.Errorf("invalid %s migration history: expected version %d, found %d", service, expected, version)
		}
	}
	for index, migration := range migrations {
		version := int64(index + 1)
		var applied bool
		if err := tx.GetContext(ctx, &applied, `SELECT EXISTS (
			SELECT 1 FROM schema_migrations WHERE service=$1 AND version=$2
		)`, service, version); err != nil {
			return fmt.Errorf("read %s migration %d: %w", service, version, err)
		}
		if applied {
			continue
		}
		if _, err := tx.ExecContext(ctx, migration); err != nil {
			return fmt.Errorf("apply %s migration %d: %w", service, version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(service,version,applied_at) VALUES ($1,$2,$3)`, service, version, time.Now().UTC()); err != nil {
			return fmt.Errorf("record %s migration %d: %w", service, version, err)
		}
	}
	if seedSQL != "" {
		if _, err := tx.ExecContext(ctx, seedSQL); err != nil {
			return fmt.Errorf("apply %s story fixture: %w", service, err)
		}
	}
	if initializeSystem {
		if err := initializeGizPaySystem(ctx, tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s migrations: %w", service, err)
	}
	return nil
}

// MigrateGizPayPostgreSQL applies only GizPay-owned schema and fixed system rows.
func MigrateGizPayPostgreSQL(ctx context.Context, dsn string) error {
	return migrateServicePostgreSQL(ctx, dsn, "gizpay", gizpaysql.Migrations, true)
}

// MigrateGizWayPostgreSQL applies only GizWay-owned schema migrations.
func MigrateGizWayPostgreSQL(ctx context.Context, dsn string) error {
	return migrateServicePostgreSQL(ctx, dsn, "gizway", gizwaysql.Migrations, false)
}

func migrateServicePostgreSQL(ctx context.Context, dsn, service string, migrations []string, initializeSystem bool) error {
	database, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("open PostgreSQL for %s migration: %w", service, err)
	}
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		return fmt.Errorf("connect PostgreSQL for %s migration: %w", service, err)
	}
	return applyServiceMigrationsWithSystem(ctx, database, service, migrations, "", initializeSystem)
}

func validateGizPaySystem(ctx context.Context, tx *sqlx.Tx) error {
	var valid bool
	if err := tx.GetContext(ctx, &valid, `SELECT
		EXISTS (SELECT 1 FROM users WHERE id=$1 AND identity_issuer='urn:gizpay:system' AND identity_subject='platform' AND email='' AND display_name='GizPay Platform' AND status='active') AND
		EXISTS (SELECT 1 FROM accounts WHERE id=$2 AND owner_user_id=$1 AND status='active') AND
		EXISTS (SELECT 1 FROM ledger_accounts WHERE id=$3 AND owner_account_id=$2 AND asset_code='credit' AND status='active') AND
		EXISTS (SELECT 1 FROM ledger_accounts WHERE id=$4 AND owner_account_id IS NULL AND asset_code='clearing' AND status='active')`,
		PlatformUserID, PlatformAccountID, PlatformCreditID, PlatformClearingID); err != nil {
		return fmt.Errorf("validate GizPay system rows: %w", err)
	}
	if !valid {
		return fmt.Errorf("conflicting GizPay system row")
	}
	return nil
}

// Close releases the PostgreSQL connection pool.
func (d *Storage) Close() error {
	if d == nil || d.SQL == nil {
		return nil
	}
	return d.SQL.Close()
}
