package app

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"

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
