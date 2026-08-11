// Package app composes Gizway's storage, stores, and HTTP transport.
package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/google/uuid"

	bifrostadapter "github.com/idy/gizway/internal/adapter/bifrost"
	paymentadapter "github.com/idy/gizway/internal/adapter/payment"
	riskadapter "github.com/idy/gizway/internal/adapter/risk"
	"github.com/idy/gizway/internal/api"
	gatewayservice "github.com/idy/gizway/internal/service/gateway"
	merchantservice "github.com/idy/gizway/internal/service/merchant"
	paymentservice "github.com/idy/gizway/internal/service/payment"
	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/timetext"
)

// Config describes one Gizway process.
type Config struct {
	Address                     string
	PostgreSQLDSN               string
	Initialize                  bool
	DevelopmentSeed             bool
	StoryTestMode               bool
	StoryResumeMode             bool
	AIProviderBaseURL           string
	AIProviderCredential        string
	AIProviderCallbackSecret    string
	AIProviderCallbackURL       string
	PaymentProviderBaseURL      string
	PaymentProviderCredential   string
	PaymentCallbackSecret       string
	CheckoutBaseURL             string
	SecretEncryptionKey         string
	RiskProviderBaseURL         string
	RiskProviderCredential      string
	PowerSyncURL                string
	PowerSyncAudience           string
	PowerSyncKeyID              string
	PowerSyncSigningKey         string
	StoryCrashAfterProviderFile string
}

