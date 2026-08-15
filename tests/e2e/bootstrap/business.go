package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	bifrostadapter "github.com/idy/gizway/internal/adapter/bifrost"
	"github.com/idy/gizway/internal/subscriptionkey"
	_ "github.com/lib/pq"
)

const fixtureIssuer = "http://zitadel:8080"

type initializedAccount struct {
	UserID            string `json:"user_id"`
	AccountID         string `json:"account_id"`
	LedgerAccountID   string `json:"ledger_account_id"`
	DefaultMerchantID string `json:"default_merchant_id"`
}

func bootstrapMilestone03(options options) error {
	variables, err := readVariables(options.identityFile)
	if err != nil {
		return err
	}
	if options.gizpayURL == "" || options.cnURL == "" || options.gizpayDSN == "" || options.cnDSN == "" || options.hmacSecretFile == "" {
		return errors.New("milestone 03 bootstrap endpoints, DSNs, and HMAC secret are required")
	}
	variables["pay_url"], variables["way_url"] = options.gizpayURL, options.cnURL
	variables["cn_url"], variables["global_url"] = options.cnURL, options.globalURL
	variables["provider_key_secret"] = "cn-provider-secret-two"
	variables["provider_key_secret_two"] = "cn-provider-secret-three"
	variables["gemini_operation"] = "story-text:generateContent"
	variables["seeded_model_name"] = "story-text"
	variables["zero_price_model"] = "story-text-zero"

	client := &http.Client{Timeout: 20 * time.Second}
	var owner, other initializedAccount
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/initialize", variables["human_token"], nil, &owner); err != nil {
		return fmt.Errorf("initialize primary Human: %w", err)
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/initialize", variables["human_token_two"], nil, &other); err != nil {
		return fmt.Errorf("initialize second Human: %w", err)
	}
	var product struct {
		ID string `json:"id"`
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/merchants/"+owner.DefaultMerchantID+"/products", variables["human_token"], map[string]any{"name": "M03 PAYG", "billing_mode": "pay_as_you_go", "terms_version": "m03-v1"}, &product); err != nil {
		return err
	}
	var hiddenProduct struct {
		ID string `json:"id"`
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/merchants/"+owner.DefaultMerchantID+"/products", variables["human_token"], map[string]any{"name": "M03 Hidden PAYG", "billing_mode": "pay_as_you_go", "terms_version": "m03-v1"}, &hiddenProduct); err != nil {
		return err
	}
	if err := setProductPublished(options.gizpayDSN, hiddenProduct.ID, false); err != nil {
		return err
	}
	var subscription struct {
		ID string `json:"id"`
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/products/"+product.ID+"/subscriptions", variables["human_token"], map[string]any{"account_id": owner.AccountID, "terms_version": "m03-v1"}, &subscription); err != nil {
		return err
	}
	var subscriptionKey struct{ ID, Key string }
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/subscriptions/"+subscription.ID+"/keys", variables["human_token"], map[string]any{}, &subscriptionKey); err != nil {
		return err
	}
	var revokedKey struct {
		ID        string
		Key       string
		RevokedAt time.Time `json:"revoked_at"`
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/subscriptions/"+subscription.ID+"/keys", variables["human_token"], map[string]any{}, &revokedKey); err != nil {
		return err
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/subscriptions/"+subscription.ID+"/keys/"+revokedKey.ID+"/revoke", variables["human_token"], nil, &revokedKey); err != nil {
		return err
	}
	var negativeSubscription struct {
		ID string `json:"id"`
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/products/"+product.ID+"/subscriptions", variables["human_token_two"], map[string]any{"account_id": other.AccountID, "terms_version": "m03-v1"}, &negativeSubscription); err != nil {
		return err
	}
	var negativeKey struct{ ID, Key string }
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/subscriptions/"+negativeSubscription.ID+"/keys", variables["human_token_two"], map[string]any{}, &negativeKey); err != nil {
		return err
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/accounts/"+other.AccountID+"/topups", variables["human_token_two"], map[string]any{"amount_microcredits": 5, "channel": "fake", "external_reference": "m03-negative-bootstrap"}, nil); err != nil {
		return err
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/accounts/"+owner.AccountID+"/topups", variables["human_token"], map[string]any{"amount_microcredits": 1_000_000_000, "channel": "fake", "external_reference": "m03-bootstrap"}, nil); err != nil {
		return err
	}

	secret, err := os.ReadFile(options.hmacSecretFile)
	if err != nil {
		return err
	}
	secret = []byte(strings.TrimSpace(string(secret)))
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
	variables["hidden_product_id"] = hiddenProduct.ID
	variables["default_merchant_id"] = owner.DefaultMerchantID

	if err := seedServicePrincipals(options.gizpayDSN, owner.UserID, variables); err != nil {
		return err
	}
	if err := seedMilestone03Region(options.cnDSN, "cn", options.cnProviderURL, "cn-provider-secret", options.cnURL, variables["human_token"], owner.DefaultMerchantID, variables); err != nil {
		return err
	}
	if options.globalDSN != "" && options.globalURL != "" {
		if err := seedMilestone03Region(options.globalDSN, "global", options.globalProviderURL, "global-provider-secret", options.globalURL, variables["human_token"], owner.DefaultMerchantID, variables); err != nil {
			return err
		}
	}
	return writeVariables(options.fixtureFile, variables)
}

