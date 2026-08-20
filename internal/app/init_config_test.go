package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validInitYAML() string {
	return `version: 1
process: gizpay
database:
  admin_dsn: postgres://admin:admin-password@database.example.test/gizpay
  service_dsn: postgres://gizpay_app:service-password@database.example.test/gizpay
  schema: public
powersync:
  publication: powersync
  source_schemas: [public, client_sync]
  source_dsn: postgres://powersync_source:source-password@database.example.test/gizpay
  storage_admin_dsn: postgres://admin:storage-admin-password@storage.example.test/postgres
  storage_dsn: postgres://powersync_storage:storage-password@storage.example.test/gizpay_sync
`
}

func TestLoadInitConfigIsStrictAndIndependentFromServeConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.yaml")
	if err := os.WriteFile(path, []byte(validInitYAML()), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadInitConfig(path, ProcessGizPay)
	if err != nil {
		t.Fatal(err)
	}
	if config.Process != ProcessGizPay || config.Database.Schema != "public" || config.PowerSync.Publication != "powersync" {
		t.Fatalf("loaded init config = %+v", config)
	}
	if err := os.WriteFile(path, []byte(validInitYAML()+"server: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadInitConfig(path, ProcessGizPay); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("runtime field was accepted by init config: %v", err)
	}
}

func TestLoadInitConfigRejectsMissingAndCrossDatabaseInputs(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"service DSN": func(value string) string {
			return strings.Replace(value, "  service_dsn:", "  missing_service_dsn:", 1)
		},
		"publication": func(value string) string {
			return strings.Replace(value, "  publication:", "  missing_publication:", 1)
		},
		"storage DSN": func(value string) string {
			return strings.Replace(value, "  storage_dsn:", "  missing_storage_dsn:", 1)
		},
		"cross database": func(value string) string {
			return strings.Replace(value, "database.example.test/gizpay\n  schema", "database.example.test/other\n  schema", 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "init.yaml")
			if err := os.WriteFile(path, []byte(edit(validInitYAML())), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadInitConfig(path, ProcessGizPay); err == nil {
				t.Fatal("LoadInitConfig unexpectedly succeeded")
			}
		})
	}
}

func TestLoadInitConfigRejectsWrongProcessAndRegion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.yaml")
	if err := os.WriteFile(path, []byte(validInitYAML()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadInitConfig(path, ProcessGizWay); err == nil {
		t.Fatal("gizway accepted gizpay init config")
	}
	withRegion := strings.Replace(validInitYAML(), "process: gizpay", "process: gizpay\nregion: global", 1)
	if err := os.WriteFile(path, []byte(withRegion), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadInitConfig(path, ProcessGizPay); err == nil {
		t.Fatal("gizpay accepted a region")
	}
}

func TestRunMigrationsRejectsUnknownProcessBeforeConnecting(t *testing.T) {
	config := InitConfig{Version: 1}
	config.Database.ServiceDSN = "postgres://user:database-password@127.0.0.1:1/missing?sslmode=disable"
	config.Database.Schema = "service"
	err := RunMigrations(config, ProcessKind("unknown"))
	if err == nil || err.Error() != `unsupported migration process "unknown"` {
		t.Fatalf("RunMigrations() error = %v", err)
	}
}

func TestSanitizeMigrationErrorRedactsDSNCredentials(t *testing.T) {
	for _, dsn := range []string{
		"postgres://migration:database-password@database.example.test/gizpay?sslmode=disable&passfile=%2Fsecret%2Fpgpass",
		"host=database.example.test dbname=gizpay user=migration password=database-password passfile=/secret/pgpass",
	} {
		err := sanitizeMigrationError(errors.New("migration failed for "+dsn), dsn)
		for _, secret := range []string{"database-password", "/secret/pgpass", "%2Fsecret%2Fpgpass"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("sanitized error %q contains %q", err, secret)
			}
		}
	}
}
