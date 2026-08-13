package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"maps"
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

func bootstrapMilestone02(options options) error {
	variables, err := readVariables(options.identityFile)
	if err != nil {
		return err
	}
	variables["pay_url"], variables["way_url"] = options.gizpayURL, options.cnURL
	variables["cn_url"], variables["global_url"] = options.cnURL, options.globalURL
	variables["incomplete_url"], variables["way_incomplete_url"] = "http://gizway-incomplete:8082", "http://gizway-incomplete:8082"
	variables["fake_url"], variables["credit_spy_url"] = options.cnProviderURL, "http://credit-spy:19400"
	variables["oauth_spy_url"], variables["toxiproxy_url"] = "http://oauth-spy:19500", "http://toxiproxy:8474"
	if strings.HasPrefix(options.story, "01-account-subscription-and-keys") {
		return writeVariables(options.fixtureFile, variables)
	}
	secret, err := os.ReadFile(options.hmacSecretFile)
	if err != nil {
		return err
	}
	secret = []byte(strings.TrimSpace(string(secret)))
	payerBalance := int64(1000000)
	if strings.HasPrefix(options.story, "03-charge-commission-and-ledger") {
		payerBalance = 200
	}
	if err := seedGizPay(options.gizpayDSN, secret, variables, payerBalance); err != nil {
		return err
	}
	if err := seedRegion(options.cnDSN, "cn", options.cnProviderURL, "cn-provider-secret", variables["cn_admin_subject"], variables["revoked_admin_subject"]); err != nil {
		return err
	}
	if err := seedRegion(options.globalDSN, "global", options.globalProviderURL, "global-provider-secret", variables["global_admin_subject"], variables["revoked_admin_subject"]); err != nil {
		return err
	}
	setBusinessVariables(variables, secret)
	return writeVariables(options.fixtureFile, variables)
}

