package openapicheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMilestone04OpenAPIReplacesInitializeWithWebhook(t *testing.T) {
	root := filepath.Join("..", "..", "api", "openapi")
	account := readM04OpenAPI(t, root, "account.yaml")
	if strings.Contains(account, "/initialize:") {
		t.Fatal("M04 account API still declares POST /account/v1/initialize")
	}
	webhook := readM04OpenAPI(t, root, "gizpay-webhooks.yaml")
	for _, required := range []string{"/zitadel/user-authenticated:", "operationId: initializeHumanFromZitadel", "ErrorResponse:"} {
		if !strings.Contains(webhook, required) {
			t.Errorf("Webhook OpenAPI lacks %q", required)
		}
	}
}

func TestMilestone04OpenAPICreatePayloadAndIdempotency(t *testing.T) {
	root := filepath.Join("..", "..", "api", "openapi")
	account := readM04OpenAPI(t, root, "account.yaml")
	for _, required := range []string{
		"required: [id, account_id, terms_version]",
		"required: [id, name]",
		"required: [id, channel, external_reference, amount_microcredits]",
		"'200':",
		"resource_id_conflict",
		"ErrorResponse:",
	} {
		if !strings.Contains(account, required) {
			t.Errorf("Account OpenAPI lacks M04 contract %q", required)
		}
	}
	gizway := readM04OpenAPI(t, root, "gizway-user.yaml")
	for _, required := range []string{
		"required: [id, name, key, status, prices]",
		"'200':",
		"resource_id_conflict",
		"ErrorResponse:",
	} {
		if !strings.Contains(gizway, required) {
			t.Errorf("GizWay User OpenAPI lacks M04 contract %q", required)
		}
	}
}

func TestMilestone04OpenAPICreditAndCatalogOwnership(t *testing.T) {
	root := filepath.Join("..", "..", "api", "openapi")
	internal := readM04OpenAPI(t, root, "internal-gizpay.yaml")
	if !strings.Contains(internal, "subscription_key_id") {
		t.Fatal("Credit Check response lacks subscription_key_id")
	}
	public := readM04OpenAPI(t, root, "gizway-public.yaml")
	for _, required := range []string{"/auth/catalog-token:", "public_catalog_token_unavailable", "expires_at", "ErrorResponse:"} {
		if !strings.Contains(public, required) {
			t.Errorf("GizWay Public OpenAPI lacks catalog contract %q", required)
		}
	}
}

func readM04OpenAPI(t *testing.T, root, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
