package storage_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/testdb"
)

// TestPostgreSQLSeparatedSchemas is an executable architecture boundary, not a
// migration compatibility test. Gizway is pre-launch: each binary initializes
// a fresh database containing only the tables it owns. Keeping the forbidden
// table assertions beside startup makes accidental cross-plane coupling fail
// before a deployment can put customer identity in a regional database or AI
// routing credentials in the central payment database.
func TestPostgreSQLSeparatedSchemas(t *testing.T) {
	tests := []struct {
		name      string
		open      func(string, bool) (*storage.Storage, error)
		required  []string
		forbidden []string
	}{
		{
			name: "GizPay owns identity money and received usage only",
			open: storage.OpenGizPayPostgreSQL,
			required: []string{
				"accounts", "api_keys", "billing_rate_publications",
				"credit_holds", "gateway_nodes", "gateway_usage_records",
				"ledger_entries", "payment_intents", "users",
			},
			forbidden: []string{
				"credit_reservations", "gateway_executions", "gateway_requests",
				"gateway_usage_outbox", "model_variants", "providers",
				"quota_exchanges", "quota_leases", "quota_windows",
			},
		},
		{
			name: "GizWay owns catalog execution and transient usage only",
			open: storage.OpenGizWayPostgreSQL,
			required: []string{
				"gateway_executions", "gateway_usage_metrics", "gateway_usage_outbox",
				"model_variants", "models", "provider_endpoints", "providers",
				"rate_publications", "rate_publication_versions",
			},
			forbidden: []string{
				"account_model_entitlements", "accounts", "api_keys", "billing_rate_publications", "credit_holds",
				"credit_reservations", "gateway_nodes", "gateway_usage_records",
				"ledger_entries", "payment_intents", "quota_exchanges", "quota_leases",
				"quota_windows", "users",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := testdb.NewSchema(t)
			database, err := test.open(dsn, true)
			if err != nil {
				t.Fatalf("initialize fresh schema: %v", err)
			}
			defer database.Close()

			assertTables(t, database, test.required, true)
			assertTables(t, database, test.forbidden, false)
			if test.name == "GizWay owns catalog execution and transient usage only" {
				var legacyFunctions int
				if err := database.SQL.Get(&legacyFunctions, `SELECT COUNT(*) FROM pg_proc
					WHERE pronamespace=(SELECT oid FROM pg_namespace WHERE nspname=current_schema())
					AND proname IN ('assert_ledger_transaction_balanced','prevent_posted_ledger_entry_mutation','prevent_posted_ledger_transaction_mutation')`); err != nil || legacyFunctions != 0 {
					t.Fatalf("regional legacy ledger functions=%d err=%v", legacyFunctions, err)
				}
			}
			var migrationCount int
			if err := database.SQL.Get(&migrationCount, `SELECT COUNT(*) FROM schema_migrations`); err != nil || migrationCount != 1 {
				t.Fatalf("migration history count=%d err=%v, want 1", migrationCount, err)
			}
			// Deployment initialization may be retried after a process interruption;
			// already-applied versions are not replayed.
			reopened, err := test.open(dsn, true)
			if err != nil {
				t.Fatalf("retry service migrations: %v", err)
			}
			defer reopened.Close()
		})
	}
}

func TestPostgreSQLServiceMigrationHistoryRejectsTheOtherBinary(t *testing.T) {
	dsn := testdb.NewSchema(t)
	database, err := storage.OpenGizPayPostgreSQL(dsn, true)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := storage.OpenGizWayPostgreSQL(dsn, true); err == nil || !strings.Contains(err.Error(), "database belongs to gizpay") {
		t.Fatalf("GizWay initialized GizPay database: %v", err)
	}
}

