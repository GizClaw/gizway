package app

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactDSN(t *testing.T) {
	tests := []struct {
		input, forbidden string
	}{
		{"postgres://user:secret@example.test/db?sslmode=disable", "secret"},
		{"postgres://user@example.test/db?password=secret", "secret"},
		{"host=example.test user=user password='secret value' dbname=db", "secret value"},
		{"host=example.test passfile=/private/pgpass dbname=db", "/private/pgpass"},
	}
	for _, test := range tests {
		if got := redactDSN(test.input); strings.Contains(got, test.forbidden) || !strings.Contains(got, "REDACTED") {
			t.Errorf("redactDSN(%q) = %q", test.input, got)
		}
	}
}

func TestLoadProcessConfigDefaultsCreditRecheckToFiveMinutes(t *testing.T) {
	directory := t.TempDir()
	secret := filepath.Join(directory, "hmac")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "gizway.yaml")
	configYAML := `version: 1
server:
  name: global.example.test
  listen_address: 127.0.0.1:0
database:
  dsn: postgres://localhost/db
  schema: gizway
authentication:
  zitadel:
    issuer: https://identity.example.test
    jwks_url: https://identity.example.test/oauth/v2/keys
    admin_audience: regional-admin
  service_account:
    token_url: https://identity.example.test/oauth/v2/token
    private_key_file: ` + secret + `
    audience: gizpay-service
    requested_scopes: [openid]
    required_roles: [subscription_credit_reader]
subscription_api_keys:
  hmac:
    secret_file: ` + secret + `
gizpay:
  service_dsn: https://pay.example.test
bifrost:
  config_store:
    type: postgresql
    dsn: postgres://localhost/db
    schema: bifrost_config
  log_store:
    type: postgresql
    dsn: postgres://localhost/db
    schema: bifrost_logs
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadProcessConfig(configPath, ProcessGizWay)
	if err != nil {
		t.Fatal(err)
	}
	if config.CreditCheck.RecheckInterval != "5m" {
		t.Fatalf("credit_check.recheck_interval = %q, want 5m", config.CreditCheck.RecheckInterval)
	}
}

func TestTLSCertificatePairEnablesHTTPSUnlessExplicitlyDisabled(t *testing.T) {
	config := ProcessConfig{}
	config.TLS.CertificateFile = "/run/secrets/server.crt"
	config.TLS.PrivateKeyFile = "/run/secrets/server.key"
	if !tlsEnabled(config) {
		t.Fatal("certificate pair without legacy enabled field did not enable HTTPS")
	}
	disabled := false
	config.TLS.Enabled = &disabled
	if tlsEnabled(config) {
		t.Fatal("explicit development TLS disable was ignored")
	}
}

func TestConfiguredLoggingLevelAndFormatControlRuntimeHandler(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(processLogHandler("warn", "text", &output))
	logger.InfoContext(context.Background(), "hidden")
	logger.ErrorContext(context.Background(), "visible")
	if got := output.String(); strings.Contains(got, "hidden") || !strings.Contains(got, "level=ERROR") || !strings.Contains(got, "msg=visible") {
		t.Fatalf("text/warn logging output = %q", got)
	}
	output.Reset()
	logger = slog.New(processLogHandler("debug", "json", &output))
	logger.DebugContext(context.Background(), "debug-visible")
	if got := output.String(); !strings.Contains(got, `"level":"DEBUG"`) || !strings.Contains(got, `"msg":"debug-visible"`) {
		t.Fatalf("json/debug logging output = %q", got)
	}
}
