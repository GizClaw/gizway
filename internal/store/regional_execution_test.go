package store_test

import (
	"testing"
	"time"

	"github.com/idy/gizway/internal/service/quotaexchange"
	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

// This test starts from the real regional-only schema. If execution/outbox
// code ever reaches for users, Accounts, API keys, or the central ledger, the
// query fails because those tables do not exist in this database.
func TestPostgreSQLRegionalExecutionCompletesWithoutCentralIdentityTables(t *testing.T) {
	database, err := storage.OpenGizWayPostgreSQL(testdb.NewSchema(t), true)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	const now = "2026-08-12T00:00:00.000000000Z"
	statements := []string{
		`INSERT INTO providers(id,slug,name,status,created_at,updated_at) VALUES ('provider','provider','Provider','active',$1,$1)`,
		`INSERT INTO provider_endpoints(id,provider_id,name,base_url,credential_ref,priority,weight,status,created_at,updated_at) VALUES ('endpoint','provider','Endpoint','https://provider.test','secret',1,100,'active',$1,$1)`,
		`INSERT INTO models(id,slug,name,modality,status,metadata,created_at,updated_at) VALUES ('model','regional-model','Regional','["text"]','active','{}',$1,$1)`,
		`INSERT INTO model_variants(id,model_id,provider_endpoint_id,provider_model_name,variant_slug,capabilities,context_window,max_output_tokens,status,created_at,updated_at) VALUES ('variant','model','endpoint','provider-model','primary','{"chat":true}',8192,2048,'active',$1,$1)`,
		`INSERT INTO model_variant_prices(id,model_variant_id,metric,unit_size,upstream_cost_microcredits,base_customer_price_microcredits,customer_price_microcredits,discount_bps,valid_from,created_at) VALUES ('rate-output','variant','output_token',1000,1,2,2,0,$1,$1)`,
	}
	for _, statement := range statements {
		if _, err := database.SQL.Exec(statement, now); err != nil {
			t.Fatalf("regional fixture: %v", err)
		}
	}
	repository := store.New(database.SQL)
	repository.ConfigureClock(func() time.Time { return time.Date(2026, 8, 12, 0, 1, 0, 0, time.UTC) })
	publication, err := repository.PrepareRegionalRatePublication(t.Context(), "global", "regional-publication", now)
	if err != nil || publication.Status != "publishing" || len(publication.Prices) != 1 {
		t.Fatalf("prepared regional publication = %+v, %v", publication, err)
	}
	replayed, err := repository.PrepareRegionalRatePublication(t.Context(), "global", "regional-publication", now)
	if err != nil || replayed.ID != publication.ID || replayed.Revision != publication.Revision {
		t.Fatalf("same-source publication replay = %+v, %v", replayed, err)
	}
	if _, err := database.SQL.Exec(`UPDATE model_variant_prices SET base_customer_price_microcredits=3,customer_price_microcredits=3 WHERE id='rate-output'`); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PrepareRegionalRatePublication(t.Context(), "global", "regional-publication", now); err != store.ErrIdempotencyConflict {
		t.Fatalf("changed same-source publication err=%v, want conflict", err)
	}
	publication, err = repository.ActivateRegionalRatePublication(t.Context(), publication.ID, "center-publication")
	if err != nil || publication.Status != "active" {
		t.Fatalf("activated regional publication = %+v, %v", publication, err)
	}
	checks, err := repository.GizWayReadinessChecks(t.Context())
	if err != nil || checks["rate_publication"] != "ready" {
		t.Fatalf("active publication readiness=%v err=%v", checks, err)
	}
	if _, err := database.SQL.Exec(`UPDATE rate_publications SET status='retired' WHERE id=$1`, publication.ID); err != nil {
		t.Fatal(err)
	}
	checks, err = repository.GizWayReadinessChecks(t.Context())
	if err != nil || checks["rate_publication"] != "pending" {
		t.Fatalf("retired-only publication readiness=%v err=%v", checks, err)
	}
	if _, err := database.SQL.Exec(`UPDATE rate_publications SET status='active' WHERE id=$1`, publication.ID); err != nil {
		t.Fatal(err)
	}
	candidates, err := repository.ResolveRegionalGatewayCandidates(t.Context(), "regional-model", "chat.completions", now)
	if err != nil || len(candidates) != 1 || candidates[0].LocalRatePublicationID != publication.ID || candidates[0].RatePublicationID != "center-publication" {
		t.Fatalf("regional candidates = %#v, %v", candidates, err)
	}
	if candidates[0].Prices["output_token"].EffectivePrice != 2 {
		t.Fatalf("execution read mutable draft price: %+v", candidates[0].Prices["output_token"])
	}
	if err := repository.BeginRegionalExecution(t.Context(), "operation", "regional-model", "variant", publication.ID, "https", "buffered", 100, now); err != nil {
		t.Fatal(err)
	}
	usage := quotaexchange.UsageRecord{
		UCGID: "ucg-regional", OperationID: "operation", PublicModel: "regional-model",
		ModelVariantID: "variant", RatePublicationID: "center-publication",
		Metrics: map[string]int64{"output_token": 500}, StartedAt: now,
		CompletedAt: "2026-08-12T00:00:01.000000000Z",
	}
	metric := store.GatewayMetric{Metric: "output_token", Quantity: 500, Price: candidates[0].Prices["output_token"], Charge: 1}
	if err := repository.CompleteRegionalExecution(t.Context(), "epoch", "opaque-token", "provider-request", usage, []store.GatewayMetric{metric}, 1); err != nil {
		t.Fatal(err)
	}
	// One provider execution may emit more than one independently-addressable
	// Usage record (for example, segmented streaming/realtime accounting). The
	// operation foreign key must therefore remain one-to-many rather than being
	// unique on gateway_usage_outbox.operation_id.
	secondUsage := usage
	secondUsage.UCGID = "ucg-regional-segment-two"
	if err := repository.EnqueuePricedUsage(t.Context(), "epoch", "opaque-token", secondUsage, 1); err != nil {
		t.Fatalf("enqueue second Usage for one execution: %v", err)
	}
	missingExecutionUsage := usage
	missingExecutionUsage.UCGID = "ucg-missing-execution"
	missingExecutionUsage.OperationID = "missing-execution"
	if err := repository.EnqueuePricedUsage(t.Context(), "epoch", "opaque-token", missingExecutionUsage, 1); err == nil {
		t.Fatal("Usage Outbox accepted an operation_id without a gateway execution")
	}
	var executionStatus, outboxStatus string
	if err := database.SQL.Get(&executionStatus, `SELECT status FROM gateway_executions WHERE id='operation'`); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.Get(&outboxStatus, `SELECT status FROM gateway_usage_outbox WHERE ucgid='ucg-regional'`); err != nil {
		t.Fatal(err)
	}
	if executionStatus != "completed" || outboxStatus != "pending" {
		t.Fatalf("execution/outbox status = %s/%s", executionStatus, outboxStatus)
	}
	var executionUsageCount int
	if err := database.SQL.Get(&executionUsageCount, `SELECT count(*) FROM gateway_usage_outbox WHERE operation_id='operation'`); err != nil {
		t.Fatal(err)
	}
	if executionUsageCount != 2 {
		t.Fatalf("Usage rows for one execution = %d, want 2", executionUsageCount)
	}
	var metricRate string
	if err := database.SQL.Get(&metricRate, `SELECT rate_version_id FROM gateway_usage_metrics WHERE ucgid='ucg-regional'`); err != nil || metricRate != "rate-output" {
		t.Fatalf("metric rate = %q, %v", metricRate, err)
	}
}
