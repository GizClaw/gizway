package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadResourceConfigRejectsUnknownFieldsAndEmptyKey(t *testing.T) {
	directory := t.TempDir()
	keyPath := filepath.Join(directory, "admin-key")
	configPath := filepath.Join(directory, "resources.yaml")
	base := `version: 1
admin_key_file: ` + keyPath + `
gizpay:
  base_url: http://pay.test
  products: [{id: product, merchant_id: merchant, name: Product, billing_mode: pay_as_you_go, published: true, status: active, terms_version: v1}]
  product_listings: []
  service_principals: []
regions:
  - name: cn
    base_url: http://cn.test
    providers: []
    models: []
    model_customer_prices: []
    model_listings: []
    provider_keys: []
`
	if err := os.WriteFile(keyPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadResourceConfig(configPath, nil); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty Admin Key error = %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("admin"), 0o600); err != nil {
		t.Fatal(err)
	}
	unknown := strings.Replace(base, "name: Product", "name: Product, mystery: value", 1)
	if err := os.WriteFile(configPath, []byte(unknown), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadResourceConfig(configPath, nil); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestLoadResourceConfigRejectsDuplicateIDsAndMissingDependencies(t *testing.T) {
	directory := t.TempDir()
	keyPath := filepath.Join(directory, "admin-key")
	if err := os.WriteFile(keyPath, []byte("admin"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeAndLoad := func(contents string) error {
		path := filepath.Join(directory, "resources.yaml")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			return err
		}
		_, _, err := loadResourceConfig(path, nil)
		return err
	}
	base := `version: 1
admin_key_file: ` + keyPath + `
gizpay:
  base_url: http://pay.test
  products: [{id: duplicate, merchant_id: merchant, name: Product, billing_mode: pay_as_you_go, published: true, status: active, terms_version: v1}]
  product_listings: [{id: duplicate, product_id: duplicate, site: test, title: Test, description: Test, billing_mode: pay_as_you_go, price_text: Test, display_order: 0, status: active}]
  service_principals: []
regions: [{name: cn, base_url: http://cn.test, providers: [], models: [], model_customer_prices: [], model_listings: [], provider_keys: []}]
`
	if err := writeAndLoad(base); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate error = %v", err)
	}
	missing := strings.Replace(base, "id: duplicate, product_id: duplicate", "id: listing, product_id: missing", 1)
	if err := writeAndLoad(missing); err == nil || !strings.Contains(err.Error(), "missing Product") {
		t.Fatalf("dependency error = %v", err)
	}
}

func TestLoadResourceConfigRejectsUnknownNestedPriceFields(t *testing.T) {
	directory := t.TempDir()
	keyPath := filepath.Join(directory, "admin-key")
	if err := os.WriteFile(keyPath, []byte("admin"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := `version: 1
admin_key_file: ` + keyPath + `
gizpay: {base_url: http://pay.test, products: [], product_listings: [], service_principals: []}
regions:
  - name: cn
    base_url: http://cn.test
    providers: [{id: provider, name: Provider, kind: openai, base_url: http://provider.test, status: active}]
    models: [{id: model, provider_id: provider, name: Model, provider_model: upstream-model, status: active}]
    model_customer_prices:
      - model_id: model
        prices: [{metric: input_tokens, unit_size: 1, price_microcredits: 1, mystery: value}]
    model_listings: []
    provider_keys: []
`
	configPath := filepath.Join(directory, "resources.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadResourceConfig(configPath, nil); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("nested unknown field error = %v", err)
	}
}
