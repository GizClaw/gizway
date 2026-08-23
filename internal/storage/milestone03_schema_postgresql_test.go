package storage_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/GizClaw/gizway/internal/testdb"
)

func TestPostgreSQLMilestone03GizPaySchemaContract(t *testing.T) {
	db := testdb.OpenGizPay(t).SQL
	required := map[string][]string{
		"users":               {"id", "identity_issuer", "identity_subject", "status", "created_at"},
		"accounts":            {"id", "owner_user_id", "status", "created_at"},
		"ledger_accounts":     {"id", "owner_account_id", "asset_code", "status"},
		"ledger_transactions": {"id", "transaction_type", "status", "created_at"},
		"ledger_entries":      {"id", "transaction_id", "ledger_account_id", "direction", "amount_microcredits"},
		"account_balances":    {"account_id", "balance_microcredits"},
		"merchants":           {"id", "settlement_account_id", "legal_name", "public_name", "is_default", "status", "created_at", "updated_at"},
		"products":            {"id", "merchant_id", "name", "billing_mode", "published", "status", "terms_version", "created_at", "updated_at"},
		"subscriptions":       {"id", "account_id", "product_id", "status", "created_at"},
		"subscription_keys":   {"id", "subscription_id", "key", "subscription_key_hmac", "status", "created_at", "revoked_at"},
		"payg_charges":        {"id", "external_order_id", "subscription_id", "gross_microcredits", "ledger_transaction_id", "created_at"},
		"charge_commissions":  {"charge_id", "merchant_id", "settlement_account_id", "amount_microcredits"},
		"topups":              {"id", "account_id", "channel", "external_reference", "amount_microcredits", "status", "ledger_transaction_id", "created_at", "credited_at"},
	}
	assertPostgreSQLTableColumns(t, db, required)
	assertPostgreSQLSchemaTableColumns(t, db, "client_sync", "charges", []string{
		"id", "account_id", "subscription_id", "owner_identity_issuer", "owner_identity_subject",
		"external_order_id", "gross_microcredits", "order_snapshot", "created_at",
	})

	for _, table := range []string{"subscription_api_keys", "payment_intents", "refunds", "transfers"} {
		assertPostgreSQLTableAbsent(t, db, table)
	}
	assertPostgreSQLUniqueColumns(t, db, "subscription_keys", []string{"key"})
	assertPostgreSQLUniqueColumns(t, db, "subscription_keys", []string{"subscription_key_hmac"})
	assertPostgreSQLUniqueColumns(t, db, "topups", []string{"channel", "external_reference"})
	assertPostgreSQLUniqueColumns(t, db, "topups", []string{"ledger_transaction_id"})
	assertPostgreSQLPositiveCheck(t, db, "topups", "amount_microcredits")
	assertPostgreSQLCheckValues(t, db, "products", "billing_mode", "pay_as_you_go")
	assertPostgreSQLCheckValues(t, db, "subscription_keys", "status", "active", "revoked")
	assertPostgreSQLCheckValues(t, db, "topups", "channel", "fake")
	assertPostgreSQLCheckValues(t, db, "topups", "status", "succeeded")
	assertPostgreSQLForeignKeyTarget(t, db, "subscription_keys", "subscription_id", "subscriptions", "id")
	assertPostgreSQLForeignKeyTarget(t, db, "topups", "account_id", "accounts", "id")
	assertPostgreSQLForeignKeyTarget(t, db, "topups", "ledger_transaction_id", "ledger_transactions", "id")
	assertPostgreSQLForeignKeyDeleteAction(t, db, "subscription_keys", "subscription_id", "NO ACTION", "RESTRICT")
	assertPostgreSQLForeignKeyDeleteAction(t, db, "topups", "account_id", "NO ACTION", "RESTRICT")
	assertPostgreSQLUniqueColumns(t, db, "users", []string{"identity_issuer", "identity_subject"})
	assertPostgreSQLUniqueColumns(t, db, "accounts", []string{"owner_user_id"})
	assertPostgreSQLUniqueColumns(t, db, "ledger_accounts", []string{"owner_account_id", "asset_code"})
	assertDefaultMerchantUniquePerAccount(t, db)
}

func TestPostgreSQLMilestone03GizWaySchemaContract(t *testing.T) {
	db := testdb.OpenGizWay(t).SQL
	required := map[string][]string{
		"gizway_user_merchants": {"owner_identity_issuer", "owner_identity_subject", "merchant_id", "created_at", "updated_at"},
		"model_customer_prices": {"model_id", "metric", "unit_size", "price_microcredits"},
		"provider_key_billing":  {"provider_key_id", "owner_identity_issuer", "owner_identity_subject", "merchant_id", "status", "created_at", "updated_at"},
		"provider_key_prices":   {"provider_key_id", "model_id", "metric", "unit_size", "microcredits_per_unit"},
		"ai_orders":             {"id", "external_order_id", "provider_key_id", "subscription_key_hmac", "account_id", "subscription_id", "product_id", "owner_identity_issuer", "owner_identity_subject", "gross_microcredits", "status", "created_at"},
		"charge_outbox":         {"id", "external_order_id", "ai_order_id", "payload", "status", "recover_duplicate", "created_at", "updated_at"},
	}
	assertPostgreSQLTableColumns(t, db, required)

	for _, table := range []string{"administrators", "models", "providers", "provider_api_keys"} {
		assertPostgreSQLTableAbsent(t, db, table)
	}
	assertPostgreSQLUniqueColumns(t, db, "gizway_user_merchants", []string{"owner_identity_issuer", "owner_identity_subject"})
	assertPostgreSQLUniqueColumns(t, db, "provider_key_billing", []string{"provider_key_id"})
	assertPostgreSQLUniqueColumns(t, db, "provider_key_prices", []string{"provider_key_id", "model_id", "metric"})
	assertPostgreSQLPositiveCheck(t, db, "provider_key_prices", "unit_size")
	assertPostgreSQLNonNegativeCheck(t, db, "provider_key_prices", "microcredits_per_unit")
}

