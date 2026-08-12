package gatewayquota

import (
	"context"
	"sync"
	"testing"
	"time"

	gizpayclient "github.com/idy/gizway/internal/client/gizpay"
	"github.com/idy/gizway/internal/service/localadmission"
	"github.com/idy/gizway/internal/service/quotaexchange"
	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

type blockingExchanger struct {
	started chan string
	release chan struct{}
}

type observedExchanger struct {
	calls chan []quotaexchange.UsageRecord
}

type scriptedExchanger struct {
	responses []gizpayclient.ExchangeResponse
	errors    []error
	calls     int
}

func (s *scriptedExchanger) Exchange(context.Context, string, []quotaexchange.UsageRecord) (gizpayclient.ExchangeResponse, error) {
	index := s.calls
	s.calls++
	if index < len(s.errors) && s.errors[index] != nil {
		return gizpayclient.ExchangeResponse{}, s.errors[index]
	}
	return s.responses[index], nil
}

func (f *observedExchanger) Exchange(_ context.Context, _ string, usage []quotaexchange.UsageRecord) (gizpayclient.ExchangeResponse, error) {
	f.calls <- append([]quotaexchange.UsageRecord(nil), usage...)
	return gizpayclient.ExchangeResponse{
		Status: "allowed", Quota: gizpayclient.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 10}, RecheckAfterSeconds: 300,
	}, nil
}

func (f *blockingExchanger) Exchange(_ context.Context, rawAPIKey string, _ []quotaexchange.UsageRecord) (gizpayclient.ExchangeResponse, error) {
	f.started <- rawAPIKey
	<-f.release
	return gizpayclient.ExchangeResponse{
		Status: "allowed", Quota: gizpayclient.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 10}, RecheckAfterSeconds: 300,
	}, nil
}

type fakeExchanger struct {
	calls     int
	responses []gizpayclient.ExchangeResponse
}

func (f *fakeExchanger) Exchange(context.Context, string, []quotaexchange.UsageRecord) (gizpayclient.ExchangeResponse, error) {
	response := f.responses[f.calls]
	f.calls++
	return response, nil
}

func TestAdmitQueriesOnceThenUsesOnlyLocalQuotaUntilDepleted(t *testing.T) {
	database := testdb.OpenGizWay(t)
	repository := store.New(database.SQL)
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	exchanger := &fakeExchanger{responses: []gizpayclient.ExchangeResponse{{
		Status: "allowed", Quota: gizpayclient.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 10}, RecheckAfterSeconds: 300,
	}, {
		Status: "allowed", Quota: gizpayclient.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 7}, RecheckAfterSeconds: 300,
	}}}
	runtime := New(exchanger, localadmission.New(func() time.Time { return now }), repository, func() time.Time { return now })
	if allowed, err := runtime.Admit(t.Context(), "giz_secret", 6); err != nil || !allowed {
		t.Fatalf("first Admit = (%v, %v)", allowed, err)
	}
	if allowed, err := runtime.Admit(t.Context(), "giz_secret", 4); err != nil || !allowed {
		t.Fatalf("second Admit = (%v, %v)", allowed, err)
	}
	if exchanger.calls != 1 {
		t.Fatalf("Exchange calls before depletion = %d, want 1", exchanger.calls)
	}
	if allowed, err := runtime.Admit(t.Context(), "giz_secret", 3); err != nil || !allowed {
		t.Fatalf("depleted Admit = (%v, %v)", allowed, err)
	}
	if exchanger.calls != 2 {
		t.Fatalf("Exchange calls after depletion = %d, want 2", exchanger.calls)
	}
}

// A denied response is itself a fresh quota answer. Until its recheck
// deadline, repeated customer attempts must be rejected locally instead of
// turning an empty balance into a hot loop against GizPay.
func TestAdmitCachesDeniedUntilRecheckDeadline(t *testing.T) {
	database := testdb.OpenGizWay(t)
	repository := store.New(database.SQL)
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	exchanger := &fakeExchanger{responses: []gizpayclient.ExchangeResponse{{
		Status: "denied", Quota: gizpayclient.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 0}, RecheckAfterSeconds: 300,
	}, {
		Status: "allowed", Quota: gizpayclient.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 10}, RecheckAfterSeconds: 300,
	}}}
	runtime := New(exchanger, localadmission.New(func() time.Time { return now }), repository, func() time.Time { return now })

	for attempt := range 10 {
		if allowed, err := runtime.Admit(t.Context(), "giz_denied", 1); err != nil || allowed {
			t.Fatalf("denied Admit %d = (%t, %v)", attempt, allowed, err)
		}
	}
	if exchanger.calls != 1 {
		t.Fatalf("Exchange calls during denied interval = %d, want 1", exchanger.calls)
	}

	now = now.Add(5 * time.Minute)
	if allowed, err := runtime.Admit(t.Context(), "giz_denied", 1); err != nil || !allowed {
		t.Fatalf("Admit at recheck deadline = (%t, %v)", allowed, err)
	}
	if exchanger.calls != 2 {
		t.Fatalf("Exchange calls after denied deadline = %d, want 2", exchanger.calls)
	}
}

