package app_test

import (
	"context"
	"encoding/base64"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/idy/gizway/internal/app"
	"github.com/idy/gizway/internal/testdb"
)

func TestRunRejectsConflictingStoryFlags(t *testing.T) {
	err := app.Run(context.Background(), app.Config{StoryTestMode: true, Initialize: true})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("Run error = %v", err)
	}
}

func TestRunRequiresPostgreSQL(t *testing.T) {
	if err := app.Run(context.Background(), app.Config{StoryTestMode: true}); err == nil || !strings.Contains(err.Error(), "PostgreSQL DSN is required") {
		t.Fatalf("missing PostgreSQL error=%v", err)
	}
}

func TestRunStoryServerLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	dsn := testdb.NewSchema(t)
	go func() {
		done <- app.Run(ctx, app.Config{
			Address: "127.0.0.1:0", PostgreSQLDSN: dsn,
			StoryTestMode: true,
		})
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}

func TestRunStopsWorkersWhenHTTPListenFails(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	dsn := testdb.NewSchema(t)

	done := make(chan error, 1)
	go func() {
		done <- app.Run(context.Background(), app.Config{
			Address: listener.Addr().String(), PostgreSQLDSN: dsn,
			StoryTestMode: true,
		})
	}()
	select {
	case runErr := <-done:
		if runErr == nil || !strings.Contains(runErr.Error(), "serve HTTP") {
			t.Fatalf("Run error = %v, want listener failure", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop workers after listener failure")
	}
}

func TestRunReturnsStorageError(t *testing.T) {
	err := app.Run(context.Background(), app.Config{PostgreSQLDSN: "host=127.0.0.1 port=1 user=gizway dbname=gizway sslmode=disable connect_timeout=1", Initialize: true})
	if err == nil {
		t.Fatal("Run with invalid database path succeeded")
	}
}

func TestRunRequiresValidProductionSecretKey(t *testing.T) {
	if err := app.Run(context.Background(), app.Config{PostgreSQLDSN: testdb.NewSchema(t), Initialize: true}); err == nil || !strings.Contains(err.Error(), "SECRET_ENCRYPTION_KEY is required") {
		t.Fatalf("missing secret key error = %v", err)
	}
	if err := app.Run(context.Background(), app.Config{PostgreSQLDSN: testdb.NewSchema(t), Initialize: true, SecretEncryptionKey: "not-base64"}); err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("invalid secret key error = %v", err)
	}
}

func TestRunRejectsIncompleteProductionIntegrationConfiguration(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	base := func(name string) app.Config {
		return app.Config{
			PostgreSQLDSN:       testdb.NewSchema(t),
			Initialize:          true,
			SecretEncryptionKey: secret,
			CheckoutBaseURL:     "https://pay.example.test",
		}
	}
	tests := []struct {
		name      string
		configure func(*app.Config)
		contains  string
	}{
		{
			name: "AI provider credential",
			configure: func(config *app.Config) {
				config.AIProviderBaseURL = "https://ai.example.test"
			},
			contains: "AI provider credential",
		},
		{
			name: "payment provider secrets",
			configure: func(config *app.Config) {
				config.PaymentProviderBaseURL = "https://payments.example.test"
			},
			contains: "payment provider credential and callback secret",
		},
		{
			name: "missing checkout URL",
			configure: func(config *app.Config) {
				config.CheckoutBaseURL = ""
			},
			contains: "public checkout base URL is required",
		},
		{
			name: "insecure checkout URL",
			configure: func(config *app.Config) {
				config.CheckoutBaseURL = "http://pay.example.test"
			},
			contains: "absolute HTTPS URL",
		},
		{
			name: "relative checkout URL",
			configure: func(config *app.Config) {
				config.CheckoutBaseURL = "/checkout"
			},
			contains: "absolute HTTPS URL",
		},
		{
			name: "risk provider credential",
			configure: func(config *app.Config) {
				config.RiskProviderBaseURL = "https://risk.example.test"
			},
			contains: "risk provider credential",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base(t.Name())
			test.configure(&config)
			err := app.Run(context.Background(), config)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Run error=%v, want substring %q", err, test.contains)
			}
		})
	}
}

func TestRunRejectsCrashInjectionOutsideStoryMode(t *testing.T) {
	err := app.Run(context.Background(), app.Config{StoryCrashAfterProviderFile: "/tmp/not-created"})
	if err == nil || !strings.Contains(err.Error(), "only in story test mode") {
		t.Fatalf("Run error=%v", err)
	}
}
