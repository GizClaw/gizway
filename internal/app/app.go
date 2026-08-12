// Package app composes Gizway's storage, stores, and HTTP transport.
package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	bifrostadapter "github.com/idy/gizway/internal/adapter/bifrost"
	paymentadapter "github.com/idy/gizway/internal/adapter/payment"
	riskadapter "github.com/idy/gizway/internal/adapter/risk"
	"github.com/idy/gizway/internal/api"
	gizpayclient "github.com/idy/gizway/internal/client/gizpay"
	gatewayservice "github.com/idy/gizway/internal/service/gateway"
	"github.com/idy/gizway/internal/service/gatewayquota"
	"github.com/idy/gizway/internal/service/localadmission"
	merchantservice "github.com/idy/gizway/internal/service/merchant"
	paymentservice "github.com/idy/gizway/internal/service/payment"
	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/timetext"
)

// Config describes one Gizway process.
type Config struct {
	Surface                    api.Surface
	Address                    string
	PostgreSQLDSN              string
	Initialize                 bool
	StoryTestMode              bool
	StoryResumeMode            bool
	AIProviderBaseURL          string
	AIProviderCredential       string
	AIProviderCallbackSecret   string
	AIProviderCallbackURL      string
	PaymentProviderBaseURL     string
	PaymentProviderCredential  string
	PaymentCallbackSecret      string
	CheckoutBaseURL            string
	SecretEncryptionKey        string
	RiskProviderBaseURL        string
	RiskProviderCredential     string
	PowerSyncURL               string
	PowerSyncAudience          string
	PowerSyncKeyID             string
	PowerSyncSigningKey        string
	TLSCertificateFile         string
	TLSPrivateKeyFile          string
	GatewayClientCAFile        string
	GizPayInternalBaseURL      string
	GizPayMTLSCertificateFile  string
	GizPayMTLSPrivateKeyFile   string
	GizPayMTLSServerCAFile     string
	NodeID                     string
	Region                     string
	QuotaRecheckInterval       time.Duration
	DeniedQuotaRecheckInterval time.Duration
}

