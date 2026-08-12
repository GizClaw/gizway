// Command gizway runs the Gizway API service.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

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
	flags := flag.NewFlagSet("gizway", flag.ContinueOnError)
	address := flags.String("address", "127.0.0.1:8080", "HTTP listen address")
	postgresDSN := flags.String("postgres-dsn", getenv("GIZWAY_POSTGRES_DSN"), "PostgreSQL DSN")
	initialize := flags.Bool("initialize", false, "apply the embedded PostgreSQL schema")
	storyTestMode := flags.Bool("story-test-mode", false, "initialize an isolated database with story-only fixtures")
	storyResumeMode := flags.Bool("story-resume-mode", false, "reopen an existing story database after an intentional crash")
	aiProviderBaseURL := flags.String("ai-provider-base-url", "", "OpenAI-compatible provider base URL")
	aiProviderCallbackURL := flags.String("ai-provider-callback-url", "", "public Gizway base URL used for provider Realtime callbacks")
	gizPayInternalBaseURL := flags.String("gizpay-internal-base-url", getenv("GIZPAY_INTERNAL_BASE_URL"), "GizPay internal HTTPS base URL")
	gizPayMTLSCertificateFile := flags.String("gizpay-mtls-cert-file", getenv("GIZPAY_MTLS_CERT_FILE"), "Gateway node mTLS certificate PEM")
	gizPayMTLSPrivateKeyFile := flags.String("gizpay-mtls-key-file", getenv("GIZPAY_MTLS_KEY_FILE"), "Gateway node mTLS private key PEM")
	gizPayMTLSServerCAFile := flags.String("gizpay-mtls-ca-file", getenv("GIZPAY_MTLS_CA_FILE"), "CA PEM trusted for GizPay server certificates")
	nodeID := flags.String("node-id", getenv("GIZWAY_NODE_ID"), "stable Gateway node ID registered in GizPay")
	region := flags.String("region", getenv("GIZWAY_REGION"), "Gateway region: cn or global")
	if err := flags.Parse(args); err != nil {
		return app.Config{}, err
	}
	return app.Config{
		Surface:                   api.SurfaceGizWay,
		Address:                   *address,
		PostgreSQLDSN:             *postgresDSN,
		Initialize:                *initialize,
		StoryTestMode:             *storyTestMode,
		StoryResumeMode:           *storyResumeMode,
		AIProviderBaseURL:         *aiProviderBaseURL,
		AIProviderCallbackURL:     *aiProviderCallbackURL,
		AIProviderCredential:      getenv("GIZWAY_AI_PROVIDER_CREDENTIAL"),
		AIProviderCallbackSecret:  getenv("GIZWAY_AI_PROVIDER_CALLBACK_SECRET"),
		SecretEncryptionKey:       getenv("GIZWAY_SECRET_ENCRYPTION_KEY"),
		GizPayInternalBaseURL:     *gizPayInternalBaseURL,
		GizPayMTLSCertificateFile: *gizPayMTLSCertificateFile,
		GizPayMTLSPrivateKeyFile:  *gizPayMTLSPrivateKeyFile,
		GizPayMTLSServerCAFile:    *gizPayMTLSServerCAFile,
		NodeID:                    *nodeID,
		Region:                    *region,
	}, nil
}
