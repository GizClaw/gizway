package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/idy/gizway/internal/generated/gizpayadmin"
	"github.com/idy/gizway/internal/generated/gizwayadmin"
	"github.com/idy/gizway/internal/subscriptionkey"
)

const fixtureIssuer = "https://identity.e2e.gizclaw.test:18080"

type initializedAccount struct {
	AccountID         string
	DefaultMerchantID string
}

func bootstrapMilestone03(options options) error {
	variables, err := readVariables(options.identityFile)
	if err != nil {
		return err
	}
	if options.gizpayURL == "" || options.cnURL == "" || options.resourceConfigFile == "" || options.hmacSecretFile == "" {
		return errors.New("milestone 03 resource endpoints, resource config, and HMAC secret are required")
	}
	variables["pay_url"], variables["way_url"] = options.gizpayURL, options.cnURL
	variables["cn_url"], variables["global_url"] = options.cnURL, options.globalURL
	variables["cn_provider_url"], variables["global_provider_url"] = options.cnProviderURL, options.globalProviderURL
	variables["fixture_issuer"] = fixtureIssuer
	variables["provider_key_secret"] = "cn-provider-secret-two"
	variables["provider_key_secret_two"] = "cn-provider-secret-three"
	variables["gemini_operation"] = "story-text:generateContent"
	variables["seeded_model_name"] = "story-text"
	variables["zero_price_model"] = "story-text-zero"

	client := &http.Client{Timeout: 20 * time.Second}
	for _, endpoint := range []string{options.gizpayURL, options.cnURL, options.globalURL} {
		if err := waitForHTTP(client, endpoint+"/healthz", 90*time.Second); err != nil {
			return err
		}
	}
	bootstrapKey, err := readMachineKey("/fixtures/zitadel-bootstrap-machine.json")
	if err != nil {
		return err
	}
	adminToken, err := exchangeJWTBearer(context.Background(), options.zitadelURL, bootstrapKey, []string{"openid", zitadelAPIScope})
	if err != nil {
		return fmt.Errorf("authenticate ZITADEL Action bootstrap: %w", err)
	}
	actionClient := &zitadelClient{baseURL: strings.TrimRight(options.zitadelURL, "/"), token: adminToken, http: client}
	if err := actionClient.configureUserInitializationAction("/fixtures"); err != nil {
		return err
	}
	owner, err := initializeFixtureHuman(client, options.gizpayURL, variables["human_token"], variables["human_subject"], "human-primary")
	if err != nil {
		return fmt.Errorf("initialize primary Human: %w", err)
	}
	other, err := initializeFixtureHuman(client, options.gizpayURL, variables["human_token_two"], variables["human_two_subject"], "human-two")
	if err != nil {
		return fmt.Errorf("initialize second Human: %w", err)
	}
	variables["default_merchant_id"] = owner.DefaultMerchantID
	resources, adminKey, err := loadResourceConfig(options.resourceConfigFile, variables)
	if err != nil {
		return err
	}
	for pass := 1; pass <= 2; pass++ {
		if err := applyBusinessResources(context.Background(), client, resources, adminKey); err != nil {
			return fmt.Errorf("API resource pass %d: %w", pass, err)
		}
	}
	if err := verifyResourceConflict(context.Background(), client, resources, adminKey); err != nil {
		return err
	}

	productID := resources.GizPay.Products[0]["id"].(string)
	var subscription struct {
		ID string `json:"id"`
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/products/"+productID+"/subscriptions", variables["human_token"], map[string]any{"id": "sub_m03_bootstrap", "account_id": owner.AccountID, "terms_version": "m03-v1"}, &subscription); err != nil {
		return err
	}
	var subscriptionKey struct{ ID, Key string }
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/subscriptions/"+subscription.ID+"/keys", variables["human_token"], map[string]any{"id": "skey_m03_bootstrap", "name": "Bootstrap"}, &subscriptionKey); err != nil {
		return err
	}
	var revokedKey struct {
		ID        string
		Key       string
		RevokedAt time.Time `json:"revoked_at"`
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/subscriptions/"+subscription.ID+"/keys", variables["human_token"], map[string]any{"id": "skey_m03_revoked", "name": "Revoked"}, &revokedKey); err != nil {
		return err
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/subscriptions/"+subscription.ID+"/keys/"+revokedKey.ID+"/revoke", variables["human_token"], nil, &revokedKey); err != nil {
		return err
	}
	var negativeSubscription struct {
		ID string `json:"id"`
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/products/"+productID+"/subscriptions", variables["human_token_two"], map[string]any{"id": "sub_m03_negative", "account_id": other.AccountID, "terms_version": "m03-v1"}, &negativeSubscription); err != nil {
		return err
	}
	var negativeKey struct{ ID, Key string }
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/subscriptions/"+negativeSubscription.ID+"/keys", variables["human_token_two"], map[string]any{"id": "skey_m03_negative", "name": "Negative"}, &negativeKey); err != nil {
		return err
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/accounts/"+other.AccountID+"/topups", variables["human_token_two"], map[string]any{"id": "topup_m03_negative", "amount_microcredits": 5, "channel": "fake", "external_reference": "m03-negative-bootstrap"}, nil); err != nil {
		return err
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/accounts/"+owner.AccountID+"/topups", variables["human_token"], map[string]any{"id": "topup_m03_bootstrap", "amount_microcredits": 1_000_000_000, "channel": "fake", "external_reference": "m03-bootstrap"}, nil); err != nil {
		return err
	}

	secret, err := os.ReadFile(options.hmacSecretFile)
	if err != nil {
		return err
	}
	secret = bytes.TrimSpace(secret)
	variables["raw_subscription_key"] = subscriptionKey.Key
	variables["active_subscription_hmac"] = subscriptionkey.HMAC(secret, subscriptionKey.Key)
	variables["revoked_subscription_key"] = revokedKey.Key
	variables["revoked_subscription_hmac"] = subscriptionkey.HMAC(secret, revokedKey.Key)
	variables["revoked_at"] = revokedKey.RevokedAt.UTC().Format(time.RFC3339Nano)
	variables["after_revocation_time"] = revokedKey.RevokedAt.UTC().Add(time.Second).Format(time.RFC3339Nano)
	variables["negative_balance_hmac"] = subscriptionkey.HMAC(secret, negativeKey.Key)
	variables["seeded_subscription_id"] = subscription.ID
	variables["seeded_model_id"] = "story-text-cn"
	variables["provider_id"] = "provider_story_cn"
	variables["account_id"] = owner.AccountID
	variables["account_id_two"] = other.AccountID
	variables["hidden_product_id"] = resources.GizPay.Products[1]["id"].(string)
	for _, region := range resources.Regions {
		variables[region.Name+"_provider_key_id"] = region.ProviderKeys[0]["id"].(string)
	}
	return writeVariables(options.fixtureFile, variables)
}

