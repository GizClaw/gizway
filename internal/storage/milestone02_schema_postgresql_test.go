package storage_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	pg_query "github.com/pganalyze/pg_query_go/v6"

	bifrostadapter "github.com/idy/gizway/internal/adapter/bifrost"
	"github.com/idy/gizway/internal/testdb"
)

// These tests describe the Milestone 02 empty-database target at the SQL
// boundary so schema constraints cannot be replaced by handler-only checks.

func TestMilestone02E2ESQLContractsParse(t *testing.T) {
	for _, name := range []string{"milestone-02-database-contract.sql", "milestone-02-ledger-contract.sql"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "tests", "e2e", "sql", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pg_query.Parse(string(raw)); err != nil {
			t.Errorf("parse %s: %v", name, err)
		}
	}
}

func TestPostgreSQLMilestone02GizPaySchemaContract(t *testing.T) {
	database := testdb.OpenGizPay(t)
	db := database.SQL

	required := map[string][]string{
		"users":                 {"id", "identity_issuer", "identity_subject", "status", "created_at"},
		"accounts":              {"id", "owner_user_id", "status", "created_at"},
		"service_principals":    {"id", "owner_user_id", "identity_issuer", "identity_subject", "status", "created_at", "revoked_at"},
		"ledger_accounts":       {"id", "owner_account_id", "asset_code", "status"},
		"ledger_transactions":   {"id", "transaction_type", "status", "created_at"},
		"ledger_entries":        {"id", "transaction_id", "ledger_account_id", "direction", "amount_microcredits"},
		"account_balances":      {"account_id", "balance_microcredits"},
		"merchants":             {"id", "settlement_account_id", "legal_name", "public_name", "status", "review_level", "created_at", "updated_at"},
		"products":              {"id", "merchant_id", "name", "billing_mode", "status", "terms_version", "created_at", "updated_at"},
		"subscriptions":         {"id", "account_id", "product_id", "status", "terms_version", "accepted_at", "created_at", "canceled_at"},
		"subscription_api_keys": {"id", "subscription_id", "key_hmac", "encrypted_key", "encryption_version", "status", "created_at", "revoked_at"},
		"payg_charges":          {"id", "external_order_id", "subscription_id", "service_principal_id", "gross_microcredits", "platform_fee_microcredits", "main_merchant_net_microcredits", "order_snapshot", "ledger_transaction_id", "created_at"},
		"charge_commissions":    {"charge_id", "merchant_id", "settlement_account_id", "amount_microcredits"},
	}
	assertPostgreSQLTableColumns(t, db, required)

	for _, table := range []string{
		"api_keys", "billing_rate_publications", "credit_holds", "credit_lots", "credit_transfers",
		"gateway_node_certificates", "gateway_nodes", "gateway_usage_records", "invoices", "merchant_accounts",
		"merchant_payment_reversals", "merchant_services", "payment_intents", "refunds", "risk_decisions", "topups",
		"webhook_endpoints",
	} {
		assertPostgreSQLTableAbsent(t, db, table)
	}

	assertPostgreSQLUniqueColumns(t, db, "users", []string{"identity_issuer", "identity_subject"})
	assertPostgreSQLUniqueColumns(t, db, "accounts", []string{"owner_user_id"})
	assertPostgreSQLUniqueColumns(t, db, "ledger_accounts", []string{"owner_account_id", "asset_code"})
	assertPostgreSQLUniqueColumns(t, db, "service_principals", []string{"identity_issuer", "identity_subject"})
	assertPostgreSQLUniqueColumns(t, db, "subscription_api_keys", []string{"key_hmac"})
	assertPostgreSQLColumnNotNull(t, db, "subscription_api_keys", "encrypted_key")
	assertPostgreSQLNonEmptyCheck(t, db, "subscription_api_keys", "encrypted_key")
	for _, field := range []struct{ table, column string }{
		{"merchants", "legal_name"}, {"merchants", "public_name"},
		{"products", "name"}, {"products", "terms_version"},
		{"subscriptions", "terms_version"},
	} {
		assertPostgreSQLTrimmedNonEmptyCheck(t, db, field.table, field.column)
	}
	assertPostgreSQLPositiveCheck(t, db, "subscription_api_keys", "encryption_version")
	assertPostgreSQLColumnAbsent(t, db, "subscription_api_keys", "hmac_version")
	assertPostgreSQLUniqueColumns(t, db, "payg_charges", []string{"external_order_id"})
	assertPostgreSQLUniqueColumns(t, db, "payg_charges", []string{"ledger_transaction_id"})
	assertPostgreSQLUniqueColumns(t, db, "charge_commissions", []string{"charge_id", "merchant_id"})
	assertPostgreSQLColumnNotNull(t, db, "payg_charges", "ledger_transaction_id")
	for _, foreignKey := range []struct {
		table, column, targetTable, targetColumn string
	}{
		{"accounts", "owner_user_id", "users", "id"},
		{"service_principals", "owner_user_id", "users", "id"},
		{"ledger_accounts", "owner_account_id", "accounts", "id"},
		{"ledger_entries", "transaction_id", "ledger_transactions", "id"},
		{"ledger_entries", "ledger_account_id", "ledger_accounts", "id"},
		{"merchants", "settlement_account_id", "accounts", "id"},
		{"products", "merchant_id", "merchants", "id"},
		{"subscriptions", "account_id", "accounts", "id"},
		{"subscriptions", "product_id", "products", "id"},
		{"subscription_api_keys", "subscription_id", "subscriptions", "id"},
		{"payg_charges", "subscription_id", "subscriptions", "id"},
		{"payg_charges", "service_principal_id", "service_principals", "id"},
		{"payg_charges", "ledger_transaction_id", "ledger_transactions", "id"},
		{"charge_commissions", "charge_id", "payg_charges", "id"},
		{"charge_commissions", "merchant_id", "merchants", "id"},
		{"charge_commissions", "settlement_account_id", "accounts", "id"},
	} {
		assertPostgreSQLForeignKeyTarget(t, db, foreignKey.table, foreignKey.column, foreignKey.targetTable, foreignKey.targetColumn)
	}
	assertPostgreSQLForeignKeyDeleteAction(t, db, "payg_charges", "service_principal_id", "NO ACTION", "RESTRICT")
	assertPostgreSQLForeignKeyDeleteAction(t, db, "payg_charges", "subscription_id", "NO ACTION", "RESTRICT")
	assertPostgreSQLForeignKeyDeleteAction(t, db, "subscription_api_keys", "subscription_id", "NO ACTION", "RESTRICT")
	assertPostgreSQLCheckValues(t, db, "products", "billing_mode", "pay_as_you_go")
	assertPostgreSQLCheckValues(t, db, "products", "status", "active", "inactive")
	assertPostgreSQLCheckValues(t, db, "subscriptions", "status", "active", "paused", "inactive")
	assertPostgreSQLCheckValues(t, db, "subscription_api_keys", "status", "active", "revoked")
	assertPostgreSQLCheckValues(t, db, "service_principals", "status", "active", "revoked")
	for _, id := range []string{"led_acct_platform", "led_clearing"} {
		var exists bool
		if err := db.GetContext(t.Context(), &exists, `SELECT EXISTS(SELECT 1 FROM ledger_accounts WHERE id=$1)`, id); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("formal GizPay initialization did not create %s", id)
		}
	}
}

