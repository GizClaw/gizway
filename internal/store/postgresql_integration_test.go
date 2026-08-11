package store_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/store"
)

// postgresTestStore creates one exact disposable schema inside the explicitly
// supplied test database. It never drops a database or a caller-owned schema.
// scripts/test-unit/test-unit-postgresql.sh supplies a disposable PostgreSQL
// instance and makes this production-authoritative suite mandatory in CI.
func postgresTestStore(t *testing.T) (*store.Store, *sqlx.DB) {
	t.Helper()
	dsn := os.Getenv("GIZWAY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required for PostgreSQL integration")
	}
	schema := "gizway_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	bootstrap, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.ExecContext(t.Context(), `CREATE SCHEMA `+schema); err != nil {
		bootstrap.Close()
		t.Fatalf("create PostgreSQL test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = bootstrap.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		_ = bootstrap.Close()
	})
	database, err := storage.OpenPostgreSQL(postgresSearchPathDSN(t, dsn, schema), true)
	if err != nil {
		t.Fatalf("migrate empty PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return store.New(database.SQL), database.SQL
}

func postgresSearchPathDSN(t *testing.T, dsn, schema string) string {
	t.Helper()
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return dsn + " search_path=" + schema
}

func TestPostgreSQLMigrationAndConcurrentSpend(t *testing.T) {
	repository, db := postgresTestStore(t)
	const now = "2026-08-10T00:00:00.000000000Z"
	statements := []string{
		`INSERT INTO users(id,email,status,created_at,updated_at) VALUES
		  ('u1','pg-u1@gizway.test','active',$1,$1),('u2','pg-u2@gizway.test','active',$1,$1),('u3','pg-u3@gizway.test','active',$1,$1),('u4','pg-u4@gizway.test','active',$1,$1)`,
		`INSERT INTO accounts(id,owner_user_id,kind,name,status,created_at,updated_at) VALUES
		  ('a1','u1','personal','sender','active',$1,$1),('a2','u2','personal','recipient two','active',$1,$1),('a3','u3','personal','recipient three','active',$1,$1),('a4','u4','personal','gateway sender','active',$1,$1)`,
		`INSERT INTO ledger_accounts(id,owner_account_id,code,kind,asset_code,normal_balance,status,created_at,updated_at) VALUES
		  ('l1','a1','PG:USER:1','user_credit','GIZ_CREDIT','credit','active',$1,$1),
		  ('l2','a2','PG:USER:2','user_credit','GIZ_CREDIT','credit','active',$1,$1),
		  ('l3','a3','PG:USER:3','user_credit','GIZ_CREDIT','credit','active',$1,$1),
		  ('l4','a4','PG:USER:4','user_credit','GIZ_CREDIT','credit','active',$1,$1),
		  ('ls',NULL,'PG:SYSTEM','system_credit_liability','GIZ_CREDIT','debit','active',$1,$1)`,
		`INSERT INTO ledger_transactions(id,transaction_type,status,idempotency_key,reference_type,reference_id,description,created_at,posted_at)
          VALUES('opening','adjustment','posted','pg-opening','test','opening','opening balance',$1,$1)`,
		`INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,sequence,direction,amount_microcredits,created_at) VALUES
		  ('opening-system','opening','ls',1,'debit',100,$1),('opening-user','opening','l1',2,'credit',100,$1)`,
		`INSERT INTO ledger_transactions(id,transaction_type,status,idempotency_key,reference_type,reference_id,description,created_at,posted_at)
		  VALUES('gateway-opening','adjustment','posted','pg-gateway-opening','test','gateway-opening','gateway opening balance',$1,$1)`,
		`INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,sequence,direction,amount_microcredits,created_at) VALUES
		  ('gateway-opening-system','gateway-opening','ls',1,'debit',100,$1),('gateway-opening-user','gateway-opening','l4',2,'credit',100,$1)`,
		`INSERT INTO api_keys(id,account_id,name,kind,key_prefix,secret_hash,scopes,status,created_at) VALUES
		  ('gateway-key','a4','PG Gateway','gateway','pg_gateway',decode(repeat('00',32),'hex'),'["gateway:invoke"]','active',$1)`,
		`INSERT INTO providers(id,slug,name,status,created_at,updated_at) VALUES ('provider','pg-provider','PG Provider','active',$1,$1)`,
		`INSERT INTO provider_endpoints(id,provider_id,name,base_url,credential_ref,priority,weight,status,created_at,updated_at) VALUES
		  ('endpoint','provider','PG Endpoint','https://provider.invalid','credential',1,100,'active',$1,$1)`,
		`INSERT INTO models(id,slug,name,modality,status,metadata,created_at,updated_at) VALUES ('model','pg-model','PG Model','["text"]','active','{}',$1,$1)`,
		`INSERT INTO model_variants(id,model_id,provider_endpoint_id,provider_model_name,variant_slug,capabilities,status,created_at,updated_at) VALUES
		  ('variant','model','endpoint','pg-wire-model','primary','{"chat":true}','active',$1,$1)`,
		`INSERT INTO merchant_accounts(account_id,owner_user_id,legal_name,public_name,review_level,merchant_status,created_at,updated_at) VALUES
		  ('a2','u2','PG Merchant LLC','PG Merchant','basic','approved',$1,$1)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(t.Context(), statement, now); err != nil {
			t.Fatalf("seed PostgreSQL concurrency fixture: %v", err)
		}
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for index, recipient := range []string{"a2", "a3"} {
		wait.Add(1)
		go func(index int, recipient string) {
			defer wait.Done()
			<-start
			payload := sha256.Sum256([]byte(recipient))
			completedAt := now
			_, _, err := repository.CreateCreditTransfer(context.Background(), "u1", fmt.Sprintf("pg-concurrent-%d", index), payload[:], store.CreditTransfer{
				ID: fmt.Sprintf("transfer-%d", index), SenderAccountID: "a1", RecipientAccountID: recipient,
				Amount: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 80}, Status: "succeeded",
				CreatedAt: now, CompletedAt: &completedAt,
			})
			results <- err
		}(index, recipient)
	}
	close(start)
	wait.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, store.ErrInsufficientBalance) {
			t.Fatalf("concurrent transfer returned non-business error after serialization retry: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent 80+80 spend from balance 100 succeeded %d times", succeeded)
	}
	var sender, recipients int64
	if err := db.GetContext(t.Context(), &sender, `SELECT balance_microcredits FROM account_balances WHERE account_id='a1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.GetContext(t.Context(), &recipients, `SELECT COALESCE(SUM(balance_microcredits),0) FROM account_balances WHERE account_id IN ('a2','a3')`); err != nil {
		t.Fatal(err)
	}
	if sender != 20 || recipients != 80 {
		t.Fatalf("post-concurrency balances sender=%d recipients=%d", sender, recipients)
	}

	start = make(chan struct{})
	results = make(chan error, 2)
	for index := range 2 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, err := repository.BeginGatewayCommand(context.Background(), store.GatewayCommand{
				ID: fmt.Sprintf("gateway-request-%d", index), AccountID: "a4", APIKeyID: "gateway-key",
				ModelID: "model", VariantID: "variant", Operation: "chat.completions",
				IdempotencyKey: fmt.Sprintf("pg-gateway-%d", index), PayloadHash: []byte{byte(index)},
				ReserveAmount: 80, StartedAt: now, ExecutionSnapshot: []byte(`{"plan":"postgres"}`),
			})
			results <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	succeeded = 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, store.ErrInsufficientBalance) {
			t.Fatalf("concurrent Gateway reservation returned non-business error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent Gateway reservations succeeded %d times", succeeded)
	}
	var activeReserved int64
	if err := db.GetContext(t.Context(), &activeReserved, `SELECT COALESCE(SUM(amount_microcredits),0) FROM credit_reservations WHERE account_id='a4' AND status='active'`); err != nil || activeReserved != 80 {
		t.Fatalf("active Gateway reservation=%d err=%v", activeReserved, err)
	}
	blockedAt := now
	blockedPayload := sha256.Sum256([]byte("reserved transfer"))
	if _, _, err := repository.CreateCreditTransfer(t.Context(), "u4", "pg-reserved-transfer", blockedPayload[:], store.CreditTransfer{ID: "pg-reserved-transfer", SenderAccountID: "a4", RecipientAccountID: "a2", Amount: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 30}, Status: "succeeded", CreatedAt: now, CompletedAt: &blockedAt}); !errors.Is(err, store.ErrInsufficientBalance) {
		t.Fatalf("PostgreSQL transfer spent active Gateway reservation: %v", err)
	}

	if _, err := db.ExecContext(t.Context(), `INSERT INTO administrators(id,email,display_name,password_hash,status,created_at,updated_at) VALUES ('admin-one','admin-one@gizway.test','one','hash','active',$1,$1),('admin-two','admin-two@gizway.test','two','hash','active',$1,$1)`, now); err != nil {
		t.Fatal(err)
	}
	start = make(chan struct{})
	results = make(chan error, 2)
	for _, adminID := range []string{"admin-one", "admin-two"} {
		wait.Add(1)
		go func(adminID string) {
			defer wait.Done()
			<-start
			_, err := repository.UpdateAdministrator(context.Background(), adminID, adminID, "", "suspended", "", "PG final-admin invariant", now)
			results <- err
		}(adminID)
	}
	close(start)
	wait.Wait()
	close(results)
	succeeded = 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, store.ErrIdempotencyConflict) {
			t.Fatalf("concurrent administrator state change returned %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent administrator suspensions succeeded %d times", succeeded)
	}
	var activeAdmins int
	if err := db.GetContext(t.Context(), &activeAdmins, `SELECT COUNT(*) FROM administrators WHERE status='active'`); err != nil || activeAdmins != 1 {
		t.Fatalf("active administrators=%d err=%v", activeAdmins, err)
	}
	merchantPage, err := repository.ListMerchantsPage(t.Context(), store.AdminListQuery{Status: "approved", Limit: 1})
	if err != nil || len(merchantPage.Items) != 1 || merchantPage.Items[0]["account_id"] != "a2" {
		t.Fatalf("PostgreSQL merchant status page=%+v err=%v", merchantPage, err)
	}
	var tables int
	if err := db.GetContext(t.Context(), &tables, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema()`); err != nil || tables < 30 {
		t.Fatalf("migrated tables=%d err=%v", tables, err)
	}
}

func TestPostgreSQLSearchPathDSN(t *testing.T) {
	for _, dsn := range []string{"postgres://user@localhost/db", "host=localhost dbname=test"} {
		if scoped := postgresSearchPathDSN(t, dsn, "gizway_test_scope"); !strings.Contains(scoped, "search_path") {
			t.Fatalf("scoped DSN=%q", scoped)
		}
	}
}

func TestPostgreSQLSessionCredentialsAreSingleUse(t *testing.T) {
	repository, db := postgresTestStore(t)
	const (
		now       = "2026-08-10T00:00:00.000000000Z"
		expiresAt = "2026-08-11T00:00:00.000000000Z"
	)
	userSecret := "pg-user-refresh-secret"
	userHash := sha256.Sum256([]byte(userSecret))
	adminSecret := "pg-admin-refresh-secret"
	adminHash := sha256.Sum256([]byte(adminSecret))
	for _, fixture := range []struct {
		statement string
		arguments []any
	}{
		{`INSERT INTO users(id,email,status,created_at,updated_at) VALUES ('refresh-user','refresh-user@gizway.test','active',$1,$1)`, []any{now}},
		{`INSERT INTO user_sessions(id,user_id,secret_hash,status,expires_at,created_at) VALUES ('old-user-session','refresh-user',$1,'active',$2,$3)`, []any{userHash[:], expiresAt, now}},
		{`INSERT INTO administrators(id,email,display_name,password_hash,status,created_at,updated_at) VALUES ('refresh-admin','refresh-admin@gizway.test','refresh admin','hash','active',$1,$1)`, []any{now}},
		{`INSERT INTO admin_sessions(id,administrator_id,secret_hash,status,expires_at,created_at) VALUES ('old-admin-session','refresh-admin',$1,'active',$2,$3)`, []any{adminHash[:], expiresAt, now}},
	} {
		if _, err := db.ExecContext(t.Context(), fixture.statement, fixture.arguments...); err != nil {
			t.Fatalf("seed session refresh fixture: %v", err)
		}
	}

	assertSingleUse := func(table, oldID string, refresh func(int) error) {
		t.Helper()
		blocker, err := db.BeginTxx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var lockedID string
		if err := blocker.GetContext(t.Context(), &lockedID, `SELECT id FROM `+table+` WHERE id=$1 FOR UPDATE`, oldID); err != nil {
			_ = blocker.Rollback()
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		for index := range 2 {
			go func(index int) {
				<-start
				results <- refresh(index)
			}(index)
		}
		close(start)
		deadline := time.Now().Add(5 * time.Second)
		for {
			var blocked int
			if err := db.GetContext(t.Context(), &blocked, `SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'UPDATE `+table+` SET status=%'`); err != nil {
				_ = blocker.Rollback()
				t.Fatal(err)
			}
			if blocked >= 2 {
				break
			}
			if time.Now().After(deadline) {
				_ = blocker.Rollback()
				t.Fatalf("refresh requests did not both reach locked %s row", table)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := blocker.Commit(); err != nil {
			t.Fatal(err)
		}
		succeeded := 0
		for range 2 {
			err := <-results
			if err == nil {
				succeeded++
			} else if !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("concurrent %s refresh returned %v", table, err)
			}
		}
		if succeeded != 1 {
			t.Fatalf("concurrent %s refresh succeeded %d times", table, succeeded)
		}
	}

	assertSingleUse("user_sessions", "old-user-session", func(index int) error {
		newHash := sha256.Sum256(fmt.Appendf(nil, "new-user-secret-%d", index))
		_, err := repository.RefreshUserSession(context.Background(), userSecret, fmt.Sprintf("new-user-session-%d", index), newHash[:], now, expiresAt)
		return err
	})
	assertSingleUse("admin_sessions", "old-admin-session", func(index int) error {
		newHash := sha256.Sum256(fmt.Appendf(nil, "new-admin-secret-%d", index))
		_, err := repository.RefreshAdminSession(context.Background(), adminSecret, fmt.Sprintf("new-admin-session-%d", index), newHash[:], now, expiresAt)
		return err
	})

	userLogoutSecret := "pg-user-logout-secret"
	userLogoutHash := sha256.Sum256([]byte(userLogoutSecret))
	adminLogoutSecret := "pg-admin-logout-secret"
	adminLogoutHash := sha256.Sum256([]byte(adminLogoutSecret))
	for _, fixture := range []struct {
		statement string
		arguments []any
	}{
		{`INSERT INTO user_sessions(id,user_id,secret_hash,status,expires_at,created_at) VALUES ('logout-user-session','refresh-user',$1,'active',$2,$3)`, []any{userLogoutHash[:], expiresAt, now}},
		{`INSERT INTO admin_sessions(id,administrator_id,secret_hash,status,expires_at,created_at) VALUES ('logout-admin-session','refresh-admin',$1,'active',$2,$3)`, []any{adminLogoutHash[:], expiresAt, now}},
	} {
		if _, err := db.ExecContext(t.Context(), fixture.statement, fixture.arguments...); err != nil {
			t.Fatalf("seed session logout fixture: %v", err)
		}
	}
	assertSingleUse("user_sessions", "logout-user-session", func(int) error {
		return repository.RevokeUserSession(context.Background(), userLogoutSecret, now)
	})
	assertSingleUse("admin_sessions", "logout-admin-session", func(int) error {
		return repository.RevokeAdminSession(context.Background(), adminLogoutSecret, now)
	})
}