func waitForHTTP(client *http.Client, endpoint string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		response, err := client.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait for %s: service did not become ready", endpoint)
		}
		time.Sleep(time.Second)
	}
}

func applyBusinessResources(ctx context.Context, httpClient *http.Client, config resourceConfig, adminKey []byte) error {
	pay, err := gizpayadmin.NewClient(strings.TrimRight(config.GizPay.BaseURL, "/")+"/admin/v1", gizpayadmin.WithHTTPClient(httpClient), gizpayadmin.WithRequestEditorFn(adminKeyEditor(adminKey)))
	if err != nil {
		return err
	}
	for _, body := range config.GizPay.Products {
		if err := createResource("GizPay", "Product", body, func(reader io.Reader) (*http.Response, error) {
			return pay.CreateAdminProductWithBody(ctx, "application/json", reader)
		}); err != nil {
			return err
		}
	}
	for _, body := range config.GizPay.ProductListings {
		if err := createResource("GizPay", "Product Listing", body, func(reader io.Reader) (*http.Response, error) {
			return pay.CreateAdminProductListingWithBody(ctx, "application/json", reader)
		}); err != nil {
			return err
		}
	}
	for _, body := range config.GizPay.ServicePrincipals {
		if err := createResource("GizPay", "Service Principal", body, func(reader io.Reader) (*http.Response, error) {
			return pay.CreateAdminServicePrincipalWithBody(ctx, "application/json", reader)
		}); err != nil {
			return err
		}
	}
	if err := verifyCollection("GizPay Product", resourceIDs(config.GizPay.Products), func() (*http.Response, error) { return pay.ListAdminProducts(ctx) }); err != nil {
		return err
	}
	if err := verifyCollection("GizPay Product Listing", resourceIDs(config.GizPay.ProductListings), func() (*http.Response, error) { return pay.ListAdminProductListings(ctx) }); err != nil {
		return err
	}
	if err := verifyCollection("GizPay Service Principal", resourceIDs(config.GizPay.ServicePrincipals), func() (*http.Response, error) { return pay.ListAdminServicePrincipals(ctx) }); err != nil {
		return err
	}

	for _, region := range config.Regions {
		way, err := gizwayadmin.NewClient(strings.TrimRight(region.BaseURL, "/")+"/admin/v1", gizwayadmin.WithHTTPClient(httpClient), gizwayadmin.WithRequestEditorFn(adminKeyEditor(adminKey)))
		if err != nil {
			return err
		}
		for _, body := range region.Providers {
			if err := createResource(region.Name, "Provider", body, func(reader io.Reader) (*http.Response, error) {
				return way.CreateAdminProviderWithBody(ctx, "application/json", reader)
			}); err != nil {
				return err
			}
		}
		for _, body := range region.Models {
			if err := createResource(region.Name, "Model", body, func(reader io.Reader) (*http.Response, error) {
				return way.CreateAdminModelWithBody(ctx, "application/json", reader)
			}); err != nil {
				return err
			}
		}
		for _, body := range region.ModelCustomerPrices {
			modelID := body.ModelID
			if err := putResource(region.Name, "Model customer prices", modelID, map[string]any{"prices": body.Prices}, func(contentType string, reader io.Reader) (*http.Response, error) {
				return way.ReplaceAdminModelCustomerPricesWithBody(ctx, modelID, contentType, reader)
			}); err != nil {
				return err
			}
		}
		for _, body := range region.ModelListings {
			if err := createResource(region.Name, "Model Listing", body, func(reader io.Reader) (*http.Response, error) {
				return way.CreateAdminModelListingWithBody(ctx, "application/json", reader)
			}); err != nil {
				return err
			}
		}
		for _, body := range region.ProviderKeys {
			if err := createResource(region.Name, "Provider Key", body, func(reader io.Reader) (*http.Response, error) {
				return way.CreateAdminProviderKeyWithBody(ctx, "application/json", reader)
			}); err != nil {
				return err
			}
		}
		if err := verifyCollection(region.Name+" Provider", resourceIDs(region.Providers), func() (*http.Response, error) { return way.ListAdminProviders(ctx) }); err != nil {
			return err
		}
		if err := verifyCollection(region.Name+" Model", resourceIDs(region.Models), func() (*http.Response, error) { return way.ListAdminModels(ctx) }); err != nil {
			return err
		}
		if err := verifyCollection(region.Name+" Model Listing", resourceIDs(region.ModelListings), func() (*http.Response, error) { return way.ListAdminModelListings(ctx) }); err != nil {
			return err
		}
		if err := verifyCollection(region.Name+" Provider Key", resourceIDs(region.ProviderKeys), func() (*http.Response, error) { return way.ListAdminProviderKeys(ctx) }); err != nil {
			return err
		}
	}
	return nil
}

