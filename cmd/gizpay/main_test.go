package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandIsRequired(t *testing.T) {
	if err := run(nil); err == nil || err.Error() != "command is required: serve or init" {
		t.Fatalf("run() error = %v", err)
	}
}

func TestUnknownCommandFailsClosed(t *testing.T) {
	if err := run([]string{"migrate"}); err == nil || !strings.Contains(err.Error(), "unsupported command") {
		t.Fatalf("run() error = %v", err)
	}
}

func TestInitRequiresConfig(t *testing.T) {
	if err := run([]string{"init"}); err == nil || err.Error() != "--config is required" {
		t.Fatalf("run() error = %v", err)
	}
}

func TestInitUsesDatabaseOnlyConfig(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "gizpay.yaml")
	config := `version: 1
process: gizpay
database:
  admin_dsn: postgres://migration:admin-password@127.0.0.1:1/gizpay
  service_dsn: postgres://gizpay_app:database-password@127.0.0.1:1/gizpay
  schema: public
powersync:
  publication: powersync
  source_dsn: postgres://powersync_source:source-password@127.0.0.1:1/gizpay
  storage_admin_dsn: postgres://migration:storage-admin-password@127.0.0.1:1/postgres
  storage_dsn: postgres://powersync_storage:storage-password@127.0.0.1:1/gizpay_sync
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"init", "--config=" + configPath})
	if err == nil {
		t.Fatal("init unexpectedly connected to an invalid DSN")
	}
	for _, runtimeValidation := range []string{"server.name", "admin.initial_key_file", "subscription_keys.hmac.secret_file", "Secret file"} {
		if strings.Contains(err.Error(), runtimeValidation) {
			t.Fatalf("init used runtime validation: %v", err)
		}
	}
	for _, secret := range []string{"database-password", "admin-password", "source-password", "storage-admin-password", "storage-password"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("init leaked database credential %q: %v", secret, err)
		}
	}
}