func TestPostgreSQLMilestone02GizWaySchemaContract(t *testing.T) {
	database := testdb.OpenGizWay(t)
	db := database.SQL

	required := map[string][]string{
		"administrators":        {"id", "identity_issuer", "identity_subject", "status", "created_at"},
		"models":                {"id", "name", "provider_id", "provider_model", "status", "created_at", "updated_at"},
		"model_customer_prices": {"model_id", "metric", "unit_size", "price_microcredits"},
		"provider_key_billing":  {"bifrost_key_id", "beneficiary_merchant_id", "status"},
		"provider_key_prices":   {"bifrost_key_id", "model_id", "metric", "unit_size", "commission_microcredits"},
		"ai_orders":             {"id", "external_order_id", "key_hmac", "product_id", "model_id", "provider_id", "bifrost_key_id", "gross_microcredits", "commission_microcredits", "pricing_snapshot", "provider_snapshot", "status", "created_at"},
		"charge_outbox":         {"id", "external_order_id", "ai_order_id", "payload", "status", "recover_duplicate", "created_at", "updated_at"},
	}
	assertPostgreSQLTableColumns(t, db, required)

	for _, table := range []string{
		"subscription_key_states", "providers", "provider_api_keys", "provider_endpoints", "gateway_usage_metrics",
		"gateway_usage_outbox", "rate_publications", "rate_publication_versions",
	} {
		assertPostgreSQLTableAbsent(t, db, table)
	}

	assertPostgreSQLUniqueColumns(t, db, "model_customer_prices", []string{"model_id", "metric"})
	assertPostgreSQLUniqueColumns(t, db, "provider_key_billing", []string{"bifrost_key_id"})
	assertPostgreSQLUniqueColumns(t, db, "provider_key_prices", []string{"bifrost_key_id", "model_id", "metric"})
	assertPostgreSQLUniqueColumns(t, db, "ai_orders", []string{"external_order_id"})
	assertPostgreSQLUniqueColumns(t, db, "charge_outbox", []string{"external_order_id"})
	assertPostgreSQLPositiveCheck(t, db, "model_customer_prices", "unit_size")
	assertPostgreSQLPositiveCheck(t, db, "provider_key_prices", "unit_size")
	assertPostgreSQLNonNegativeCheck(t, db, "model_customer_prices", "price_microcredits")
	assertPostgreSQLNonNegativeCheck(t, db, "provider_key_prices", "commission_microcredits")
	for _, field := range []struct{ table, column string }{
		{"models", "name"}, {"models", "provider_id"}, {"models", "provider_model"},
		{"provider_key_billing", "beneficiary_merchant_id"},
	} {
		assertPostgreSQLTrimmedNonEmptyCheck(t, db, field.table, field.column)
	}
	for _, column := range []string{"provider_id", "bifrost_key_id", "gross_microcredits", "commission_microcredits", "pricing_snapshot", "provider_snapshot"} {
		assertPostgreSQLColumnNotNull(t, db, "ai_orders", column)
	}
	assertPostgreSQLNonNegativeCheck(t, db, "ai_orders", "gross_microcredits")
	assertPostgreSQLNonNegativeCheck(t, db, "ai_orders", "commission_microcredits")
	assertPostgreSQLForeignKeyTarget(t, db, "model_customer_prices", "model_id", "models", "id")
	assertPostgreSQLForeignKeyTarget(t, db, "provider_key_prices", "model_id", "models", "id")
	assertPostgreSQLForeignKeyTarget(t, db, "ai_orders", "model_id", "models", "id")
	assertPostgreSQLForeignKeyTarget(t, db, "charge_outbox", "ai_order_id", "ai_orders", "id")
	assertPostgreSQLCheckValues(t, db, "models", "status", "active", "inactive")
	assertPostgreSQLCheckValues(t, db, "provider_key_billing", "status", "active", "inactive")
	assertPostgreSQLCheckValues(t, db, "charge_outbox", "status", "pending", "sending", "reported", "abandoned")
	assertPostgreSQLNoSecretColumns(t, db)

	var persistentQuotaTables []string
	if err := db.SelectContext(t.Context(), &persistentQuotaTables, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema=current_schema()
		  AND (table_name ILIKE '%quota%' OR table_name ILIKE '%reservation%' OR table_name ILIKE '%lease%')
		ORDER BY table_name`); err != nil {
		t.Fatal(err)
	}
	if len(persistentQuotaTables) != 0 {
		t.Errorf("Milestone 02 must not persist Gateway Quota state: %v", persistentQuotaTables)
	}
}

func TestPostgreSQLMilestone02UsesOfficialBifrostStores(t *testing.T) {
	baseDSN := os.Getenv("GIZWAY_TEST_POSTGRES_DSN")
	if baseDSN == "" {
		t.Fatal("GIZWAY_TEST_POSTGRES_DSN is required; run tests through scripts/test-unit")
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	configSchema, logSchema := "bifrost_config_"+suffix, "bifrost_logs_"+suffix
	stores, err := bifrostadapter.OpenStores(t.Context(),
		bifrostadapter.StoreConfig{Type: "postgresql", DSN: baseDSN, Schema: configSchema},
		bifrostadapter.StoreConfig{Type: "postgresql", DSN: baseDSN, Schema: logSchema},
	)
	if err != nil {
		t.Fatalf("open official Bifrost stores: %v", err)
	}
	t.Cleanup(func() {
		_ = stores.Close(context.Background())
		db, openErr := sqlx.Open("postgres", baseDSN)
		if openErr == nil {
			_, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + configSchema + ` CASCADE`)
			_, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + logSchema + ` CASCADE`)
			_ = db.Close()
		}
	})

	provider := bifrostadapter.ProviderRecord{
		ID: "provider-contract", Name: "Contract Provider", Kind: "openai",
		BaseURL: "https://provider.example", Status: "active", CreatedAt: time.Now().UTC(),
	}
	if err := stores.CreateProvider(t.Context(), provider); err != nil {
		t.Fatalf("official Config Store AddProvider: %v", err)
	}
	storedProvider, err := stores.Provider(t.Context(), provider.ID)
	if err != nil {
		t.Fatalf("read Provider through Config Store adapter: %v", err)
	}
	if storedProvider.Name != provider.Name || storedProvider.Status != provider.Status {
		t.Fatalf("stored Bifrost Provider = %+v", storedProvider)
	}
	key := bifrostadapter.KeyRecord{
		ID: "key-contract", ProviderID: provider.ID, Name: "primary",
		APIKey: "provider-secret-contract", Weight: 80, Enabled: true, Status: "active",
	}
	if err := stores.CreateKey(t.Context(), key); err != nil {
		t.Fatalf("official Config Store CreateProviderKey: %v", err)
	}
	stored, err := stores.Key(t.Context(), provider.ID, key.ID)
	if err != nil {
		t.Fatalf("read Provider Key through Config Store adapter: %v", err)
	}
	if stored.APIKey != key.APIKey || stored.Weight != key.Weight || !stored.Enabled {
		t.Fatalf("stored Bifrost Key = %+v", stored)
	}

	db, err := sqlx.Open("postgres", baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, qualifiedTable := range []string{configSchema + ".config_providers", configSchema + ".config_keys", logSchema + ".logs"} {
		var exists bool
		if err := db.Get(&exists, `SELECT to_regclass($1) IS NOT NULL`, qualifiedTable); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("official Bifrost migration did not create %s", qualifiedTable)
		}
	}
	var rawSecret string
	if err := db.Get(&rawSecret, `SELECT value FROM `+configSchema+`.config_keys WHERE key_id=$1`, key.ID); err != nil {
		t.Fatal(err)
	}
	if rawSecret != key.APIKey {
		t.Fatalf("Config Store Provider Key value=%q", rawSecret)
	}

	if err := stores.WriteLog(t.Context(), map[string]any{
		"id": "log-contract", "provider_id": provider.ID, "model_id": "model-contract",
		"selected_key_id": key.ID, "gross_microcredits": int64(7), "created_at": time.Now().UTC(),
	}); err != nil {
		t.Fatalf("official Log Store Create: %v", err)
	}
	logs, err := stores.LogsList(t.Context())
	if err != nil || len(logs) != 1 || logs[0]["selected_key_id"] != key.ID {
		t.Fatalf("official Log Store round trip logs=%v err=%v", logs, err)
	}
}

func assertPostgreSQLTableColumns(t *testing.T, db *sqlx.DB, required map[string][]string) {
	t.Helper()
	tables := make([]string, 0, len(required))
	for table := range required {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		t.Run("table_"+table, func(t *testing.T) {
			var columns []string
			if err := db.SelectContext(t.Context(), &columns, `
				SELECT column_name FROM information_schema.columns
				WHERE table_schema=current_schema() AND table_name=$1
				ORDER BY ordinal_position`, table); err != nil {
				t.Fatal(err)
			}
			if len(columns) == 0 {
				t.Fatalf("missing Milestone 02 table %s", table)
			}
			actual := make(map[string]bool, len(columns))
			for _, column := range columns {
				actual[column] = true
			}
			for _, column := range required[table] {
				if !actual[column] {
					t.Errorf("table %s missing column %s; columns=%v", table, column, columns)
				}
			}
		})
	}
}

func assertPostgreSQLTableAbsent(t *testing.T, db *sqlx.DB, table string) {
	t.Helper()
	t.Run("legacy_table_absent_"+table, func(t *testing.T) {
		var exists bool
		if err := db.GetContext(t.Context(), &exists, `SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL`, table); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("legacy or wrong-owner table %s must not exist in Milestone 02", table)
		}
	})
}

func assertPostgreSQLUniqueColumns(t *testing.T, db *sqlx.DB, table string, columns []string) {
	t.Helper()
	t.Run("unique_"+table+"_"+strings.Join(columns, "_"), func(t *testing.T) {
		var found bool
		if err := db.GetContext(t.Context(), &found, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_constraint c
				JOIN pg_class r ON r.oid=c.conrelid
				JOIN pg_namespace n ON n.oid=r.relnamespace
				WHERE n.nspname=current_schema() AND r.relname=$1 AND c.contype IN ('u','p')
				AND ARRAY(
					SELECT a.attname::text
					FROM unnest(c.conkey) WITH ORDINALITY AS k(attnum, ord)
					JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.attnum
					ORDER BY k.ord
				) = $2::text[]
			)`, table, "{"+strings.Join(columns, ",")+"}"); err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Errorf("table %s lacks unique constraint on (%s)", table, strings.Join(columns, ", "))
		}
	})
}

