package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jmoiron/sqlx"
	"go.yaml.in/yaml/v3"

	bifrostadapter "github.com/idy/gizway/internal/adapter/bifrost"
	"github.com/idy/gizway/internal/api"
	payservice "github.com/idy/gizway/internal/gizpay"
	wayservice "github.com/idy/gizway/internal/gizway"
	"github.com/idy/gizway/internal/identity"
	"github.com/idy/gizway/internal/storage"
)

type ProcessKind string

const (
	ProcessGizPay ProcessKind = "gizpay"
	ProcessGizWay ProcessKind = "gizway"

	defaultCreditCacheCleanupInterval = "1m"
)

type ProcessConfig struct {
	Version int `yaml:"version" json:"version"`
	Server  struct {
		Name            string `yaml:"name" json:"name"`
		ListenAddress   string `yaml:"listen_address" json:"listen_address"`
		ShutdownTimeout string `yaml:"shutdown_timeout" json:"shutdown_timeout,omitempty"`
	} `yaml:"server" json:"server"`
	Database       StoreConfig `yaml:"database" json:"database"`
	Authentication struct {
		ZITADEL struct {
			Issuer              string              `yaml:"issuer" json:"issuer"`
			JWKSURL             string              `yaml:"jwks_url" json:"jwks_url"`
			HumanAudience       string              `yaml:"human_audience" json:"human_audience,omitempty"`
			ServiceAudience     string              `yaml:"service_audience" json:"service_audience,omitempty"`
			ManagementClient    MachineClientConfig `yaml:"management_client" json:"management_client"`
			JWKSRefreshInterval string              `yaml:"jwks_refresh_interval" json:"jwks_refresh_interval,omitempty"`
		} `yaml:"zitadel" json:"zitadel"`
		ServiceAccount MachineClientConfig `yaml:"service_account" json:"service_account"`
	} `yaml:"authentication" json:"authentication"`
	SubscriptionKeys struct {
		HMAC struct {
			SecretFile string `yaml:"secret_file" json:"secret_file"`
		} `yaml:"hmac" json:"hmac"`
	} `yaml:"subscription_keys" json:"subscription_keys"`
	CreditCheck struct {
		RecheckInterval string `yaml:"recheck_interval" json:"recheck_interval"`
	} `yaml:"credit_check" json:"credit_check"`
	CreditCache struct {
		CleanupInterval string `yaml:"cleanup_interval" json:"cleanup_interval"`
	} `yaml:"credit_cache" json:"credit_cache"`
	PAYGCharges struct {
		PlatformFeeBPS        int64 `yaml:"platform_fee_bps" json:"platform_fee_bps"`
		MaxOrderMetadataBytes int   `yaml:"max_order_metadata_bytes" json:"max_order_metadata_bytes"`
		MaxCommissions        int   `yaml:"max_commissions_per_charge" json:"max_commissions_per_charge"`
	} `yaml:"payg_charges" json:"payg_charges"`
	TLS struct {
		Enabled         *bool  `yaml:"enabled" json:"enabled,omitempty"`
		CertificateFile string `yaml:"certificate_file" json:"certificate_file,omitempty"`
		PrivateKeyFile  string `yaml:"private_key_file" json:"private_key_file,omitempty"`
	} `yaml:"tls" json:"tls"`
	GizPay struct {
		ServiceDSN     string `yaml:"service_dsn" json:"service_dsn"`
		RequestTimeout string `yaml:"request_timeout" json:"request_timeout,omitempty"`
	} `yaml:"gizpay" json:"gizpay"`
	ChargeOutbox struct {
		MaxBatchSize  int    `yaml:"max_batch_size" json:"max_batch_size,omitempty"`
		RetryInterval string `yaml:"retry_interval" json:"retry_interval,omitempty"`
		AbandonAfter  string `yaml:"abandon_after" json:"abandon_after,omitempty"`
	} `yaml:"charge_outbox" json:"charge_outbox"`
	Bifrost struct {
		ConfigStore StoreConfig `yaml:"config_store" json:"config_store"`
		LogStore    StoreConfig `yaml:"log_store" json:"log_store"`
		Execution   struct {
			MaxRetries     int    `yaml:"max_retries" json:"max_retries,omitempty"`
			RequestTimeout string `yaml:"request_timeout" json:"request_timeout,omitempty"`
		} `yaml:"execution" json:"execution"`
	} `yaml:"bifrost" json:"bifrost"`
	ProviderCallbacks struct {
		PublicBaseURL      string `yaml:"public_base_url" json:"public_base_url,omitempty"`
		CallbackSecretFile string `yaml:"callback_secret_file" json:"callback_secret_file,omitempty"`
	} `yaml:"provider_callbacks" json:"provider_callbacks"`
	Logging struct {
		Level  string `yaml:"level" json:"level,omitempty"`
		Format string `yaml:"format" json:"format,omitempty"`
	} `yaml:"logging" json:"logging"`
	configDirectory string
}

