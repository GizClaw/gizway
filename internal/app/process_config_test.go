package app

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestLoadProcessConfigDefaultsCreditIntervals(t *testing.T) {
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
admin:
  initial_key_file: ` + secret + `
database:
  dsn: postgres://localhost/db
  schema: gizway
authentication:
  zitadel:
    issuer: https://identity.example.test
    jwks_url: https://identity.example.test/oauth/v2/keys
    human_audience: gizway-human
    service_audience: gizway-service
  service_account:
    token_url: https://identity.example.test/oauth/v2/token
    private_key_file: ` + secret + `
    audience: gizpay-service
    requested_scopes: [openid]
    required_roles: [credit_check]
subscription_keys:
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
	if config.CreditCache.CleanupInterval != "1m" {
		t.Fatalf("credit_cache.cleanup_interval = %q, want 1m", config.CreditCache.CleanupInterval)
	}

	configuredYAML := strings.Replace(configYAML, "gizpay:\n", "credit_cache:\n  cleanup_interval: 37s\ngizpay:\n", 1)
	if err := os.WriteFile(configPath, []byte(configuredYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	configured, err := LoadProcessConfig(configPath, ProcessGizWay)
	if err != nil {
		t.Fatal(err)
	}
	if configured.CreditCache.CleanupInterval != "37s" || configuredCreditCacheCleanupInterval(configured) != 37*time.Second {
		t.Fatalf("configured credit_cache.cleanup_interval = %q (%s), want 37s", configured.CreditCache.CleanupInterval, configuredCreditCacheCleanupInterval(configured))
	}

	withManagementCredentials := configYAML + "initialization:\n  database_admin_dsn: postgres://admin:secret@localhost/db\n"
	if err := os.WriteFile(configPath, []byte(withManagementCredentials), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProcessConfig(configPath, ProcessGizWay); err == nil || !strings.Contains(err.Error(), "field initialization not found") {
		t.Fatalf("serve config accepted initialization credentials: %v", err)
	}
}

func TestCreditCacheCleanupIntervalValidationAndRuntimeValue(t *testing.T) {
	config := ProcessConfig{}
	config.CreditCache.CleanupInterval = "37s"
	if got := configuredCreditCacheCleanupInterval(config); got != 37*time.Second {
		t.Fatalf("configured Credit Cache cleanup interval = %s, want 37s", got)
	}

	for _, value := range []string{"0s", "-1s", "not-a-duration"} {
		t.Run(value, func(t *testing.T) {
			invalid := validGizWayProcessConfigForDurationTests(t)
			invalid.CreditCache.CleanupInterval = value
			if err := ValidateProcessConfig(invalid, ProcessGizWay); err == nil || !strings.Contains(err.Error(), "credit_cache.cleanup_interval must be a positive duration") {
				t.Fatalf("ValidateProcessConfig() error = %v", err)
			}
		})
	}
}

func validGizWayProcessConfigForDurationTests(t *testing.T) ProcessConfig {
	t.Helper()
	directory := t.TempDir()
	secret := filepath.Join(directory, "secret")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := ProcessConfig{Version: 1}
	config.configDirectory = directory
	config.Server.Name = "global.example.test"
	config.Server.ListenAddress = "127.0.0.1:0"
	config.Database.DSN = "postgres://localhost/db"
	config.Database.Schema = "gizway"
	config.Admin.InitialKeyFile = secret
	config.SubscriptionKeys.HMAC.SecretFile = secret
	config.Authentication.ZITADEL.Issuer = "https://identity.example.test"
	config.Authentication.ZITADEL.JWKSURL = "https://identity.example.test/oauth/v2/keys"
	config.Authentication.ZITADEL.HumanAudience = "gizway-human"
	config.Authentication.ServiceAccount.TokenURL = "https://identity.example.test/oauth/v2/token"
	config.Authentication.ServiceAccount.PrivateKeyFile = secret
	config.Authentication.ServiceAccount.Audience = "gizpay-service"
	config.Authentication.ServiceAccount.RequestedScopes = []string{"openid"}
	config.Authentication.ServiceAccount.RequiredRoles = []string{"credit_check"}
	config.GizPay.ServiceDSN = "https://pay.example.test"
	config.Bifrost.ConfigStore.Type = "postgresql"
	config.Bifrost.ConfigStore.DSN = config.Database.DSN
	config.Bifrost.ConfigStore.Schema = "bifrost_config"
	config.Bifrost.LogStore.Type = "postgresql"
	config.Bifrost.LogStore.DSN = config.Database.DSN
	config.Bifrost.LogStore.Schema = "bifrost_logs"
	return config
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
