package bifrost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	bfcore "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/framework/postgresconn"
	"gorm.io/gorm"
)

type StoreConfig struct {
	Type, DSN, Schema   string
	RetentionDays       int
	WriterMaxBatchSize  int
	WriterBatchInterval string
	WriterQueueCapacity int
}

type Stores struct {
	Config       configstore.ConfigStore
	Logs         logstore.LogStore
	configSchema string
	logType      string
	logSchema    string
}

type ProviderRecord struct {
	ID, Name, Kind, BaseURL, Status string
	CreatedAt                       time.Time
}

type KeyRecord struct {
	ID, ProviderID, Name, APIKey, Status string
	Weight                               int
	Enabled                              bool
}

func OpenStores(ctx context.Context, configSpec, logSpec StoreConfig) (*Stores, error) {
	configPG, err := postgresConfig(ctx, configSpec)
	if err != nil {
		return nil, fmt.Errorf("bifrost Config Store: %w", err)
	}
	configStore, err := configstore.NewConfigStore(ctx, &configstore.Config{
		Enabled: true, Type: configstore.ConfigStoreTypePostgres, Config: configPG,
	}, bfcore.NewNoOpLogger())
	if err != nil {
		return nil, fmt.Errorf("open Bifrost Config Store: %w", err)
	}
	stores := &Stores{Config: configStore, configSchema: configSpec.Schema, logType: logSpec.Type, logSchema: logSpec.Schema}
	writer := logWriterConfig(logSpec)
	closeOnError := func(err error) (*Stores, error) {
		_ = configStore.Close(ctx)
		return nil, err
	}
	switch logSpec.Type {
	case "postgresql":
		logPG, err := postgresConfig(ctx, logSpec)
		if err != nil {
			return closeOnError(fmt.Errorf("bifrost Log Store: %w", err))
		}
		stores.Logs, err = logstore.NewLogStore(ctx, &logstore.Config{
			Enabled: true, Type: logstore.LogStoreTypePostgres, RetentionDays: logSpec.RetentionDays,
			Config: &logstore.PostgresConfig{Config: *logPG}, Writer: writer,
		}, bfcore.NewNoOpLogger())
		if err != nil {
			return closeOnError(fmt.Errorf("open Bifrost Log Store: %w", err))
		}
	case "clickhouse":
		clickhouse, err := clickHouseConfig(logSpec)
		if err != nil {
			return closeOnError(fmt.Errorf("bifrost ClickHouse Log Store: %w", err))
		}
		stores.Logs, err = logstore.NewLogStore(ctx, &logstore.Config{
			Enabled: true, Type: logstore.LogStoreTypeClickHouse, RetentionDays: logSpec.RetentionDays, Config: clickhouse, Writer: writer,
		}, bfcore.NewNoOpLogger())
		if err != nil {
			return closeOnError(fmt.Errorf("open Bifrost ClickHouse Log Store: %w", err))
		}
	default:
		return closeOnError(fmt.Errorf("unsupported Log Store type %q", logSpec.Type))
	}
	return stores, nil
}

func logWriterConfig(spec StoreConfig) *logstore.WriterConfig {
	return &logstore.WriterConfig{
		MaxBatchSize:       spec.WriterMaxBatchSize,
		BatchInterval:      spec.WriterBatchInterval,
		WriteQueueCapacity: spec.WriterQueueCapacity,
	}
}

func (s *Stores) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	var errs []error
	if s.Logs != nil {
		errs = append(errs, s.Logs.Close(ctx))
	}
	if s.Config != nil {
		errs = append(errs, s.Config.Close(ctx))
	}
	return errors.Join(errs...)
}

func postgresConfig(ctx context.Context, spec StoreConfig) (*postgresconn.Config, error) {
	if spec.Type != "postgresql" {
		return nil, fmt.Errorf("type must be postgresql")
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(spec.Schema) {
		return nil, fmt.Errorf("invalid schema %q", spec.Schema)
	}
	parsed, err := url.Parse(spec.DSN)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return nil, fmt.Errorf("DSN must be a PostgreSQL URL")
	}
	if err := configurePostgresSearchPath(ctx, spec.DSN, spec.Schema); err != nil {
		return nil, err
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		host, portText = parsed.Hostname(), parsed.Port()
	}
	if portText == "" {
		portText = "5432"
	}
	password, _ := parsed.User.Password()
	sslMode := parsed.Query().Get("sslmode")
	if sslMode == "" {
		sslMode = "require"
	}
	return &postgresconn.Config{
		Host: schemas.NewSecretVar(host), Port: schemas.NewSecretVar(portText),
		User: schemas.NewSecretVar(parsed.User.Username()), Password: schemas.NewSecretVar(password),
		DBName: schemas.NewSecretVar(strings.TrimPrefix(parsed.Path, "/")), SSLMode: schemas.NewSecretVar(sslMode),
		MaxIdleConns: 2, MaxOpenConns: 10,
	}, nil
}

