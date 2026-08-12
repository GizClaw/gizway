package store_test

import (
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/idy/gizway/internal/service/quotaexchange"
	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

func openGizPayQuotaTestDatabase(t *testing.T) *storage.Storage {
	t.Helper()
	database := testdb.OpenGizPay(t)
	at := "2026-08-11T00:00:00.000000000Z"
	keyHash := sha256.Sum256([]byte("giz_story_user_active_1"))
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,email,display_name,status,created_at,updated_at) VALUES ('user-quota','quota@test.invalid','Quota','active',$1,$1)`, []any{at}},
		{`INSERT INTO accounts(id,owner_user_id,kind,name,status,created_at,updated_at) VALUES ('21000000-0000-4000-8000-000000000001','user-quota','personal','Quota','active',$1,$1)`, []any{at}},
		{`INSERT INTO api_keys(id,account_id,kind,name,key_prefix,secret_hash,status,created_at) VALUES ('key-quota','21000000-0000-4000-8000-000000000001','gateway','Quota','giz_story',$1,'active',$2)`, []any{keyHash[:], at}},
		{`INSERT INTO gateway_nodes(id,region,name,created_at,updated_at) VALUES ('story-global','global','Global story node',$1,$1)`, []any{at}},
		{`INSERT INTO ledger_accounts(id,owner_account_id,code,kind,asset_code,normal_balance,status,created_at,updated_at) VALUES
			('ledger-user','21000000-0000-4000-8000-000000000001','USER:quota','user_credit','GIZ_CREDIT','credit','active',$1,$1),
			('ledger-system',NULL,'SYSTEM:CREDIT_LIABILITY','system_credit_liability','GIZ_CREDIT','debit','active',$1,$1)`, []any{at}},
		{`INSERT INTO ledger_transactions(id,transaction_type,status,idempotency_key,initiated_by_account_id,reference_type,reference_id,created_at,posted_at)
			VALUES ('ledger-opening','adjustment','posted','opening-quota','21000000-0000-4000-8000-000000000001','fixture','opening',$1,$1)`, []any{at}},
		{`INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,sequence,direction,amount_microcredits,created_at) VALUES
			('entry-opening-user','ledger-opening','ledger-user',1,'credit',100000000,$1),
			('entry-opening-system','ledger-opening','ledger-system',2,'debit',100000000,$1)`, []any{at}},
		{`INSERT INTO billing_rate_publications(id,node_id,region,source_publication_id,revision,payload_hash,status,effective_at,created_at)
			VALUES ('ratepub_story_global_1','story-global','global','story-source',1,decode('01','hex'),'active',$1,$1)`, []any{at}},
		{`INSERT INTO billing_rate_versions(id,publication_id,model_variant_id,public_model,metric,unit_size,base_price_microcredits,customer_price_microcredits,discount_bps)
			VALUES ('rate-story-request','ratepub_story_global_1','91000000-0000-4000-8000-000000000001','story-text','request',1,10,10,0)`, nil},
	}
	for _, statement := range statements {
		if _, err := database.SQL.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed GizPay quota fixture: %v", err)
		}
	}
	return database
}

func storyUsage(ucgid string, quantity int64) quotaexchange.UsageRecord {
	return quotaexchange.UsageRecord{
		UCGID: ucgid, OperationID: "op-" + ucgid, PublicModel: "story-text",
		ModelVariantID:    "91000000-0000-4000-8000-000000000001",
		RatePublicationID: "ratepub_story_global_1",
		Metrics:           map[string]int64{"request": quantity},
		StartedAt:         "2026-08-11T12:00:00.000000000Z", CompletedAt: "2026-08-11T12:00:01.000000000Z",
	}
}

func TestPostgreSQLQuotaExchangeConcurrentUCGIDCreatesOneCharge(t *testing.T) {
	database := openGizPayQuotaTestDatabase(t)
	defer database.Close()
	repository := store.New(database.SQL)
	repository.ConfigureClock(func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) })

	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Go(func() {
			<-start
			_, err := repository.ExchangeQuota(t.Context(), "giz_story_user_active_1", "story-global", "global", []quotaexchange.UsageRecord{storyUsage("ucg-concurrent", 1)})
			errorsFound <- err
		})
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatalf("concurrent ExchangeQuota: %v", err)
		}
	}
	var usageCount, ledgerCount int
	if err := database.SQL.Get(&usageCount, `SELECT COUNT(*) FROM gateway_usage_records WHERE ucgid='ucg-concurrent'`); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.Get(&ledgerCount, `SELECT COUNT(*) FROM ledger_transactions WHERE idempotency_key='ucgid:ucg-concurrent'`); err != nil {
		t.Fatal(err)
	}
	if usageCount != 1 || ledgerCount != 1 {
		t.Fatalf("usage=%d ledger=%d, want one each", usageCount, ledgerCount)
	}

	changed := storyUsage("ucg-concurrent", 2)
	if _, err := repository.ExchangeQuota(t.Context(), "giz_story_user_active_1", "story-global", "global", []quotaexchange.UsageRecord{changed}); !errors.Is(err, store.ErrUCGIDConflict) {
		t.Fatalf("changed UCGID replay err=%v", err)
	}
}

func TestPostgreSQLQuotaExchangeDeniesFrozenBalance(t *testing.T) {
	database := openGizPayQuotaTestDatabase(t)
	defer database.Close()
	repository := store.New(database.SQL)
	repository.ConfigureClock(func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) })
	if _, err := database.SQL.Exec(`UPDATE ledger_accounts SET status='frozen' WHERE id='ledger-user'`); err != nil {
		t.Fatal(err)
	}
	result, err := repository.ExchangeQuota(t.Context(), "giz_story_user_active_1", "story-global", "global", nil)
	if err != nil || result.Allowed || result.QuotaMicrocredits != 0 || result.PostedMicrocredits != 100_000_000 {
		t.Fatalf("frozen ExchangeQuota = %+v, %v", result, err)
	}
}

func TestPostgreSQLQuotaExchangeBatchRollsBackEveryPartialMutation(t *testing.T) {
	database := openGizPayQuotaTestDatabase(t)
	defer database.Close()
	repository := store.New(database.SQL)
	repository.ConfigureClock(func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) })
	valid := storyUsage("ucg-batch-valid", 1)
	invalid := storyUsage("ucg-batch-invalid", 1)
	invalid.RatePublicationID = "missing-publication"
	if _, err := repository.ExchangeQuota(t.Context(), "giz_story_user_active_1", "story-global", "global", []quotaexchange.UsageRecord{valid, invalid}); !errors.Is(err, store.ErrUnpriceableUsage) {
		t.Fatalf("mixed batch err=%v", err)
	}
	var count int
	if err := database.SQL.Get(&count, `SELECT COUNT(*) FROM gateway_usage_records WHERE ucgid IN ('ucg-batch-valid','ucg-batch-invalid')`); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("mixed batch left %d received rows", count)
	}
}

func TestPostgreSQLReceivedUsageRequiresExistingLedgerTransaction(t *testing.T) {
	database := openGizPayQuotaTestDatabase(t)
	defer database.Close()
	at := "2026-08-12T00:00:00.000000000Z"
	_, err := database.SQL.Exec(`INSERT INTO gateway_usage_records
		(ucgid,account_id,node_id,region,operation_id,public_model,model_variant_id,
		 rate_publication_id,canonical_payload_hash,charged_microcredits,ledger_transaction_id,
		 started_at,completed_at,received_at)
		VALUES ('ucg-missing-ledger','21000000-0000-4000-8000-000000000001','story-global','global',
		'op-missing-ledger','story-text','91000000-0000-4000-8000-000000000001',
		'ratepub_story_global_1',decode('01','hex'),1,'missing-ledger',$1,$1,$1)`, at)
	if err == nil {
		t.Fatal("Usage referencing a nonexistent ledger transaction was accepted")
	}

	repository := store.New(database.SQL)
	repository.ConfigureClock(func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) })
	if _, err := repository.ExchangeQuota(t.Context(), "giz_story_user_active_1", "story-global", "global",
		[]quotaexchange.UsageRecord{storyUsage("ucg-ledger-fk", 1)}); err != nil {
		t.Fatal(err)
	}
	var linked bool
	if err := database.SQL.Get(&linked, `SELECT EXISTS (
		SELECT 1 FROM gateway_usage_records usage
		JOIN ledger_transactions ledger ON ledger.id=usage.ledger_transaction_id
		WHERE usage.ucgid='ucg-ledger-fk'
	)`); err != nil || !linked {
		t.Fatalf("received Usage ledger link=%t err=%v", linked, err)
	}
}

func TestPostgreSQLQuotaSubtractsOnlyRealActivePaymentHoldsWithoutCreatingAIHold(t *testing.T) {
	database := openGizPayQuotaTestDatabase(t)
	defer database.Close()
	repository := store.New(database.SQL)
	repository.ConfigureClock(func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) })
	if _, err := database.SQL.Exec(`INSERT INTO credit_holds
		(id,account_id,purpose,reference_type,reference_id,amount_microcredits,status,expires_at,created_at)
		VALUES ('hold','21000000-0000-4000-8000-000000000001','payment','payment_intent','intent',30000000,'active',
		'2026-08-13T00:00:00.000000000Z','2026-08-11T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	var ledgerBefore int
	if err := database.SQL.Get(&ledgerBefore, `SELECT COUNT(*) FROM ledger_transactions`); err != nil {
		t.Fatal(err)
	}
	result, err := repository.ExchangeQuota(t.Context(), "giz_story_user_active_1", "story-global", "global", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.QuotaMicrocredits != 70_000_000 || !result.Allowed {
		t.Fatalf("quota with payment hold = %+v", result)
	}
	var ledgerAfter, holds int
	if err := database.SQL.Get(&ledgerAfter, `SELECT COUNT(*) FROM ledger_transactions`); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.Get(&holds, `SELECT COUNT(*) FROM credit_holds`); err != nil {
		t.Fatal(err)
	}
	if ledgerAfter != ledgerBefore || holds != 1 {
		t.Fatalf("quota-only query mutated finance: ledger %d->%d holds=%d", ledgerBefore, ledgerAfter, holds)
	}
}

func TestPostgreSQLQuotaUsesOnlyCallingNodesSnapshotAndStillPricesRetiredVersions(t *testing.T) {
	database := openGizPayQuotaTestDatabase(t)
	defer database.Close()
	repository := store.New(database.SQL)
	repository.ConfigureClock(func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) })

	if _, err := repository.ExchangeQuota(t.Context(), "giz_story_user_active_1", "different-global-node", "global",
		[]quotaexchange.UsageRecord{storyUsage("ucg-wrong-node", 1)}); !errors.Is(err, store.ErrUnpriceableUsage) {
		t.Fatalf("cross-node publication use err=%v, want unpriceable", err)
	}
	price := store.PublishedPrice{ModelVariantID: "new-variant", PublicModel: "new-model", Metric: "request", UnitSize: 1,
		BasePriceMicrocredits: 10, CustomerPriceMicrocredits: 9, DiscountBPS: 1000}
	if _, _, err := repository.PublishRatePublication(t.Context(), "story-global", "global", "new-source", 2,
		"2026-08-12T00:00:00.000000000Z", []byte("new-price-hash"), []store.PublishedPrice{price}); err != nil {
		t.Fatalf("publish replacement snapshot: %v", err)
	}
	result, err := repository.ExchangeQuota(t.Context(), "giz_story_user_active_1", "story-global", "global",
		[]quotaexchange.UsageRecord{storyUsage("ucg-retired-publication", 1)})
	if err != nil || result.PostedMicrocredits >= 100_000_000 {
		t.Fatalf("late Usage for retired snapshot result=%+v err=%v", result, err)
	}
}
