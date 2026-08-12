package store_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/idy/gizway/internal/service/quotaexchange"
	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

func TestPostgreSQLUsageOutboxAbandonsRestartedProcessWithoutPersistingIdentity(t *testing.T) {
	database := testdb.OpenGizWay(t)
	defer database.Close()
	repository := store.New(database.SQL)
	record := quotaexchange.UsageRecord{
		UCGID: "ucg-outbox", OperationID: "op-outbox", PublicModel: "story-text",
		ModelVariantID: "variant", RatePublicationID: "publication",
		Metrics:   map[string]int64{"request": 1},
		StartedAt: "2026-08-11T00:00:00.000000000Z", CompletedAt: "2026-08-11T00:00:01.000000000Z",
	}
	beginUsageOutboxExecution(t, database, repository, record.OperationID)
	if err := repository.EnqueueUsage(t.Context(), "epoch-one", "opaque-runtime-token", record); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err := database.SQL.Get(&payload, `SELECT payload::TEXT FROM gateway_usage_outbox WHERE ucgid='ucg-outbox'`); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"api_key", "account_id", "giz_story_user_active_1"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("outbox payload persisted forbidden identity %q: %s", forbidden, payload)
		}
	}
	diagnostics, err := repository.AdminRowsPage(t.Context(), "usage_outbox", store.AdminListQuery{Status: "pending"})
	if err != nil || len(diagnostics.Items) != 1 {
		t.Fatalf("outbox diagnostics=%+v err=%v", diagnostics, err)
	}
	if _, exists := diagnostics.Items[0]["runtime_key_token"]; exists {
		t.Fatal("outbox diagnostics exposed runtime key token")
	}
	if _, exists := diagnostics.Items[0]["payload"]; exists {
		t.Fatal("outbox diagnostics exposed Usage payload")
	}
	if changed, err := repository.AbandonUsageOutboxOnStartup(t.Context()); err != nil || changed != 1 {
		t.Fatalf("AbandonUsageOutboxOnStartup = (%d, %v), want (1, nil)", changed, err)
	}
	var status string
	if err := database.SQL.Get(&status, `SELECT status FROM gateway_usage_outbox WHERE ucgid='ucg-outbox'`); err != nil || status != "abandoned" {
		t.Fatalf("outbox status = %q, %v", status, err)
	}
}

func TestPostgreSQLUsageOutboxRetriesWithBoundedExponentialBackoff(t *testing.T) {
	database := testdb.OpenGizWay(t)
	defer database.Close()
	repository := store.New(database.SQL)
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	repository.ConfigureClock(func() time.Time { return now })
	record := quotaexchange.UsageRecord{
		UCGID: "ucg-backoff", OperationID: "op-backoff", PublicModel: "story-text",
		ModelVariantID: "variant", RatePublicationID: "publication", Metrics: map[string]int64{"request": 1},
		StartedAt: "2026-08-11T00:00:00.000000000Z", CompletedAt: "2026-08-11T00:00:01.000000000Z",
	}
	beginUsageOutboxExecution(t, database, repository, record.OperationID)
	if err := repository.EnqueueUsage(t.Context(), "epoch", "token", record); err != nil {
		t.Fatal(err)
	}

	for attempt, delay := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		claimed, err := repository.ClaimUsage(t.Context(), "epoch", "token", 1, 256<<10)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("attempt %d claim = %#v, %v", attempt+1, claimed, err)
		}
		if err := repository.ReturnUsagePending(t.Context(), []string{"ucg-backoff"}, "temporary failure"); err != nil {
			t.Fatal(err)
		}
		if claimed, err := repository.ClaimUsage(t.Context(), "epoch", "token", 1, 256<<10); err != nil || len(claimed) != 0 {
			t.Fatalf("attempt %d ignored backoff: %#v, %v", attempt+1, claimed, err)
		}
		now = now.Add(delay)
	}
}