func configurePostgresSearchPath(ctx context.Context, dsn, schema string) error {
	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+quoteIdentifier(schema)); err != nil {
		return fmt.Errorf("create Bifrost schema: %w", err)
	}
	var role, database string
	if err := db.QueryRowxContext(ctx, `SELECT current_user,current_database()`).Scan(&role, &database); err != nil {
		return err
	}
	statement := `ALTER ROLE ` + quoteIdentifier(role) + ` IN DATABASE ` + quoteIdentifier(database) + ` SET search_path TO ` + quoteIdentifier(schema)
	if _, err := db.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("configure Bifrost role search_path: %w", err)
	}
	return nil
}

func quoteIdentifier(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }

func clickHouseConfig(spec StoreConfig) (*logstore.ClickHouseConfig, error) {
	parsed, err := url.Parse(spec.DSN)
	if err != nil {
		return nil, err
	}
	protocol, secure := "native", false
	switch parsed.Scheme {
	case "clickhouse":
	case "clickhouses":
		secure = true
	case "http":
		protocol = "http"
	case "https":
		protocol, secure = "http", true
	default:
		return nil, fmt.Errorf("unsupported ClickHouse DSN scheme %q", parsed.Scheme)
	}
	password, _ := parsed.User.Password()
	database := strings.TrimPrefix(parsed.Path, "/")
	if spec.Schema != "" {
		database = spec.Schema
	}
	port := parsed.Port()
	if port == "" {
		if protocol == "http" {
			port = map[bool]string{false: "8123", true: "8443"}[secure]
		} else {
			port = map[bool]string{false: "9000", true: "9440"}[secure]
		}
	}
	dialTimeout, _ := strconv.Atoi(parsed.Query().Get("dial_timeout_ms"))
	return &logstore.ClickHouseConfig{
		Host: schemas.NewSecretVar(parsed.Hostname()), Port: schemas.NewSecretVar(port),
		Database: schemas.NewSecretVar(database), Username: schemas.NewSecretVar(parsed.User.Username()), Password: schemas.NewSecretVar(password),
		Protocol: protocol, Secure: secure, DialTimeout: dialTimeout, Cluster: parsed.Query().Get("cluster"),
	}, nil
}

func providerConfig(record ProviderRecord) configstore.ProviderConfig {
	return configstore.ProviderConfig{
		NetworkConfig:        &schemas.NetworkConfig{BaseURL: record.BaseURL, AllowPrivateNetwork: true},
		CustomProviderConfig: &schemas.CustomProviderConfig{BaseProviderType: schemas.ModelProvider(record.Kind)},
		Status:               record.Status, Description: record.Name,
	}
}

func providerRecord(provider schemas.ModelProvider, config *configstore.ProviderConfig, created time.Time) ProviderRecord {
	record := ProviderRecord{ID: string(provider), Status: config.Status, Name: config.Description, CreatedAt: created}
	if config.NetworkConfig != nil {
		record.BaseURL = config.NetworkConfig.BaseURL
	}
	if config.CustomProviderConfig != nil {
		record.Kind = string(config.CustomProviderConfig.BaseProviderType)
	}
	return record
}

