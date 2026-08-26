package app

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/GizClaw/gizway/internal/testdb"
)

func TestRunInitializationIsIdempotentAndSeparatesDatabaseRoles(t *testing.T) {
	sourceAdminDSN := testdb.NewDatabase(t)
	storageAdminDSN := testdb.NewDatabase(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	serviceRole := "gizpay_app_" + suffix
	sourceRole := "powersync_source_" + suffix
	storageRole := "powersync_storage_" + suffix
	publication := "powersync_" + suffix

	config := InitConfig{Version: 1, Process: ProcessGizPay}
	config.Database.AdminDSN = sourceAdminDSN
	config.Database.ServiceDSN = roleDSN(t, sourceAdminDSN, serviceRole, "service-password")
	config.Database.Schema = "public"
	config.PowerSync.Publication = publication
	config.PowerSync.SourceSchemas = []string{"public", "client_sync"}
	config.PowerSync.SourceDSN = roleDSN(t, sourceAdminDSN, sourceRole, "source-password")
	config.PowerSync.StorageAdminDSN = sourceAdminDSN
	config.PowerSync.StorageDSN = roleDSN(t, storageAdminDSN, storageRole, "storage-password")

	for attempt := range 2 {
		if err := RunInitialization(config, ProcessGizPay); err != nil {
			t.Fatalf("RunInitialization() attempt %d: %v", attempt+1, err)
		}
	}

	admin := openTestDatabase(t, sourceAdminDSN)
	for role, wantReplication := range map[string]bool{
		serviceRole: false,
		sourceRole:  true,
		storageRole: false,
	} {
		var login, replication, superuser bool
		if err := admin.QueryRow(`SELECT rolcanlogin, rolreplication, rolsuper FROM pg_roles WHERE rolname=$1`, role).Scan(&login, &replication, &superuser); err != nil {
			t.Fatalf("inspect role %s: %v", role, err)
		}
		if !login || replication != wantReplication || superuser {
			t.Fatalf("role %s attributes login=%v replication=%v superuser=%v", role, login, replication, superuser)
		}
	}
	var allTables bool
	if err := admin.QueryRow(`SELECT puballtables FROM pg_publication WHERE pubname=$1`, publication).Scan(&allTables); err != nil || !allTables {
		t.Fatalf("publication all-tables contract: allTables=%v err=%v", allTables, err)
	}
	for _, table := range []string{"users", "accounts", "products"} {
		var count int
		if err := admin.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s contains product seed rows: count=%d err=%v", table, count, err)
		}
	}

	source := openTestDatabase(t, config.PowerSync.SourceDSN)
	if _, err := source.Exec(`SELECT 1 FROM client_sync.user_profiles LIMIT 1`); err != nil {
		t.Fatalf("PowerSync source cannot read replicated schema: %v", err)
	}
	if _, err := source.Exec(`INSERT INTO users(id) VALUES ('forbidden')`); err == nil {
		t.Fatal("PowerSync source unexpectedly received product write permission")
	}

	storage := openTestDatabase(t, config.PowerSync.StorageDSN)
	var relationCount int
	if err := storage.QueryRow(`SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
		AND c.relkind IN ('r','p','v','m','S','f')`).Scan(&relationCount); err != nil || relationCount != 0 {
		t.Fatalf("PowerSync storage is not empty: relations=%d err=%v", relationCount, err)
	}
	if _, err := storage.Exec(`CREATE TABLE powersync_probe(id integer primary key)`); err != nil {
		t.Fatalf("PowerSync storage owner cannot create its runtime schema: %v", err)
	}
}