func assertPostgreSQLColumnNotNull(t *testing.T, db *sqlx.DB, table, column string) {
	t.Helper()
	t.Run("not_null_"+table+"_"+column, func(t *testing.T) {
		var nullable string
		if err := db.GetContext(t.Context(), &nullable, `
			SELECT is_nullable FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2`, table, column); err != nil {
			if err == sql.ErrNoRows {
				t.Fatalf("missing %s.%s", table, column)
			}
			t.Fatal(err)
		}
		if nullable != "NO" {
			t.Errorf("%s.%s must be NOT NULL", table, column)
		}
	})
}

func assertPostgreSQLColumnAbsent(t *testing.T, db *sqlx.DB, table, column string) {
	t.Helper()
	t.Run("column_absent_"+table+"_"+column, func(t *testing.T) {
		var exists bool
		if err := db.GetContext(t.Context(), &exists, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2
			)`, table, column); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("%s.%s must not exist in Milestone 02", table, column)
		}
	})
}

func assertPostgreSQLForeignKeyDeleteAction(t *testing.T, db *sqlx.DB, table, column string, allowed ...string) {
	t.Helper()
	t.Run("delete_action_"+table+"_"+column, func(t *testing.T) {
		var action string
		if err := db.GetContext(t.Context(), &action, `
			SELECT rc.delete_rule
			FROM information_schema.referential_constraints rc
			JOIN information_schema.key_column_usage k
			  ON k.constraint_schema=rc.constraint_schema AND k.constraint_name=rc.constraint_name
			WHERE k.table_schema=current_schema() AND k.table_name=$1 AND k.column_name=$2`, table, column); err != nil {
			if err == sql.ErrNoRows {
				t.Fatalf("missing foreign key %s.%s", table, column)
			}
			t.Fatal(err)
		}
		if slices.Contains(allowed, action) {
			return
		}
		t.Errorf("%s.%s delete action=%s, want one of %v", table, column, action, allowed)
	})
}

func assertPostgreSQLForeignKeyTarget(t *testing.T, db *sqlx.DB, table, column, targetTable, targetColumn string) {
	t.Helper()
	t.Run("foreign_key_"+table+"_"+column, func(t *testing.T) {
		var found bool
		if err := db.GetContext(t.Context(), &found, `
			SELECT EXISTS (
				SELECT 1
				FROM information_schema.referential_constraints rc
				JOIN information_schema.key_column_usage source
				  ON source.constraint_schema=rc.constraint_schema AND source.constraint_name=rc.constraint_name
				JOIN information_schema.constraint_column_usage target
				  ON target.constraint_schema=rc.unique_constraint_schema AND target.constraint_name=rc.unique_constraint_name
				WHERE source.table_schema=current_schema() AND source.table_name=$1 AND source.column_name=$2
				  AND target.table_schema=current_schema() AND target.table_name=$3 AND target.column_name=$4
			)`, table, column, targetTable, targetColumn); err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Errorf("%s.%s must reference %s.%s", table, column, targetTable, targetColumn)
		}
	})
}

func assertPostgreSQLPositiveCheck(t *testing.T, db *sqlx.DB, table, column string) {
	t.Helper()
	t.Run("positive_"+table+"_"+column, func(t *testing.T) {
		var definitions []string
		if err := db.SelectContext(t.Context(), &definitions, `
			SELECT pg_get_constraintdef(c.oid)
			FROM pg_constraint c
			JOIN pg_class r ON r.oid=c.conrelid
			JOIN pg_namespace n ON n.oid=r.relnamespace
			WHERE n.nspname=current_schema() AND r.relname=$1 AND c.contype='c'`, table); err != nil {
			t.Fatal(err)
		}
		needle := column + " > 0"
		for _, definition := range definitions {
			normalized := strings.NewReplacer("(", "", ")", "", "::bigint", "", "::integer", "").Replace(definition)
			if strings.Contains(normalized, needle) {
				return
			}
		}
		t.Errorf("table %s lacks positive check for %s; checks=%s", table, column, fmt.Sprint(definitions))
	})
}

func assertPostgreSQLNonNegativeCheck(t *testing.T, db *sqlx.DB, table, column string) {
	t.Helper()
	t.Run("non_negative_"+table+"_"+column, func(t *testing.T) {
		var definitions []string
		if err := db.SelectContext(t.Context(), &definitions, `
			SELECT pg_get_constraintdef(c.oid)
			FROM pg_constraint c
			JOIN pg_class r ON r.oid=c.conrelid
			JOIN pg_namespace n ON n.oid=r.relnamespace
			WHERE n.nspname=current_schema() AND r.relname=$1 AND c.contype='c'`, table); err != nil {
			t.Fatal(err)
		}
		for _, definition := range definitions {
			normalized := strings.NewReplacer("(", "", ")", "", "::bigint", "", "::integer", "").Replace(definition)
			if strings.Contains(normalized, column+" >= 0") {
				return
			}
		}
		t.Errorf("table %s must allow zero and reject negative %s; checks=%s", table, column, fmt.Sprint(definitions))
	})
}

func assertPostgreSQLNonEmptyCheck(t *testing.T, db *sqlx.DB, table, column string) {
	t.Helper()
	t.Run("non_empty_"+table+"_"+column, func(t *testing.T) {
		var definitions []string
		if err := db.SelectContext(t.Context(), &definitions, `
			SELECT pg_get_constraintdef(c.oid)
			FROM pg_constraint c
			JOIN pg_class r ON r.oid=c.conrelid
			JOIN pg_namespace n ON n.oid=r.relnamespace
			WHERE n.nspname=current_schema() AND r.relname=$1 AND c.contype='c'`, table); err != nil {
			t.Fatal(err)
		}
		for _, definition := range definitions {
			normalized := strings.NewReplacer("(", "", ")", "", "::bigint", "", "::integer", "").Replace(definition)
			if strings.Contains(normalized, "octet_length"+column+" > 0") ||
				strings.Contains(normalized, "length"+column+" > 0") {
				return
			}
		}
		t.Errorf("table %s must reject an empty %s; checks=%s", table, column, fmt.Sprint(definitions))
	})
}

func assertPostgreSQLTrimmedNonEmptyCheck(t *testing.T, db *sqlx.DB, table, column string) {
	t.Helper()
	t.Run("trimmed_non_empty_"+table+"_"+column, func(t *testing.T) {
		var definitions []string
		if err := db.SelectContext(t.Context(), &definitions, `
			SELECT pg_get_constraintdef(c.oid)
			FROM pg_constraint c
			JOIN pg_class r ON r.oid=c.conrelid
			JOIN pg_namespace n ON n.oid=r.relnamespace
			WHERE n.nspname=current_schema() AND r.relname=$1 AND c.contype='c'`, table); err != nil {
			t.Fatal(err)
		}
		needle := "lengthtrimbothfrom" + column + ">0"
		for _, definition := range definitions {
			normalized := strings.NewReplacer("(", "", ")", "", " ", "").Replace(strings.ToLower(definition))
			if strings.Contains(normalized, needle) {
				return
			}
		}
		t.Errorf("table %s must reject blank %s values; checks=%s", table, column, fmt.Sprint(definitions))
	})
}

func assertPostgreSQLNoSecretColumns(t *testing.T, db *sqlx.DB) {
	t.Helper()
	t.Run("gizway_does_not_copy_provider_secrets", func(t *testing.T) {
		var columns []string
		if err := db.SelectContext(t.Context(), &columns, `
			SELECT table_name || '.' || column_name
			FROM information_schema.columns
			WHERE table_schema=current_schema()
			  AND column_name = ANY($1::text[])
			ORDER BY table_name, column_name`, "{api_key,provider_api_key,provider_secret,credential,credential_ref,encrypted_key,secret}"); err != nil {
			t.Fatal(err)
		}
		if len(columns) != 0 {
			t.Errorf("GizWay must keep only bifrost_key_id billing references, not Provider secrets: %v", columns)
		}
	})
}

func assertPostgreSQLCheckValues(t *testing.T, db *sqlx.DB, table, column string, values ...string) {
	t.Helper()
	t.Run("enum_"+table+"_"+column, func(t *testing.T) {
		var definitions []string
		if err := db.SelectContext(t.Context(), &definitions, `
			SELECT pg_get_constraintdef(c.oid)
			FROM pg_constraint c
			JOIN pg_class r ON r.oid=c.conrelid
			JOIN pg_namespace n ON n.oid=r.relnamespace
			WHERE n.nspname=current_schema() AND r.relname=$1 AND c.contype='c'
			  AND pg_get_constraintdef(c.oid) ILIKE '%' || $2 || '%'`, table, column); err != nil {
			t.Fatal(err)
		}
		var enumLabels []string
		if err := db.SelectContext(t.Context(), &enumLabels, `
			SELECT enum.enumlabel
			FROM pg_class relation
			JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
			JOIN pg_attribute attribute ON attribute.attrelid=relation.oid
			JOIN pg_type type ON type.oid=attribute.atttypid
			JOIN pg_enum enum ON enum.enumtypid=type.oid
			WHERE namespace.nspname=current_schema() AND relation.relname=$1
			  AND attribute.attname=$2
			ORDER BY enum.enumsortorder`, table, column); err != nil {
			t.Fatal(err)
		}
		actual := enumLabels
		if len(actual) == 0 {
			quoted := regexp.MustCompile(`'([^']+)'`)
			seen := map[string]bool{}
			for _, definition := range definitions {
				for _, match := range quoted.FindAllStringSubmatch(definition, -1) {
					seen[match[1]] = true
				}
			}
			for value := range seen {
				actual = append(actual, value)
			}
		}
		want := append([]string(nil), values...)
		sort.Strings(actual)
		sort.Strings(want)
		if strings.Join(actual, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("%s.%s allows %v, want exactly %v; checks=%s", table, column, actual, want, strings.Join(definitions, " "))
		}
	})
}

