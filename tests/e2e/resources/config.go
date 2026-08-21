package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

type resourceConfig struct {
	Version      int               `yaml:"version"`
	AdminKeyFile string            `yaml:"admin_key_file"`
	GizPay       gizPayResources   `yaml:"gizpay"`
	Regions      []gizWayResources `yaml:"regions"`
}

type gizPayResources struct {
	BaseURL           string           `yaml:"base_url"`
	Products          []map[string]any `yaml:"products"`
	ProductListings   []map[string]any `yaml:"product_listings"`
	ServicePrincipals []map[string]any `yaml:"service_principals"`
}

type gizWayResources struct {
	Name                string               `yaml:"name"`
	BaseURL             string               `yaml:"base_url"`
	Providers           []map[string]any     `yaml:"providers"`
	Models              []map[string]any     `yaml:"models"`
	ModelCustomerPrices []modelPriceResource `yaml:"model_customer_prices"`
	ModelListings       []map[string]any     `yaml:"model_listings"`
	ProviderKeys        []map[string]any     `yaml:"provider_keys"`
}

type modelPriceResource struct {
	ModelID string           `yaml:"model_id" json:"model_id"`
	Prices  []map[string]any `yaml:"prices" json:"prices"`
}

func loadResourceConfig(path string, variables map[string]string) (resourceConfig, []byte, error) {
	var config resourceConfig
	raw, err := os.ReadFile(path)
	if err != nil {
		return config, nil, err
	}
	for key, value := range variables {
		raw = bytes.ReplaceAll(raw, []byte("${"+key+"}"), []byte(value))
	}
	if bytes.Contains(raw, []byte("${")) {
		return config, nil, errors.New("resource config contains an unresolved variable")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return config, nil, err
	}
	if config.Version != 1 || strings.TrimSpace(config.AdminKeyFile) == "" || strings.TrimSpace(config.GizPay.BaseURL) == "" || len(config.Regions) == 0 {
		return config, nil, errors.New("resource config version, admin_key_file, GizPay URL, and regions are required")
	}
	adminKey, err := os.ReadFile(config.AdminKeyFile)
	if err != nil {
		return config, nil, fmt.Errorf("read Admin Key file: %w", err)
	}
	adminKey = bytes.TrimSpace(adminKey)
	if len(adminKey) == 0 {
		return config, nil, errors.New("admin Key file is empty")
	}
	if err := validateResourceConfig(config); err != nil {
		return config, nil, err
	}
	return config, adminKey, nil
}

