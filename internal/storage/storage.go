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
	storage, err := openServicePostgreSQL(dsn, initialize, "gizpay", gizpaysql.Migrations, "")
	if err == nil && initialize {
		err = initializeGizPaySystem(context.Background(), storage.SQL)
	}
	if err != nil && storage != nil {
		_ = storage.Close()
	}
	return storage, err
}

// OpenGizWayPostgreSQL initializes one fresh regional data-plane database.
func OpenGizWayPostgreSQL(dsn string, initialize bool) (*Storage, error) {
	return openServicePostgreSQL(dsn, initialize, "gizway", gizwaysql.Migrations, "")
}

// OpenGizPayStoryPostgreSQL creates a fresh GizPay schema plus its own test
// fixture. It never imports or executes regional SQL.
func OpenGizPayStoryPostgreSQL(dsn string) (*Storage, error) {
	storage, err := openServicePostgreSQL(dsn, true, "gizpay", gizpaysql.Migrations, gizpaysql.StoryBaseSeed)
	if err == nil {
		err = initializeGizPaySystem(context.Background(), storage.SQL)
	}
	if err != nil && storage != nil {
		_ = storage.Close()
	}
	return storage, err
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
func initializeGizPaySystem(ctx context.Context, database *sqlx.DB) error {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin GizPay system initialization: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
		`INSERT INTO users(id,identity_issuer,identity_subject,email,display_name,status) VALUES
		 ('usr_platform','urn:gizpay:system','platform','','GizPay Platform','active') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO accounts(id,owner_user_id,status) VALUES
		 ('acct_platform','usr_platform','active') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO ledger_accounts(id,owner_account_id,asset_code,status) VALUES
		 ('led_acct_platform','acct_platform','credit','active'),
		 ('led_clearing',NULL,'credit','active') ON CONFLICT (id) DO NOTHING`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize GizPay system ledger: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit GizPay system initialization: %w", err)
	}
	return nil
}

// OpenGizWayStoryPostgreSQL creates a fresh regional schema plus its own test
// fixture. It never imports or executes GizPay SQL.
func OpenGizWayStoryPostgreSQL(dsn string) (*Storage, error) {
	return openServicePostgreSQL(dsn, true, "gizway", gizwaysql.Migrations, gizwaysql.StoryBaseSeed)
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

func openServicePostgreSQL(dsn string, initialize bool, service string, migrations []string, seedSQL string) (*Storage, error) {
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
		if err := applyServiceMigrations(context.Background(), database, service, migrations, seedSQL); err != nil {
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
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s migrations: %w", service, err)
	}
	defer tx.Rollback()
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s migrations: %w", service, err)
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