// Different customer keys are independent admission subjects. A slow GizPay
// check for one key must not serialize every other customer behind it; only
// concurrent refreshes for the same key are serialized so they cannot each
// replace the counter with the same full balance.
func TestAdmitSerializesPerKeyWithoutBlockingOtherKeys(t *testing.T) {
	database := testdb.OpenGizWay(t)
	repository := store.New(database.SQL)
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	exchanger := &blockingExchanger{started: make(chan string, 2), release: make(chan struct{})}
	runtime := New(exchanger, localadmission.New(func() time.Time { return now }), repository, func() time.Time { return now })

	var wait sync.WaitGroup
	wait.Add(2)
	for _, key := range []string{"giz_key_a", "giz_key_b"} {
		go func() {
			defer wait.Done()
			_, _ = runtime.Admit(t.Context(), key, 1)
		}()
	}

	seen := map[string]bool{}
	for range 2 {
		select {
		case key := <-exchanger.started:
			seen[key] = true
		case <-time.After(time.Second):
			close(exchanger.release)
			wait.Wait()
			t.Fatalf("different key was blocked by another key's Exchange; started=%v", seen)
		}
	}
	close(exchanger.release)
	wait.Wait()
}

func TestPostgreSQLWorkerReportsCurrentProcessUsageWithoutAnotherCustomerRequest(t *testing.T) {
	database := testdb.OpenGizWay(t)
	repository := store.New(database.SQL)
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	repository.ConfigureClock(func() time.Time { return now })
	exchanger := &observedExchanger{calls: make(chan []quotaexchange.UsageRecord, 2)}
	runtime := New(exchanger, localadmission.New(func() time.Time { return now }), repository, func() time.Time { return now })
	if allowed, err := runtime.Admit(t.Context(), "giz_worker_key", 1); err != nil || !allowed {
		t.Fatalf("initial Admit=(%t,%v)", allowed, err)
	}
	<-exchanger.calls // initial empty report-and-query
	token, ok := runtime.runtimeToken("giz_worker_key")
	if !ok {
		t.Fatal("runtime token was not established")
	}
	record := quotaexchange.UsageRecord{
		UCGID: "ucg-worker", OperationID: "op-worker", PublicModel: "story-text",
		ModelVariantID: "variant", RatePublicationID: "publication", Metrics: map[string]int64{"request": 1},
		StartedAt: "2026-08-11T00:00:00.000000000Z", CompletedAt: "2026-08-11T00:00:01.000000000Z",
	}
	beginRuntimeExecution(t, database, repository, record.OperationID)
	if err := repository.EnqueueUsage(t.Context(), runtime.processID, token, record); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.Run(ctx, time.Hour)
	}()
	runtime.scheduleRetry("giz_worker_key")
	select {
	case usage := <-exchanger.calls:
		if len(usage) != 1 || usage[0].UCGID != record.UCGID {
			t.Fatalf("worker usage=%+v", usage)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not report queued Usage")
	}
	var status string
	var queryErr error
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if queryErr = database.SQL.Get(&status, `SELECT status FROM gateway_usage_outbox WHERE ucgid='ucg-worker'`); queryErr == nil && status == "reported" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if status != "reported" {
		t.Fatalf("worker outbox status=%q err=%v", status, queryErr)
	}
}

