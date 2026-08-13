package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idy/gizway/internal/storage"
)

// bootstrapEmptyAPI uses only Milestone 02 HTTP APIs to create business and
// machine identities. The sole SQL fixture is an opening test balance because
// top-up is intentionally outside Milestone 02.
func bootstrapEmptyAPI(options options) error {
	variables, err := readVariables(options.identityFile)
	if err != nil {
		return err
	}
	if options.gizpayURL == "" || options.gizpayDSN == "" || options.cnDSN == "" || options.fixtureFile == "" {
		return errors.New("gizpay-url, gizpay-dsn, cn-dsn, and fixture-file are required")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	accountID := func(token string) (string, error) {
		var response struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := bootstrapAPIJSON(client, http.MethodGet, options.gizpayURL+"/account/v1/accounts", token, nil, &response); err != nil {
			return "", err
		}
		if len(response.Data) != 1 || response.Data[0].ID == "" {
			return "", errors.New("account API did not create exactly one personal Account")
		}
		return response.Data[0].ID, nil
	}
	mainAccountID, err := accountID(variables["human_token"])
	if err != nil {
		return fmt.Errorf("main Account: %w", err)
	}
	payerAccountID, err := accountID(variables["human_token_two"])
	if err != nil {
		return fmt.Errorf("payer Account: %w", err)
	}
	providerAccountID, err := accountID(variables["provider_merchant_human_token"])
	if err != nil {
		return fmt.Errorf("provider Account: %w", err)
	}

	createMerchant := func(token, accountID, name string) (string, error) {
		var response struct {
			ID string `json:"id"`
		}
		body := map[string]any{"settlement_account_id": accountID, "legal_name": name + " LLC", "public_name": name}
		err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/merchants", token, body, &response)
		return response.ID, err
	}
	mainMerchantID, err := createMerchant(variables["human_token"], mainAccountID, "Empty API Main Merchant")
	if err != nil {
		return err
	}
	providerMerchantID, err := createMerchant(variables["provider_merchant_human_token"], providerAccountID, "Empty API Provider Merchant")
	if err != nil {
		return err
	}
	var product struct {
		ID string `json:"id"`
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/merchants/"+mainMerchantID+"/products", variables["human_token"], map[string]any{
		"name": "Empty API PAYG", "billing_mode": "pay_as_you_go", "terms_version": "2026-08-13",
	}, &product); err != nil {
		return err
	}
	var subscription struct {
		ID string `json:"id"`
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/products/"+product.ID+"/subscriptions", variables["human_token_two"], map[string]any{
		"account_id": payerAccountID, "terms_version": "2026-08-13",
	}, &subscription); err != nil {
		return err
	}
	var subscriptionKey struct {
		ID     string `json:"id"`
		APIKey string `json:"api_key"`
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/subscriptions/"+subscription.ID+"/api-keys", variables["human_token_two"], map[string]any{"name": "empty-api-e2e"}, &subscriptionKey); err != nil {
		return err
	}
	var serviceAccount struct {
		ID         string `json:"id"`
		Credential struct {
			Key json.RawMessage `json:"key"`
		} `json:"credential"`
	}
	if err := bootstrapAPIJSON(client, http.MethodPost, options.gizpayURL+"/account/v1/service-accounts", variables["human_token"], map[string]any{
		"name": "empty-api-gateway", "roles": []string{"subscription_credit_reader", "subscription_charger"},
	}, &serviceAccount); err != nil {
		return err
	}
	if len(serviceAccount.Credential.Key) == 0 || !json.Valid(serviceAccount.Credential.Key) {
		return errors.New("service Account API returned no valid private-key document")
	}
	credentialPath := filepath.Join(options.outputDirectory, "gizway-empty-service.json")
	if err := os.WriteFile(credentialPath, append(serviceAccount.Credential.Key, '\n'), 0o600); err != nil {
		return err
	}

	// The regional Administrator allowlist is infrastructure bootstrap state:
	// Milestone 02 deliberately exposes no endpoint that can grant its own Admin
	// authority. Initialize the empty regional schema and bind the already
	// bootstrapped ZITADEL identity before the Admin API creates all regional
	// business configuration.
	regionalRoot, err := waitDatabase(options.cnDSN)
	if err != nil {
		return err
	}
	if _, err := regionalRoot.Exec(`CREATE SCHEMA IF NOT EXISTS gizway`); err != nil {
		_ = regionalRoot.Close()
		return err
	}
	if err := regionalRoot.Close(); err != nil {
		return err
	}
	regional, err := storage.OpenGizWayPostgreSQL(options.cnDSN+"&search_path=gizway", true)
	if err != nil {
		return err
	}
	if _, err := regional.SQL.Exec(`INSERT INTO administrators(id,identity_issuer,identity_subject,status,created_at) VALUES($1,$2,$3,'active',$4)`,
		"adm_empty_cn", fixtureIssuer, variables["cn_admin_subject"], time.Now().UTC()); err != nil {
		_ = regional.Close()
		return err
	}
	if err := regional.Close(); err != nil {
		return err
	}

	db, err := waitDatabase(options.gizpayDSN)
	if err != nil {
		return err
	}
	tx, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		_ = db.Close()
		return err
	}
	if err := seedBalance(tx, payerAccountID, 1_000_000, time.Now().UTC()); err != nil {
		_ = tx.Rollback()
		_ = db.Close()
		return err
	}
	if err := tx.Commit(); err != nil {
		_ = db.Close()
		return err
	}
	_ = db.Close()

	variables["pay_url"] = options.gizpayURL
	variables["way_url"] = options.cnURL
	variables["fake_url"] = options.cnProviderURL
	variables["main_merchant_id"] = mainMerchantID
	variables["provider_merchant_id"] = providerMerchantID
	variables["payer_account_id"] = payerAccountID
	variables["product_id"] = product.ID
	variables["subscription_id"] = subscription.ID
	variables["raw_subscription_key"] = subscriptionKey.APIKey
	variables["service_account_id"] = serviceAccount.ID
	return writeVariables(options.fixtureFile, variables)
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