// Run owns the service lifecycle until the context is cancelled.
func Run(ctx context.Context, config Config) error {
	runContext, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	storyEnvironment := config.StoryTestMode || config.StoryResumeMode
	businessNow := time.Now
	var advanceClock func(time.Duration) time.Time
	if storyEnvironment {
		fixtureClock := newMutableClock(storyFixtureInstant)
		businessNow = fixtureClock.Now
		advanceClock = fixtureClock.Advance
	}
	if config.StoryTestMode && config.StoryResumeMode {
		return errors.New("story test and story resume modes are mutually exclusive")
	}
	if storyEnvironment && (config.Initialize || config.DevelopmentSeed) {
		return errors.New("story test mode cannot be combined with initialization or development seed flags")
	}
	if config.StoryCrashAfterProviderFile != "" && !storyEnvironment {
		return errors.New("AI crash injection is available only in story test mode")
	}
	if config.PostgreSQLDSN == "" {
		return errors.New("PostgreSQL DSN is required")
	}
	var database *storage.Storage
	var err error
	if config.StoryTestMode {
		database, err = storage.OpenStoryPostgreSQL(config.PostgreSQLDSN)
	} else if config.StoryResumeMode {
		database, err = storage.OpenExistingPostgreSQL(config.PostgreSQLDSN)
	} else {
		database, err = storage.OpenDevelopmentPostgreSQL(config.PostgreSQLDSN, config.Initialize, config.DevelopmentSeed)
	}
	if err != nil {
		return err
	}
	defer database.Close()

	var secretKey []byte
	if storyEnvironment {
		secretKey = []byte("gizway-story-secret-key-32bytes!")
	} else {
		if config.SecretEncryptionKey == "" {
			return errors.New("GIZWAY_SECRET_ENCRYPTION_KEY is required outside story test mode")
		}
		secretKey, err = decodeSecretEncryptionKey(config.SecretEncryptionKey)
		if err != nil {
			return err
		}
	}
	repository, err := store.NewWithSecretKey(database.SQL, secretKey)
	if err != nil {
		return err
	}
	repository.ConfigureClock(businessNow)

	var gateway *gatewayservice.Service
	var payments *paymentservice.Service
	var executor *bifrostadapter.Adapter
	if config.AIProviderBaseURL != "" {
		if config.AIProviderCredential == "" {
			return errors.New("AI provider credential is required when AI provider base URL is configured")
		}
		executor, err = bifrostadapter.NewOpenAI(runContext, config.AIProviderBaseURL, config.AIProviderCredential)
		if err != nil {
			return err
		}
		defer executor.Shutdown()
		gateway = gatewayservice.NewWithRealtimeProviderCallback(repository, executor, config.AIProviderCallbackURL, config.AIProviderCallbackSecret)
	} else {
		// Production catalog rows carry the authoritative encrypted endpoint and
		// credential. A process-level provider remains only an optional default
		// for development/backward compatibility.
		executor = bifrostadapter.NewLazy()
		defer executor.Shutdown()
		gateway = gatewayservice.NewWithRealtimeProviderCallback(repository, executor, config.AIProviderCallbackURL, config.AIProviderCallbackSecret)
	}
	if config.StoryCrashAfterProviderFile != "" {
		marker := config.StoryCrashAfterProviderFile
		gateway.ConfigureStoryCrashRecovery(time.Second, func() {
			file, createErr := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if errors.Is(createErr, os.ErrExist) {
				return
			}
			if createErr != nil {
				log.Printf("create AI crash marker: %v", createErr)
				return
			}
			_, _ = file.WriteString("provider succeeded; settlement intentionally interrupted\n")
			_ = file.Sync()
			_ = file.Close()
			os.Exit(86)
		})
	}
	gateway.ConfigureClock(businessNow)
	if storyEnvironment {
		gateway.ConfigureRealtimeSessionTimeout(500 * time.Millisecond)
	}
	if config.PaymentProviderBaseURL != "" {
		if config.PaymentProviderCredential == "" || config.PaymentCallbackSecret == "" {
			return errors.New("payment provider credential and callback secret are required")
		}
		payments = paymentservice.New(repository, paymentadapter.New(config.PaymentProviderBaseURL, config.PaymentProviderCredential), config.PaymentCallbackSecret)
		payments.ConfigureClock(businessNow)
	}

	checkoutBaseURL := config.CheckoutBaseURL
	if checkoutBaseURL == "" && storyEnvironment {
		checkoutBaseURL = "https://pay.gizway.test"
	}
	if checkoutBaseURL == "" {
		return errors.New("public checkout base URL is required outside story test mode")
	}
	checkoutURL, parseErr := url.Parse(checkoutBaseURL)
	if parseErr != nil || checkoutURL.Host == "" || (checkoutURL.Scheme != "https" && !(storyEnvironment && checkoutURL.Scheme == "http")) {
		return errors.New("public checkout base URL must be an absolute HTTPS URL")
	}
	merchant := merchantservice.NewConfigured(repository, nil, storyEnvironment, checkoutBaseURL)
	if config.RiskProviderBaseURL != "" {
		if config.RiskProviderCredential == "" {
			return errors.New("risk provider credential is required when risk provider base URL is configured")
		}
		merchant = merchantservice.NewConfigured(repository, riskadapter.New(config.RiskProviderBaseURL, config.RiskProviderCredential), storyEnvironment, checkoutBaseURL)
	}
	merchant.ConfigureClock(businessNow)
	apiServer := api.NewWithServicesAndClock(repository, gateway, payments, merchant, businessNow, advanceClock)
	powerSyncURL, powerSyncAudience, powerSyncKeyID := config.PowerSyncURL, config.PowerSyncAudience, config.PowerSyncKeyID
	var powerSyncKey []byte
	if storyEnvironment && powerSyncURL == "" && powerSyncAudience == "" && powerSyncKeyID == "" && config.PowerSyncSigningKey == "" {
		powerSyncURL, powerSyncAudience, powerSyncKeyID = "https://sync.gizway.test", "powersync-story", "gizway-story-hs256"
		powerSyncKey = []byte("gizway-story-powersync-signing-key")
	} else if powerSyncURL != "" || powerSyncAudience != "" || powerSyncKeyID != "" || config.PowerSyncSigningKey != "" {
		if powerSyncURL == "" || powerSyncAudience == "" || powerSyncKeyID == "" || config.PowerSyncSigningKey == "" {
			return errors.New("PowerSync URL, audience, key ID, and signing key must be configured together")
		}
		parsedPowerSyncURL, parseErr := url.Parse(powerSyncURL)
		if parseErr != nil || parsedPowerSyncURL.Host == "" || (parsedPowerSyncURL.Scheme != "https" && !(storyEnvironment && parsedPowerSyncURL.Scheme == "http")) {
			return errors.New("PowerSync URL must be an absolute HTTPS URL")
		}
		powerSyncKey, err = decodePowerSyncSigningKey(config.PowerSyncSigningKey)
		if err != nil {
			return err
		}
	}
	if len(powerSyncKey) != 0 {
		apiServer.ConfigurePowerSync(powerSyncURL, powerSyncAudience, powerSyncKeyID, powerSyncKey)
	}
	server := &http.Server{
		Addr:    config.Address,
		Handler: apiServer.Handler(),
		// Every request, including an HTTP connection later hijacked by the
		// Realtime WebSocket handler, is a child of the application lifetime.
		// Cancelling Run's context therefore stops long-lived proxy loops before
		// the deferred provider executor and database shutdowns can run.
		BaseContext:       func(net.Listener) context.Context { return runContext },
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	dispatcherDone := make(chan struct{})
	go func() {
		defer close(dispatcherDone)
		merchant.RunDispatcher(runContext, time.Second)
	}()
	settlementDone := make(chan struct{})
	go func() {
		defer close(settlementDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			workerContext := store.WithAuditRequestID(runContext, "recovery-"+uuid.NewString())
			_ = repository.RecoverGatewaySettlements(workerContext, 32)
			_, _ = repository.ExpirePaymentIntents(workerContext, timetext.Format(businessNow()), 32)
			if payments != nil {
				_ = payments.RecoverPendingRefunds(workerContext, 32)
			}
			if gateway != nil {
				_ = gateway.RecoverRealtimeProviderEvents(workerContext, 32)
				_ = gateway.RecoverExpiredRealtimeSessions(workerContext, 32)
			}
			select {
			case <-runContext.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	// Provider replay owns a separate lifecycle from database-only settlement,
	// payment, refund and Realtime recovery. Its bounded API worker pool may wait
	// on slow upstreams, but it can never delay those other economic state
	// machines or their next one-second tick.
	gatewayRecoveryDone := make(chan struct{})
	go func() {
		defer close(gatewayRecoveryDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			workerContext := store.WithAuditRequestID(runContext, "gateway-recovery-"+uuid.NewString())
			// An expired HTTPS lease is ambiguous, not provider failure. The API
			// pool replays the encrypted request with its immutable provider plan;
			// it never releases a reservation the provider may have consumed.
			_ = apiServer.RecoverGatewayCommands(workerContext, 32)
			select {
			case <-runContext.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-runContext.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := apiServer.CloseRealtimeConnections(shutdownContext); err != nil {
			log.Printf("Realtime shutdown timed out: %v", err)
			// Dependency teardown must never race a hijacked handler. CloseNow has
			// already interrupted every tracked socket; if its settlement cleanup
			// exceeded the graceful budget, continue waiting rather than closing
			// Bifrost/sqlx underneath it.
			_ = apiServer.CloseRealtimeConnections(context.Background())
		}
		if err := server.Shutdown(shutdownContext); err != nil {
			log.Printf("graceful HTTP shutdown timed out: %v", err)
			// Shutdown does not force-close ordinary active connections after its
			// deadline. BaseContext cancellation has already stopped hijacked
			// Realtime proxies; Close now terminates any remaining HTTP handlers so
			// Run cannot tear down their Store/executor dependencies underneath them.
			if closeErr := server.Close(); closeErr != nil {
				log.Printf("force close HTTP server: %v", closeErr)
			}
		}
	}()

	log.Printf("Gizway API listening on http://%s", config.Address)
	err = server.ListenAndServe()
	// ListenAndServe can fail independently of the caller context, for example
	// when the address is already occupied. Stop and join every worker before
	// deferred provider/database teardown on every server exit path.
	cancelRun()
	<-shutdownDone
	<-dispatcherDone
	<-settlementDone
	<-gatewayRecoveryDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}

func decodeSecretEncryptionKey(encoded string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		key, err := encoding.DecodeString(encoded)
		if err == nil && len(key) == 32 {
			return key, nil
		}
	}
	return nil, errors.New("GIZWAY_SECRET_ENCRYPTION_KEY must be base64 encoding of exactly 32 bytes")
}

func decodePowerSyncSigningKey(encoded string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		key, err := encoding.DecodeString(encoded)
		if err == nil && len(key) >= 32 {
			return key, nil
		}
	}
	return nil, errors.New("GIZWAY_POWERSYNC_SIGNING_KEY must be base64 encoding of at least 32 bytes")
}