func TestPermanentUsageFailureTerminatesWithoutRetry(t *testing.T) {
	database := testdb.OpenGizWay(t)
	repository := store.New(database.SQL)
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	repository.ConfigureClock(func() time.Time { return now })
	exchanger := &scriptedExchanger{
		responses: []gizpayclient.ExchangeResponse{{Status: "allowed", Quota: gizpayclient.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 10}, RecheckAfterSeconds: 300}, {}},
		errors:    []error{nil, gizpayclient.ErrUsageUnpriceable},
	}
	runtime := New(exchanger, localadmission.New(func() time.Time { return now }), repository, func() time.Time { return now })
	if allowed, err := runtime.Admit(t.Context(), "giz_permanent", 1); err != nil || !allowed {
		t.Fatalf("Admit=(%t,%v)", allowed, err)
	}
	token, _ := runtime.runtimeToken("giz_permanent")
	record := quotaexchange.UsageRecord{UCGID: "ucg-permanent", OperationID: "op-permanent", PublicModel: "model", ModelVariantID: "variant", RatePublicationID: "rate", Metrics: map[string]int64{"request": 1}, StartedAt: "2026-08-11T00:00:00.000000000Z", CompletedAt: "2026-08-11T00:00:01.000000000Z"}
	beginRuntimeExecution(t, database, repository, record.OperationID)
	if err := repository.EnqueueUsage(t.Context(), runtime.processID, token, record); err != nil {
		t.Fatal(err)
	}
	runtime.scheduleRetry("giz_permanent")
	runtime.flushKey(t.Context(), "giz_permanent")
	var terminal struct {
		Status string `db:"status"`
		Reason string `db:"last_error"`
	}
	if err := database.SQL.Get(&terminal, `SELECT status,last_error FROM gateway_usage_outbox WHERE ucgid='ucg-permanent'`); err != nil || terminal.Status != "failed" || terminal.Reason != "usage unpriceable" {
		t.Fatalf("terminal state=%+v err=%v", terminal, err)
	}
	runtime.flushRetries(t.Context())
	if exchanger.calls != 2 {
		t.Fatalf("permanent failure retried: calls=%d", exchanger.calls)
	}
}

func TestPostgreSQLTemporaryUsageFailureRetriesAndReports(t *testing.T) {
	database := testdb.OpenGizWay(t)
	repository := store.New(database.SQL)
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	repository.ConfigureClock(func() time.Time { return now })
	exchanger := &scriptedExchanger{
		responses: []gizpayclient.ExchangeResponse{
			{Status: "allowed", Quota: gizpayclient.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 10}, RecheckAfterSeconds: 300},
			{},
			{Status: "allowed", Quota: gizpayclient.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 9}, RecheckAfterSeconds: 300},
		},
		errors: []error{nil, gizpayclient.ErrTemporarilyUnavailable, nil},
	}
	runtime := New(exchanger, localadmission.New(func() time.Time { return now }), repository, func() time.Time { return now })
	if allowed, err := runtime.Admit(t.Context(), "giz_temporary", 1); err != nil || !allowed {
		t.Fatalf("Admit=(%t,%v)", allowed, err)
	}
	token, _ := runtime.runtimeToken("giz_temporary")
	record := quotaexchange.UsageRecord{UCGID: "ucg-temporary", OperationID: "op-temporary", PublicModel: "model", ModelVariantID: "variant", RatePublicationID: "rate", Metrics: map[string]int64{"request": 1}, StartedAt: "2026-08-11T00:00:00.000000000Z", CompletedAt: "2026-08-11T00:00:01.000000000Z"}
	beginRuntimeExecution(t, database, repository, record.OperationID)
	if err := repository.EnqueueUsage(t.Context(), runtime.processID, token, record); err != nil {
		t.Fatal(err)
	}
	runtime.scheduleRetry("giz_temporary")
	runtime.flushKey(t.Context(), "giz_temporary")
	var status string
	if err := database.SQL.Get(&status, `SELECT status FROM gateway_usage_outbox WHERE ucgid='ucg-temporary'`); err != nil || status != "pending" {
		t.Fatalf("temporary status=%q err=%v", status, err)
	}
	now = now.Add(2 * time.Second)
	runtime.flushRetries(t.Context())
	if err := database.SQL.Get(&status, `SELECT status FROM gateway_usage_outbox WHERE ucgid='ucg-temporary'`); err != nil || status != "reported" {
		t.Fatalf("retried status=%q err=%v", status, err)
	}
	if exchanger.calls != 3 {
		t.Fatalf("Exchange calls=%d, want initial plus one retry", exchanger.calls)
	}
}

func TestInvalidKeyDropsUnusedRuntimeContext(t *testing.T) {
	database := testdb.OpenGizWay(t)
	repository := store.New(database.SQL)
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	exchanger := &scriptedExchanger{responses: []gizpayclient.ExchangeResponse{{}}, errors: []error{gizpayclient.ErrInvalidAPIKey}}
	local := localadmission.New(func() time.Time { return now })
	runtime := New(exchanger, local, repository, func() time.Time { return now })
	if allowed, err := runtime.Admit(t.Context(), "giz_invalid", 1); err == nil || allowed {
		t.Fatalf("Admit=(%t,%v), want permanent error", allowed, err)
	}
	if _, ok := runtime.runtimeToken("giz_invalid"); ok {
		t.Fatal("invalid API key retained runtime token")
	}
	if !local.Forgettable("giz_invalid") {
		t.Fatal("invalid API key retained a usable local state")
	}
}

