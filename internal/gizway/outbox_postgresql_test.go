package gizway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/GizClaw/gizway/internal/testdb"
)

func TestReportOutboxDoesNotSendWhenSendingStateCannotBePersisted(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required; run through make test-unit-go-race")
	}
	database := testdb.OpenGizWay(t)
	db := database.SQL
	_, err := db.ExecContext(t.Context(), `
		INSERT INTO ai_orders(id,external_order_id,provider_key_id,subscription_key_hmac,subscription_key_id,account_id,subscription_id,product_id,owner_identity_issuer,owner_identity_subject,model_id,provider_id,gross_microcredits,commission_microcredits,pricing_snapshot,provider_snapshot,status)
		VALUES ('ai-order-outbox-send','order-outbox-send','key-outbox-send','hmac-outbox-send','skey-outbox-send','account-outbox-send','subscription-outbox-send','product-outbox-send','issuer','subject','model-outbox-send','provider-outbox-send',10,1,'{}','{}','pending');
		INSERT INTO charge_outbox(id,external_order_id,ai_order_id,payload,status,recover_duplicate)
		VALUES ('outbox-send','order-outbox-send','ai-order-outbox-send','{}','pending',false);
		CREATE FUNCTION reject_outbox_sending() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.status='sending' THEN
				RAISE EXCEPTION 'injected sending-state write failure';
			END IF;
			RETURN NEW;
		END;
		$$;
		CREATE TRIGGER reject_outbox_sending_before_remote_call
		BEFORE UPDATE ON charge_outbox
		FOR EACH ROW EXECUTE FUNCTION reject_outbox_sending();
	`)
	if err != nil {
		t.Fatalf("prepare Outbox sending failure: %v", err)
	}

	var remoteRequests atomic.Int64
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		remoteRequests.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer remote.Close()
	handler := &Handler{config: Config{
		DB: db, GizPayURL: remote.URL, HTTPClient: remote.Client(), Now: time.Now,
		ServiceToken: func(context.Context) (string, error) { return "machine-token", nil },
	}}

	handler.reportOutbox("order-outbox-send")
	if got := remoteRequests.Load(); got != 0 {
		t.Fatalf("remote Charge requests=%d after sending-state write failure, want 0", got)
	}
	var status string
	var recoverDuplicate bool
	if err := db.QueryRowxContext(t.Context(), `SELECT status,recover_duplicate FROM charge_outbox WHERE id='outbox-send'`).Scan(&status, &recoverDuplicate); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || recoverDuplicate {
		t.Fatalf("Outbox state=(%s,%t), want unchanged pending,false", status, recoverDuplicate)
	}
}

func TestReportOutboxRejectsInvalidGizPayURLWithoutChangingState(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required; run through make test-unit-go-race")
	}
	database := testdb.OpenGizWay(t)
	db := database.SQL
	insertOutboxFixture(t, db, "invalid-url")
	handler := &Handler{config: Config{
		DB: db, GizPayURL: "://invalid", Now: time.Now,
		ServiceToken: func(context.Context) (string, error) { return "machine-token", nil },
	}}

	handler.reportOutbox("order-invalid-url")
	assertOutboxState(t, db, "outbox-invalid-url", "pending", false)
}

func TestOutboxWorkerReclaimsSendingRowsWithoutRestart(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required; run through make test-unit-go-race")
	}
	database := testdb.OpenGizWay(t)
	db := database.SQL
	insertOutboxFixture(t, db, "reclaim")
	if _, err := db.ExecContext(t.Context(), `UPDATE charge_outbox SET status='sending',recover_duplicate=true WHERE id='outbox-reclaim'`); err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer remote.Close()
	handler := &Handler{
		config: Config{
			DB: db, GizPayURL: remote.URL, HTTPClient: remote.Client(), Now: time.Now,
			ServiceToken:        func(context.Context) (string, error) { return "machine-token", nil },
			OutboxRetryInterval: time.Millisecond,
		},
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	go handler.runBackgroundWorkers()
	t.Cleanup(func() { _ = handler.Close() })

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := db.GetContext(t.Context(), &status, `SELECT status FROM charge_outbox WHERE id='outbox-reclaim'`); err != nil {
			t.Fatal(err)
		}
		if status == "reported" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("sending Outbox row was not reclaimed by the running worker")
}

func insertOutboxFixture(t *testing.T, db *sqlx.DB, suffix string) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `
		WITH inserted_order AS (
			INSERT INTO ai_orders(id,external_order_id,provider_key_id,subscription_key_hmac,subscription_key_id,account_id,subscription_id,product_id,owner_identity_issuer,owner_identity_subject,model_id,provider_id,gross_microcredits,commission_microcredits,pricing_snapshot,provider_snapshot,status)
			VALUES ($3,$4,'key-outbox','hmac-outbox','skey-outbox','account-outbox','subscription-outbox','product-outbox','issuer','subject',$1,$2,10,1,'{}','{}','pending')
			RETURNING id
		)
		INSERT INTO charge_outbox(id,external_order_id,ai_order_id,payload,status,recover_duplicate)
		SELECT $5,$4,id,'{}','pending',false FROM inserted_order
	`, "model-"+suffix, "provider-"+suffix, "ai-order-"+suffix, "order-"+suffix, "outbox-"+suffix)
	if err != nil {
		t.Fatalf("insert Outbox fixture: %v", err)
	}
}

func assertOutboxState(t *testing.T, db *sqlx.DB, id, wantStatus string, wantRecover bool) {
	t.Helper()
	var status string
	var recover bool
	if err := db.QueryRowxContext(t.Context(), `SELECT status,recover_duplicate FROM charge_outbox WHERE id=$1`, id).Scan(&status, &recover); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || recover != wantRecover {
		t.Fatalf("Outbox state=(%s,%t), want (%s,%t)", status, recover, wantStatus, wantRecover)
	}
}
