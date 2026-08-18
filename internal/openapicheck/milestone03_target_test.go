package openapicheck

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMilestone03OpenAPIRootsRetainCurrentContracts(t *testing.T) {
	root := filepath.Join("..", "..", "api", "openapi")
	for _, name := range []string{"account.yaml", "gizpay-admin.yaml", "gizpay-webhooks.yaml", "gizway-admin.yaml", "internal-gizpay.yaml", "gizway-public.yaml", "gizway-user.yaml"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("Milestone 03 OpenAPI root %s: %v", name, err)
		}
	}

	for _, name := range []string{"account.yaml", "internal-gizpay.yaml", "gizway-public.yaml"} {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(string(raw))
		for _, legacy := range []string{"subscriptionapikey", "api_key_hmac", "api-keys", "bifrost_key_id"} {
			if strings.Contains(lower, strings.ToLower(legacy)) {
				t.Errorf("%s retains legacy identifier %q", name, legacy)
			}
		}
	}
}

func TestMilestone03DocumentedOperationsHaveHurlRequests(t *testing.T) {
	root := filepath.Join("..", "..", "tests", "api", "stories", "24-milestone-03")
	expected := map[string][]string{
		"01-initialize-and-account.hurl": {
			"GET {{pay_url}}/account/v1/accounts",
			"GET {{pay_url}}/account/v1/accounts/{{account_id}}/balance",
			"GET {{pay_url}}/account/v1/accounts/{{account_id}}/transactions",
			"GET {{pay_url}}/account/v1/accounts/{{account_id}}/charges",
		},
		"02-merchant-product-subscription.hurl": {
			"GET {{pay_url}}/account/v1/merchants",
			"POST {{pay_url}}/account/v1/merchants",
			"GET {{pay_url}}/account/v1/merchants/{{merchant_id}}",
			"PATCH {{pay_url}}/account/v1/merchants/{{merchant_id}}",
			"GET {{pay_url}}/account/v1/products",
			"GET {{pay_url}}/account/v1/merchants/{{merchant_id}}/products",
			"POST {{pay_url}}/account/v1/merchants/{{merchant_id}}/products",
			"GET {{pay_url}}/account/v1/products/{{product_id}}",
			"PATCH {{pay_url}}/account/v1/products/{{product_id}}",
			"POST {{pay_url}}/account/v1/products/{{product_id}}/subscriptions",
			"GET {{pay_url}}/account/v1/subscriptions",
			"GET {{pay_url}}/account/v1/subscriptions/{{subscription_id}}",
			"PATCH {{pay_url}}/account/v1/subscriptions/{{subscription_id}}",
		},
		"03-subscription-keys.hurl": {
			"GET {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/keys",
			"POST {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/keys",
			"GET {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/keys/{{subscription_key_id}}",
			"POST {{pay_url}}/account/v1/subscriptions/{{subscription_id}}/keys/{{subscription_key_id}}/revoke",
		},
		"04-topups-and-ledger.hurl": {
			"GET {{pay_url}}/account/v1/accounts/{{account_id}}/topups",
			"POST {{pay_url}}/account/v1/accounts/{{account_id}}/topups",
		},
		"05-service-accounts-and-charge.hurl": {
			"GET {{pay_url}}/account/v1/service-accounts",
			"POST {{pay_url}}/account/v1/service-accounts",
			"DELETE {{pay_url}}/account/v1/service-accounts/{{service_account_id}}",
			"POST {{pay_url}}/service/v1/subscription-credit-checks",
			"POST {{pay_url}}/service/v1/payg-charges",
			"GET {{pay_url}}/service/v1/payg-charges/{{external_order_id}}",
		},
		"06-provider-key-commands.hurl": {
			"POST {{way_url}}/user/v1/providers/{{provider_id}}/keys",
			"PUT {{way_url}}/user/v1/provider-keys/{{provider_key_id}}/prices",
			"POST {{way_url}}/user/v1/provider-keys/{{provider_key_id}}/disable",
		},
		"07-health.hurl": {
			"GET {{pay_url}}/healthz",
			"GET {{way_url}}/healthz",
		},
		"08-ai-protocols.hurl": {
			"GET {{way_url}}/v1/models",
			"POST {{way_url}}/v1/chat/completions",
			"POST {{way_url}}/v1/messages",
			"POST {{way_url}}/v1beta/models/{{gemini_operation}}",
			"POST {{way_url}}/v1/realtime/client_secrets",
			"GET {{way_url}}/v1/realtime",
		},
	}
	for name, requests := range expected {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for _, request := range requests {
			if !strings.Contains(string(raw), request) {
				t.Errorf("%s lacks documented request %s", name, request)
			}
		}
	}
}

func TestMilestone03HurlVariablesAreCapturedOrDeclaredFixtures(t *testing.T) {
	root := filepath.Join("..", "..", "tests", "api", "stories", "24-milestone-03")
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
	reference := regexp.MustCompile(`\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}`)
	capture := regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*):`)
	files, err := filepath.Glob(filepath.Join(root, "*.hurl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("Milestone 03 Hurl inventory is empty")
	}
	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
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
					t.Errorf("%s:%d uses %q before capture and outside fixtures", filepath.Base(path), lineNumber, match[1])
				}
			}
		}
		_ = file.Close()
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
	}
}