func seedServicePrincipals(dsn, ownerUserID string, variables map[string]string) error {
	db, err := waitDatabase(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	now := time.Now().UTC()
	fixtures := []struct {
		id, subject string
		roles       string
	}{
		{"svc_cn", variables["service_subject"], `["credit_check","charge"]`},
		{"svc_global", variables["global_service_subject"], `["credit_check","charge"]`},
		{"svc_charger", variables["service_charger_subject"], `["charge"]`},
	}
	for _, fixture := range fixtures {
		if fixture.subject == "" {
			return errors.New("service account subject fixture is missing")
		}
		if _, err = db.Exec(`INSERT INTO service_principals(id,owner_user_id,identity_issuer,identity_subject,name,roles,status,created_at) VALUES($1,$2,$3,$4,$5,$6,'active',$7) ON CONFLICT(identity_issuer,identity_subject) DO NOTHING`, fixture.id, ownerUserID, fixtureIssuer, fixture.subject, fixture.id, fixture.roles, now); err != nil {
			return err
		}
	}
	return nil
}

func seedMilestone03Region(dsn, region, providerURL, credential, apiURL, humanToken, merchantID string, variables map[string]string) error {
	storeDSN, err := withoutSearchPath(dsn)
	if err != nil {
		return err
	}
	stores, err := bifrostadapter.OpenStores(context.Background(), bifrostadapter.StoreConfig{Type: "postgresql", DSN: storeDSN, Schema: "bifrost_config"}, bifrostadapter.StoreConfig{Type: "postgresql", DSN: storeDSN, Schema: "bifrost_logs"})
	if err != nil {
		return err
	}
	defer stores.Close(context.Background())
	providerID, modelID := "provider_story_"+region, "story-text-"+region
	zeroModelID := "story-text-zero-" + region
	if err = stores.CreateProvider(context.Background(), bifrostadapter.ProviderRecord{ID: providerID, Name: "Story " + region, Kind: "openai", BaseURL: providerURL, Status: "active", CreatedAt: time.Now().UTC()}); err != nil {
		return err
	}
	db, err := waitDatabase(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO client_sync.providers(id,name,kind,status) VALUES($1,$2,'openai','active')`, providerID, "Story "+region); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO client_sync.models(id,provider_id,name,provider_model,status) VALUES($1,$2,'story-text','fake-text-v1','active')`, modelID, providerID); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO client_sync.models(id,provider_id,name,provider_model,status) VALUES($1,$2,'story-text-zero','fake-text-v1','active')`, zeroModelID, providerID); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO client_sync.models(id,provider_id,name,provider_model,status) VALUES($1,$2,'story-text-inactive','fake-text-v1','inactive')`, "story-text-inactive-"+region, providerID); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO model_customer_prices(model_id,metric,unit_size,price_microcredits) VALUES($1,'input_tokens',1000000,1000),($1,'output_tokens',1000000,2000)`, modelID); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO model_customer_prices(model_id,metric,unit_size,price_microcredits) VALUES($1,'input_tokens',1000000,0),($1,'output_tokens',1000000,0)`, zeroModelID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	body := map[string]any{"name": "Bootstrap Provider Key", "key": credential, "status": "active", "prices": []any{
		map[string]any{"model_id": modelID, "metric": "input_tokens", "unit_size": 1000000, "microcredits_per_unit": 500},
		map[string]any{"model_id": modelID, "metric": "output_tokens", "unit_size": 1000000, "microcredits_per_unit": 700},
		map[string]any{"model_id": zeroModelID, "metric": "input_tokens", "unit_size": 1000000, "microcredits_per_unit": 0},
		map[string]any{"model_id": zeroModelID, "metric": "output_tokens", "unit_size": 1000000, "microcredits_per_unit": 0},
	}}
	var key struct {
		ProviderKeyID string `json:"provider_key_id"`
		MerchantID    string `json:"merchant_id"`
	}
	if err = bootstrapAPIJSON(&http.Client{Timeout: 20 * time.Second}, http.MethodPost, strings.TrimRight(apiURL, "/")+"/user/v1/providers/"+providerID+"/keys", humanToken, body, &key); err != nil {
		return err
	}
	if key.MerchantID != merchantID {
		return fmt.Errorf("regional Provider Key merchant %q does not match initialized merchant %q", key.MerchantID, merchantID)
	}
	variables[region+"_provider_key_id"] = key.ProviderKeyID
	return nil
}

func setProductPublished(dsn, productID string, published bool) error {
	db, err := waitDatabase(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	result, err := db.Exec(`UPDATE products SET published=$1,updated_at=now() WHERE id=$2`, published, productID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return fmt.Errorf("publish Product %q: affected %d rows: %w", productID, rows, err)
	}
	return nil
}

func withoutSearchPath(dsn string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Del("search_path")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
func waitDatabase(dsn string) (*sqlx.DB, error) {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		db, err := sqlx.Open("postgres", dsn)
		if err == nil && db.Ping() == nil {
			return db, nil
		}
		if db != nil {
			_ = db.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, errors.New("database did not become ready")
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
