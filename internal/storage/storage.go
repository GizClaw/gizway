// Package storage owns Gizway's direct PostgreSQL connection and schema setup.
package storage

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	sqlassets "github.com/idy/gizway/data/sql"
)

// Storage owns the PostgreSQL connection pool.
type Storage struct {
	SQL *sqlx.DB
}

// OpenPostgreSQL opens PostgreSQL and optionally initializes the empty schema.
func OpenPostgreSQL(dsn string, initialize bool) (*Storage, error) {
	return openPostgreSQL(dsn, initialize, "")
}

// OpenDevelopmentPostgreSQL optionally applies the deterministic development
// fixture after initializing an empty schema.
func OpenDevelopmentPostgreSQL(dsn string, initialize, seed bool) (*Storage, error) {
	seedSQL := ""
	if seed {
		seedSQL = sqlassets.DevelopmentSeed
	}
	return openPostgreSQL(dsn, initialize, seedSQL)
}

// OpenStoryPostgreSQL initializes an isolated schema and applies the automated
// story fixture. The caller supplies the schema through the PostgreSQL DSN.
func OpenStoryPostgreSQL(dsn string) (*Storage, error) {
	return openPostgreSQL(dsn, true, sqlassets.StoryBaseSeed)
}

// OpenExistingPostgreSQL reopens an initialized schema after an intentional
// process crash without reapplying migration or fixture data.
func OpenExistingPostgreSQL(dsn string) (*Storage, error) {
	return openPostgreSQL(dsn, false, "")
}

func openPostgreSQL(dsn string, initialize bool, seedSQL string) (*Storage, error) {
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
		if err := applyPostgreSQLSetup(context.Background(), database, sqlassets.PostgreSQLSchema, seedSQL); err != nil {
			_ = result.Close()
			return nil, err
		}
	} else if seedSQL != "" {
		_ = result.Close()
		return nil, fmt.Errorf("apply PostgreSQL seed without schema initialization")
	}
	return result, nil
}

func applyPostgreSQLSetup(ctx context.Context, database *sqlx.DB, schemaSQL, seedSQL string) error {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin PostgreSQL setup: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply PostgreSQL schema: %w", err)
	}
	if seedSQL != "" {
		if _, err := tx.ExecContext(ctx, seedSQL); err != nil {
			return fmt.Errorf("apply PostgreSQL seed: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit PostgreSQL setup: %w", err)
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
