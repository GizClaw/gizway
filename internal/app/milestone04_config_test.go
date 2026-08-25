package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProcessConfigAcceptsMilestone04SiteAndCatalogIdentity(t *testing.T) {
	directory := t.TempDir()
	secret := filepath.Join(directory, "secret")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "gizway.yaml")
	contents := validM04GizWayYAML(secret)
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadProcessConfig(configPath, ProcessGizWay)
	if err != nil {
		t.Fatal(err)
	}
	publisher, ok := any(config).(interface{ MarshalPublicRuntimeConfig() ([]byte, error) })
	if !ok {
		t.Fatal("ProcessConfig does not expose the M04 public runtime configuration")
	}
	encoded, err := publisher.MarshalPublicRuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	public := string(encoded)
	for _, required := range []string{"global.example.test", "gizway-human", "/auth/catalog-token", "https://sync.pay.example.test", "https://sync.global.example.test"} {
		if !strings.Contains(public, required) {
			t.Errorf("public runtime config lacks %q: %s", required, public)
		}
	}
	if strings.Contains(public, "catalog-secret") || strings.Contains(public, "public_catalog_service_account") {
		t.Fatalf("public runtime config leaked Service Account configuration: %s", public)
	}
	for _, websiteField := range []string{"client_id", "redirect_uri", "post_logout_redirect_uri"} {
		if strings.Contains(public, websiteField) {
			t.Fatalf("public runtime config retained website field %q: %s", websiteField, public)
		}
	}
	if strings.Contains(public, secret) || strings.Contains(public, "admin") {
		t.Fatalf("public runtime config leaked Admin configuration: %s", public)
	}
}

func TestAdminKeyConfigIsRequiredAndMustReferenceNonEmptyFile(t *testing.T) {
	directory := t.TempDir()
	secret := filepath.Join(directory, "secret")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := validM04GizWayYAML(secret)
	missing := strings.Replace(base, "admin:\n  initial_key_file: "+secret+"\n", "", 1)
	missingPath := filepath.Join(directory, "missing.yaml")
	if err := os.WriteFile(missingPath, []byte(missing), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProcessConfig(missingPath, ProcessGizWay); err == nil || !strings.Contains(err.Error(), "admin.initial_key_file is required") {
		t.Fatalf("missing Admin Key error = %v", err)
	}
	empty := filepath.Join(directory, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	emptyPath := filepath.Join(directory, "empty.yaml")
	if err := os.WriteFile(emptyPath, []byte(strings.Replace(base, "initial_key_file: "+secret, "initial_key_file: "+empty, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProcessConfig(emptyPath, ProcessGizWay); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("empty Admin Key error = %v", err)
	}
}

func TestMilestone04CatalogIdentityConfigValidation(t *testing.T) {
	directory := t.TempDir()
	secret := filepath.Join(directory, "secret")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := validM04GizWayYAML(secret)
	for name, contents := range map[string]string{
		"opaque token": strings.Replace(base, "access_token_type: JWT", "access_token_type: opaque", 1),
		"zero TTL":     strings.Replace(base, "token_ttl: 12h", "token_ttl: 0s", 1),
		"overlong TTL": strings.Replace(base, "token_ttl: 12h", "token_ttl: 25h", 1),
		"late refresh": strings.Replace(base, "refresh_before: 1h", "refresh_before: 13h", 1),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(directory, strings.ReplaceAll(name, " ", "-")+".yaml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadProcessConfig(path, ProcessGizWay); err == nil {
				t.Fatal("invalid Public Catalog identity configuration was accepted")
			}
		})
	}
}

func validM04GizWayYAML(secret string) string {
	return `version: 1
server:
  name: global.example.test
  listen_address: 127.0.0.1:0
admin:
  initial_key_file: ` + secret + `
site:
  hostname: global.example.test
identity:
  issuer: https://identity.example.test
  public_catalog_service_account:
    client_id: gizway-global-catalog
    client_secret: catalog-secret
    access_token_type: JWT
    scope: "openid roles audience"
    token_ttl: 12h
    refresh_before: 1h
services:
  public_catalog_token_url: https://global.example.test/auth/catalog-token
  gizpay_powersync_url: https://sync.pay.example.test
  gizpay_api_url: https://pay.example.test
  gizway_powersync_url: https://sync.global.example.test
  gizway_api_url: https://global.example.test
database:
  dsn: postgres://localhost/db
  schema: gizway
authentication:
  zitadel:
    issuer: https://identity.example.test
    jwks_url: https://identity.example.test/oauth/v2/keys
    human_audience: gizway-human
    service_audience: gizway-service
  service_account:
    token_url: https://identity.example.test/oauth/v2/token
    private_key_file: ` + secret + `
    audience: gizpay-service
    requested_scopes: [openid]
    required_roles: [credit_check]
subscription_keys:
  hmac:
    secret_file: ` + secret + `
gizpay:
  service_dsn: https://pay.example.test
bifrost:
  config_store:
    type: postgresql
    dsn: postgres://localhost/db
    schema: bifrost_config
  log_store:
    type: postgresql
    dsn: postgres://localhost/db
    schema: bifrost_logs
`
}
