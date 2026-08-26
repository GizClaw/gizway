package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/lib/pq"

	"github.com/GizClaw/gizway/internal/storage"
)

// RunMigrations creates the configured service schema and applies only the
// service-owned schema migrations. Bifrost stores remain runtime-managed.
func RunMigrations(config InitConfig, kind ProcessKind) error {
	if kind != ProcessGizPay && kind != ProcessGizWay {
		return fmt.Errorf("unsupported migration process %q", kind)
	}
	dsn, err := withSearchPath(config.Database.ServiceDSN, config.Database.Schema)
	if err != nil {
		return sanitizeInitializationError(err, config)
	}
	if kind == ProcessGizPay {
		err = storage.MigrateGizPayPostgreSQL(context.Background(), dsn)
	} else {
		err = storage.MigrateGizWayPostgreSQL(context.Background(), dsn)
	}
	return sanitizeInitializationError(err, config)
}

// RunInitialization applies the service schema and prepares the two database
// resources required by PowerSync. PowerSync remains responsible for its own
// internal storage schema.
func RunInitialization(config InitConfig, kind ProcessKind) error {
	if kind != ProcessGizPay && kind != ProcessGizWay {
		return fmt.Errorf("unsupported initialization process %q", kind)
	}
	service, err := ensureLoginRole(config.Database.AdminDSN, config.Database.ServiceDSN, false)
	if err != nil {
		return sanitizeInitializationError(err, config)
	}
	if err := ensureOwnedSchema(config.Database.AdminDSN, service.Database, config.Database.Schema, service.Name); err != nil {
		return sanitizeInitializationError(err, config)
	}
	if err := RunMigrations(config, kind); err != nil {
		return err
	}
	source, err := ensureLoginRole(config.Database.AdminDSN, config.PowerSync.SourceDSN, true)
	if err != nil {
		return sanitizeInitializationError(err, config)
	}
	if err := ensurePowerSyncSource(config.Database.AdminDSN, source.Database, config.PowerSync.SourceSchemas, service.Name, source.Name, config.PowerSync.Publication); err != nil {
		return sanitizeInitializationError(err, config)
	}
	storageRole, err := ensureLoginRole(config.PowerSync.StorageAdminDSN, config.PowerSync.StorageDSN, false)
	if err != nil {
		return sanitizeInitializationError(err, config)
	}
	if err := ensurePostgreSQLDatabase(config.PowerSync.StorageAdminDSN, storageRole.Database, storageRole.Name); err != nil {
		return sanitizeInitializationError(err, config)
	}
	return nil
}

type databaseRole struct {
	Name, Password, Database string
}

func parseRoleDSN(dsn string) (databaseRole, error) {
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.User == nil {
		return databaseRole{}, errors.New("must be a PostgreSQL URL with a login role")
	}
	password, ok := parsed.User.Password()
	database := strings.TrimPrefix(parsed.Path, "/")
	role := databaseRole{Name: parsed.User.Username(), Password: password, Database: database}
	if !ok || role.Password == "" {
		return databaseRole{}, errors.New("must include a non-empty password")
	}
	if !databaseSchemaPattern.MatchString(role.Name) || !databaseSchemaPattern.MatchString(role.Database) {
		return databaseRole{}, errors.New("role and database must be lowercase SQL identifiers")
	}
	return role, nil
}

func ensureLoginRole(adminDSN, roleDSN string, replication bool) (databaseRole, error) {
	role, err := parseRoleDSN(roleDSN)
	if err != nil {
		return databaseRole{}, err
	}
	db, err := openPostgreSQL(adminDSN, "database admin")
	if err != nil {
		return databaseRole{}, err
	}
	defer db.Close()
	var currentReplication bool
	err = db.QueryRowContext(context.Background(), `SELECT rolreplication FROM pg_roles WHERE rolname=$1`, role.Name).Scan(&currentReplication)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return databaseRole{}, fmt.Errorf("inspect database role: %w", err)
	}
	attributes := "LOGIN"
	if replication {
		if !exists || !currentReplication {
			attributes += " REPLICATION"
		}
	} else if exists && currentReplication {
		attributes += " NOREPLICATION"
	}
	statement := "ALTER ROLE "
	if !exists {
		statement = "CREATE ROLE "
	}
	quotedRole := pq.QuoteIdentifier(role.Name)
	if exists {
		if err := grantDatabaseAdministratorRoleMembership(db, quotedRole); err != nil {
			return databaseRole{}, err
		}
	}
	if _, err := db.ExecContext(context.Background(), statement+quotedRole+" WITH "+attributes+" PASSWORD "+pq.QuoteLiteral(role.Password)); err != nil {
		return databaseRole{}, fmt.Errorf("configure database role: %w", err)
	}
	if !exists {
		if err := grantDatabaseAdministratorRoleMembership(db, quotedRole); err != nil {
			return databaseRole{}, err
		}
	}
	return role, nil
}

func grantDatabaseAdministratorRoleMembership(db *sql.DB, quotedRole string) error {
	if _, err := db.ExecContext(context.Background(), "GRANT "+quotedRole+" TO CURRENT_USER WITH SET TRUE"); err != nil {
		return fmt.Errorf("grant database administrator role membership: %w", err)
	}
	return nil
}

