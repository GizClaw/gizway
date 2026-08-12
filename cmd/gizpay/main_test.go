package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/idy/gizway/internal/api"
	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/testdb"
)

func TestConfigOwnsOnlyGizPaySurface(t *testing.T) {
	t.Parallel()
	config, err := configFromArgs([]string{"-postgres-dsn", "postgres://gizpay", "-checkout-base-url", "https://credit.gizway.test"}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if config.Surface != api.SurfaceGizPay || config.PostgreSQLDSN != "postgres://gizpay" || config.AIProviderBaseURL != "" {
		t.Fatalf("unexpected GizPay config: %+v", config)
	}
}

func TestBootstrapAdministratorCreatesCentralOperator(t *testing.T) {
	dsn := testdb.NewSchema(t)
	database, err := storage.OpenGizPayPostgreSQL(dsn, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	emailFile := filepath.Join(directory, "email")
	passwordFile := filepath.Join(directory, "password")
	if err := os.WriteFile(emailFile, []byte("operator@gizpay.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passwordFile, []byte("bootstrap-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runBootstrapAdministrator([]string{
		"-postgres-dsn", dsn, "-email-file", emailFile, "-password-file", passwordFile,
	}, func(string) string { return "" }, &output); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Email    string `json:"email"`
		Replayed bool   `json:"replayed"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Email != "operator@gizpay.invalid" || response.Replayed {
		t.Fatalf("bootstrap response: %+v", response)
	}
}

func TestBootstrapAdministratorRequiresSecretFiles(t *testing.T) {
	if _, err := bootstrapAdministratorConfigFromArgs(nil, func(string) string { return "" }); err == nil {
		t.Fatal("expected incomplete bootstrap configuration error")
	}
}

func TestConfigParsesQuotaRecheckPolicy(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"GIZPAY_POSTGRES_DSN":                    "postgres://gizpay",
		"GIZPAY_DEFAULT_QUOTA_RECHECK_INTERVAL":  "2m",
		"GIZPAY_DEFAULT_DENIED_RECHECK_INTERVAL": "30s",
	}
	config, err := configFromArgs([]string{"-checkout-base-url", "https://credit.gizway.test"}, func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if config.QuotaRecheckInterval != 2*time.Minute || config.DeniedQuotaRecheckInterval != 30*time.Second {
		t.Fatalf("quota recheck config = %s/%s", config.QuotaRecheckInterval, config.DeniedQuotaRecheckInterval)
	}
}
