package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "github.com/idy/gizway/internal/app"
)

// These black-box tests exercise the real composition roots. --check-config
// loads and validates Secret files without opening sockets or contacting a
// dependency; --print-effective-config=json is a redacted diagnostic used to
// prove Flag precedence and the absence of environment overlays.
func TestMilestone02CommandsStrictYAMLFlagOverlayAndNoApplicationEnvironment(t *testing.T) {
	for _, command := range []string{"gizpay", "gizway"} {
		t.Run(command, func(t *testing.T) {
			config := writeMilestone02ProcessConfig(t, command, false)
			output, err := runMilestone02Command(t, command, []string{
				"--config", config,
				"--server-name", "flag-override.example.test",
				"--check-config",
				"--print-effective-config=json",
			},
				"GIZPAY_SERVER_NAME=forbidden-env.example.test",
				"GIZWAY_SERVER_NAME=forbidden-env.example.test",
				"GIZPAY_POSTGRES_DSN=postgres://forbidden-env/gizpay",
				"GIZWAY_POSTGRES_DSN=postgres://forbidden-env/gizway",
				"GIZPAY_INTERNAL_BASE_URL=https://forbidden-env.example.test",
				"GIZWAY_NODE_ID=forbidden-env-node",
				"GIZWAY_REGION=forbidden-env-region",
			)
			if err != nil {
				t.Fatalf("valid YAML plus explicit Flag failed: %v\n%s", err, output)
			}
			var effective struct {
				Server struct {
					Name string `json:"name"`
				} `json:"server"`
				Database struct {
					DSN string `json:"dsn"`
				} `json:"database"`
				GizPay struct {
					ServiceDSN string `json:"service_dsn"`
				} `json:"gizpay"`
			}
			if err := json.Unmarshal(output, &effective); err != nil {
				t.Fatalf("effective config is not JSON: %v\n%s", err, output)
			}
			if effective.Server.Name != "flag-override.example.test" {
				t.Errorf("explicit Flag did not override YAML, or an environment value leaked: server.name=%q", effective.Server.Name)
			}
			if effective.Database.DSN != "postgres://unused:REDACTED@127.0.0.1:1/unused?sslmode=disable" {
				t.Errorf("application environment overrode YAML database.dsn: %q", effective.Database.DSN)
			}
			if command == "gizway" && effective.GizPay.ServiceDSN != "https://credit.example.test" {
				t.Errorf("application environment overrode YAML gizpay.service_dsn: %q", effective.GizPay.ServiceDSN)
			}
			if strings.Contains(string(output), "fixture-hmac-secret") {
				t.Fatal("redacted effective config exposed Secret file contents")
			}
			if strings.Contains(string(output), "unused:unused@") {
				t.Fatal("redacted effective config exposed database credentials")
			}
		})
	}
}

func TestMilestone02CommandsUseYAMLDatabaseInitializeUnlessFlagExplicitlyOverrides(t *testing.T) {
	for _, command := range []string{"gizpay", "gizway"} {
		t.Run(command, func(t *testing.T) {
			configPath := writeMilestone02ProcessConfig(t, command, false)
			raw, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			updated := strings.Replace(string(raw), "  schema: public\n", "  schema: public\n  initialize: true\n", 1)
			if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
				t.Fatal(err)
			}

			assertInitialize := func(args []string, want bool) {
				t.Helper()
				output, runErr := runMilestone02Command(t, command, append([]string{
					"--config", configPath, "--check-config", "--print-effective-config=json",
				}, args...))
				if runErr != nil {
					t.Fatalf("effective config failed: %v\n%s", runErr, output)
				}
				var effective struct {
					Database struct {
						Initialize bool `json:"initialize"`
					} `json:"database"`
				}
				if err := json.Unmarshal(output, &effective); err != nil {
					t.Fatalf("decode effective config: %v\n%s", err, output)
				}
				if effective.Database.Initialize != want {
					t.Fatalf("database.initialize = %t, want %t", effective.Database.Initialize, want)
				}
			}

			assertInitialize(nil, true)
			assertInitialize([]string{"--initialize=false"}, false)
			assertInitialize([]string{"--initialize=true"}, true)
		})
	}
}