func ensureOwnedSchema(adminDSN, database, schema, owner string) error {
	db, err := openPostgreSQL(adminDSN, "database admin")
	if err != nil {
		return err
	}
	defer db.Close()
	quotedSchema, quotedOwner := pq.QuoteIdentifier(schema), pq.QuoteIdentifier(owner)
	if _, err := db.ExecContext(context.Background(), "GRANT CONNECT, CREATE ON DATABASE "+pq.QuoteIdentifier(database)+" TO "+quotedOwner); err != nil {
		return fmt.Errorf("grant service database connection: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), "CREATE SCHEMA IF NOT EXISTS "+quotedSchema+" AUTHORIZATION "+quotedOwner); err != nil {
		return fmt.Errorf("create service schema: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), "GRANT USAGE, CREATE ON SCHEMA "+quotedSchema+" TO "+quotedOwner); err != nil {
		return fmt.Errorf("grant service schema: %w", err)
	}
	return nil
}

func ensurePostgreSQLDatabase(adminDSN, database, owner string) error {
	db, err := sql.Open("postgres", adminDSN)
	if err != nil {
		return fmt.Errorf("open database admin connection: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		return fmt.Errorf("ping database admin connection: %w", err)
	}
	var existingOwner sql.NullString
	if err := db.QueryRowContext(context.Background(), `SELECT r.rolname
		FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba
		WHERE d.datname=$1`, database).Scan(&existingOwner); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("inspect PowerSync storage database: %w", err)
	}
	quotedDatabase, quotedOwner := pq.QuoteIdentifier(database), pq.QuoteIdentifier(owner)
	if !existingOwner.Valid {
		if _, err := db.ExecContext(context.Background(), "CREATE DATABASE "+quotedDatabase+" OWNER "+quotedOwner); err != nil {
			return fmt.Errorf("create PowerSync storage database: %w", err)
		}
		return nil
	}
	if existingOwner.String == owner {
		return nil
	}
	empty, err := databaseIsEmpty(adminDSN, database)
	if err != nil {
		return err
	}
	if !empty {
		return fmt.Errorf("PowerSync storage database %q already exists with owner %q and is not empty", database, existingOwner.String)
	}
	if _, err := db.ExecContext(context.Background(), "ALTER DATABASE "+quotedDatabase+" OWNER TO "+quotedOwner); err != nil {
		return fmt.Errorf("configure PowerSync storage database owner: %w", err)
	}
	return nil
}

func databaseIsEmpty(adminDSN, database string) (bool, error) {
	targetDSN, err := replaceDSNDatabase(adminDSN, database)
	if err != nil {
		return false, err
	}
	db, err := openPostgreSQL(targetDSN, "PowerSync storage database")
	if err != nil {
		return false, err
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*)
		FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
		AND c.relkind IN ('r','p','v','m','S','f')`).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect PowerSync storage contents: %w", err)
	}
	return count == 0, nil
}

func replaceDSNDatabase(dsn, database string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", errors.New("database admin DSN must be a PostgreSQL URL")
	}
	parsed.Path = "/" + database
	return parsed.String(), nil
}

func ensurePowerSyncSource(adminDSN, database string, schemas []string, serviceRole, sourceRole, publication string) error {
	db, err := openPostgreSQL(adminDSN, "PowerSync source database")
	if err != nil {
		return err
	}
	defer db.Close()
	quotedService := pq.QuoteIdentifier(serviceRole)
	quotedSource, quotedPublication := pq.QuoteIdentifier(sourceRole), pq.QuoteIdentifier(publication)
	statements := []string{"GRANT CONNECT ON DATABASE " + pq.QuoteIdentifier(database) + " TO " + quotedSource}
	for _, schema := range schemas {
		quotedSchema := pq.QuoteIdentifier(schema)
		statements = append(statements,
			"GRANT USAGE ON SCHEMA "+quotedSchema+" TO "+quotedSource,
			"GRANT SELECT ON ALL TABLES IN SCHEMA "+quotedSchema+" TO "+quotedSource,
			"ALTER DEFAULT PRIVILEGES FOR ROLE "+quotedService+" IN SCHEMA "+quotedSchema+" GRANT SELECT ON TABLES TO "+quotedSource,
		)
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(context.Background(), statement); err != nil {
			return fmt.Errorf("grant PowerSync source permission: %w", err)
		}
	}
	var allTables bool
	if err := db.QueryRowContext(context.Background(), `SELECT puballtables FROM pg_publication WHERE pubname=$1`, publication).Scan(&allTables); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("inspect PowerSync publication: %w", err)
	}
	if allTables {
		return nil
	}
	var exists bool
	if err := db.QueryRowContext(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname=$1)`, publication).Scan(&exists); err != nil {
		return fmt.Errorf("inspect PowerSync publication: %w", err)
	}
	if exists {
		return fmt.Errorf("PowerSync publication %q already exists but is not FOR ALL TABLES", publication)
	}
	if _, err := db.ExecContext(context.Background(), "CREATE PUBLICATION "+quotedPublication+" FOR ALL TABLES"); err != nil {
		return fmt.Errorf("create PowerSync publication: %w", err)
	}
	return nil
}

func openPostgreSQL(dsn, label string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s connection: %w", label, err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping %s connection: %w", label, err)
	}
	return db, nil
}

func sanitizeInitializationError(err error, config InitConfig) error {
	for _, dsn := range []string{config.Database.AdminDSN, config.Database.ServiceDSN, config.PowerSync.SourceDSN, config.PowerSync.StorageAdminDSN, config.PowerSync.StorageDSN} {
		err = sanitizeMigrationError(err, dsn)
	}
	return err
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
