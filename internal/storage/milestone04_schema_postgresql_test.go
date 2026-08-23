package storage_test

import (
	"context"
	"testing"

	"github.com/GizClaw/gizway/internal/testdb"
)

func TestPostgreSQLMilestone04GizPaySchemaContract(t *testing.T) {
	db := testdb.OpenGizPay(t).SQL
	assertPostgreSQLTableColumns(t, db, map[string][]string{
		"users":             {"email", "display_name"},
		"subscription_keys": {"name", "last_used_at"},
		"product_listings":  {"id", "product_id", "site", "title", "description", "billing_mode", "price_text", "display_order", "status"},
	})
	assertPostgreSQLUniqueColumns(t, db, "subscriptions", []string{"account_id", "product_id"})
}

func TestPostgreSQLMilestone04GizWaySchemaContract(t *testing.T) {
	db := testdb.OpenGizWay(t).SQL
	assertPostgreSQLTableColumns(t, db, map[string][]string{
		"provider_key_billing": {"name", "last_used_at", "earned_microcredits"},
		"ai_orders":            {"subscription_key_id"},
		"model_listings":       {"id", "model_id", "title", "description", "family", "context", "latency", "accent", "featured", "display_order", "availability"},
	})
	assertPostgreSQLNonNegativeCheck(t, db, "provider_key_billing", "earned_microcredits")
}

func TestPostgreSQLMilestone04SubscriptionKeyNameRequired(t *testing.T) {
	db := testdb.OpenGizPay(t).SQL
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO users(id,identity_issuer,identity_subject,email,display_name)
		VALUES ('user-m04-name','https://issuer.test','subject-m04-name','','M04 User');
		INSERT INTO accounts(id,owner_user_id) VALUES ('account-m04-name','user-m04-name');
		INSERT INTO merchants(id,settlement_account_id,legal_name,public_name,is_default)
		VALUES ('merchant-m04-name','account-m04-name','M04','M04',true);
		INSERT INTO products(id,merchant_id,name,terms_version)
		VALUES ('product-m04-name','merchant-m04-name','M04 PAYG','v1');
		INSERT INTO subscriptions(id,account_id,product_id,terms_version)
		VALUES ('subscription-m04-name','account-m04-name','product-m04-name','v1');
		INSERT INTO subscription_keys(id,subscription_id,name,key,subscription_key_hmac)
		VALUES ('key-m04-name','subscription-m04-name','   ','gsk_m04_name','m04-name-hmac');
	`)
	if err == nil {
		t.Fatal("Subscription Key accepted a blank name")
	}
}

func TestPostgreSQLMilestone04SubscriptionKeyAllowsLastUsedAtNoOp(t *testing.T) {
	db := testdb.OpenGizPay(t).SQL
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO users(id,identity_issuer,identity_subject,email,display_name)
		VALUES ('user-m04-last-use','https://issuer.test','subject-m04-last-use','','M04 User');
		INSERT INTO accounts(id,owner_user_id) VALUES ('account-m04-last-use','user-m04-last-use');
		INSERT INTO merchants(id,settlement_account_id,legal_name,public_name,is_default)
		VALUES ('merchant-m04-last-use','account-m04-last-use','M04','M04',true);
		INSERT INTO products(id,merchant_id,name,terms_version)
		VALUES ('product-m04-last-use','merchant-m04-last-use','M04 PAYG','v1');
		INSERT INTO subscriptions(id,account_id,product_id,terms_version)
		VALUES ('subscription-m04-last-use','account-m04-last-use','product-m04-last-use','v1');
		INSERT INTO subscription_keys(id,subscription_id,name,key,subscription_key_hmac,last_used_at)
		VALUES ('key-m04-last-use','subscription-m04-last-use','M04 key','gsk_m04_last_use','m04-last-use-hmac','2026-08-16T00:00:00Z');
		UPDATE subscription_keys
		SET last_used_at=CASE WHEN last_used_at < '2026-08-15T23:00:00Z' THEN '2026-08-15T23:00:00Z' ELSE last_used_at END
		WHERE id='key-m04-last-use';
	`)
	if err != nil {
		t.Fatalf("Subscription Key rejected a no-op last_used_at update: %v", err)
	}
}

func TestPostgreSQLMilestone04SubscriptionKeyRejectsClearedLastUsedAt(t *testing.T) {
	db := testdb.OpenGizPay(t).SQL
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO users(id,identity_issuer,identity_subject,email,display_name)
		VALUES ('user-m04-clear-use','https://issuer.test','subject-m04-clear-use','','M04 User');
		INSERT INTO accounts(id,owner_user_id) VALUES ('account-m04-clear-use','user-m04-clear-use');
		INSERT INTO merchants(id,settlement_account_id,legal_name,public_name,is_default)
		VALUES ('merchant-m04-clear-use','account-m04-clear-use','M04','M04',true);
		INSERT INTO products(id,merchant_id,name,terms_version)
		VALUES ('product-m04-clear-use','merchant-m04-clear-use','M04 PAYG','v1');
		INSERT INTO subscriptions(id,account_id,product_id,terms_version)
		VALUES ('subscription-m04-clear-use','account-m04-clear-use','product-m04-clear-use','v1');
		INSERT INTO subscription_keys(id,subscription_id,name,key,subscription_key_hmac,last_used_at)
		VALUES ('key-m04-clear-use','subscription-m04-clear-use','M04 key','gsk_m04_clear_use','m04-clear-use-hmac','2026-08-16T00:00:00Z');
		UPDATE subscription_keys SET last_used_at=NULL WHERE id='key-m04-clear-use';
	`)
	if err == nil {
		t.Fatal("Subscription Key allowed last_used_at to be cleared")
	}
}