func TestPostgreSQLMilestone03SubscriptionKeyIsImmutableAfterCreation(t *testing.T) {
	db := testdb.OpenGizPay(t).SQL
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO users(id,identity_issuer,identity_subject,email,display_name,status,created_at)
		VALUES ('user-m03-key','https://issuer.test','subject-m03-key','','M03 Key User','active',now());
		INSERT INTO accounts(id,owner_user_id,status,created_at)
		VALUES ('account-m03-key','user-m03-key','active',now());
		INSERT INTO merchants(id,settlement_account_id,legal_name,public_name,is_default,status,created_at,updated_at)
		VALUES ('merchant-m03-key','account-m03-key','M03 Key Merchant','M03 Key Merchant',true,'active',now(),now());
		INSERT INTO products(id,merchant_id,name,billing_mode,published,status,terms_version,created_at,updated_at)
		VALUES ('product-m03-key','merchant-m03-key','M03 PAYG','pay_as_you_go',true,'active','v1',now(),now()),
		       ('product-m03-other','merchant-m03-key','M03 Other','pay_as_you_go',true,'active','v1',now(),now());
		INSERT INTO subscriptions(id,account_id,product_id,status,created_at)
		VALUES ('subscription-m03-key','account-m03-key','product-m03-key','active',now()),
		       ('subscription-m03-other','account-m03-key','product-m03-other','active',now());
		INSERT INTO subscription_keys(id,subscription_id,name,key,subscription_key_hmac,status,created_at)
		VALUES ('key-m03','subscription-m03-key','M03 Key','gsk_m03_plaintext','m03-hmac','active',now());
	`)
	if err != nil {
		t.Fatalf("create Milestone 03 key fixture: %v", err)
	}

	for name, statement := range map[string]string{
		"plaintext":    `UPDATE subscription_keys SET key='changed' WHERE id='key-m03'`,
		"hmac":         `UPDATE subscription_keys SET subscription_key_hmac='changed' WHERE id='key-m03'`,
		"subscription": `UPDATE subscription_keys SET subscription_id='subscription-m03-other' WHERE id='key-m03'`,
		"created_at":   `UPDATE subscription_keys SET created_at=now() + interval '1 second' WHERE id='key-m03'`,
		"delete":       `DELETE FROM subscription_keys WHERE id='key-m03'`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.ExecContext(ctx, statement); err == nil {
				t.Fatalf("immutable Subscription Key accepted %s mutation", name)
			}
		})
	}
	if _, err := db.ExecContext(ctx, `UPDATE subscription_keys SET status='revoked',revoked_at=now() WHERE id='key-m03'`); err != nil {
		t.Fatalf("active to revoked transition: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE subscription_keys SET last_used_at=now() WHERE id='key-m03'`); err != nil {
		t.Fatalf("record invocation that started before revocation: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE subscription_keys SET status='active',revoked_at=NULL WHERE id='key-m03'`); err == nil {
		t.Fatal("revoked Subscription Key became active")
	}
}

func TestPostgreSQLMilestone03LedgerEntryMoveChecksOriginalTransaction(t *testing.T) {
	db := testdb.OpenGizPay(t).SQL
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO users(id,identity_issuer,identity_subject,email,display_name,status,created_at)
		VALUES ('user-ledger-move','https://issuer.test','subject-ledger-move','','Ledger User','active',now());
		INSERT INTO accounts(id,owner_user_id,status,created_at)
		VALUES ('account-ledger-move','user-ledger-move','active',now());
		INSERT INTO ledger_accounts(id,owner_account_id,asset_code,status)
		VALUES ('ledger-ledger-move','account-ledger-move','credit','active');
		INSERT INTO ledger_transactions(id,transaction_type,status)
		VALUES ('transaction-posted','test','pending'),('transaction-pending','test','pending');
		INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,direction,amount_microcredits)
		VALUES ('entry-posted-debit','transaction-posted','ledger-ledger-move','debit',10),
		       ('entry-posted-credit','transaction-posted','ledger-ledger-move','credit',10);
		UPDATE ledger_transactions SET status='posted' WHERE id='transaction-posted';
	`)
	if err != nil {
		t.Fatalf("create Ledger move fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE ledger_entries
		SET transaction_id='transaction-pending'
		WHERE id='entry-posted-debit'
	`); err == nil {
		t.Fatal("moving an entry away from a posted Transaction left the original Transaction unbalanced")
	}
}

func assertDefaultMerchantUniquePerAccount(t *testing.T, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) {
	t.Helper()
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO users(id,identity_issuer,identity_subject,email,display_name,status,created_at)
		VALUES ('user-default-merchant','https://issuer.test','subject-default-merchant','','Merchant User','active',now());
		INSERT INTO accounts(id,owner_user_id,status,created_at)
		VALUES ('account-default-merchant','user-default-merchant','active',now());
		INSERT INTO merchants(id,settlement_account_id,legal_name,public_name,is_default,status,created_at,updated_at)
		VALUES ('merchant-default-one','account-default-merchant','One','One',true,'active',now(),now());
	`)
	if err != nil {
		t.Fatalf("create default Merchant fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO merchants(id,settlement_account_id,legal_name,public_name,is_default,status,created_at,updated_at)
		VALUES ('merchant-default-two','account-default-merchant','Two','Two',true,'active',now(),now())
	`); err == nil {
		t.Fatal("one Account accepted two default Merchants")
	}
}