func createResource(service, kind string, body map[string]any, call func(io.Reader) (*http.Response, error)) error {
	return resourceRequest(service, kind, body["id"].(string), body, []int{http.StatusOK, http.StatusCreated}, call)
}

func putResource(service, kind, id string, body any, call func(string, io.Reader) (*http.Response, error)) error {
	return resourceRequest(service, kind, id, body, []int{http.StatusOK}, func(reader io.Reader) (*http.Response, error) {
		return call("application/json", reader)
	})
}

func resourceRequest(service, kind, id string, body any, allowed []int, call func(io.Reader) (*http.Response, error)) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	response, err := call(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("%s %s %s: %w", service, kind, id, err)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if slices.Contains(allowed, response.StatusCode) {
		return nil
	}
	return fmt.Errorf("%s %s %s returned %d: %s", service, kind, id, response.StatusCode, strings.TrimSpace(string(responseBody)))
}

func verifyCollection(kind string, expected []string, call func() (*http.Response, error)) error {
	response, err := call()
	if err != nil {
		return fmt.Errorf("list %s: %w", kind, err)
	}
	defer response.Body.Close()
	var collection struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&collection) != nil {
		return fmt.Errorf("list %s returned invalid response %d", kind, response.StatusCode)
	}
	for _, id := range expected {
		found := false
		for _, resource := range collection.Data {
			if id == resource.ID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("list %s omitted %s", kind, id)
		}
	}
	return nil
}