func TestPostgreSQLUsageOutboxPermanentFailureIsTerminal(t *testing.T) {
	database := testdb.OpenGizWay(t)
	defer database.Close()
	repository := store.New(database.SQL)
	record := quotaexchange.UsageRecord{
		UCGID: "ucg-terminal", OperationID: "op-terminal", PublicModel: "story-text",
		ModelVariantID: "variant", RatePublicationID: "publication", Metrics: map[string]int64{"request": 1},
		StartedAt: "2026-08-11T00:00:00.000000000Z", CompletedAt: "2026-08-11T00:00:01.000000000Z",
	}
	beginUsageOutboxExecution(t, database, repository, record.OperationID)
	if err := repository.EnqueueUsage(t.Context(), "epoch", "token", record); err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.ClaimUsage(t.Context(), "epoch", "token", 1, 256<<10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	if err := repository.MarkUsageFailed(t.Context(), []string{record.UCGID}, "usage unpriceable"); err != nil {
		t.Fatal(err)
	}
	claimed, err = repository.ClaimUsage(t.Context(), "epoch", "token", 1, 256<<10)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("terminal Usage was reclaimed: %#v err=%v", claimed, err)
	}
	var status, reason string
	if err := database.SQL.QueryRowx(`SELECT status,last_error FROM gateway_usage_outbox WHERE ucgid=$1`, record.UCGID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || reason != "usage unpriceable" {
		t.Fatalf("terminal Usage status=%q reason=%q", status, reason)
	}
}

func TestPostgreSQLUsageOutboxClaimsOnlyCurrentKeyAndAcknowledgesWholeBatch(t *testing.T) {
	database := testdb.OpenGizWay(t)
	defer database.Close()
	repository := store.New(database.SQL)
	for _, fixture := range []struct {
		ucgid string
		token string
	}{
		{"ucg-one", "token-one"},
		{"ucg-two", "token-one"},
		{"ucg-other-key", "token-two"},
	} {
		record := quotaexchange.UsageRecord{
			UCGID: fixture.ucgid, OperationID: "op-" + fixture.ucgid,
			PublicModel: "story-text", ModelVariantID: "variant", RatePublicationID: "publication",
			Metrics:   map[string]int64{"request": 1},
			StartedAt: "2026-08-11T00:00:00.000000000Z", CompletedAt: "2026-08-11T00:00:01.000000000Z",
		}
		beginUsageOutboxExecution(t, database, repository, record.OperationID)
		if err := repository.EnqueueUsage(t.Context(), "epoch", fixture.token, record); err != nil {
			t.Fatal(err)
		}
	}

	claimed, err := repository.ClaimUsage(t.Context(), "epoch", "token-one", 200, 256<<10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 2 || claimed[0].UCGID != "ucg-one" || claimed[1].UCGID != "ucg-two" {
		t.Fatalf("claimed current-key batch = %#v", claimed)
	}
	if bytes.Contains(claimed[0].Payload, []byte("token-one")) {
		t.Fatal("runtime token leaked into Exchange Usage payload")
	}
	// A center acknowledgement followed by a local status-write failure leaves
	// rows in sending. The same live process must resend the same UCGIDs so the
	// center can safely deduplicate them instead of silently stranding Usage.
	claimed, err = repository.ClaimUsage(t.Context(), "epoch", "token-one", 200, 256<<10)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("reclaim ambiguous sending batch = %#v, %v", claimed, err)
	}
	if err := repository.MarkUsageReported(t.Context(), []string{"ucg-one", "ucg-two"}); err != nil {
		t.Fatal(err)
	}
	var reported, pending int
	if err := database.SQL.Get(&reported, `SELECT COUNT(*) FROM gateway_usage_outbox WHERE status='reported'`); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.Get(&pending, `SELECT COUNT(*) FROM gateway_usage_outbox WHERE status='pending'`); err != nil {
		t.Fatal(err)
	}
	if reported != 2 || pending != 1 {
		t.Fatalf("outbox states reported=%d pending=%d, want 2/1", reported, pending)
	}
}

func beginUsageOutboxExecution(t *testing.T, database *storage.Storage, repository *store.Store, operationID string) {
	t.Helper()
	seedMinimalExecutionDependencies(t, database)
	if err := repository.BeginRegionalExecution(t.Context(), operationID, "story-text",
		"outbox-variant", "outbox-publication", "https", "buffered", 0,
		"2026-08-11T00:00:00.000000000Z"); err != nil {
		t.Fatalf("begin Usage Outbox execution %q: %v", operationID, err)
	}
}

func seedMinimalExecutionDependencies(t *testing.T, database *storage.Storage) {
	t.Helper()
	const at = "2026-08-11T00:00:00.000000000Z"
	for _, statement := range []string{
		`INSERT INTO providers(id,slug,name,status,created_at,updated_at) VALUES ('outbox-provider','outbox-provider','Outbox Provider','active',$1,$1) ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO provider_endpoints(id,provider_id,name,base_url,credential_ref,priority,weight,status,created_at,updated_at) VALUES ('outbox-endpoint','outbox-provider','Outbox Endpoint','https://provider.invalid','test-only',1,100,'active',$1,$1) ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO models(id,slug,name,modality,status,metadata,created_at,updated_at) VALUES ('outbox-model','story-text','Outbox Model','["text"]','active','{}',$1,$1) ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO model_variants(id,model_id,provider_endpoint_id,provider_model_name,variant_slug,capabilities,status,created_at,updated_at) VALUES ('outbox-variant','outbox-model','outbox-endpoint','outbox-model','primary','{}','active',$1,$1) ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO rate_publications(id,region,revision,content_hash,status,effective_at,created_at,updated_at) VALUES ('outbox-publication','global',1,decode('01','hex'),'active',$1,$1,$1) ON CONFLICT (id) DO NOTHING`,
	} {
		if _, err := database.SQL.Exec(statement, at); err != nil {
			t.Fatalf("seed minimal execution dependency: %v", err)
		}
	}
}