func TestPostgreSQLMilestone02RevocationIsIrreversible(t *testing.T) {
	database := testdb.OpenGizPay(t)
	db := database.SQL
	ctx := context.Background()

	// The fixture uses raw SQL on purpose: these invariants must survive every
	// future Store implementation and cannot depend on an HTTP handler behaving.
	_, err := db.ExecContext(ctx, `
		INSERT INTO users(id, identity_issuer, identity_subject, status, created_at)
		VALUES ('user-soft', 'https://issuer.test', 'human-soft', 'active', now());
		INSERT INTO accounts(id, owner_user_id, status, created_at)
		VALUES ('account-soft', 'user-soft', 'active', now());
		INSERT INTO service_principals(id, owner_user_id, identity_issuer, identity_subject, status, created_at)
		VALUES ('principal-soft', 'user-soft', 'https://issuer.test', 'machine-soft', 'active', now());
		UPDATE service_principals SET status='revoked', revoked_at=now() WHERE id='principal-soft';
	`)
	if err != nil {
		t.Fatalf("create and revoke service principal fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE service_principals SET status='active' WHERE id='principal-soft'`); err == nil {
		t.Fatal("revoked service principal became active again")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM service_principals WHERE id='principal-soft'`); err == nil {
		t.Fatal("revoked service principal was physically deleted")
	}
	var servicePrincipalCount int
	if err := db.GetContext(ctx, &servicePrincipalCount, `SELECT count(*) FROM service_principals WHERE id='principal-soft'`); err != nil {
		t.Fatal(err)
	}
	if servicePrincipalCount != 1 {
		t.Fatalf("revoked service principal count=%d, want permanent row", servicePrincipalCount)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO merchants(id, settlement_account_id, legal_name, public_name, status, review_level, created_at, updated_at)
		VALUES ('merchant-soft', 'account-soft', 'Soft Merchant LLC', 'Soft Merchant', 'active', 'basic', now(), now());
		INSERT INTO products(id, merchant_id, name, billing_mode, status, terms_version, created_at, updated_at)
		VALUES ('product-soft', 'merchant-soft', 'Soft PAYG', 'pay_as_you_go', 'active', 'v1', now(), now());
		INSERT INTO subscriptions(id, account_id, product_id, status, terms_version, accepted_at, created_at)
		VALUES ('subscription-soft', 'account-soft', 'product-soft', 'active', 'v1', now(), now());
		INSERT INTO subscription_api_keys(id, subscription_id, key_hmac, encrypted_key, encryption_version, status, created_at)
		VALUES ('key-soft', 'subscription-soft', 'hmac-soft', decode('001122', 'hex'), 1, 'active', now());
		UPDATE subscription_api_keys SET status='revoked', revoked_at=now() WHERE id='key-soft';
	`)
	if err != nil {
		t.Fatalf("create and revoke Subscription API Key fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE subscription_api_keys SET status='active' WHERE id='key-soft'`); err == nil {
		t.Fatal("revoked Subscription API Key became active again")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM subscription_api_keys WHERE id='key-soft'`); err == nil {
		t.Fatal("revoked Subscription API Key was physically deleted")
	}
	var subscriptionKeyCount int
	if err := db.GetContext(ctx, &subscriptionKeyCount, `SELECT count(*) FROM subscription_api_keys WHERE id='key-soft'`); err != nil {
		t.Fatal(err)
	}
	if subscriptionKeyCount != 1 {
		t.Fatalf("revoked Subscription API Key count=%d, want permanent row", subscriptionKeyCount)
	}
}

func TestPostgreSQLMilestone02ChecksBothTransactionsWhenMovingPostedLedgerEntry(t *testing.T) {
	database := testdb.OpenGizPay(t)
	db := database.SQL
	_, err := db.ExecContext(t.Context(), `
		INSERT INTO ledger_transactions(id,transaction_type,status,created_at) VALUES
		 ('txn-move-a','fixture','pending',now()),('txn-move-b','fixture','pending',now());
		INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,direction,amount_microcredits) VALUES
		 ('entry-move-a-debit','txn-move-a','led_acct_platform','debit',10),
		 ('entry-move-a-credit','txn-move-a','led_clearing','credit',10),
		 ('entry-move-b-debit','txn-move-b','led_acct_platform','debit',20),
		 ('entry-move-b-credit','txn-move-b','led_clearing','credit',20);
		UPDATE ledger_transactions SET status='posted' WHERE id IN ('txn-move-a','txn-move-b');
	`)
	if err != nil {
		t.Fatalf("create balanced posted transactions: %v", err)
	}
	tx, err := db.BeginTxx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(t.Context(), `
		UPDATE ledger_entries SET transaction_id='txn-move-b' WHERE id='entry-move-a-debit';
		INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,direction,amount_microcredits)
		VALUES ('entry-move-b-balancing-credit','txn-move-b','led_clearing','credit',10)
	`); err != nil {
		_ = tx.Rollback()
		t.Fatalf("prepare final-state Entry move: %v", err)
	}
	// Transaction B is balanced in the final state (30 debit, 30 credit).
	// Only Transaction A is unbalanced, so a trigger that checks B alone would
	// incorrectly allow this commit.
	if err = tx.Commit(); err == nil {
		t.Fatal("moving an Entry out of a posted transaction bypassed the old Transaction balance check")
	}
	var transactionID string
	if err := db.GetContext(t.Context(), &transactionID, `SELECT transaction_id FROM ledger_entries WHERE id='entry-move-a-debit'`); err != nil {
		t.Fatal(err)
	}
	if transactionID != "txn-move-a" {
		t.Fatalf("failed Entry move persisted transaction_id=%q", transactionID)
	}
	var balancingEntryCount int
	if err := db.GetContext(t.Context(), &balancingEntryCount, `SELECT count(*) FROM ledger_entries WHERE id='entry-move-b-balancing-credit'`); err != nil {
		t.Fatal(err)
	}
	if balancingEntryCount != 0 {
		t.Fatalf("failed Entry move persisted %d balancing entries", balancingEntryCount)
	}
}
