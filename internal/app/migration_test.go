package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMigrationConfigUsesStrictSchemaWithoutReadingRuntimeSecrets(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "migration.yaml")
	config := `version: 1
server:
  name: invalid-for-runtime
admin:
  initial_key_file: /missing/admin-key
database:
  dsn: postgres://migration:database-password@database.example.test/gizpay
  schema: service_schema
authentication:
  zitadel:
    management_client:
      private_key_file: /missing/management.pem
subscription_keys:
  hmac:
    secret_file: /missing/hmac
bifrost:
  config_store:
    type: unsupported-for-runtime
    dsn: postgres://bifrost:bifrost-password@database.example.test/gizway
    schema: bifrost_config
tls:
  certificate_file: /missing/server.crt
  private_key_file: /missing/server.key
`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadMigrationConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 1 || loaded.Database.Schema != "service_schema" || !strings.Contains(loaded.Database.DSN, "database-password") {
		t.Fatalf("loaded migration config = %+v", loaded.Database)
	}
}

func TestLoadMigrationConfigRejectsInvalidInput(t *testing.T) {
	for name, yaml := range map[string]string{
		"unknown field": "version: 1\ndatabase:\n  dsn: postgres://localhost/db\n  schema: service\n  surprise: true\n",
		"version":       "version: 2\ndatabase:\n  dsn: postgres://localhost/db\n  schema: service\n",
		"dsn":           "version: 1\ndatabase:\n  schema: service\n",
		"schema":        "version: 1\ndatabase:\n  dsn: postgres://localhost/db\n  schema: Bad-Schema\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "migration.yaml")
			if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadMigrationConfig(path); err == nil {
				t.Fatal("LoadMigrationConfig unexpectedly succeeded")
			}
		})
	}
}

func TestRunMigrationsRejectsUnknownProcessBeforeConnecting(t *testing.T) {
	config := ProcessConfig{Version: 1}
	config.Database.DSN = "postgres://user:database-password@127.0.0.1:1/missing?sslmode=disable"
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