func TestInitializationRejectsConflictingPublication(t *testing.T) {
	adminDSN := testdb.NewDatabase(t)
	db := openTestDatabase(t, adminDSN)
	publication := "powersync_conflict"
	if _, err := db.Exec(`CREATE PUBLICATION ` + publication + ` FOR TABLE pg_catalog.pg_class`); err == nil {
		t.Fatal("test setup unexpectedly allowed publishing a system table")
	}
	if _, err := db.Exec(`CREATE TABLE publication_probe(id integer primary key); CREATE PUBLICATION ` + publication + ` FOR TABLE publication_probe`); err != nil {
		t.Fatal(err)
	}
	if err := ensurePowerSyncSource(adminDSN, databaseName(t, adminDSN), []string{"public"}, currentRole(t, db), currentRole(t, db), publication); err == nil || !strings.Contains(err.Error(), "not FOR ALL TABLES") {
		t.Fatalf("conflicting publication error = %v", err)
	}
}

func TestEnsureLoginRoleGrantsRestrictedAdministratorMembership(t *testing.T) {
	adminDSN := testdb.NewDatabase(t)
	bootstrap := openTestDatabase(t, adminDSN)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	adminRole := "managed_admin_" + suffix
	serviceRole := "managed_service_" + suffix
	schema := "managed_schema_" + suffix
	adminPassword := "managed-admin-password"
	servicePassword := "managed-service-password"
	database := databaseName(t, adminDSN)

	t.Cleanup(func() {
		statements := []string{
			"DROP SCHEMA IF EXISTS " + pq.QuoteIdentifier(schema) + " CASCADE",
			"REVOKE " + pq.QuoteIdentifier(serviceRole) + " FROM " + pq.QuoteIdentifier(adminRole) + " GRANTED BY " + pq.QuoteIdentifier(adminRole),
			"REVOKE " + pq.QuoteIdentifier(serviceRole) + " FROM " + pq.QuoteIdentifier(adminRole) + " GRANTED BY CURRENT_USER",
			"DROP OWNED BY " + pq.QuoteIdentifier(serviceRole),
			"DROP OWNED BY " + pq.QuoteIdentifier(adminRole),
			"DROP ROLE IF EXISTS " + pq.QuoteIdentifier(serviceRole),
			"DROP ROLE IF EXISTS " + pq.QuoteIdentifier(adminRole),
		}
		for _, statement := range statements {
			if _, err := bootstrap.Exec(statement); err != nil {
				t.Errorf("restricted administrator cleanup: %v", err)
			}
		}
	})

	if _, err := bootstrap.Exec("CREATE ROLE " + pq.QuoteIdentifier(adminRole) +
		" WITH LOGIN CREATEROLE PASSWORD " + pq.QuoteLiteral(adminPassword)); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.Exec("GRANT CONNECT, CREATE ON DATABASE " + pq.QuoteIdentifier(database) +
		" TO " + pq.QuoteIdentifier(adminRole) + " WITH GRANT OPTION"); err != nil {
		t.Fatal(err)
	}
	restrictedAdminDSN := roleDSN(t, adminDSN, adminRole, adminPassword)
	serviceDSN := roleDSN(t, adminDSN, serviceRole, servicePassword)

	for attempt := range 2 {
		if _, err := ensureLoginRole(restrictedAdminDSN, serviceDSN, false); err != nil {
			t.Fatalf("ensureLoginRole() attempt %d: %v", attempt+1, err)
		}
		if err := ensureOwnedSchema(restrictedAdminDSN, database, schema, serviceRole); err != nil {
			t.Fatalf("ensureOwnedSchema() attempt %d: %v", attempt+1, err)
		}
		if attempt == 0 {
			if _, err := bootstrap.Exec("REVOKE " + pq.QuoteIdentifier(serviceRole) + " FROM " + pq.QuoteIdentifier(adminRole) + " GRANTED BY " + pq.QuoteIdentifier(adminRole)); err != nil {
				t.Fatalf("remove explicit SET membership before retry: %v", err)
			}
		}
	}

	var adminCanSetRole, serviceIsMember bool
	if err := bootstrap.QueryRow(`SELECT pg_has_role($1, $2, 'SET'), pg_has_role($2, $1, 'MEMBER')`, adminRole, serviceRole).Scan(&adminCanSetRole, &serviceIsMember); err != nil {
		t.Fatal(err)
	}
	if !adminCanSetRole || serviceIsMember {
		t.Fatalf("role membership direction: admin-can-set-service=%v service-to-admin=%v", adminCanSetRole, serviceIsMember)
	}
	var owner string
	if err := bootstrap.QueryRow(`SELECT r.rolname FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname=$1`, schema).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != serviceRole {
		t.Fatalf("schema owner = %q, want %q", owner, serviceRole)
	}
}

