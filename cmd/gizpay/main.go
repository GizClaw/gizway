// Command gizpay runs the unified Account, Payment, and settlement control plane.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/idy/gizway/internal/api"
	"github.com/idy/gizway/internal/app"
)

func main() {
	var err error
	if len(os.Args) > 1 && os.Args[1] == "bootstrap-admin" {
		err = runBootstrapAdministrator(os.Args[2:], os.Getenv, os.Stdout)
	} else {
		err = run()
	}
	if err != nil {
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
	quotaDefault, err := durationOrDefault(getenv("GIZPAY_DEFAULT_QUOTA_RECHECK_INTERVAL"), 5*time.Minute)
	if err != nil {
		return app.Config{}, err
	}
	deniedQuotaDefault, err := durationOrDefault(getenv("GIZPAY_DEFAULT_DENIED_RECHECK_INTERVAL"), 5*time.Minute)
	if err != nil {
		return app.Config{}, err
	}
	flags := flag.NewFlagSet("gizpay", flag.ContinueOnError)
	address := flags.String("address", "127.0.0.1:8081", "HTTP listen address")
	postgresDSN := flags.String("postgres-dsn", getenv("GIZPAY_POSTGRES_DSN"), "GizPay PostgreSQL DSN")
	initialize := flags.Bool("initialize", false, "apply the embedded GizPay PostgreSQL schema")
	storyTestMode := flags.Bool("story-test-mode", false, "initialize an isolated database with story fixtures")
	paymentProviderBaseURL := flags.String("payment-provider-base-url", "", "fiat payment provider base URL")
	checkoutBaseURL := flags.String("checkout-base-url", "", "public checkout base URL")
	riskProviderBaseURL := flags.String("risk-provider-base-url", "", "compliance and risk provider base URL")
	powerSyncURL := flags.String("powersync-url", "", "PowerSync service URL")
	powerSyncAudience := flags.String("powersync-audience", "", "PowerSync audience")
	powerSyncKeyID := flags.String("powersync-key-id", "", "PowerSync signing key ID")
	tlsCertificateFile := flags.String("tls-cert-file", "", "GizPay TLS certificate PEM")
	tlsPrivateKeyFile := flags.String("tls-key-file", "", "GizPay TLS private key PEM")
	gatewayClientCAFile := flags.String("gateway-client-ca-file", "", "CA PEM trusted for Gateway node client certificates")
	quotaRecheck := flags.Duration("quota-recheck-interval", quotaDefault, "allowed Quota response recheck interval")
	deniedQuotaRecheck := flags.Duration("denied-quota-recheck-interval", deniedQuotaDefault, "denied Quota response recheck interval")
	if err := flags.Parse(args); err != nil {
		return app.Config{}, err
	}
	if *quotaRecheck < time.Second || *deniedQuotaRecheck < time.Second {
		return app.Config{}, errors.New("quota recheck intervals must be at least one second")
	}
	return app.Config{
		Surface:                    api.SurfaceGizPay,
		Address:                    *address,
		PostgreSQLDSN:              *postgresDSN,
		Initialize:                 *initialize,
		StoryTestMode:              *storyTestMode,
		PaymentProviderBaseURL:     *paymentProviderBaseURL,
		PaymentProviderCredential:  getenv("GIZPAY_PAYMENT_PROVIDER_CREDENTIAL"),
		PaymentCallbackSecret:      getenv("GIZPAY_PAYMENT_CALLBACK_SECRET"),
		CheckoutBaseURL:            *checkoutBaseURL,
		SecretEncryptionKey:        getenv("GIZPAY_SECRET_ENCRYPTION_KEY"),
		RiskProviderBaseURL:        *riskProviderBaseURL,
		RiskProviderCredential:     getenv("GIZPAY_RISK_PROVIDER_CREDENTIAL"),
		PowerSyncURL:               *powerSyncURL,
		PowerSyncAudience:          *powerSyncAudience,
		PowerSyncKeyID:             *powerSyncKeyID,
		PowerSyncSigningKey:        getenv("GIZPAY_POWERSYNC_SIGNING_KEY"),
		TLSCertificateFile:         *tlsCertificateFile,
		TLSPrivateKeyFile:          *tlsPrivateKeyFile,
		GatewayClientCAFile:        *gatewayClientCAFile,
		QuotaRecheckInterval:       *quotaRecheck,
		DeniedQuotaRecheckInterval: *deniedQuotaRecheck,
	}, nil
}

func durationOrDefault(value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < time.Second {
		return 0, errors.New("quota recheck interval must be a valid duration of at least one second")
	}
	return parsed, nil
}
