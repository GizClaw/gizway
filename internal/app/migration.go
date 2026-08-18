package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/idy/gizway/internal/storage"
)

// LoadMigrationConfig decodes the normal process configuration strictly, but
// validates only fields required by the one-shot migration command. Runtime
// credentials are deliberately neither required nor read.
func LoadMigrationConfig(path string) (ProcessConfig, error) {
	config, err := loadRawProcessConfig(path)
	if err != nil {
		return ProcessConfig{}, err
	}
	if config.Version != 1 {
		return ProcessConfig{}, errors.New("config version must be 1")
	}
	if config.Database.DSN == "" {
		return ProcessConfig{}, errors.New("database.dsn is required")
	}
	if !databaseSchemaPattern.MatchString(config.Database.Schema) {
		return ProcessConfig{}, errors.New("database.schema must be a lowercase SQL identifier")
	}
	return config, nil
}

// RunMigrations creates the configured service schema and applies only the
// service-owned schema migrations. Bifrost stores remain runtime-managed.
func RunMigrations(config ProcessConfig, kind ProcessKind) error {
	if kind != ProcessGizPay && kind != ProcessGizWay {
		return fmt.Errorf("unsupported migration process %q", kind)
	}
	if err := ensureDatabaseSchema(config.Database.DSN, config.Database.Schema); err != nil {
		return sanitizeMigrationError(err, config.Database.DSN)
	}
	dsn, err := withSearchPath(config.Database.DSN, config.Database.Schema)
	if err != nil {
		return sanitizeMigrationError(err, config.Database.DSN)
	}
	if kind == ProcessGizPay {
		err = storage.MigrateGizPayPostgreSQL(context.Background(), dsn)
	} else {
		err = storage.MigrateGizWayPostgreSQL(context.Background(), dsn)
	}
	return sanitizeMigrationError(err, config.Database.DSN)
}

func sanitizeMigrationError(err error, dsn string) error {
	if err == nil {
		return nil
	}
	message := strings.ReplaceAll(err.Error(), dsn, redactDSN(dsn))
	if parsed, parseErr := url.Parse(dsn); parseErr == nil {
		if parsed.User != nil {
			if password, ok := parsed.User.Password(); ok && password != "" {
				message = strings.ReplaceAll(message, password, "REDACTED")
			}
		}
		query := parsed.Query()
		for _, key := range []string{"password", "passfile"} {
			if value := query.Get(key); value != "" {
				message = strings.ReplaceAll(message, value, "REDACTED")
			}
		}
	}
	message = redactDSN(message)
	return errors.New(message)
}
