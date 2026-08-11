package merchant

import (
	"context"
	"crypto/sha256"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
	"github.com/idy/gizway/internal/timetext"
)

func TestPolicyValidation(t *testing.T) {
	service := New(nil)
	service.now = func() time.Time { return time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC) }
	validExpiry := "2026-08-11T00:00:00.000000000Z"
	invalid := []CreateIntentRequest{
		{},
		{ExternalOrderID: "o", Amount: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 100}, ExpiresAt: "bad"},
		{ExternalOrderID: "o", Amount: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 1}, ExpiresAt: validExpiry},
		{ExternalOrderID: "o", Amount: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: math.MaxInt64}, ExpiresAt: validExpiry},
	}
	for i, request := range invalid {
		if _, _, err := service.CreateIntent(context.Background(), "merchant", "", "key", request); err == nil {
			t.Fatalf("invalid intent %d succeeded", i)
		}
	}

	for _, test := range []struct {
		url    string
		events []string
	}{
		{url: "relative", events: []string{"payment_intent.succeeded"}},
		{url: "ftp://example.test", events: []string{"payment_intent.succeeded"}},
		{url: "https://user:pass@example.test/hook", events: []string{"payment_intent.succeeded"}},
		{url: "http://localhost/hook", events: []string{"payment_intent.succeeded"}},
		{url: "http://127.0.0.1/hook", events: []string{"payment_intent.succeeded"}},
		{url: "http://[::1]/hook", events: []string{"payment_intent.succeeded"}},
		{url: "http://169.254.169.254/latest", events: []string{"payment_intent.succeeded"}},
		{url: "http://10.0.0.1/hook", events: []string{"payment_intent.succeeded"}},
		{url: "https://example.test"},
		{url: "https://example.test", events: []string{"unknown"}},
	} {
		if _, _, _, err := service.CreateWebhookEndpoint(context.Background(), "merchant", "test-key", test.url, test.events); err == nil {
			t.Fatalf("invalid webhook %+v succeeded", test)
		}
	}
	if err := validateWebhookURL("https://webhooks.example.com/events", false); err != nil {
		t.Fatalf("public webhook URL rejected: %v", err)
	}
	if err := validateWebhookURL("http://webhooks.example.com/events", false); err == nil {
		t.Fatal("production plaintext HTTP webhook accepted")
	}
	if err := validateWebhookURL("http://127.0.0.1/events", true); err != nil {
		t.Fatalf("story loopback webhook URL rejected: %v", err)
	}
	if _, err := safeWebhookDialContext(context.Background(), "tcp", "127.0.0.1:80"); err == nil {
		t.Fatal("safe webhook dialer accepted loopback")
	}
	if _, err := safeWebhookDialContext(context.Background(), "tcp", "missing-port"); err == nil {
		t.Fatal("safe webhook dialer accepted malformed address")
	}
	production := New(nil)
	privateRedirect := &http.Request{URL: &url.URL{Scheme: "http", Host: "127.0.0.1", Path: "/hook"}}
	if err := production.http.CheckRedirect(privateRedirect, nil); err == nil {
		t.Fatal("production webhook client accepted private redirect")
	}
	story := NewForStoryTests(nil)
	if err := story.http.CheckRedirect(privateRedirect, nil); err != nil {
		t.Fatalf("story webhook client rejected fixture redirect: %v", err)
	}
	for _, address := range []string{"0.0.0.0", "169.254.1.1", "224.0.0.1", "ff02::1"} {
		if !unsafeWebhookIP(net.ParseIP(address)) {
			t.Fatalf("unsafeWebhookIP(%s) = false", address)
		}
	}
}