type StoreConfig struct {
	Type          string `yaml:"type" json:"type,omitempty"`
	DSN           string `yaml:"dsn" json:"dsn"`
	Schema        string `yaml:"schema" json:"schema"`
	Initialize    bool   `yaml:"initialize" json:"initialize,omitempty"`
	RetentionDays int    `yaml:"retention_days" json:"retention_days,omitempty"`
	Writer        struct {
		MaxBatchSize       int    `yaml:"max_batch_size" json:"max_batch_size,omitempty"`
		BatchInterval      string `yaml:"batch_interval" json:"batch_interval,omitempty"`
		WriteQueueCapacity int    `yaml:"write_queue_capacity" json:"write_queue_capacity,omitempty"`
	} `yaml:"writer" json:"writer"`
}

type MachineClientConfig struct {
	TokenURL           string   `yaml:"token_url" json:"token_url,omitempty"`
	Subject            string   `yaml:"subject" json:"subject,omitempty"`
	KeyID              string   `yaml:"key_id" json:"key_id,omitempty"`
	PrivateKeyFile     string   `yaml:"private_key_file" json:"private_key_file,omitempty"`
	Audience           string   `yaml:"audience" json:"audience,omitempty"`
	RequestedScopes    []string `yaml:"requested_scopes" json:"requested_scopes,omitempty"`
	RequiredRoles      []string `yaml:"required_roles" json:"required_roles,omitempty"`
	TokenRefreshBefore string   `yaml:"token_refresh_before" json:"token_refresh_before,omitempty"`
}