func TestRunInitializationStopsAndSanitizesMembershipGrantFailure(t *testing.T) {
	adminDSN := testdb.NewDatabase(t)
	bootstrap := openTestDatabase(t, adminDSN)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	adminRole := "managed_admin_" + suffix
	schema := "blocked_schema_" + suffix
	adminPassword := "admin-password-" + suffix
	servicePassword := "service-password-" + suffix
	database := databaseName(t, adminDSN)

	t.Cleanup(func() {
		if _, err := bootstrap.Exec("DROP OWNED BY " + pq.QuoteIdentifier(adminRole)); err != nil {
			t.Errorf("membership failure cleanup: %v", err)
		}
		if _, err := bootstrap.Exec("DROP ROLE IF EXISTS " + pq.QuoteIdentifier(adminRole)); err != nil {
			t.Errorf("membership failure cleanup: %v", err)
		}
	})
	if _, err := bootstrap.Exec("CREATE ROLE " + pq.QuoteIdentifier(adminRole) +
		" WITH LOGIN CREATEROLE PASSWORD " + pq.QuoteLiteral(adminPassword)); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.Exec("GRANT CONNECT, CREATE ON DATABASE " + pq.QuoteIdentifier(database) +
		" TO " + pq.QuoteIdentifier(adminRole) + " WITH GRANT OPTION"); err != nil {
		t.Fatal(err)
	}

	restrictedAdminDSN := roleDSN(t, adminDSN, adminRole, adminPassword)
	config := InitConfig{Version: 1, Process: ProcessGizPay}
	config.Database.AdminDSN = restrictedAdminDSN
	config.Database.ServiceDSN = roleDSN(t, adminDSN, adminRole, servicePassword)
	config.Database.Schema = schema
	config.PowerSync.SourceDSN = roleDSN(t, adminDSN, "unused_source_"+suffix, "source-password-"+suffix)
	config.PowerSync.StorageAdminDSN = restrictedAdminDSN
	config.PowerSync.StorageDSN = roleDSN(t, adminDSN, "unused_storage_"+suffix, "storage-password-"+suffix)

	err := RunInitialization(config, ProcessGizPay)
	if err == nil || !strings.Contains(err.Error(), "grant database administrator role membership") {
		t.Fatalf("RunInitialization() error = %v", err)
	}
	for _, secret := range []string{
		config.Database.AdminDSN,
		config.Database.ServiceDSN,
		adminPassword,
		servicePassword,
		"source-password-" + suffix,
		"storage-password-" + suffix,
	} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("RunInitialization() exposed secret %q in %q", secret, err)
		}
	}
	originalCredential, err := sql.Open("postgres", restrictedAdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer originalCredential.Close()
	if err := originalCredential.PingContext(context.Background()); err != nil {
		t.Fatalf("original administrator credential was rotated: %v", err)
	}
	replacementCredential, err := sql.Open("postgres", config.Database.ServiceDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer replacementCredential.Close()
	if err := replacementCredential.PingContext(context.Background()); err == nil {
		t.Fatal("requested replacement credential became valid after membership grant failure")
	}
	var schemaExists bool
	if err := bootstrap.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname=$1)`, schema).Scan(&schemaExists); err != nil {
		t.Fatal(err)
	}
	if schemaExists {
		t.Fatalf("schema %q exists after membership grant failure", schema)
	}
}

func roleDSN(t *testing.T, dsn, role, password string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(role, password)
	return parsed.String()
}

func databaseName(t *testing.T, dsn string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(parsed.Path, "/")
}

func currentRole(t *testing.T, db *sql.DB) string {
	t.Helper()
	var role string
	if err := db.QueryRow(`SELECT current_user`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	return role
}

func openTestDatabase(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
