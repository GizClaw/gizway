package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/idy/gizway/internal/api"
	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/testdb"
)

func TestConfigFromArgs(t *testing.T) {
	t.Parallel()
	environment := map[string]string{
		"GIZWAY_POSTGRES_DSN":                "postgres://environment",
		"GIZWAY_AI_PROVIDER_CREDENTIAL":      "ai-secret",
		"GIZWAY_AI_PROVIDER_CALLBACK_SECRET": "callback-secret",
		"GIZWAY_SECRET_ENCRYPTION_KEY":       "encryption-key",
		"GIZPAY_INTERNAL_BASE_URL":           "https://gizpay.internal",
		"GIZPAY_MTLS_CERT_FILE":              "/certs/node.crt",
		"GIZPAY_MTLS_KEY_FILE":               "/certs/node.key",
		"GIZPAY_MTLS_CA_FILE":                "/certs/ca.crt",
		"GIZWAY_NODE_ID":                     "gw-cn-1",
		"GIZWAY_REGION":                      "cn",
	}
	config, err := configFromArgs([]string{
		"-address", "127.0.0.1:18080",
		"-postgres-dsn", "postgres://example",
		"-initialize",
		"-story-test-mode",
		"-story-resume-mode",
		"-ai-provider-base-url", "https://ai.example",
		"-ai-provider-callback-url", "https://api.example",
	}, func(key string) string { return environment[key] })
	if err != nil {
		t.Fatalf("configFromArgs: %v", err)
	}

	if config.Surface != api.SurfaceGizWay || config.Address != "127.0.0.1:18080" || config.PostgreSQLDSN != "postgres://example" {
		t.Fatalf("unexpected database/listener config: %+v", config)
	}
	if !config.Initialize || !config.StoryTestMode || !config.StoryResumeMode {
		t.Fatalf("expected all requested lifecycle flags: %+v", config)
	}
	if config.AIProviderBaseURL != "https://ai.example" || config.AIProviderCallbackURL != "https://api.example" || config.AIProviderCredential != "ai-secret" || config.AIProviderCallbackSecret != "callback-secret" {
		t.Fatalf("unexpected AI config: %+v", config)
	}
	if config.SecretEncryptionKey != "encryption-key" {
		t.Fatalf("unexpected regional security config: %+v", config)
	}
	if config.GizPayInternalBaseURL != "https://gizpay.internal" || config.GizPayMTLSCertificateFile != "/certs/node.crt" ||
		config.GizPayMTLSPrivateKeyFile != "/certs/node.key" || config.GizPayMTLSServerCAFile != "/certs/ca.crt" ||
		config.NodeID != "gw-cn-1" || config.Region != "cn" {
		t.Fatalf("unexpected GizPay boundary config: %+v", config)
	}
	if config.PaymentProviderBaseURL != "" || config.CheckoutBaseURL != "" || config.RiskProviderBaseURL != "" || config.PowerSyncURL != "" {
		t.Fatalf("GizWay accepted central-only configuration: %+v", config)
	}
}

func TestBootstrapAdministratorCreatesRegionalOperator(t *testing.T) {
	dsn := testdb.NewSchema(t)
	database, err := storage.OpenGizWayPostgreSQL(dsn, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	emailFile := filepath.Join(directory, "email")
	passwordFile := filepath.Join(directory, "password")
	if err := os.WriteFile(emailFile, []byte("cn-operator@gizway.invalid\n"), 0o600); err != nil {
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
	if response.Email != "cn-operator@gizway.invalid" || response.Replayed {
		t.Fatalf("bootstrap response: %+v", response)
	}
}

func TestBootstrapAdministratorRequiresSecretFiles(t *testing.T) {
	if _, err := bootstrapAdministratorConfigFromArgs(nil, func(string) string { return "" }); err == nil {
		t.Fatal("expected incomplete bootstrap configuration error")
	}
}

func TestConfigFromArgsRejectsInvalidFlag(t *testing.T) {
	t.Parallel()
	if _, err := configFromArgs([]string{"-not-a-real-flag"}, func(string) string { return "" }); err == nil {
		t.Fatal("expected invalid flag error")
	}
}

func TestConfigRejectsRemovedCentralServiceFlags(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{
		"-payment-provider-base-url", "-checkout-base-url", "-risk-provider-base-url",
		"-powersync-url", "-powersync-audience", "-powersync-key-id",
	} {
		t.Run(flag, func(t *testing.T) {
			if _, err := configFromArgs([]string{flag, "https://central.example"}, func(string) string { return "" }); err == nil {
				t.Fatalf("GizWay accepted removed central-service flag %s", flag)
			}
		})
	}
}
