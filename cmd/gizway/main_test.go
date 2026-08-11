package main

import (
	"testing"
)

func TestConfigFromArgs(t *testing.T) {
	t.Parallel()
	environment := map[string]string{
		"GIZWAY_POSTGRES_DSN":                    "postgres://environment",
		"GIZWAY_AI_PROVIDER_CREDENTIAL":          "ai-secret",
		"GIZWAY_AI_PROVIDER_CALLBACK_SECRET":     "callback-secret",
		"GIZWAY_PAYMENT_PROVIDER_CREDENTIAL":     "payment-secret",
		"GIZWAY_PAYMENT_CALLBACK_SECRET":         "payment-callback",
		"GIZWAY_SECRET_ENCRYPTION_KEY":           "encryption-key",
		"GIZWAY_RISK_PROVIDER_CREDENTIAL":        "risk-secret",
		"GIZWAY_POWERSYNC_SIGNING_KEY":           "powersync-secret",
		"GIZWAY_STORY_CRASH_AFTER_PROVIDER_FILE": "/tmp/crash-marker",
	}
	config, err := configFromArgs([]string{
		"-address", "127.0.0.1:18080",
		"-postgres-dsn", "postgres://example",
		"-initialize",
		"-development-seed",
		"-story-test-mode",
		"-story-resume-mode",
		"-ai-provider-base-url", "https://ai.example",
		"-ai-provider-callback-url", "https://api.example",
		"-payment-provider-base-url", "https://pay.example",
		"-checkout-base-url", "https://checkout.example",
		"-risk-provider-base-url", "https://risk.example",
		"-powersync-url", "https://sync.example",
		"-powersync-audience", "gizway",
		"-powersync-key-id", "key-1",
	}, func(key string) string { return environment[key] })
	if err != nil {
		t.Fatalf("configFromArgs: %v", err)
	}

	if config.Address != "127.0.0.1:18080" || config.PostgreSQLDSN != "postgres://example" {
		t.Fatalf("unexpected database/listener config: %+v", config)
	}
	if !config.Initialize || !config.DevelopmentSeed || !config.StoryTestMode || !config.StoryResumeMode {
		t.Fatalf("expected all requested lifecycle flags: %+v", config)
	}
	if config.AIProviderBaseURL != "https://ai.example" || config.AIProviderCallbackURL != "https://api.example" || config.AIProviderCredential != "ai-secret" || config.AIProviderCallbackSecret != "callback-secret" {
		t.Fatalf("unexpected AI config: %+v", config)
	}
	if config.PaymentProviderBaseURL != "https://pay.example" || config.CheckoutBaseURL != "https://checkout.example" || config.PaymentProviderCredential != "payment-secret" || config.PaymentCallbackSecret != "payment-callback" {
		t.Fatalf("unexpected payment config: %+v", config)
	}
	if config.RiskProviderBaseURL != "https://risk.example" || config.RiskProviderCredential != "risk-secret" || config.SecretEncryptionKey != "encryption-key" {
		t.Fatalf("unexpected security/risk config: %+v", config)
	}
	if config.PowerSyncURL != "https://sync.example" || config.PowerSyncAudience != "gizway" || config.PowerSyncKeyID != "key-1" || config.PowerSyncSigningKey != "powersync-secret" {
		t.Fatalf("unexpected PowerSync config: %+v", config)
	}
	if config.StoryCrashAfterProviderFile != "/tmp/crash-marker" {
		t.Fatalf("unexpected crash fixture path: %q", config.StoryCrashAfterProviderFile)
	}
}

func TestConfigFromArgsRejectsInvalidFlag(t *testing.T) {
	t.Parallel()
	if _, err := configFromArgs([]string{"-not-a-real-flag"}, func(string) string { return "" }); err == nil {
		t.Fatal("expected invalid flag error")
	}
}