func validateResourceConfig(config resourceConfig) error {
	seen := map[string]string{}
	check := func(kind string, resources []map[string]any) error {
		for _, resource := range resources {
			id, ok := resource["id"].(string)
			if !ok || strings.TrimSpace(id) == "" {
				return fmt.Errorf("%s resource has no id", kind)
			}
			if previous, exists := seen[id]; exists {
				return fmt.Errorf("duplicate resource id %q in %s and %s", id, previous, kind)
			}
			seen[id] = kind
		}
		return nil
	}
	checkKeys := func(kind string, resources []map[string]any, allowed ...string) error {
		allowedKeys := make(map[string]bool, len(allowed))
		for _, key := range allowed {
			allowedKeys[key] = true
		}
		for _, resource := range resources {
			for key := range resource {
				if !allowedKeys[key] {
					return fmt.Errorf("%s %v has unknown field %q", kind, resource["id"], key)
				}
			}
		}
		return nil
	}
	checkPriceKeys := func(kind string, prices []map[string]any, allowed ...string) error {
		allowedKeys := make(map[string]bool, len(allowed))
		for _, key := range allowed {
			allowedKeys[key] = true
		}
		for _, price := range prices {
			for key := range price {
				if !allowedKeys[key] {
					return fmt.Errorf("%s has unknown field %q", kind, key)
				}
			}
		}
		return nil
	}
	if err := checkKeys("product", config.GizPay.Products, "id", "merchant_id", "name", "billing_mode", "published", "status", "terms_version"); err != nil {
		return err
	}
	if err := checkKeys("product listing", config.GizPay.ProductListings, "id", "product_id", "site", "title", "description", "billing_mode", "price_text", "display_order", "status"); err != nil {
		return err
	}
	if err := checkKeys("service principal", config.GizPay.ServicePrincipals, "id", "owner_identity_issuer", "owner_identity_subject", "identity_issuer", "identity_subject", "name", "roles", "status"); err != nil {
		return err
	}
	if err := check("product", config.GizPay.Products); err != nil {
		return err
	}
	if err := check("product listing", config.GizPay.ProductListings); err != nil {
		return err
	}
	if err := check("service principal", config.GizPay.ServicePrincipals); err != nil {
		return err
	}
	regionNames := map[string]bool{}
	products := resourceIDSet(config.GizPay.Products)
	for _, listing := range config.GizPay.ProductListings {
		if productID, ok := listing["product_id"].(string); !ok || !products[productID] {
			return fmt.Errorf("product Listing %v references missing Product", listing["id"])
		}
	}
	for _, region := range config.Regions {
		if region.Name == "" || region.BaseURL == "" || regionNames[region.Name] {
			return fmt.Errorf("region name and base_url must be unique and non-empty: %q", region.Name)
		}
		regionNames[region.Name] = true
		if err := checkKeys(region.Name+" provider", region.Providers, "id", "name", "kind", "base_url", "status"); err != nil {
			return err
		}
		if err := checkKeys(region.Name+" model", region.Models, "id", "provider_id", "name", "provider_model", "status"); err != nil {
			return err
		}
		if err := checkKeys(region.Name+" model listing", region.ModelListings, "id", "model_id", "title", "description", "family", "context", "latency", "accent", "featured", "display_order", "availability"); err != nil {
			return err
		}
		if err := checkKeys(region.Name+" provider key", region.ProviderKeys, "id", "provider_id", "owner_identity_issuer", "owner_identity_subject", "merchant_id", "name", "key", "status", "prices"); err != nil {
			return err
		}
		for kind, resources := range map[string][]map[string]any{
			region.Name + " provider":      region.Providers,
			region.Name + " model":         region.Models,
			region.Name + " model listing": region.ModelListings,
			region.Name + " provider key":  region.ProviderKeys,
		} {
			if err := check(kind, resources); err != nil {
				return err
			}
		}
		modelPriceIDs := map[string]bool{}
		for _, price := range region.ModelCustomerPrices {
			if price.ModelID == "" || len(price.Prices) == 0 {
				return fmt.Errorf("%s model customer price requires model_id and prices", region.Name)
			}
			if modelPriceIDs[price.ModelID] {
				return fmt.Errorf("%s has duplicate model customer prices for %q", region.Name, price.ModelID)
			}
			modelPriceIDs[price.ModelID] = true
			if err := checkPriceKeys(region.Name+" model customer price", price.Prices, "metric", "unit_size", "price_microcredits"); err != nil {
				return err
			}
		}
		providers, models := resourceIDSet(region.Providers), resourceIDSet(region.Models)
		for _, model := range region.Models {
			if providerID, ok := model["provider_id"].(string); !ok || !providers[providerID] {
				return fmt.Errorf("%s Model %v references missing Provider", region.Name, model["id"])
			}
		}
		for _, price := range region.ModelCustomerPrices {
			if !models[price.ModelID] {
				return fmt.Errorf("%s prices reference missing Model %q", region.Name, price.ModelID)
			}
		}
		for _, listing := range region.ModelListings {
			if modelID, ok := listing["model_id"].(string); !ok || !models[modelID] {
				return fmt.Errorf("%s Model Listing %v references missing Model", region.Name, listing["id"])
			}
		}
		for _, key := range region.ProviderKeys {
			if providerID, ok := key["provider_id"].(string); !ok || !providers[providerID] {
				return fmt.Errorf("%s Provider Key %v references missing Provider", region.Name, key["id"])
			}
			prices, ok := key["prices"].([]any)
			if !ok || len(prices) == 0 {
				return fmt.Errorf("%s Provider Key %v requires prices", region.Name, key["id"])
			}
			for _, rawPrice := range prices {
				price, ok := rawPrice.(map[string]any)
				modelID, idOK := price["model_id"].(string)
				if !ok || !idOK || !models[modelID] {
					return fmt.Errorf("%s Provider Key %v price references missing Model", region.Name, key["id"])
				}
				if err := checkPriceKeys(region.Name+" Provider Key price", []map[string]any{price}, "model_id", "metric", "unit_size", "microcredits_per_unit"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func resourceIDSet(resources []map[string]any) map[string]bool {
	result := make(map[string]bool, len(resources))
	for _, resource := range resources {
		if id, ok := resource["id"].(string); ok {
			result[id] = true
		}
	}
	return result
}

func resourceIDs(resources []map[string]any) []string {
	ids := make([]string, 0, len(resources))
	for _, resource := range resources {
		ids = append(ids, resource["id"].(string))
	}
	sort.Strings(ids)
	return ids
}
