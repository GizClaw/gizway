// Command gizway runs the Gizway API service.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/idy/gizway/internal/app"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	config, err := configFromArgs(os.Args[1:], os.Getenv)
	if err != nil {
		return err
	}
	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(shutdownContext, config)
}

func configFromArgs(args []string, getenv func(string) string) (app.Config, error) {
	flags := flag.NewFlagSet("gizway", flag.ContinueOnError)
	address := flags.String("address", "127.0.0.1:8080", "HTTP listen address")
	postgresDSN := flags.String("postgres-dsn", getenv("GIZWAY_POSTGRES_DSN"), "PostgreSQL DSN")
	initialize := flags.Bool("initialize", false, "apply the embedded PostgreSQL schema")
	developmentSeed := flags.Bool("development-seed", false, "load deterministic non-production seed data")
	storyTestMode := flags.Bool("story-test-mode", false, "initialize an isolated database with story-only fixtures")
	storyResumeMode := flags.Bool("story-resume-mode", false, "reopen an existing story database after an intentional crash")
	aiProviderBaseURL := flags.String("ai-provider-base-url", "", "OpenAI-compatible provider base URL")
	aiProviderCallbackURL := flags.String("ai-provider-callback-url", "", "public Gizway base URL used for provider Realtime callbacks")
	paymentProviderBaseURL := flags.String("payment-provider-base-url", "", "fiat payment provider base URL")
	checkoutBaseURL := flags.String("checkout-base-url", "", "public Gizway checkout base URL")
	riskProviderBaseURL := flags.String("risk-provider-base-url", "", "compliance and risk provider base URL")
	powerSyncURL := flags.String("powersync-url", "", "PowerSync service URL returned to authenticated clients")
	powerSyncAudience := flags.String("powersync-audience", "", "audience accepted by the PowerSync service")
	powerSyncKeyID := flags.String("powersync-key-id", "", "PowerSync HS256 key identifier")
	if err := flags.Parse(args); err != nil {
		return app.Config{}, err
	}
	return app.Config{
		Address:                     *address,
		PostgreSQLDSN:               *postgresDSN,
		Initialize:                  *initialize,
		DevelopmentSeed:             *developmentSeed,
		StoryTestMode:               *storyTestMode,
		StoryResumeMode:             *storyResumeMode,
		AIProviderBaseURL:           *aiProviderBaseURL,
		AIProviderCallbackURL:       *aiProviderCallbackURL,
		AIProviderCredential:        getenv("GIZWAY_AI_PROVIDER_CREDENTIAL"),
		AIProviderCallbackSecret:    getenv("GIZWAY_AI_PROVIDER_CALLBACK_SECRET"),
		PaymentProviderBaseURL:      *paymentProviderBaseURL,
		CheckoutBaseURL:             *checkoutBaseURL,
		PaymentProviderCredential:   getenv("GIZWAY_PAYMENT_PROVIDER_CREDENTIAL"),
		PaymentCallbackSecret:       getenv("GIZWAY_PAYMENT_CALLBACK_SECRET"),
		SecretEncryptionKey:         getenv("GIZWAY_SECRET_ENCRYPTION_KEY"),
		RiskProviderBaseURL:         *riskProviderBaseURL,
		RiskProviderCredential:      getenv("GIZWAY_RISK_PROVIDER_CREDENTIAL"),
		PowerSyncURL:                *powerSyncURL,
		PowerSyncAudience:           *powerSyncAudience,
		PowerSyncKeyID:              *powerSyncKeyID,
		PowerSyncSigningKey:         getenv("GIZWAY_POWERSYNC_SIGNING_KEY"),
		StoryCrashAfterProviderFile: getenv("GIZWAY_STORY_CRASH_AFTER_PROVIDER_FILE"),
	}, nil
}