func TestPostgreSQLServiceSchemasOwnAuthenticationAndHotPathIndexes(t *testing.T) {
	t.Run("GizPay", func(t *testing.T) {
		database, err := storage.OpenGizPayPostgreSQL(testdb.NewSchema(t), true)
		if err != nil {
			t.Fatal(err)
		}
		defer database.Close()
		assertIndexes(t, database, []string{
			"admin_api_keys_secret_hash_uidx",
			"api_keys_secret_hash_uidx",
			"gateway_usage_records_account_received_idx",
			"gateway_usage_records_node_received_idx",
			"ledger_accounts_owner_asset_uidx",
			"ledger_entries_ledger_account_idx",
		})

		at := "2026-08-12T00:00:00.000000000Z"
		if _, err := database.SQL.Exec(`INSERT INTO users(id,email,created_at,updated_at) VALUES ('user-index','index@test.invalid',$1,$1)`, at); err != nil {
			t.Fatal(err)
		}
		if _, err := database.SQL.Exec(`INSERT INTO accounts(id,owner_user_id,kind,name,created_at,updated_at) VALUES ('account-index','user-index','personal','Index',$1,$1)`, at); err != nil {
			t.Fatal(err)
		}
		if _, err := database.SQL.Exec(`INSERT INTO api_keys(id,account_id,kind,name,key_prefix,secret_hash,created_at) VALUES ('key-one','account-index','gateway','one','giz_one',decode('aa','hex'),$1)`, at); err != nil {
			t.Fatal(err)
		}
		if _, err := database.SQL.Exec(`INSERT INTO api_keys(id,account_id,kind,name,key_prefix,secret_hash,created_at) VALUES ('key-two','account-index','gateway','two','giz_two',decode('aa','hex'),$1)`, at); err == nil {
			t.Fatal("duplicate API key secret hash was accepted")
		}
		assertAdminAPIKeyHashUnique(t, database, at, "pay")
		if _, err := database.SQL.Exec(`INSERT INTO ledger_accounts(id,owner_account_id,code,kind,normal_balance,created_at,updated_at) VALUES ('ledger-one','account-index','USER:index:one','user_credit','credit',$1,$1)`, at); err != nil {
			t.Fatal(err)
		}
		if _, err := database.SQL.Exec(`INSERT INTO ledger_accounts(id,owner_account_id,code,kind,normal_balance,created_at,updated_at) VALUES ('ledger-two','account-index','USER:index:two','user_credit','credit',$1,$1)`, at); err == nil {
			t.Fatal("duplicate owner and asset ledger account was accepted")
		}
	})

	t.Run("GizWay", func(t *testing.T) {
		database, err := storage.OpenGizWayPostgreSQL(testdb.NewSchema(t), true)
		if err != nil {
			t.Fatal(err)
		}
		defer database.Close()
		assertIndexes(t, database, []string{"admin_api_keys_secret_hash_uidx", "gateway_usage_outbox_claim_idx"})
		assertAdminAPIKeyHashUnique(t, database, "2026-08-12T00:00:00.000000000Z", "way")
	})
}

func assertAdminAPIKeyHashUnique(t *testing.T, database *storage.Storage, at, suffix string) {
	t.Helper()
	administratorID := "administrator-" + suffix
	if _, err := database.SQL.Exec(`INSERT INTO administrators(id,email,display_name,password_hash,status,created_at,updated_at)
		VALUES ($1,$2,'Index Administrator','hash','active',$3,$3)`, administratorID, suffix+"@index.invalid", at); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO admin_api_keys(id,administrator_id,name,key_prefix,secret_hash,status,created_at)
		VALUES ($1,$2,'one',$3,decode('bb','hex'),'active',$4)`, "admin-key-one-"+suffix, administratorID, "gizadm_one_"+suffix, at); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO admin_api_keys(id,administrator_id,name,key_prefix,secret_hash,status,created_at)
		VALUES ($1,$2,'two',$3,decode('bb','hex'),'active',$4)`, "admin-key-two-"+suffix, administratorID, "gizadm_two_"+suffix, at); err == nil {
		t.Fatal("duplicate administrator API key secret hash was accepted")
	}
}

