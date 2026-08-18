package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateOnlyRejectsConflictingModesBeforeLoadingConfig(t *testing.T) {
	for name, args := range map[string][]string{
		"initialize":       {"--config=/does/not/exist", "--migrate-only", "--initialize"},
		"initialize false": {"--config=/does/not/exist", "--migrate-only", "--initialize=false"},
		"check config":     {"--config=/does/not/exist", "--migrate-only", "--check-config"},
		"effective config": {"--config=/does/not/exist", "--migrate-only", "--print-effective-config=json"},
	} {
		t.Run(name, func(t *testing.T) {
			err := run(args)
			if err == nil || err.Error() != "--migrate-only cannot be combined with --initialize, --check-config, or --print-effective-config" {
				t.Fatalf("run() error = %v", err)
			}
		})
	}
}

func TestMigrateOnlyRequiresConfig(t *testing.T) {
	err := run([]string{"--migrate-only"})
	if err == nil || err.Error() != "--config is required" {
		t.Fatalf("run() error = %v", err)
	}
}

func TestMigrateOnlyUsesMigrationConfigWithoutRuntimeSecrets(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "gizpay.yaml")
	config := `version: 1
server:
  name: not-a-runtime-domain
admin:
  initial_key_file: /missing/admin-key
database:
  dsn: invalid
  schema: public
subscription_keys:
  hmac:
    secret_file: /missing/hmac
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"--config=" + configPath, "--migrate-only"})
	if err == nil {
		t.Fatal("migration unexpectedly connected to an invalid DSN")
	}
	for _, runtimeValidation := range []string{"server.name", "admin.initial_key_file", "subscription_keys.hmac.secret_file", "Secret file"} {
		if strings.Contains(err.Error(), runtimeValidation) {
			t.Fatalf("migration used runtime validation: %v", err)
		}
	}
}