// Run owns the service lifecycle until the context is cancelled.
func Run(ctx context.Context, config Config) error {
	runContext, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	storyEnvironment := config.StoryTestMode || config.StoryResumeMode
	runsGateway := config.Surface == api.SurfaceGizWay
	runsControlPlane := config.Surface == api.SurfaceGizPay
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
	if storyEnvironment && config.Initialize {
		return errors.New("story test mode cannot be combined with initialization")
	}
	if config.PostgreSQLDSN == "" {
		return errors.New("PostgreSQL DSN is required")
	}
	var database *storage.Storage
	var err error
	if config.StoryTestMode {
		switch config.Surface {
		case api.SurfaceGizPay:
			database, err = storage.OpenGizPayStoryPostgreSQL(config.PostgreSQLDSN)
		case api.SurfaceGizWay:
			database, err = storage.OpenGizWayStoryPostgreSQL(config.PostgreSQLDSN)
		}
	} else if config.StoryResumeMode {
		database, err = storage.OpenExistingPostgreSQL(config.PostgreSQLDSN)
	} else {
		switch config.Surface {
		case api.SurfaceGizPay:
			database, err = storage.OpenGizPayPostgreSQL(config.PostgreSQLDSN, config.Initialize)
		case api.SurfaceGizWay:
			database, err = storage.OpenGizWayPostgreSQL(config.PostgreSQLDSN, config.Initialize)
		}
	}
	if database == nil && err == nil {
		return errors.New("GizWay or GizPay surface is required")
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
	if runsGateway {
		if _, err := repository.AbandonUsageOutboxOnStartup(runContext); err != nil {
			// Cancellation during startup is the same graceful shutdown requested
			// after ListenAndServe begins; do not turn that race into a process
			// failure merely because startup cleanup was the active database call.
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}

	var gateway *gatewayservice.Service
	var regionalQuota *gatewayquota.Runtime
	var payments *paymentservice.Service
	var executor *bifrostadapter.Adapter
	var gizPayClient *gizpayclient.Client
	if runsGateway && config.AIProviderBaseURL != "" {
		if config.AIProviderCredential == "" {
			return errors.New("AI provider credential is required when AI provider base URL is configured")
		}
		executor, err = bifrostadapter.NewOpenAI(runContext, config.AIProviderBaseURL, config.AIProviderCredential)
		if err != nil {
			return err
		}
		defer executor.Shutdown()
		gateway = gatewayservice.NewWithRealtimeProviderCallback(repository, executor, config.AIProviderCallbackURL, config.AIProviderCallbackSecret)
	} else if runsGateway {
		// Production catalog rows carry the authoritative encrypted endpoint and
		// credential. A process-level provider remains an optional development
		// default when no explicit upstream is configured.
		executor = bifrostadapter.NewLazy()
		defer executor.Shutdown()
		gateway = gatewayservice.NewWithRealtimeProviderCallback(repository, executor, config.AIProviderCallbackURL, config.AIProviderCallbackSecret)
	}
	if config.Surface == api.SurfaceGizWay {
		if config.NodeID == "" || (config.Region != "cn" && config.Region != "global") {
			return errors.New("GizWay node ID and cn/global region are required")
		}
		if config.GizPayInternalBaseURL == "" || config.GizPayMTLSCertificateFile == "" ||
			config.GizPayMTLSPrivateKeyFile == "" || config.GizPayMTLSServerCAFile == "" {
			return errors.New("GizPay internal URL, node certificate, node key, and server CA are required by GizWay")
		}
		gizPayClient, err = gizpayclient.NewMTLS(config.GizPayInternalBaseURL, config.GizPayMTLSCertificateFile,
			config.GizPayMTLSPrivateKeyFile, config.GizPayMTLSServerCAFile)
		if err != nil {
			return err
		}
		regionalQuota = gatewayquota.New(gizPayClient, localadmission.New(businessNow), repository, businessNow)
		gateway.ConfigureRegionalQuota(regionalQuota)
	}
	if gateway != nil {
		gateway.ConfigureClock(businessNow)
		if storyEnvironment {
			gateway.ConfigureRealtimeSessionTimeout(500 * time.Millisecond)
		}
	}
	if runsControlPlane && config.PaymentProviderBaseURL != "" {
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
	if runsControlPlane && checkoutBaseURL == "" {
		return errors.New("public checkout base URL is required outside story test mode")
	}
	checkoutURL, parseErr := url.Parse(checkoutBaseURL)
	if runsControlPlane && (parseErr != nil || checkoutURL.Host == "" || (checkoutURL.Scheme != "https" && !(storyEnvironment && checkoutURL.Scheme == "http"))) {
		return errors.New("public checkout base URL must be an absolute HTTPS URL")
	}
	var merchant *merchantservice.Service
	if runsControlPlane {
		merchant = merchantservice.NewConfigured(repository, nil, storyEnvironment, checkoutBaseURL)
	}
	if runsControlPlane && config.RiskProviderBaseURL != "" {
		if config.RiskProviderCredential == "" {
			return errors.New("risk provider credential is required when risk provider base URL is configured")
		}
		merchant = merchantservice.NewConfigured(repository, riskadapter.New(config.RiskProviderBaseURL, config.RiskProviderCredential), storyEnvironment, checkoutBaseURL)
	}
	if merchant != nil {
		merchant.ConfigureClock(businessNow)
	}
	apiServer := api.NewWithServicesAndClockSurface(repository, gateway, payments, merchant, businessNow, advanceClock, config.Surface)
	if config.Surface == api.SurfaceGizPay {
		apiServer.ConfigureQuotaRecheckPolicy(config.QuotaRecheckInterval, config.DeniedQuotaRecheckInterval)
	}
	if gizPayClient != nil {
		apiServer.ConfigureRegionalRatePublication(config.Region, func(ctx context.Context, id string, revision int64, effectiveAt string, prices []store.PublishedPrice) (string, string, error) {
			published := make([]gizpayclient.PublishedPrice, len(prices))
			for index, price := range prices {
				published[index] = gizpayclient.PublishedPrice{
					ModelVariantID: price.ModelVariantID, PublicModel: price.PublicModel,
					Metric: price.Metric, UnitSize: price.UnitSize,
					BasePriceMicrocredits:     price.BasePriceMicrocredits,
					CustomerPriceMicrocredits: price.CustomerPriceMicrocredits,
					DiscountBPS:               price.DiscountBPS,
				}
			}
			result, err := gizPayClient.PublishRatePublication(ctx, id, revision, effectiveAt, published)
			if err != nil {
				// POST may have committed before its response was lost. Recover by
				// querying the same source publication ID instead of creating a new
				// financial snapshot.
				result, err = gizPayClient.GetRatePublication(ctx, id)
			}
			return result.ID, result.ContentSHA256, err
		})
	}
	apiServer.ConfigureReadiness(func(ctx context.Context, internal bool) (map[string]any, error) {
		var checks map[string]string
		var err error
		result := map[string]any{}
		switch config.Surface {
		case api.SurfaceGizPay:
			checks, err = repository.GizPayReadinessChecks(ctx, internal)
			if err == nil {
				checks["secret_encryption"] = "ready"
				if internal {
					checks["mtls_ca"] = "ready"
					checks["rate_publication_store"] = "ready"
				}
			}
			result["service"] = "gizpay"
		case api.SurfaceGizWay:
			checks, err = repository.GizWayReadinessChecks(ctx)
			if err == nil {
				if gizPayClient.CheckReadiness(ctx, config.NodeID, config.Region) == nil {
					checks["node_identity"] = "ready"
					checks["quota_exchange"] = "ready"
				} else {
					checks["node_identity"] = "pending"
					checks["quota_exchange"] = "pending"
				}
			}
			result["service"] = "gizway"
			result["node_id"] = config.NodeID
			result["region"] = config.Region
		}
		if err != nil {
			return nil, err
		}
		status := "ready"
		for _, check := range checks {
			if check != "ready" {
				status = "not_ready"
				break
			}
		}
		result["status"] = status
		result["checks"] = checks
		return result, nil
	})
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
	serveTLS := config.Surface == api.SurfaceGizPay && (!storyEnvironment || config.TLSCertificateFile != "" || config.TLSPrivateKeyFile != "" || config.GatewayClientCAFile != "")
	if serveTLS {
		if config.TLSCertificateFile == "" || config.TLSPrivateKeyFile == "" || config.GatewayClientCAFile == "" {
			return errors.New("GizPay TLS certificate, private key, and Gateway client CA are required")
		}
		clientCAPEM, err := os.ReadFile(config.GatewayClientCAFile)
		if err != nil {
			return fmt.Errorf("read Gateway client CA: %w", err)
		}
		clientCAs := x509.NewCertPool()
		if !clientCAs.AppendCertsFromPEM(clientCAPEM) {
			return errors.New("gateway client CA file contains no certificates")
		}
		server.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS13,
			ClientAuth: tls.VerifyClientCertIfGiven,
			ClientCAs:  clientCAs,
		}
	}
	dispatcherDone := make(chan struct{})
	go func() {
		defer close(dispatcherDone)
		if merchant != nil {
			merchant.RunDispatcher(runContext, time.Second)
		}
	}()
	quotaExchangeDone := make(chan struct{})
	go func() {
		defer close(quotaExchangeDone)
		if regionalQuota == nil {
			<-runContext.Done()
			return
		}
		// Usage delivery is event-driven with a one-second retry poll. Database
		// next_attempt_at applies the bounded exponential backoff, and shutdown
		// abandons rather than recovering any unfinished customer association.
		regionalQuota.Run(runContext, time.Second)
	}()
	settlementDone := make(chan struct{})
	go func() {
		defer close(settlementDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			workerContext := store.WithAuditRequestID(runContext, "control-plane-recovery")
			if runsControlPlane {
				_, _ = repository.ExpirePaymentIntents(workerContext, timetext.Format(businessNow()), 32)
			}
			if payments != nil {
				_ = payments.RecoverPendingRefunds(workerContext, 32)
			}
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

	scheme := "http"
	if serveTLS {
		scheme = "https"
	}
	log.Printf("Gizway API listening on %s://%s", scheme, config.Address)
	if serveTLS {
		err = server.ListenAndServeTLS(config.TLSCertificateFile, config.TLSPrivateKeyFile)
	} else {
		err = server.ListenAndServe()
	}
	// ListenAndServe can fail independently of the caller context, for example
	// when the address is already occupied. Stop and join every worker before
	// deferred provider/database teardown on every server exit path.
	cancelRun()
	<-shutdownDone
	<-dispatcherDone
	<-quotaExchangeDone
	<-settlementDone
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
