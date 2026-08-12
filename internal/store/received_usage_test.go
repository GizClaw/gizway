package store_test

import (
	"testing"
	"time"

	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

// This test opens the real GizPay-only schema. It prevents account history or
// PowerSync from silently acquiring a dependency on regional executions,
// customer Key IDs or any regional execution table.
func TestPostgreSQLGizPayUsageVisibilityReadsOnlyReceivedUsage(t *testing.T) {
	database, err := storage.OpenGizPayPostgreSQL(testdb.NewSchema(t), true)
	if err != nil {
		t.Fatalf("initialize GizPay schema: %v", err)
	}
	defer database.Close()
	now := "2026-08-12T00:00:00.000000000Z"
	statements := []string{
		`INSERT INTO users(id,email,display_name,status,created_at,updated_at)
		 VALUES ('user','owner@example.com','Owner','active',$1,$1)`,
		`INSERT INTO accounts(id,owner_user_id,kind,name,status,created_at,updated_at)
		 VALUES ('account','user','personal','Owner','active',$1,$1)`,
		`INSERT INTO gateway_nodes(id,region,name,created_at,updated_at)
		 VALUES ('node-global','global','Global node',$1,$1)`,
		`INSERT INTO billing_rate_publications(id,node_id,region,source_publication_id,revision,payload_hash,status,effective_at,created_at)
		 VALUES ('publication','node-global','global','source-publication',1,decode('01','hex'),'active',$1,$1)`,
		`INSERT INTO billing_rate_versions(id,publication_id,model_variant_id,public_model,metric,unit_size,base_price_microcredits,customer_price_microcredits,discount_bps)
			 SELECT 'rate-version','publication','variant','public-model','request',1,7,7,0 WHERE $1::text IS NOT NULL`,
		`INSERT INTO ledger_transactions(id,transaction_type,status,idempotency_key,initiated_by_account_id,reference_type,reference_id,created_at,posted_at)
		 VALUES
		 ('ledger','usage','posted','usage:ucgid','account','gateway_usage','ucgid',$1,$1),
		 ('ledger-z','usage','posted','usage:ucgid-z','account','gateway_usage','ucgid-z',$1,$1),
		 ('ledger-late','usage','posted','usage:ucgid-0','account','gateway_usage','ucgid-0',$1,$1)`,
		`INSERT INTO gateway_usage_records(
			ucgid,account_id,node_id,region,operation_id,public_model,model_variant_id,
			rate_publication_id,canonical_payload_hash,charged_microcredits,
			ledger_transaction_id,started_at,completed_at,received_at)
		VALUES ('ucgid','account','node-global','global','operation','public-model','variant',
			'publication',decode('02','hex'),7,'ledger',$1,$1,$1)`,
		`INSERT INTO gateway_usage_metrics(ucgid,rate_version_id,metric,quantity,unit_size,price_microcredits,charged_microcredits)
		 SELECT 'ucgid','rate-version','request',1,1,7,7 WHERE $1::text IS NOT NULL`,
	}
	for _, statement := range statements {
		if _, err := database.SQL.Exec(statement, now); err != nil {
			t.Fatal(err)
		}
	}

	repository := store.New(database.SQL)
	repository.ConfigureClock(func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) })
	if _, err := database.SQL.Exec(`INSERT INTO gateway_usage_records(
		ucgid,account_id,node_id,region,operation_id,public_model,model_variant_id,
		rate_publication_id,canonical_payload_hash,charged_microcredits,
		ledger_transaction_id,started_at,completed_at,received_at)
		VALUES ('ucgid-z','account','node-global','global','operation-z','public-model','variant',
		'publication',decode('03','hex'),1,'ledger-z',$1,$1,$1)`, now); err != nil {
		t.Fatal(err)
	}
	page, err := repository.ListReceivedGatewayUsagePage(
		t.Context(), "user", "account", "2026-08-11T00:00:00.000000000Z", "2026-08-13T00:00:00.000000000Z", store.AccountListQuery{Limit: 1},
	)
	if err != nil || len(page.Items) != 1 || page.Items[0]["api_key_id"] != nil || !page.HasMore || page.NextCursor == nil {
		t.Fatalf("received Usage page=%v err=%v", page.Items, err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO gateway_usage_records(
		ucgid,account_id,node_id,region,operation_id,public_model,model_variant_id,
		rate_publication_id,canonical_payload_hash,charged_microcredits,
		ledger_transaction_id,started_at,completed_at,received_at)
		VALUES ('ucgid-0','account','node-global','global','late-operation','public-model','variant',
		'publication',decode('04','hex'),1,'ledger-late',$1,$1,'2026-08-12T00:00:01.000000000Z')`, now); err != nil {
		t.Fatal(err)
	}
	next, err := repository.ListReceivedGatewayUsagePage(t.Context(), "user", "account",
		"2026-08-11T00:00:00.000000000Z", "2026-08-13T00:00:00.000000000Z", store.AccountListQuery{Limit: 1, Cursor: *page.NextCursor})
	if err != nil || len(next.Items) != 1 || databaseTextForTest(next.Items[0]["ucgid"]) != "ucgid-z" || next.AsOf != page.AsOf {
		t.Fatalf("snapshot-stable second page=%+v first_as_of=%s err=%v", next, page.AsOf, err)
	}
	adminPage, err := repository.AdminRowsPage(t.Context(), "received_usage", store.AdminListQuery{AccountID: "account", NodeID: "node-global", Region: "global"})
	if err != nil || len(adminPage.Items) != 3 {
		t.Fatalf("Admin received Usage page=%+v err=%v", adminPage, err)
	}
	for _, item := range adminPage.Items {
		if _, exists := item["api_key_id"]; exists {
			t.Fatal("Admin received Usage exposed API Key identity")
		}
	}
	var projected int
	if err := database.SQL.Get(&projected, `SELECT COUNT(*) FROM powersync_gateway_usage WHERE account_id='account' AND ucgid='ucgid'`); err != nil || projected != 1 {
		t.Fatalf("PowerSync received Usage count=%d err=%v", projected, err)
	}
}

func databaseTextForTest(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}
