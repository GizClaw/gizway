package openapicheck

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMilestone02HurlVariablesAreCapturedOrDeclaredFixtures(t *testing.T) {
	assertMilestone02HurlVariables(t, filepath.Join("..", "..", "tests", "api", "stories", "23-milestone-02"))
	assertMilestone02HurlVariables(t, filepath.Join("..", "..", "tests", "e2e", "hurl", "milestone-02"))
}

func assertMilestone02HurlVariables(t *testing.T, root string) {
	t.Helper()
	t.Run(filepath.Base(filepath.Dir(root))+"_"+filepath.Base(root), func(t *testing.T) {
		manifest, err := os.ReadFile(filepath.Join(root, "fixtures.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		fixtureLine := regexp.MustCompile(`^  ([a-z][a-z0-9_]*):`)
		fixtures := map[string]bool{}
		for line := range strings.SplitSeq(string(manifest), "\n") {
			if match := fixtureLine.FindStringSubmatch(line); match != nil {
				fixtures[match[1]] = true
			}
		}
		if len(fixtures) == 0 {
			t.Fatal("Milestone 02 fixture manifest has no variables")
		}

		reference := regexp.MustCompile(`\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}`)
		capture := regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*):`)
		files, err := filepath.Glob(filepath.Join(root, "*.hurl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			t.Run(filepath.Base(path), func(t *testing.T) {
				file, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				captured := map[string]bool{}
				inCaptures := false
				scanner := bufio.NewScanner(file)
				for lineNumber := 1; scanner.Scan(); lineNumber++ {
					line := strings.TrimSpace(scanner.Text())
					if strings.HasPrefix(line, "[") {
						inCaptures = line == "[Captures]"
						continue
					}
					if inCaptures {
						if match := capture.FindStringSubmatch(line); match != nil {
							captured[match[1]] = true
						}
					}
					for _, match := range reference.FindAllStringSubmatch(line, -1) {
						if !fixtures[match[1]] && !captured[match[1]] {
							t.Errorf("line %d uses %q before a capture and it is absent from fixtures.yaml", lineNumber, match[1])
						}
					}
				}
				if err := scanner.Err(); err != nil {
					t.Fatal(err)
				}
			})
		}
	})
}

// This temporary path matrix is derived directly from the Milestone 02 data
// contract. It keeps pre-OpenAPI TDD honest: every documented operation must
// already be a real Hurl request, not just a coverage comment. Once the new
// OpenAPI lands, CheckHurlCoverage enforces the same rule by operationId.
func TestMilestone02DocumentedOperationsHaveHurlRequests(t *testing.T) {
	root := filepath.Join("..", "..", "tests", "api", "stories", "23-milestone-02")
	expected := map[string][]string{
		"01-account-subscription-and-keys.hurl": {
			"GET {{pay_url}}/account/v1/accounts",
			"GET {{pay_url}}/account/v1/service-accounts",
			"POST {{pay_url}}/account/v1/service-accounts",
			"DELETE {{pay_url}}/account/v1/service-accounts/{{service_account_id}}",
			"GET {{pay_url}}/account/v1/merchants",
			"POST {{pay_url}}/account/v1/merchants",
			"GET {{pay_url}}/account/v1/merchants/{{merchant_id}}",
			"PATCH {{pay_url}}/account/v1/merchants/{{merchant_id}}",
			"GET {{pay_url}}/account/v1/merchants/{{merchant_id}}/products",
			"POST {{pay_url}}/account/v1/merchants/{{merchant_id}}/products",
			"GET {{pay_url}}/account/v1/products/{{product_id}}",
			"PATCH {{pay_url}}/account/v1/products/{{product_id}}",
			"POST {{pay_url}}/account/v1/products/{{product_id}}/subscriptions",
			"GET {{pay_url}}/account/v1/subscriptions",
			"GET {{pay_url}}/account/v1/subscriptions/{{subscription_id}}",
			"PATCH {{pay_url}}/account/v1/subscriptions/{{subscription_id}}",
			"GET {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/api-keys",
			"POST {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/api-keys",
			"GET {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/api-keys/{{api_key_id}}",
			"POST {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/api-keys/{{api_key_id}}/revoke",
		},
		"02-credit-check.hurl": {
			"POST {{pay_url}}/service/v1/subscription-credit-checks",
		},
		"03-charge-commission-and-ledger.hurl": {
			"POST {{pay_url}}/service/v1/payg-charges",
			"GET {{pay_url}}/service/v1/payg-charges/ord_m02_charge_0001",
			"GET {{pay_url}}/account/v1/accounts/{{payer_account_id}}/balance",
			"GET {{pay_url}}/account/v1/accounts/{{payer_account_id}}/transactions",
			"GET {{pay_url}}/account/v1/accounts/{{payer_account_id}}/charges",
		},
		"04-regional-admin-and-bifrost.hurl": {
			"GET {{way_url}}/admin/v1/models",
			"POST {{way_url}}/admin/v1/models",
			"GET {{way_url}}/admin/v1/models/{{model_id}}",
			"PATCH {{way_url}}/admin/v1/models/{{model_id}}",
			"GET {{way_url}}/admin/v1/models/{{model_id}}/prices",
			"PUT {{way_url}}/admin/v1/models/{{model_id}}/prices",
			"GET {{way_url}}/admin/v1/providers",
			"POST {{way_url}}/admin/v1/providers",
			"GET {{way_url}}/admin/v1/providers/{{provider_id}}",
			"PATCH {{way_url}}/admin/v1/providers/{{provider_id}}",
			"GET {{way_url}}/admin/v1/providers/{{provider_id}}/api-keys",
			"POST {{way_url}}/admin/v1/providers/{{provider_id}}/api-keys",
			"GET {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}",
			"PATCH {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}",
			"POST {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}/disable",
			"GET {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}/billing",
			"PUT {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}/billing",
			"GET {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}/prices",
			"PUT {{way_url}}/admin/v1/provider-api-keys/{{bifrost_key_id}}/prices",
		},
		"05-ai-protocols-and-orders.hurl": {
			"GET {{way_url}}/admin/v1/ai-orders",
			"GET {{way_url}}/admin/v1/charge-outbox",
			"GET {{way_url}}/admin/v1/bifrost-logs",
		},
	}

	for name, requests := range expected {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		contents := string(raw)
		for _, request := range requests {
			if !strings.Contains(contents, request) {
				t.Errorf("%s lacks documented request %s", name, request)
			}
		}
	}
}

func TestMilestone02IsTheOnlyAPIAndE2EBusinessStorySet(t *testing.T) {
	for _, root := range []string{
		filepath.Join("..", "..", "tests", "api", "stories"),
		filepath.Join("..", "..", "tests", "e2e", "hurl"),
	} {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || filepath.Ext(path) != ".hurl" {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			first, _, _ := strings.Cut(filepath.ToSlash(relative), "/")
			if first != "23-milestone-02" && first != "milestone-02" {
				t.Errorf("legacy business Hurl remains: %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