func TestMilestone02CommandsRejectUnknownYAMLFields(t *testing.T) {
	for _, command := range []string{"gizpay", "gizway"} {
		config := writeMilestone02ProcessConfig(t, command, true)
		output, err := runMilestone02Command(t, command, []string{"--config", config, "--check-config"})
		if err == nil {
			t.Fatalf("cmd/%s accepted an unknown YAML field", command)
		}
		if !strings.Contains(strings.ToLower(string(output)), "unknown") {
			t.Errorf("cmd/%s did not identify the unknown field: %s", command, output)
		}
	}
}

func TestMilestone02CommandsRequireReadableSecretFiles(t *testing.T) {
	for _, command := range []string{"gizpay", "gizway"} {
		config := writeMilestone02ProcessConfig(t, command, false)
		if err := os.Remove(filepath.Join(filepath.Dir(config), "subscription-hmac")); err != nil {
			t.Fatal(err)
		}
		output, err := runMilestone02Command(t, command, []string{"--config", config, "--check-config"})
		if err == nil {
			t.Fatalf("cmd/%s started with a missing Secret file", command)
		}
		if strings.Contains(string(output), "fixture-hmac-secret") {
			t.Fatalf("cmd/%s logged Secret contents on configuration failure", command)
		}
	}
}

func TestMilestone02GizPayEncryptionActiveVersionSelectsConfiguredKey(t *testing.T) {
	configPath := writeMilestone02ProcessConfig(t, "gizpay", false)
	directory := filepath.Dir(configPath)
	secondKey := filepath.Join(directory, "subscription-encryption-v2")
	if err := os.WriteFile(secondKey, []byte("fixture-encryption-key-v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(raw), "active_version: 1", "active_version: 2", 1)
	updated = strings.Replace(updated, "      - version: 1\n        secret_file: ", "      - version: 1\n        secret_file: ", 1)
	updated = strings.Replace(updated, "payg_charges:", fmt.Sprintf("      - version: 2\n        secret_file: %s\npayg_charges:", secondKey), 1)
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := appconfig.LoadProcessConfig(configPath, appconfig.ProcessGizPay)
	if err != nil {
		t.Fatal(err)
	}
	if config.SubscriptionAPIKeys.Encryption.ActiveVersion != 2 || len(config.SubscriptionAPIKeys.Encryption.Keys) != 2 {
		t.Fatalf("versioned encryption config = %+v", config.SubscriptionAPIKeys.Encryption)
	}
}

func TestMilestone02TLSRequiresCertificatePair(t *testing.T) {
	config := appconfig.ProcessConfig{Version: 1}
	config.Server.Name = "pay.example.test"
	config.Server.ListenAddress = "127.0.0.1:0"
	config.Database.DSN = "postgres://example/test"
	config.Database.Schema = "public"
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	config.SubscriptionAPIKeys.HMAC.SecretFile = secret
	config.Authentication.ZITADEL.Issuer = "https://identity.example.test"
	config.Authentication.ZITADEL.JWKSURL = "https://identity.example.test/oauth/v2/keys"
	config.Authentication.ZITADEL.AdminAudience = "admin-project"
	config.Authentication.ServiceAccount.TokenURL = "https://identity.example.test/oauth/v2/token"
	config.Authentication.ServiceAccount.PrivateKeyFile = secret
	config.Authentication.ServiceAccount.Audience = "service-project"
	config.Authentication.ServiceAccount.RequestedScopes = []string{"openid"}
	config.Authentication.ServiceAccount.RequiredRoles = []string{"subscription_credit_reader"}
	config.Bifrost.ConfigStore = appconfig.StoreConfig{Type: "postgresql", DSN: "postgres://example/test", Schema: "bifrost_config"}
	config.Bifrost.LogStore = appconfig.StoreConfig{Type: "postgresql", DSN: "postgres://example/test", Schema: "bifrost_logs"}
	config.TLS.CertificateFile = secret
	config.GizPay.ServiceDSN = "https://pay.example.test"
	if err := appconfig.ValidateProcessConfig(config, appconfig.ProcessGizWay); err == nil || !strings.Contains(err.Error(), "private_key_file") {
		t.Fatalf("ValidateProcessConfig certificate-only error = %v", err)
	}
}

func TestMilestone02ServerNameRejectsURLPortPathAndInvalidLabels(t *testing.T) {
	for _, name := range []string{"https://global.example.test", "global.example.test:8080", "global.example.test/path", "global_example.test", "-global.example.test", "localhost"} {
		t.Run(name, func(t *testing.T) {
			config := appconfig.ProcessConfig{Version: 1}
			config.Server.Name = name
			if err := appconfig.ValidateProcessConfig(config, appconfig.ProcessGizWay); err == nil || !strings.Contains(err.Error(), "server.name") {
				t.Fatalf("invalid server.name %q error = %v", name, err)
			}
		})
	}
}

func writeMilestone02ProcessConfig(t *testing.T, command string, unknown bool) string {
	t.Helper()
	directory := t.TempDir()
	secret := filepath.Join(directory, "subscription-hmac")
	if err := os.WriteFile(secret, []byte("fixture-hmac-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	managementKey := filepath.Join(directory, "management-key.json")
	if err := os.WriteFile(managementKey, []byte("fixture-management-private-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	encryptionKey := filepath.Join(directory, "subscription-encryption")
	if err := os.WriteFile(encryptionKey, []byte("fixture-encryption-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	unknownLine := ""
	if unknown {
		unknownLine = "  legacy_node_id: forbidden\n"
	}
	common := fmt.Sprintf(`version: 1
server:
  name: yaml-value.example.test
  listen_address: 127.0.0.1:0
%sdatabase:
  dsn: postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable
  schema: public
`, unknownLine)
	if command == "gizpay" {
		common += fmt.Sprintf(`authentication:
  zitadel:
    issuer: https://identity.example.test
    jwks_url: https://identity.example.test/oauth/v2/keys
    human_audience: human-project
    service_audience: service-project
    management_client:
      token_url: https://identity.example.test/oauth/v2/token
      subject: management-client
      key_id: management-key-v1
      private_key_file: %s
subscription_api_keys:
  hmac:
    secret_file: %s
  encryption:
    active_version: 1
    keys:
      - version: 1
        secret_file: %s
payg_charges:
  platform_fee_bps: 200
`, managementKey, secret, encryptionKey)
	} else {
		common += fmt.Sprintf(`authentication:
  zitadel:
    issuer: https://identity.example.test
    jwks_url: https://identity.example.test/oauth/v2/keys
    admin_audience: regional-admin-project
  service_account:
    token_url: https://identity.example.test/oauth/v2/token
    subject: regional-service
    key_id: regional-service-v1
    private_key_file: %s
    audience: gizpay-service-project
    requested_scopes: [openid]
    required_roles: [subscription_credit_reader, subscription_charger]
subscription_api_keys:
  hmac:
    secret_file: %s
gizpay:
  service_dsn: https://credit.example.test
bifrost:
  config_store:
    type: postgresql
    dsn: postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable
    schema: bifrost_config
  log_store:
    type: postgresql
    dsn: postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable
    schema: bifrost_logs
`, managementKey, secret)
	}
	path := filepath.Join(directory, command+".yaml")
	if err := os.WriteFile(path, []byte(common), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runMilestone02Command(t *testing.T, command string, args []string, extraEnvironment ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	root := milestone02RepositoryRoot(t)
	commandArgs := append([]string{"run", "./cmd/" + command}, args...)
	process := exec.CommandContext(ctx, "go", commandArgs...)
	process.Dir = root
	process.Env = append(os.Environ(), extraEnvironment...)
	return process.CombinedOutput()
}