func TestCleanupDropsExpiredContextWithoutPendingUsage(t *testing.T) {
	database := testdb.OpenGizWay(t)
	repository := store.New(database.SQL)
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	exchanger := &fakeExchanger{responses: []gizpayclient.ExchangeResponse{{
		Status: "allowed", Quota: gizpayclient.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 10}, RecheckAfterSeconds: 300,
	}}}
	local := localadmission.New(func() time.Time { return now })
	runtime := New(exchanger, local, repository, func() time.Time { return now })
	if allowed, err := runtime.Admit(t.Context(), "giz_expired", 1); err != nil || !allowed {
		t.Fatalf("Admit=(%t,%v)", allowed, err)
	}
	if _, ok := runtime.runtimeToken("giz_expired"); !ok || local.Forgettable("giz_expired") {
		t.Fatalf("initial context=(token=%t forgettable=%t), want (true,false)", ok, local.Forgettable("giz_expired"))
	}

	runtime.Release("giz_expired", 1)
	now = now.Add(5 * time.Minute)
	runtime.cleanupIdleContexts(t.Context())
	if _, ok := runtime.runtimeToken("giz_expired"); ok {
		t.Fatal("expired key retained runtime token")
	}
	if !local.Forgettable("giz_expired") {
		t.Fatal("expired key retained a usable local state")
	}
}

func TestCleanupKeepsExpiredContextWithPendingUsage(t *testing.T) {
	database := testdb.OpenGizWay(t)
	repository := store.New(database.SQL)
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	exchanger := &fakeExchanger{responses: []gizpayclient.ExchangeResponse{{
		Status: "allowed", Quota: gizpayclient.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 10}, RecheckAfterSeconds: 300,
	}}}
	local := localadmission.New(func() time.Time { return now })
	runtime := New(exchanger, local, repository, func() time.Time { return now })
	if allowed, err := runtime.Admit(t.Context(), "giz_pending", 1); err != nil || !allowed {
		t.Fatalf("Admit=(%t,%v)", allowed, err)
	}
	token, ok := runtime.runtimeToken("giz_pending")
	if !ok {
		t.Fatal("runtime token was not established")
	}
	record := quotaexchange.UsageRecord{
		UCGID: "ucg-cleanup-pending", OperationID: "op-cleanup-pending", PublicModel: "model",
		ModelVariantID: "variant", RatePublicationID: "rate", Metrics: map[string]int64{"request": 1},
		StartedAt: "2026-08-11T00:00:00.000000000Z", CompletedAt: "2026-08-11T00:00:01.000000000Z",
	}
	beginRuntimeExecution(t, database, repository, record.OperationID)
	if err := repository.EnqueueUsage(t.Context(), runtime.processID, token, record); err != nil {
		t.Fatal(err)
	}
	runtime.Release("giz_pending", 1)
	now = now.Add(5 * time.Minute)
	runtime.cleanupIdleContexts(t.Context())
	if _, ok := runtime.runtimeToken("giz_pending"); !ok {
		t.Fatal("pending Usage lost its runtime key context")
	}
}

func beginRuntimeExecution(t *testing.T, database *storage.Storage, repository *store.Store, operationID string) {
	t.Helper()
	seedRuntimeExecutionDependencies(t, database)
	if err := repository.BeginRegionalExecution(t.Context(), operationID, "story-text",
		"runtime-variant", "runtime-publication", "https", "buffered", 0,
		"2026-08-11T00:00:00.000000000Z"); err != nil {
		t.Fatalf("begin runtime execution %q: %v", operationID, err)
	}
}

func seedRuntimeExecutionDependencies(t *testing.T, database *storage.Storage) {
	t.Helper()
	const at = "2026-08-11T00:00:00.000000000Z"
	for _, statement := range []string{
		`INSERT INTO providers(id,slug,name,status,created_at,updated_at) VALUES ('runtime-provider','runtime-provider','Runtime Provider','active',$1,$1) ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO provider_endpoints(id,provider_id,name,base_url,credential_ref,priority,weight,status,created_at,updated_at) VALUES ('runtime-endpoint','runtime-provider','Runtime Endpoint','https://provider.invalid','test-only',1,100,'active',$1,$1) ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO models(id,slug,name,modality,status,metadata,created_at,updated_at) VALUES ('runtime-model','story-text','Runtime Model','["text"]','active','{}',$1,$1) ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO model_variants(id,model_id,provider_endpoint_id,provider_model_name,variant_slug,capabilities,status,created_at,updated_at) VALUES ('runtime-variant','runtime-model','runtime-endpoint','runtime-model','primary','{}','active',$1,$1) ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO rate_publications(id,region,revision,content_hash,status,effective_at,created_at,updated_at) VALUES ('runtime-publication','global',1,decode('01','hex'),'active',$1,$1,$1) ON CONFLICT (id) DO NOTHING`,
	} {
		if _, err := database.SQL.Exec(statement, at); err != nil {
			t.Fatalf("seed runtime execution dependency: %v", err)
		}
	}
}