func LoadProcessConfig(path string, kind ProcessKind) (ProcessConfig, error) {
	if path == "" {
		return ProcessConfig{}, errors.New("--config is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return ProcessConfig{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	config := ProcessConfig{}
	config.CreditCheck.RecheckInterval = "5m"
	config.CreditCache.CleanupInterval = defaultCreditCacheCleanupInterval
	config.PAYGCharges.MaxOrderMetadataBytes = 8192
	config.PAYGCharges.MaxCommissions = 32
	if err := decoder.Decode(&config); err != nil {
		return ProcessConfig{}, fmt.Errorf("decode config (unknown fields are forbidden): %w", err)
	}
	config.configDirectory = filepath.Dir(path)
	if err := ValidateProcessConfig(config, kind); err != nil {
		return ProcessConfig{}, err
	}
	return config, nil
}

func ValidateProcessConfig(config ProcessConfig, kind ProcessKind) error {
	if config.Version != 1 {
		return errors.New("config version must be 1")
	}
	if !validServerName(config.Server.Name) {
		return errors.New("server.name must be a domain name")
	}
	if config.Server.ListenAddress == "" {
		return errors.New("server.listen_address is required")
	}
	if config.Database.DSN == "" || config.Database.Schema == "" {
		return errors.New("database.dsn and database.schema are required")
	}
	if config.SubscriptionKeys.HMAC.SecretFile == "" {
		return errors.New("subscription_keys.hmac.secret_file is required")
	}
	files := []string{
		config.SubscriptionKeys.HMAC.SecretFile,
		config.Authentication.ZITADEL.ManagementClient.PrivateKeyFile,
		config.Authentication.ServiceAccount.PrivateKeyFile,
		config.TLS.CertificateFile,
		config.TLS.PrivateKeyFile,
		config.ProviderCallbacks.CallbackSecretFile,
	}
	for _, path := range files {
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(config.configDirectory, path)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read Secret file %s: %w", path, err)
		}
		if len(strings.TrimSpace(string(contents))) == 0 {
			return fmt.Errorf("secret file %s is empty", path)
		}
	}
	if kind == ProcessGizPay && (config.PAYGCharges.PlatformFeeBPS < 0 || config.PAYGCharges.PlatformFeeBPS > 10000) {
		return errors.New("payg_charges.platform_fee_bps must be between 0 and 10000")
	}
	if kind == ProcessGizPay && (config.PAYGCharges.MaxOrderMetadataBytes < 0 || config.PAYGCharges.MaxCommissions < 0) {
		return errors.New("payg_charges metadata and commission limits cannot be negative")
	}
	recheckValue := config.CreditCheck.RecheckInterval
	if recheckValue == "" {
		recheckValue = "5m"
	}
	recheckInterval, err := time.ParseDuration(recheckValue)
	if err != nil || recheckInterval < time.Second || recheckInterval%time.Second != 0 {
		return errors.New("credit_check.recheck_interval must be a positive whole-second duration")
	}
	if kind == ProcessGizPay {
		management := config.Authentication.ZITADEL.ManagementClient
		if config.Authentication.ZITADEL.Issuer == "" || config.Authentication.ZITADEL.JWKSURL == "" || config.Authentication.ZITADEL.HumanAudience == "" || config.Authentication.ZITADEL.ServiceAudience == "" || management.TokenURL == "" || management.PrivateKeyFile == "" {
			return errors.New("GizPay ZITADEL issuer, audiences, and management client are required")
		}
	}
	if kind == ProcessGizWay {
		service := config.Authentication.ServiceAccount
		if config.Authentication.ZITADEL.Issuer == "" || config.Authentication.ZITADEL.JWKSURL == "" || config.Authentication.ZITADEL.HumanAudience == "" ||
			service.TokenURL == "" || service.PrivateKeyFile == "" || service.Audience == "" || len(service.RequestedScopes) == 0 || len(service.RequiredRoles) == 0 {
			return errors.New("GizWay ZITADEL human and Service Account configuration is required")
		}
		if config.GizPay.ServiceDSN == "" {
			return errors.New("gizpay.service_dsn is required")
		}
		if config.Bifrost.ConfigStore.Type != "postgresql" || config.Bifrost.ConfigStore.DSN == "" || config.Bifrost.ConfigStore.Schema == "" {
			return errors.New("bifrost.config_store PostgreSQL DSN and schema are required")
		}
		if config.Bifrost.ConfigStore.DSN != config.Database.DSN {
			return errors.New("bifrost.config_store.dsn must equal database.dsn for atomic Provider Key transactions")
		}
		if config.Bifrost.LogStore.Type != "postgresql" && config.Bifrost.LogStore.Type != "clickhouse" {
			return errors.New("bifrost.log_store.type must be postgresql or clickhouse")
		}
		if config.Bifrost.LogStore.DSN == "" || config.Bifrost.LogStore.Schema == "" {
			return errors.New("bifrost.log_store DSN and schema are required")
		}
		if config.Bifrost.Execution.MaxRetries < 0 || config.Bifrost.LogStore.Writer.MaxBatchSize < 0 || config.Bifrost.LogStore.Writer.WriteQueueCapacity < 0 {
			return errors.New("bifrost retries and log store writer limits cannot be negative")
		}
		seenRoles := map[string]bool{}
		for _, role := range service.RequiredRoles {
			if strings.TrimSpace(role) == "" || seenRoles[role] {
				return errors.New("authentication.service_account.required_roles must be nonempty and unique")
			}
			seenRoles[role] = true
		}
		if (config.ProviderCallbacks.PublicBaseURL == "") != (config.ProviderCallbacks.CallbackSecretFile == "") {
			return errors.New("provider_callbacks public_base_url and callback_secret_file must be configured together")
		}
		if config.ProviderCallbacks.PublicBaseURL != "" {
			callbackURL, parseErr := url.Parse(config.ProviderCallbacks.PublicBaseURL)
			if parseErr != nil || callbackURL.Scheme == "" || callbackURL.Host == "" {
				return errors.New("provider_callbacks.public_base_url must be an absolute URL")
			}
		}
	}
	if config.Logging.Level != "" && config.Logging.Level != "debug" && config.Logging.Level != "info" && config.Logging.Level != "warn" && config.Logging.Level != "error" {
		return errors.New("logging.level must be debug, info, warn, or error")
	}
	if config.Logging.Format != "" && config.Logging.Format != "json" && config.Logging.Format != "text" {
		return errors.New("logging.format must be json or text")
	}
	for name, value := range map[string]string{
		"server.shutdown_timeout":                             config.Server.ShutdownTimeout,
		"authentication.zitadel.jwks_refresh_interval":        config.Authentication.ZITADEL.JWKSRefreshInterval,
		"authentication.service_account.token_refresh_before": config.Authentication.ServiceAccount.TokenRefreshBefore,
		"gizpay.request_timeout":                              config.GizPay.RequestTimeout,
		"credit_cache.cleanup_interval":                       config.CreditCache.CleanupInterval,
		"charge_outbox.retry_interval":                        config.ChargeOutbox.RetryInterval,
		"charge_outbox.abandon_after":                         config.ChargeOutbox.AbandonAfter,
		"bifrost.log_store.writer.batch_interval":             config.Bifrost.LogStore.Writer.BatchInterval,
		"bifrost.execution.request_timeout":                   config.Bifrost.Execution.RequestTimeout,
	} {
		if value != "" {
			if duration, durationErr := time.ParseDuration(value); durationErr != nil || duration <= 0 {
				return fmt.Errorf("%s must be a positive duration", name)
			}
		}
	}
	if (config.TLS.CertificateFile == "") != (config.TLS.PrivateKeyFile == "") {
		return errors.New("tls.certificate_file and tls.private_key_file must be configured together")
	}
	if config.TLS.Enabled != nil && *config.TLS.Enabled && config.TLS.CertificateFile == "" {
		return errors.New("TLS is enabled but server certificate files are missing")
	}
	return nil
}

func validServerName(name string) bool {
	if len(name) > 253 || strings.ContainsAny(name, ":/ ") || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return false
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return false
	}
	labelPattern := regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	for _, label := range labels {
		if !labelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func WriteEffectiveConfig(writer io.Writer, config ProcessConfig) error {
	config.Database.DSN = redactDSN(config.Database.DSN)
	config.Bifrost.ConfigStore.DSN = redactDSN(config.Bifrost.ConfigStore.DSN)
	config.Bifrost.LogStore.DSN = redactDSN(config.Bifrost.LogStore.DSN)
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(config)
}

func redactDSN(value string) string {
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" {
		if parsed.User != nil {
			if _, hasPassword := parsed.User.Password(); hasPassword {
				parsed.User = url.UserPassword(parsed.User.Username(), "REDACTED")
			}
		}
		query := parsed.Query()
		for _, key := range []string{"password", "passfile"} {
			if query.Has(key) {
				query.Set(key, "REDACTED")
			}
		}
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	password := regexp.MustCompile(`(?i)(password|passfile)(\s*=\s*)(?:'[^']*(?:''[^']*)*'|[^\s]+)`)
	return password.ReplaceAllString(value, `${1}${2}REDACTED`)
}

// RunProcess is replaced by the service composition step. Keeping the command
// boundary explicit now ensures configuration cannot fall back to environment.
func RunProcess(config ProcessConfig, kind ProcessKind) error {
	if err := ensureDatabaseSchema(config.Database.DSN, config.Database.Schema); err != nil {
		return err
	}
	dsn, err := withSearchPath(config.Database.DSN, config.Database.Schema)
	if err != nil {
		return err
	}
	var database *storage.Storage
	var surface api.Surface
	now := time.Now
	var advance func(time.Duration) time.Time
	if strings.HasSuffix(config.Server.Name, ".test") {
		clock := newStoryClock(time.Now().UTC())
		now, advance = clock.Now, clock.Advance
	}
	if kind == ProcessGizPay {
		database, err = storage.OpenGizPayPostgreSQL(dsn, config.Database.Initialize)
		surface = api.SurfaceGizPay
	} else {
		database, err = storage.OpenGizWayPostgreSQL(dsn, config.Database.Initialize)
		surface = api.SurfaceGizWay
	}
	if err != nil {
		return err
	}
	defer database.Close()
	logger := processLogger(config.Logging.Level, config.Logging.Format)
	zitadelClient := &http.Client{Timeout: 10 * time.Second}
	var business http.Handler
	if kind == ProcessGizPay {
		hmacSecret, err := readConfiguredSecret(config, config.SubscriptionKeys.HMAC.SecretFile)
		if err != nil {
			return err
		}
		management := config.Authentication.ZITADEL.ManagementClient
		managementKeyFile := management.PrivateKeyFile
		if !filepath.IsAbs(managementKeyFile) {
			managementKeyFile = filepath.Join(config.configDirectory, managementKeyFile)
		}
		managementScopes := append([]string(nil), management.RequestedScopes...)
		managementScopes = append(managementScopes, "urn:zitadel:iam:org:project:id:zitadel:aud")
		managementToken, tokenErr := identity.NewMachineTokenSource(identity.MachineTokenConfig{
			TokenURL: management.TokenURL, AssertionAudience: config.Authentication.ZITADEL.Issuer,
			Subject: management.Subject, KeyID: management.KeyID, PrivateKeyFile: managementKeyFile,
			Scopes: managementScopes, RefreshBefore: durationOr(management.TokenRefreshBefore, 30*time.Second),
		}, zitadelClient)
		if tokenErr != nil {
			return tokenErr
		}
		managementTokenFunc := func(ctx context.Context) (string, error) {
			return managementToken.Token(ctx)
		}
		serviceAccounts, managerErr := identity.NewZITADELServiceAccountManager(config.Authentication.ZITADEL.Issuer, config.Authentication.ZITADEL.ServiceAudience, managementTokenFunc, zitadelClient)
		if managerErr != nil {
			return managerErr
		}
		recheckValue := config.CreditCheck.RecheckInterval
		if recheckValue == "" {
			recheckValue = "5m"
		}
		recheckInterval, _ := time.ParseDuration(recheckValue)
		verifier := identity.NewVerifierWithRefreshAndClient(config.Authentication.ZITADEL.Issuer, config.Authentication.ZITADEL.JWKSURL, durationOr(config.Authentication.ZITADEL.JWKSRefreshInterval, 5*time.Minute), zitadelClient)
		business, err = payservice.New(payservice.Config{
			DB:                    database.SQL,
			Verifier:              verifier,
			HumanAudience:         config.Authentication.ZITADEL.HumanAudience,
			ServiceAudience:       config.Authentication.ZITADEL.ServiceAudience,
			ServiceAccounts:       serviceAccounts,
			HMACSecret:            hmacSecret,
			PlatformFeeBPS:        config.PAYGCharges.PlatformFeeBPS,
			RecheckInterval:       recheckInterval,
			MaxOrderMetadataBytes: config.PAYGCharges.MaxOrderMetadataBytes,
			MaxCommissions:        config.PAYGCharges.MaxCommissions,
		})
		if err != nil {
			return err
		}
	} else {
		hmacSecret, secretErr := readConfiguredSecret(config, config.SubscriptionKeys.HMAC.SecretFile)
		if secretErr != nil {
			return secretErr
		}
		var callbackSecret []byte
		if config.ProviderCallbacks.CallbackSecretFile != "" {
			callbackSecret, secretErr = readConfiguredSecret(config, config.ProviderCallbacks.CallbackSecretFile)
			if secretErr != nil {
				return secretErr
			}
		}
		var serviceToken func(context.Context) (string, error)
		if privateKeyFile := config.Authentication.ServiceAccount.PrivateKeyFile; privateKeyFile != "" {
			if !filepath.IsAbs(privateKeyFile) {
				privateKeyFile = filepath.Join(config.configDirectory, privateKeyFile)
			}
			tokenSource, tokenErr := identity.NewMachineTokenSource(identity.MachineTokenConfig{
				TokenURL:          config.Authentication.ServiceAccount.TokenURL,
				AssertionAudience: config.Authentication.ZITADEL.Issuer,
				Subject:           config.Authentication.ServiceAccount.Subject,
				KeyID:             config.Authentication.ServiceAccount.KeyID,
				PrivateKeyFile:    privateKeyFile,
				Audience:          config.Authentication.ServiceAccount.Audience,
				Scopes:            config.Authentication.ServiceAccount.RequestedScopes,
				RefreshBefore:     durationOr(config.Authentication.ServiceAccount.TokenRefreshBefore, 30*time.Second),
			}, zitadelClient)
			if tokenErr != nil {
				return tokenErr
			}
			serviceToken = func(ctx context.Context) (string, error) {
				token, tokenErr := tokenSource.Token(ctx)
				if tokenErr == nil {
					tokenErr = identity.RequireTokenRoles(token, config.Authentication.ServiceAccount.Audience, config.Authentication.ServiceAccount.RequiredRoles)
				}
				return token, tokenErr
			}
		}
		bifrostStores, storeErr := bifrostadapter.OpenStores(context.Background(),
			bifrostadapter.StoreConfig{Type: config.Bifrost.ConfigStore.Type, DSN: config.Bifrost.ConfigStore.DSN, Schema: config.Bifrost.ConfigStore.Schema},
			bifrostadapter.StoreConfig{
				Type: config.Bifrost.LogStore.Type, DSN: config.Bifrost.LogStore.DSN, Schema: config.Bifrost.LogStore.Schema,
				RetentionDays:       config.Bifrost.LogStore.RetentionDays,
				WriterMaxBatchSize:  config.Bifrost.LogStore.Writer.MaxBatchSize,
				WriterBatchInterval: config.Bifrost.LogStore.Writer.BatchInterval,
				WriterQueueCapacity: config.Bifrost.LogStore.Writer.WriteQueueCapacity,
			},
		)
		if storeErr != nil {
			return storeErr
		}
		defer bifrostStores.Close(context.Background())
		verifier := identity.NewVerifierWithRefreshAndClient(config.Authentication.ZITADEL.Issuer, config.Authentication.ZITADEL.JWKSURL, durationOr(config.Authentication.ZITADEL.JWKSRefreshInterval, 5*time.Minute), zitadelClient)
		business, err = wayservice.New(wayservice.Config{
			DB:                         database.SQL,
			DatabaseSchema:             config.Database.Schema,
			Verifier:                   verifier,
			HumanAudience:              config.Authentication.ZITADEL.HumanAudience,
			HMACSecret:                 hmacSecret,
			GizPayURL:                  config.GizPay.ServiceDSN,
			ServiceToken:               serviceToken,
			Now:                        now,
			HTTPClient:                 &http.Client{Timeout: durationOr(config.GizPay.RequestTimeout, 30*time.Second)},
			BifrostStores:              bifrostStores,
			Logger:                     logger,
			ProviderCallbackBaseURL:    config.ProviderCallbacks.PublicBaseURL,
			ProviderCallbackSecret:     callbackSecret,
			OutboxBatchSize:            positiveOr(config.ChargeOutbox.MaxBatchSize, 20),
			OutboxRetryInterval:        durationOr(config.ChargeOutbox.RetryInterval, 250*time.Millisecond),
			OutboxAbandonAfter:         durationOr(config.ChargeOutbox.AbandonAfter, 24*time.Hour),
			CreditCacheCleanupInterval: configuredCreditCacheCleanupInterval(config),
			BifrostMaxRetries:          config.Bifrost.Execution.MaxRetries,
			BifrostRequestTimeout:      durationOr(config.Bifrost.Execution.RequestTimeout, 10*time.Second),
		})
		if err != nil {
			return err
		}
	}
	if closer, ok := business.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	apiServer := api.NewMilestone03(surface, config.Server.Name, business, advance)
	listener, err := net.Listen("tcp", config.Server.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	server := &http.Server{Handler: apiServer.Handler(), ReadHeaderTimeout: 10 * time.Second, ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError)}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() {
		var err error
		if tlsEnabled(config) {
			certificateFile := configuredPath(config, config.TLS.CertificateFile)
			privateKeyFile := configuredPath(config, config.TLS.PrivateKeyFile)
			err = server.ServeTLS(listener, certificateFile, privateKeyFile)
		} else {
			err = server.Serve(listener)
		}
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), durationOr(config.Server.ShutdownTimeout, 10*time.Second))
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func processLogger(level, format string) *slog.Logger {
	return slog.New(processLogHandler(level, format, os.Stderr))
}

func processLogHandler(level, format string, writer io.Writer) slog.Handler {
	var configured slog.Level
	switch level {
	case "debug":
		configured = slog.LevelDebug
	case "warn":
		configured = slog.LevelWarn
	case "error":
		configured = slog.LevelError
	default:
		configured = slog.LevelInfo
	}
	options := &slog.HandlerOptions{Level: configured}
	if format == "text" {
		return slog.NewTextHandler(writer, options)
	}
	return slog.NewJSONHandler(writer, options)
}

func durationOr(value string, fallback time.Duration) time.Duration {
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}

func configuredCreditCacheCleanupInterval(config ProcessConfig) time.Duration {
	return durationOr(config.CreditCache.CleanupInterval, time.Minute)
}

func positiveOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func tlsEnabled(config ProcessConfig) bool {
	if config.TLS.Enabled != nil {
		return *config.TLS.Enabled
	}
	return config.TLS.CertificateFile != "" && config.TLS.PrivateKeyFile != ""
}

func configuredPath(config ProcessConfig, path string) string {
	if path != "" && !filepath.IsAbs(path) {
		return filepath.Join(config.configDirectory, path)
	}
	return path
}

type storyClock struct {
	mu  sync.RWMutex
	now time.Time
}

func newStoryClock(now time.Time) *storyClock { return &storyClock{now: now} }

func (c *storyClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *storyClock) Advance(duration time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
	return c.now
}

func readConfiguredSecret(config ProcessConfig, path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(config.configDirectory, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(string(raw))), nil
}

func withSearchPath(dsn, schema string) (string, error) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			return "", err
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String(), nil
	}
	return dsn + " search_path=" + schema, nil
}

func ensureDatabaseSchema(dsn, schema string) error {
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(schema) {
		return errors.New("database.schema must be a lowercase SQL identifier")
	}
	database, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Ping(); err != nil {
		return err
	}
	_, err = database.Exec(`CREATE SCHEMA IF NOT EXISTS ` + schema)
	return err
}