func verifyResourceConflict(ctx context.Context, httpClient *http.Client, config resourceConfig, adminKey []byte) error {
	region := config.Regions[0]
	way, err := gizwayadmin.NewClient(strings.TrimRight(region.BaseURL, "/")+"/admin/v1", gizwayadmin.WithHTTPClient(httpClient), gizwayadmin.WithRequestEditorFn(adminKeyEditor(adminKey)))
	if err != nil {
		return err
	}
	conflict := make(map[string]any, len(region.Providers[0]))
	maps.Copy(conflict, region.Providers[0])
	conflict["kind"] = "anthropic"
	raw, _ := json.Marshal(conflict)
	response, err := way.CreateAdminProviderWithBody(ctx, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		return fmt.Errorf("conflicting Provider resource returned %d, want 409", response.StatusCode)
	}
	return nil
}

func adminKeyEditor(key []byte) func(context.Context, *http.Request) error {
	return func(_ context.Context, request *http.Request) error {
		request.Header.Set("X-GizWay-Admin-Key", string(key))
		return nil
	}
}

func initializeFixtureHuman(client *http.Client, gizpayURL, token, subject, displayName string) (initializedAccount, error) {
	var initialized initializedAccount
	secret, err := os.ReadFile("/fixtures/zitadel-action-signing-key")
	if err != nil {
		return initialized, err
	}
	body, _ := json.Marshal(map[string]any{"user": map[string]any{"id": subject, "human": map[string]any{}}, "userinfo": map[string]any{"email": displayName + "@example.test", "name": displayName}})
	timestamp := fmt.Sprint(time.Now().UTC().Unix())
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(gizpayURL, "/")+"/webhooks/v1/zitadel/user-authenticated", bytes.NewReader(body))
	if err != nil {
		return initialized, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-ZITADEL-Signature", "t="+timestamp+",v1="+hex.EncodeToString(mac.Sum(nil)))
	response, err := client.Do(request)
	if err != nil {
		return initialized, err
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return initialized, fmt.Errorf("webhook returned %d", response.StatusCode)
	}
	var accounts struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := bootstrapAPIJSON(client, http.MethodGet, strings.TrimRight(gizpayURL, "/")+"/account/v1/accounts", token, nil, &accounts); err != nil || len(accounts.Data) != 1 {
		return initialized, fmt.Errorf("query initialized Account: %w", err)
	}
	var merchants struct {
		Data []struct {
			ID        string `json:"id"`
			IsDefault bool   `json:"is_default"`
		} `json:"data"`
	}
	if err := bootstrapAPIJSON(client, http.MethodGet, strings.TrimRight(gizpayURL, "/")+"/account/v1/merchants", token, nil, &merchants); err != nil {
		return initialized, err
	}
	for _, merchant := range merchants.Data {
		if merchant.IsDefault {
			if initialized.DefaultMerchantID != "" {
				return initialized, errors.New("multiple default Merchants")
			}
			initialized.DefaultMerchantID = merchant.ID
		}
	}
	if initialized.DefaultMerchantID == "" {
		return initialized, errors.New("default Merchant is missing")
	}
	initialized.AccountID = accounts.Data[0].ID
	return initialized, nil
}

func readVariables(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if ok {
			values[key] = value
		}
	}
	return values, scanner.Err()
}

func bootstrapAPIJSON(client *http.Client, method, target, token string, body, result any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, target, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%s %s returned %d: %s", method, target, response.StatusCode, strings.TrimSpace(string(raw)))
	}
	if result != nil && len(raw) != 0 {
		return json.Unmarshal(raw, result)
	}
	return nil
}