func assertIndexes(t *testing.T, database *storage.Storage, names []string) {
	t.Helper()
	for _, name := range names {
		var exists bool
		if err := database.SQL.Get(&exists, `SELECT EXISTS (
			SELECT 1 FROM pg_indexes WHERE schemaname=current_schema() AND indexname=$1
		)`, name); err != nil || !exists {
			t.Fatalf("index %s exists=%t err=%v", name, exists, err)
		}
	}
}

func assertTables(t *testing.T, database *storage.Storage, names []string, want bool) {
	t.Helper()
	sort.Strings(names)
	for _, name := range names {
		var exists bool
		if err := database.SQL.GetContext(context.Background(), &exists, `
			SELECT EXISTS (
				SELECT 1
				FROM information_schema.tables
				WHERE table_schema = current_schema() AND table_name = $1
			)
		`, name); err != nil {
			t.Fatalf("look up table %s: %v", name, err)
		}
		if exists != want {
			t.Fatalf("table %s existence=%t, want %t; checked %s", name, exists, want, strings.Join(names, ", "))
		}
	}
}

func TestOpenSeparatedPostgreSQLModes(t *testing.T) {
	t.Run("empty GizPay initialized", func(t *testing.T) {
		database, err := storage.OpenGizPayPostgreSQL(testdb.NewSchema(t), true)
		if err != nil {
			t.Fatalf("OpenGizPayPostgreSQL: %v", err)
		}
		defer database.Close()
		var users int
		if err := database.SQL.Get(&users, `SELECT COUNT(*) FROM users`); err != nil || users != 0 {
			t.Fatalf("empty users=%d err=%v", users, err)
		}
	})

	t.Run("GizPay story seed and reopen", func(t *testing.T) {
		dsn := testdb.NewSchema(t)
		database, err := storage.OpenGizPayStoryPostgreSQL(dsn)
		if err != nil {
			t.Fatalf("OpenGizPayStoryPostgreSQL: %v", err)
		}
		var users int
		if err := database.SQL.Get(&users, `SELECT COUNT(*) FROM users`); err != nil || users != 3 {
			t.Fatalf("story users=%d err=%v", users, err)
		}
		reopened, err := storage.OpenExistingPostgreSQL(dsn)
		if err != nil {
			_ = database.Close()
			t.Fatalf("OpenExistingPostgreSQL: %v", err)
		}
		defer reopened.Close()
		defer database.Close()
		if err := reopened.SQL.Get(&users, `SELECT COUNT(*) FROM users`); err != nil || users != 3 {
			t.Fatalf("reopened users=%d err=%v", users, err)
		}
	})
}

func TestOpenSeparatedPostgreSQLRequiresLiveDatabase(t *testing.T) {
	_, err := storage.OpenGizPayPostgreSQL("host=127.0.0.1 port=1 user=gizway dbname=gizway sslmode=disable connect_timeout=1", false)
	if err == nil {
		t.Fatal("OpenPostgreSQL without a live database succeeded")
	}
}

func TestOpenSeparatedPostgreSQLInitializationIsRetryable(t *testing.T) {
	dsn := testdb.NewSchema(t)
	first, err := storage.OpenGizPayPostgreSQL(dsn, true)
	if err != nil {
		t.Fatalf("first OpenGizPayPostgreSQL: %v", err)
	}
	defer first.Close()
	reopened, err := storage.OpenGizPayPostgreSQL(dsn, true)
	if err != nil {
		t.Fatalf("retry GizPay initialization: %v", err)
	}
	defer reopened.Close()

	empty, err := storage.OpenExistingPostgreSQL(testdb.NewSchema(t))
	if err != nil {
		t.Fatalf("open uninitialized PostgreSQL: %v", err)
	}
	if err := empty.Close(); err != nil {
		t.Fatalf("close uninitialized storage: %v", err)
	}
}