func TestDeliverRejectsPersistedPrivateTarget(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	endpoint := store.WebhookEndpoint{
		ID: "unsafe-endpoint", URL: "http://127.0.0.1/private", Events: store.JSON(`["payment_intent.succeeded"]`),
		SigningSecret: "whsec_test", CreatedAt: "2026-08-10T00:00:00.000000000Z",
	}
	hash := sha256.Sum256([]byte("unsafe endpoint"))
	if _, _, err := repository.CreateWebhookEndpoint(context.Background(), "22000000-0000-4000-8000-000000000002", "unsafe-key", hash[:], endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO webhook_events(id,merchant_account_id,event_type,resource_id,payload,created_at) VALUES ('unsafe-event','22000000-0000-4000-8000-000000000002','payment_intent.succeeded','intent','{}','2026-08-10T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO webhook_deliveries(id,event_id,endpoint_id,attempt,status,created_at) VALUES ('unsafe-delivery','unsafe-event','unsafe-endpoint',1,'pending','2026-08-10T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	if err := New(repository).Deliver(context.Background(), "unsafe-delivery"); err == nil {
		t.Fatal("Deliver accepted a persisted private target")
	}
}

func TestDispatchRecoverableClaimsAndCompletesPendingOutbox(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	calls := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Gizway-Signature") == "" {
			t.Error("dispatcher omitted signature")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	endpoint := store.WebhookEndpoint{ID: "recovery-endpoint", URL: receiver.URL, Events: store.JSON(`["payment_intent.succeeded"]`), SigningSecret: "whsec_recovery", CreatedAt: "2026-08-10T00:00:00.000000000Z"}
	hash := sha256.Sum256([]byte("recovery endpoint"))
	if _, _, err := repository.CreateWebhookEndpoint(t.Context(), "22000000-0000-4000-8000-000000000002", "recovery-key", hash[:], endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO webhook_events(id,merchant_account_id,event_type,resource_id,payload,created_at) VALUES ('recovery-event','22000000-0000-4000-8000-000000000002','payment_intent.succeeded','intent','{}','2026-08-10T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO webhook_deliveries(id,event_id,endpoint_id,signing_secret_snapshot,attempt,status,created_at) VALUES ('recovery-delivery','recovery-event','recovery-endpoint','whsec_recovery',1,'pending','2026-08-10T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	service := NewForStoryTests(repository)
	service.now = func() time.Time { return time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC) }
	if err := service.DispatchRecoverable(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := database.SQL.Get(&status, `SELECT status FROM webhook_deliveries WHERE id='recovery-delivery'`); err != nil || status != "succeeded" || calls != 1 {
		t.Fatalf("status=%q calls=%d err=%v", status, calls, err)
	}
	// A second dispatcher pass sees no work and cannot repeat the side effect.
	if err := service.DispatchRecoverable(t.Context(), 10); err != nil || calls != 1 {
		t.Fatalf("replay calls=%d err=%v", calls, err)
	}
}

func TestFailedWebhookSchedulesBoundedBackoffAttempt(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	calls := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer receiver.Close()
	endpoint := store.WebhookEndpoint{ID: "retry-endpoint", URL: receiver.URL, Events: store.JSON(`["payment_intent.succeeded"]`), SigningSecret: "whsec_retry", CreatedAt: "2026-08-10T00:00:00.000000000Z"}
	hash := sha256.Sum256([]byte("retry endpoint"))
	if _, _, err := repository.CreateWebhookEndpoint(t.Context(), "22000000-0000-4000-8000-000000000002", "retry-key", hash[:], endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO webhook_events(id,merchant_account_id,event_type,resource_id,payload,created_at) VALUES ('retry-event','22000000-0000-4000-8000-000000000002','payment_intent.succeeded','retry-intent','{}','2026-08-10T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO webhook_deliveries(id,event_id,endpoint_id,signing_secret_snapshot,attempt,status,created_at) VALUES ('retry-delivery','retry-event','retry-endpoint','whsec_retry',1,'pending','2026-08-10T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	service := NewForStoryTests(repository)
	current := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return current }
	if err := service.DispatchRecoverable(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	var attempt, pending int
	var next string
	if err := database.SQL.QueryRow(`SELECT attempt,next_attempt_at FROM webhook_deliveries WHERE event_id='retry-event' AND status='pending'`).Scan(&attempt, &next); err != nil {
		t.Fatal(err)
	}
	if attempt != 2 || next != timetext.Format(current.Add(time.Second)) || calls != 1 {
		t.Fatalf("attempt=%d next=%s calls=%d", attempt, next, calls)
	}
	// Before next_attempt_at, another dispatcher pass performs no network call.
	if err := service.DispatchRecoverable(t.Context(), 10); err != nil || calls != 1 {
		t.Fatalf("early retry calls=%d err=%v", calls, err)
	}
	current = current.Add(time.Second)
	_ = service.DispatchRecoverable(t.Context(), 10)
	if err := database.SQL.Get(&pending, `SELECT COUNT(*) FROM webhook_deliveries WHERE event_id='retry-event' AND attempt=3 AND status='pending'`); err != nil || pending != 1 || calls != 2 {
		t.Fatalf("second retry pending=%d calls=%d err=%v", pending, calls, err)
	}
}

func TestWebhookAttemptFiveExhaustsWithoutAnotherRow(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer receiver.Close()
	endpoint := store.WebhookEndpoint{ID: "exhaust-endpoint", URL: receiver.URL, Events: store.JSON(`["payment_intent.succeeded"]`), SigningSecret: "whsec_exhaust", CreatedAt: "2026-08-10T00:00:00.000000000Z"}
	hash := sha256.Sum256([]byte("exhaust endpoint"))
	if _, _, err := repository.CreateWebhookEndpoint(t.Context(), "22000000-0000-4000-8000-000000000002", "exhaust-key", hash[:], endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO webhook_events(id,merchant_account_id,event_type,resource_id,payload,created_at) VALUES ('exhaust-event','22000000-0000-4000-8000-000000000002','payment_intent.succeeded','exhaust-intent','{}','2026-08-10T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO webhook_deliveries(id,event_id,endpoint_id,attempt,status,created_at) VALUES ('exhaust-delivery','exhaust-event','exhaust-endpoint',5,'pending','2026-08-10T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	service := NewForStoryTests(repository)
	service.now = func() time.Time { return time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC) }
	if err := service.DispatchRecoverable(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	var status string
	var rows int
	if err := database.SQL.Get(&status, `SELECT status FROM webhook_deliveries WHERE id='exhaust-delivery'`); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.Get(&rows, `SELECT COUNT(*) FROM webhook_deliveries WHERE event_id='exhaust-event'`); err != nil || status != "exhausted" || rows != 1 {
		t.Fatalf("status=%s rows=%d err=%v", status, rows, err)
	}
}