func (s *Stores) configTransaction(ctx context.Context, operation func(*gorm.DB) error) error {
	rdb, ok := s.Config.(*configstore.RDBConfigStore)
	if !ok {
		return errors.New("bifrost Config Store is not a relational store")
	}
	return rdb.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SET LOCAL search_path TO ` + quoteIdentifier(s.configSchema)).Error; err != nil {
			return err
		}
		return operation(tx)
	})
}

// ExecuteConfigTransaction lets a caller atomically update Bifrost Config
// Store tables and service-owned extension tables in the same PostgreSQL
// transaction.
func (s *Stores) ExecuteConfigTransaction(ctx context.Context, operation func(*gorm.DB) error) error {
	return s.configTransaction(ctx, operation)
}

// CreateKeyInTransaction persists a Provider Key in a caller-owned Config
// Store transaction. The caller is responsible for committing or rolling back.
func (s *Stores) CreateKeyInTransaction(ctx context.Context, record KeyRecord, tx *gorm.DB) error {
	if tx == nil {
		return errors.New("bifrost config store transaction is required")
	}
	return s.Config.CreateProviderKey(ctx, schemas.ModelProvider(record.ProviderID), schemaKey(record), tx)
}

func (s *Stores) UpdateKeyInTransaction(ctx context.Context, record KeyRecord, tx *gorm.DB) error {
	if tx == nil {
		return errors.New("bifrost config store transaction is required")
	}
	return s.Config.UpdateProviderKey(ctx, schemas.ModelProvider(record.ProviderID), record.ID, schemaKey(record), tx)
}

func (s *Stores) CreateProvider(ctx context.Context, record ProviderRecord) error {
	return s.configTransaction(ctx, func(tx *gorm.DB) error {
		if err := s.Config.AddProvider(ctx, schemas.ModelProvider(record.ID), providerConfig(record), tx); err != nil {
			return err
		}
		// Bifrost's single-provider AddProvider API does not currently persist
		// ProviderConfig.Status or Description. Keep those official table fields
		// in the same transaction so the regional active-state contract is not
		// lost between create and the first lookup.
		return tx.Model(&tables.TableProvider{}).Where("name = ?", record.ID).
			Updates(map[string]any{"status": record.Status, "description": record.Name}).Error
	})
}

func (s *Stores) UpdateProvider(ctx context.Context, record ProviderRecord) error {
	return s.configTransaction(ctx, func(tx *gorm.DB) error {
		if err := s.Config.UpdateProvider(ctx, schemas.ModelProvider(record.ID), providerConfig(record), tx); err != nil {
			return err
		}
		return tx.Model(&tables.TableProvider{}).Where("name = ?", record.ID).
			Updates(map[string]any{"status": record.Status, "description": record.Name}).Error
	})
}

func (s *Stores) Provider(ctx context.Context, id string) (ProviderRecord, error) {
	var table tables.TableProvider
	err := s.configTransaction(ctx, func(tx *gorm.DB) error { return tx.Preload("Keys").Where("name = ?", id).First(&table).Error })
	if err != nil {
		return ProviderRecord{}, err
	}
	config := configstore.ProviderConfig{NetworkConfig: table.NetworkConfig, CustomProviderConfig: table.CustomProviderConfig, Status: table.Status, Description: table.Description}
	return providerRecord(schemas.ModelProvider(id), &config, table.CreatedAt), nil
}

func (s *Stores) Providers(ctx context.Context) ([]ProviderRecord, error) {
	var providerTables []tables.TableProvider
	if err := s.configTransaction(ctx, func(tx *gorm.DB) error { return tx.Order("created_at,name").Find(&providerTables).Error }); err != nil {
		return nil, err
	}
	result := make([]ProviderRecord, 0, len(providerTables))
	for _, table := range providerTables {
		config := configstore.ProviderConfig{NetworkConfig: table.NetworkConfig, CustomProviderConfig: table.CustomProviderConfig, Status: table.Status, Description: table.Description}
		result = append(result, providerRecord(schemas.ModelProvider(table.Name), &config, table.CreatedAt))
	}
	return result, nil
}

func schemaKey(record KeyRecord) schemas.Key {
	enabled := record.Enabled
	return schemas.Key{
		ID: record.ID, Name: record.Name, Value: *schemas.NewSecretVar(record.APIKey),
		Models: schemas.WhiteList{"*"}, Weight: float64(record.Weight), Enabled: &enabled,
		Status: schemas.KeyStatusType(record.Status),
	}
}

func keyRecord(providerID string, key schemas.Key) KeyRecord {
	enabled := key.Enabled != nil && *key.Enabled
	return KeyRecord{ID: key.ID, ProviderID: providerID, Name: key.Name, APIKey: key.Value.GetValue(), Weight: int(key.Weight), Enabled: enabled, Status: string(key.Status)}
}

func (s *Stores) CreateKey(ctx context.Context, record KeyRecord) error {
	return s.configTransaction(ctx, func(tx *gorm.DB) error {
		return s.Config.CreateProviderKey(ctx, schemas.ModelProvider(record.ProviderID), schemaKey(record), tx)
	})
}

func (s *Stores) UpdateKey(ctx context.Context, record KeyRecord) error {
	return s.configTransaction(ctx, func(tx *gorm.DB) error {
		return s.Config.UpdateProviderKey(ctx, schemas.ModelProvider(record.ProviderID), record.ID, schemaKey(record), tx)
	})
}

func (s *Stores) Key(ctx context.Context, providerID, keyID string) (KeyRecord, error) {
	var table tables.TableKey
	err := s.configTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("provider = ? AND key_id = ?", providerID, keyID).First(&table).Error
	})
	if err != nil {
		return KeyRecord{}, err
	}
	key := schemas.Key{ID: table.KeyID, Name: table.Name, Value: table.Value, Weight: valueOr(table.Weight, 1), Enabled: table.Enabled, Status: schemas.KeyStatusType(table.Status)}
	return keyRecord(providerID, key), nil
}

func (s *Stores) Keys(ctx context.Context, providerID string) ([]KeyRecord, error) {
	var keyTables []tables.TableKey
	if err := s.configTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("provider = ?", providerID).Order("created_at,key_id").Find(&keyTables).Error
	}); err != nil {
		return nil, err
	}
	result := make([]KeyRecord, len(keyTables))
	for index, table := range keyTables {
		key := schemas.Key{ID: table.KeyID, Name: table.Name, Value: table.Value, Weight: valueOr(table.Weight, 1), Enabled: table.Enabled, Status: schemas.KeyStatusType(table.Status)}
		result[index] = keyRecord(providerID, key)
	}
	return result, nil
}

func (s *Stores) FindKey(ctx context.Context, keyID string) (KeyRecord, error) {
	var table tables.TableKey
	err := s.configTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("key_id = ?", keyID).First(&table).Error
	})
	if err != nil {
		return KeyRecord{}, err
	}
	key := schemas.Key{ID: table.KeyID, Name: table.Name, Value: table.Value, Weight: valueOr(table.Weight, 1), Enabled: table.Enabled, Status: schemas.KeyStatusType(table.Status)}
	return keyRecord(table.Provider, key), nil
}

func (s *Stores) WriteLog(ctx context.Context, record map[string]any) error {
	metadata, _ := json.Marshal(record)
	selectedKeyID, _ := record["selected_key_id"].(string)
	modelID, _ := record["model_id"].(string)
	providerID, _ := record["provider_id"].(string)
	status, _ := record["status"].(string)
	if status == "" {
		status = "success"
	}
	executionMode, _ := record["execution_mode"].(string)
	createdAt, _ := record["created_at"].(time.Time)
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	entry := &logstore.Log{
		ID: fmt.Sprint(record["id"]), Timestamp: createdAt, CreatedAt: createdAt,
		Object: "chat.completion", Provider: providerID, Model: modelID,
		SelectedKeyID: selectedKeyID, SelectedKeyName: selectedKeyID, Status: status, Metadata: new(string(metadata)),
	}
	entry.Stream = executionMode == "streaming" || executionMode == "realtime"
	if executionMode == "realtime" {
		entry.Object = "realtime"
	}
	if latency, ok := record["latency_ms"].(float64); ok {
		entry.Latency = &latency
	}
	if usage, ok := record["usage"].(*schemas.BifrostLLMUsage); ok {
		entry.TokenUsageParsed = usage
	}
	if message, ok := record["error"].(string); ok && message != "" {
		entry.ErrorDetailsParsed = &schemas.BifrostError{IsBifrostError: true, Error: &schemas.ErrorField{Message: message}}
	}
	if s.logType == "postgresql" {
		rdb, ok := s.Logs.(*logstore.RDBLogStore)
		if !ok {
			return errors.New("bifrost PostgreSQL Log Store is not relational")
		}
		return rdb.ScopedDB(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(`SET LOCAL search_path TO ` + quoteIdentifier(s.logSchema)).Error; err != nil {
				return err
			}
			return tx.Create(entry).Error
		})
	}
	return s.Logs.Create(ctx, entry)
}

func (s *Stores) LogsList(ctx context.Context) ([]map[string]any, error) {
	var logs []*logstore.Log
	var err error
	if s.logType == "postgresql" {
		rdb, ok := s.Logs.(*logstore.RDBLogStore)
		if !ok {
			return nil, errors.New("bifrost PostgreSQL Log Store is not relational")
		}
		err = rdb.ScopedDB(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(`SET LOCAL search_path TO ` + quoteIdentifier(s.logSchema)).Error; err != nil {
				return err
			}
			return tx.Order("created_at,id").Find(&logs).Error
		})
	} else {
		logs, err = s.Logs.FindAll(ctx, map[string]any{})
	}
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(logs))
	for _, entry := range logs {
		row := map[string]any{"id": entry.ID, "selected_key_id": entry.SelectedKeyID, "created_at": entry.CreatedAt}
		if entry.Metadata != nil {
			_ = json.Unmarshal([]byte(*entry.Metadata), &row)
		}
		result = append(result, row)
	}
	return result, nil
}

func valueOr[T any](value *T, fallback T) T {
	if value == nil {
		return fallback
	}
	return *value
}