func seedGizPay(dsn string, secret []byte, variables map[string]string, payerBalance int64) error {
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
	now := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	identities := []struct{ userID, subject, accountID string }{
		{"usr_main", variables["human_subject"], "acct_main"}, {"usr_payer", variables["human_two_subject"], "acct_payer"},
		{"usr_provider_1", variables["provider_merchant_human_subject"], "acct_provider_1"}, {"usr_provider_2", variables["provider_merchant_human_two_subject"], "acct_provider_2"},
		{"usr_quota10", "fixture-quota10", "acct_quota10"},
		{"usr_quota100", "fixture-quota100", "acct_quota100"}, {"usr_quota20", "fixture-quota20", "acct_quota20"},
		{"usr_zero", "fixture-zero", "acct_zero"}, {"usr_negative", "fixture-negative", "acct_negative"},
		{"usr_paused", "fixture-paused", "acct_paused"}, {"usr_inactive", "fixture-inactive", "acct_inactive"},
		{"usr_inactive_product", "fixture-inactive-product", "acct_inactive_product"},
	}
	for _, identity := range identities {
		if _, err := tx.Exec(`INSERT INTO users(id,identity_issuer,identity_subject,status,created_at) VALUES($1,$2,$3,'active',$4)`, identity.userID, fixtureIssuer, identity.subject, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO accounts(id,owner_user_id,status,created_at) VALUES($1,$2,'active',$3)`, identity.accountID, identity.userID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO ledger_accounts(id,owner_account_id,asset_code,status) VALUES($1,$2,'credit','active')`, "led_"+identity.accountID, identity.accountID); err != nil {
			return err
		}
	}
	balances := map[string]int64{"acct_payer": payerBalance, "acct_quota10": 10, "acct_quota100": 100, "acct_quota20": 20, "acct_negative": -10}
	for accountID, amount := range balances {
		if err := seedBalance(tx, accountID, amount, now); err != nil {
			return err
		}
	}
	merchants := [][]any{
		{"mer_main", "acct_main", "Main Merchant LLC", "Main Merchant", "active"},
		{"mer_provider_1", "acct_provider_1", "Provider One LLC", "Provider One", "active"},
		{"mer_provider_2", "acct_provider_2", "Provider Two LLC", "Provider Two", "active"},
		{"mer_provider_inactive", "acct_platform", "Inactive Provider LLC", "Inactive Provider", "inactive"},
		{"mer_main_inactive", "acct_main", "Inactive Main LLC", "Inactive Main", "inactive"},
	}
	for _, merchant := range merchants {
		if _, err := tx.Exec(`INSERT INTO merchants(id,settlement_account_id,legal_name,public_name,status,review_level,created_at,updated_at) VALUES($1,$2,$3,$4,$5,'basic',$6,$6)`, append(merchant, now)...); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO products(id,merchant_id,name,billing_mode,status,terms_version,created_at,updated_at) VALUES
	 ('prod_active','mer_main','Story PAYG','pay_as_you_go','active','2026-08-13',$1,$1),
		 ('prod_other','mer_main','Other PAYG','pay_as_you_go','active','2026-08-13',$1,$1),
		 ('prod_inactive','mer_main','Inactive PAYG','pay_as_you_go','inactive','2026-08-13',$1,$1),
		 ('prod_inactive_main','mer_main_inactive','Inactive Main PAYG','pay_as_you_go','active','2026-08-13',$1,$1)`, now); err != nil {
		return err
	}
	type keyFixture struct{ id, account, product, subscriptionStatus, keyStatus, raw string }
	keys := []keyFixture{
		{"active", "acct_payer", "prod_active", "active", "active", "gzs_m02_shared_key"},
		{"other_product", "acct_payer", "prod_other", "active", "active", "gzs_m02_other_product"},
		{"revoked", "acct_payer", "prod_active", "active", "revoked", "gzs_m02_revoked"},
		{"quota10", "acct_quota10", "prod_active", "active", "active", "gzs_m02_quota_10"},
		{"quota100", "acct_quota100", "prod_active", "active", "active", "gzs_m02_quota_100"},
		{"quota20", "acct_quota20", "prod_active", "active", "active", "gzs_m02_quota_20"},
		{"zero", "acct_zero", "prod_active", "active", "active", "gzs_m02_zero"},
		{"negative", "acct_negative", "prod_active", "active", "active", "gzs_m02_negative"},
		{"paused", "acct_paused", "prod_active", "paused", "active", "gzs_m02_paused"},
		{"inactive", "acct_inactive", "prod_active", "inactive", "active", "gzs_m02_inactive"},
		{"inactive_product", "acct_inactive_product", "prod_inactive", "active", "active", "gzs_m02_inactive_product"},
		{"inactive_main", "acct_payer", "prod_inactive_main", "active", "active", "gzs_m02_inactive_main"},
	}
	for _, key := range keys {
		subscriptionID, keyID := "sub_"+key.id, "key_"+key.id
		if _, err := tx.Exec(`INSERT INTO subscriptions(id,account_id,product_id,status,terms_version,accepted_at,created_at) VALUES($1,$2,$3,$4,'2026-08-13',$5,$5)`, subscriptionID, key.account, key.product, key.subscriptionStatus, now); err != nil {
			return err
		}
		revokedAt := any(nil)
		if key.keyStatus == "revoked" {
			revokedAt = now
		}
		if _, err := tx.Exec(`INSERT INTO subscription_api_keys(id,subscription_id,key_hmac,encrypted_key,encryption_version,status,created_at,revoked_at) VALUES($1,$2,$3,'fixture-ciphertext',1,$4,$5,$6)`, keyID, subscriptionID, keyHMAC(secret, key.raw), key.keyStatus, now, revokedAt); err != nil {
			return err
		}
	}
	principals := []struct{ id, owner, subject, status string }{
		{"sp_cn", "usr_main", "gizway-cn-service", "active"}, {"sp_global", "usr_main", "gizway-global-service", "active"},
		{"sp_charger", "usr_main", "service-charger", "active"}, {"sp_reader", "usr_main", "service-reader", "active"},
		{"sp_rotated", "usr_main", "service-rotated", "active"}, {"sp_other", "usr_provider_1", "service-other-user", "active"},
		{"sp_revoked", "usr_main", "service-revoked", "revoked"},
	}
	for _, principal := range principals {
		revokedAt := any(nil)
		if principal.status == "revoked" {
			revokedAt = now
		}
		if _, err := tx.Exec(`INSERT INTO service_principals(id,owner_user_id,identity_issuer,identity_subject,status,created_at,revoked_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, principal.id, principal.owner, fixtureIssuer, principal.subject, principal.status, now, revokedAt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	variables["payer_account_id"], variables["main_merchant_account_id"] = "acct_payer", "acct_main"
	variables["provider_merchant_id"], variables["provider_merchant_id_two"] = "mer_provider_1", "mer_provider_2"
	variables["inactive_provider_merchant_id"] = "mer_provider_inactive"
	variables["provider_settlement_account_id"], variables["provider_settlement_account_id_two"] = "acct_provider_1", "acct_provider_2"
	variables["platform_fee_account_id"] = "acct_platform"
	return nil
}

func seedBalance(tx *sqlx.Tx, accountID string, amount int64, now time.Time) error {
	if amount == 0 {
		return nil
	}
	id := "seed_" + accountID
	if _, err := tx.Exec(`INSERT INTO ledger_transactions(id,transaction_type,status,created_at) VALUES($1,'fixture','posted',$2)`, id, now); err != nil {
		return err
	}
	direction, clearing := "credit", "debit"
	if amount < 0 {
		amount, direction, clearing = -amount, "debit", "credit"
	}
	var ledgerAccountID string
	if err := tx.Get(&ledgerAccountID, `SELECT id FROM ledger_accounts WHERE owner_account_id=$1 AND asset_code='credit'`, accountID); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,direction,amount_microcredits) VALUES
	 ($1,$2,$3,$4,$5),($6,$2,'led_clearing',$7,$5)`, id+"_account", id, ledgerAccountID, direction, amount, id+"_clearing", clearing)
	return err
}

func seedRegion(dsn, region, providerURL, credential, adminSubject, revokedAdminSubject string) error {
	storeDSN, err := withoutSearchPath(dsn)
	if err != nil {
		return err
	}
	stores, err := bifrostadapter.OpenStores(context.Background(),
		bifrostadapter.StoreConfig{Type: "postgresql", DSN: storeDSN, Schema: "bifrost_config"},
		bifrostadapter.StoreConfig{Type: "postgresql", DSN: storeDSN, Schema: "bifrost_logs"},
	)
	if err != nil {
		return err
	}
	defer stores.Close(context.Background())
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
	now := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	adminID := "adm_" + region
	if _, err := tx.Exec(`INSERT INTO administrators(id,identity_issuer,identity_subject,status,created_at) VALUES($1,$2,$3,'active',$4),($5,$2,$6,'inactive',$4)`, adminID, fixtureIssuer, adminSubject, now, "adm_revoked_"+region, revokedAdminSubject); err != nil {
		return err
	}
	providerID, keyID, altKeyID := "provider_story_"+region, "bifrost_key_story_"+region, "bifrost_key_alt_"+region
	if err := stores.CreateProvider(context.Background(), bifrostadapter.ProviderRecord{ID: providerID, Name: "Story " + region, Kind: "openai", BaseURL: providerURL, Status: "active", CreatedAt: now}); err != nil {
		return err
	}
	for _, key := range []bifrostadapter.KeyRecord{
		{ID: keyID, ProviderID: providerID, Name: "primary", APIKey: credential, Weight: 100, Enabled: false, Status: "inactive"},
		{ID: altKeyID, ProviderID: providerID, Name: "alternate", APIKey: credential, Weight: 10, Enabled: false, Status: "inactive"},
	} {
		if err := stores.CreateKey(context.Background(), key); err != nil {
			return err
		}
	}
	models := []struct{ id, name string }{{"mdl_story_" + region, "story-text"}, {"mdl_region_" + region, region + "-story-model"}}
	for _, model := range models {
		if _, err := tx.Exec(`INSERT INTO models(id,name,provider_id,provider_model,status,created_at,updated_at) VALUES($1,$2,$3,'fake-text-v1','active',$4,$4)`, model.id, model.name, providerID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO model_customer_prices(model_id,metric,unit_size,price_microcredits) VALUES($1,'input_token',10,3),($1,'output_token',10,3)`, model.id); err != nil {
			return err
		}
	}
	for _, activeKey := range []string{keyID, altKeyID} {
		if _, err := tx.Exec(`INSERT INTO provider_key_billing(bifrost_key_id,beneficiary_merchant_id,status) VALUES($1,'mer_provider_1','active')`, activeKey); err != nil {
			return err
		}
		for _, model := range models {
			if _, err := tx.Exec(`INSERT INTO provider_key_prices(bifrost_key_id,model_id,metric,unit_size,commission_microcredits) VALUES($1,$2,'input_token',10,2),($1,$2,'output_token',10,1)`, activeKey, model.id); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, key := range []bifrostadapter.KeyRecord{
		{ID: keyID, ProviderID: providerID, Name: "primary", APIKey: credential, Weight: 100, Enabled: true, Status: "active"},
		{ID: altKeyID, ProviderID: providerID, Name: "alternate", APIKey: credential, Weight: 10, Enabled: true, Status: "active"},
	} {
		if err := stores.UpdateKey(context.Background(), key); err != nil {
			return err
		}
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

func keyHMAC(secret []byte, raw string) string {
	return subscriptionkey.HMAC(secret, raw)
}

func setBusinessVariables(values map[string]string, secret []byte) {
	raw := map[string]string{
		"raw_subscription_key": "gzs_m02_shared_key", "raw_subscription_key_other_product": "gzs_m02_other_product",
		"raw_subscription_key_quota_10": "gzs_m02_quota_10", "raw_subscription_key_quota_100": "gzs_m02_quota_100",
		"raw_subscription_key_quota_concurrent_20": "gzs_m02_quota_20",
	}
	maps.Copy(values, raw)
	values["active_subscription_hmac"] = keyHMAC(secret, raw["raw_subscription_key"])
	for name, rawKey := range map[string]string{"revoked_subscription_hmac": "gzs_m02_revoked", "zero_balance_subscription_hmac": "gzs_m02_zero", "negative_balance_subscription_hmac": "gzs_m02_negative", "paused_subscription_hmac": "gzs_m02_paused", "inactive_subscription_hmac": "gzs_m02_inactive", "inactive_product_subscription_hmac": "gzs_m02_inactive_product", "inactive_main_merchant_subscription_hmac": "gzs_m02_inactive_main"} {
		values[name] = keyHMAC(secret, rawKey)
	}
	values["provider_key_secret"], values["provider_key_secret_replacement"] = "cn-provider-secret", "cn-provider-secret-replacement"
	values["provider_key_secret_two"], values["provider_key_secret_three"] = "cn-provider-secret-two", "cn-provider-secret-three"
	values["seeded_model_id"], values["seeded_bifrost_key_id"] = "mdl_story_cn", "bifrost_key_story_cn"
	values["seeded_bifrost_alt_key_id"] = "bifrost_key_alt_cn"
}

var _ = fmt.Sprintf
