package app

import (
	"errors"
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

// InitConfig is deliberately separate from ProcessConfig. A serve process
// cannot decode, retain, or print the one-shot database management credentials.
type InitConfig struct {
	Version  int         `yaml:"version"`
	Process  ProcessKind `yaml:"process"`
	Region   string      `yaml:"region,omitempty"`
	Database struct {
		AdminDSN   string `yaml:"admin_dsn"`
		ServiceDSN string `yaml:"service_dsn"`
		Schema     string `yaml:"schema"`
	} `yaml:"database"`
	PowerSync struct {
		Publication     string   `yaml:"publication"`
		SourceSchemas   []string `yaml:"source_schemas"`
		SourceDSN       string   `yaml:"source_dsn"`
		StorageAdminDSN string   `yaml:"storage_admin_dsn"`
		StorageDSN      string   `yaml:"storage_dsn"`
	} `yaml:"powersync"`
}

// LoadInitConfig reads only the inputs needed by a one-shot init job.
func LoadInitConfig(path string, kind ProcessKind) (InitConfig, error) {
	if path == "" {
		return InitConfig{}, errors.New("--config is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return InitConfig{}, fmt.Errorf("open init config: %w", err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	config := InitConfig{}
	if err := decoder.Decode(&config); err != nil {
		return InitConfig{}, fmt.Errorf("decode init config (unknown fields are forbidden): %w", err)
	}
	if config.Version != 1 {
		return InitConfig{}, errors.New("config version must be 1")
	}
	if config.Process != kind {
		return InitConfig{}, fmt.Errorf("process must be %q", kind)
	}
	if kind == ProcessGizPay && config.Region != "" {
		return InitConfig{}, errors.New("region must be empty for gizpay")
	}
	if kind == ProcessGizWay && config.Region != "global" && config.Region != "cn" {
		return InitConfig{}, errors.New("region must be global or cn for gizway")
	}
	if config.Database.AdminDSN == "" || config.Database.ServiceDSN == "" {
		return InitConfig{}, errors.New("database.admin_dsn and database.service_dsn are required")
	}
	if !databaseSchemaPattern.MatchString(config.Database.Schema) {
		return InitConfig{}, errors.New("database.schema must be a lowercase SQL identifier")
	}
	if !databaseSchemaPattern.MatchString(config.PowerSync.Publication) {
		return InitConfig{}, errors.New("powersync.publication must be a lowercase SQL identifier")
	}
	if len(config.PowerSync.SourceSchemas) == 0 {
		return InitConfig{}, errors.New("powersync.source_schemas is required")
	}
	foundServiceSchema := false
	seenSchemas := map[string]bool{}
	for _, schema := range config.PowerSync.SourceSchemas {
		if !databaseSchemaPattern.MatchString(schema) || seenSchemas[schema] {
			return InitConfig{}, errors.New("powersync.source_schemas must contain unique lowercase SQL identifiers")
		}
		seenSchemas[schema] = true
		foundServiceSchema = foundServiceSchema || schema == config.Database.Schema
	}
	if !foundServiceSchema {
		return InitConfig{}, errors.New("powersync.source_schemas must include database.schema")
	}
	if config.PowerSync.SourceDSN == "" || config.PowerSync.StorageAdminDSN == "" || config.PowerSync.StorageDSN == "" {
		return InitConfig{}, errors.New("powersync.source_dsn, powersync.storage_admin_dsn and powersync.storage_dsn are required")
	}
	for label, dsn := range map[string]string{
		"database.admin_dsn":          config.Database.AdminDSN,
		"database.service_dsn":        config.Database.ServiceDSN,
		"powersync.source_dsn":        config.PowerSync.SourceDSN,
		"powersync.storage_admin_dsn": config.PowerSync.StorageAdminDSN,
		"powersync.storage_dsn":       config.PowerSync.StorageDSN,
	} {
		if _, err := parseRoleDSN(dsn); err != nil {
			return InitConfig{}, fmt.Errorf("%s: %w", label, err)
		}
	}
	databaseAdmin, _ := parseRoleDSN(config.Database.AdminDSN)
	databaseService, _ := parseRoleDSN(config.Database.ServiceDSN)
	source, _ := parseRoleDSN(config.PowerSync.SourceDSN)
	if databaseAdmin.Database != databaseService.Database || databaseAdmin.Database != source.Database {
		return InitConfig{}, errors.New("database admin, service and PowerSync source DSNs must name the same source database")
	}
	return config, nil
}
