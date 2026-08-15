package assertions

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type BillingSnapshot struct {
	AIOrders, Outboxes, ReportedOutboxes, Logs int64
	Charges, Commissions, LedgerTransactions   int64
	LedgerEntries, ProviderCalls               int64
}

func CaptureBillingSnapshot(t *testing.T, globalDSN, payDSN, providerURL string) BillingSnapshot {
	t.Helper()
	return BillingSnapshot{
		AIOrders:           countQuery(t, globalDSN, `SELECT count(*) FROM gizway.ai_orders`),
		Outboxes:           countQuery(t, globalDSN, `SELECT count(*) FROM gizway.charge_outbox`),
		ReportedOutboxes:   countQuery(t, globalDSN, `SELECT count(*) FROM gizway.charge_outbox WHERE status='reported'`),
		Logs:               countQuery(t, globalDSN, `SELECT count(*) FROM bifrost_logs.logs`),
		Charges:            countQuery(t, payDSN, `SELECT count(*) FROM payg_charges`),
		Commissions:        countQuery(t, payDSN, `SELECT count(*) FROM charge_commissions`),
		LedgerTransactions: countQuery(t, payDSN, `SELECT count(*) FROM ledger_transactions WHERE transaction_type='payg_charge'`),
		LedgerEntries:      countQuery(t, payDSN, `SELECT count(*) FROM ledger_entries entry JOIN ledger_transactions transaction ON transaction.id=entry.transaction_id WHERE transaction.transaction_type='payg_charge'`),
		ProviderCalls:      providerChatCalls(t, providerURL),
	}
}

func AssertBilledCallEventually(t *testing.T, before BillingSnapshot, globalDSN, payDSN, providerURL, providerKeyID, executionMode string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		after := CaptureBillingSnapshot(t, globalDSN, payDSN, providerURL)
		if after.AIOrders == before.AIOrders+1 &&
			after.Outboxes == before.Outboxes+1 &&
			after.ReportedOutboxes == before.ReportedOutboxes+1 &&
			after.Logs == before.Logs+1 &&
			after.Charges == before.Charges+1 &&
			after.Commissions == before.Commissions+1 &&
			after.LedgerTransactions == before.LedgerTransactions+1 &&
			after.LedgerEntries > before.LedgerEntries &&
			after.ProviderCalls == before.ProviderCalls+1 {
			assertLatestExecutionLog(t, globalDSN, providerKeyID, executionMode, "success", true)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("billed call state did not converge: before=%+v after=%+v", before, after)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func AssertNoFinancialChange(t *testing.T, before BillingSnapshot, globalDSN, payDSN, providerURL string, providerDelta, logDelta int64) BillingSnapshot {
	t.Helper()
	after := CaptureBillingSnapshot(t, globalDSN, payDSN, providerURL)
	if after.AIOrders != before.AIOrders || after.Outboxes != before.Outboxes ||
		after.Charges != before.Charges || after.Commissions != before.Commissions ||
		after.LedgerTransactions != before.LedgerTransactions || after.LedgerEntries != before.LedgerEntries {
		t.Fatalf("failed call changed financial state: before=%+v after=%+v", before, after)
	}
	if after.ProviderCalls != before.ProviderCalls+providerDelta || after.Logs != before.Logs+logDelta {
		t.Fatalf("failed call execution delta mismatch: before=%+v after=%+v want provider=%d logs=%d", before, after, providerDelta, logDelta)
	}
	return after
}

func AssertLatestErrorLog(t *testing.T, globalDSN, providerKeyID, executionMode string) {
	t.Helper()
	assertLatestExecutionLog(t, globalDSN, providerKeyID, executionMode, "error", false)
}

func AssertLatestSuccessLog(t *testing.T, globalDSN, providerKeyID, executionMode string) {
	t.Helper()
	assertLatestExecutionLog(t, globalDSN, providerKeyID, executionMode, "success", true)
}

func assertLatestExecutionLog(t *testing.T, dsn, providerKeyID, executionMode, status string, requireUsage bool) {
	t.Helper()
	connection, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(t.Context())
	var selectedKeyID, gotStatus, gotMode string
	var latency float64
	var promptTokens, completionTokens int
	err = connection.QueryRow(t.Context(), `
		SELECT selected_key_id,status,latency,prompt_tokens,completion_tokens,
		       metadata::jsonb->>'execution_mode'
		FROM bifrost_logs.logs
		WHERE selected_key_id=$1 AND status=$2
		  AND metadata::jsonb->>'execution_mode'=$3
		ORDER BY created_at DESC,id DESC LIMIT 1
	`, providerKeyID, status, executionMode).Scan(&selectedKeyID, &gotStatus, &latency, &promptTokens, &completionTokens, &gotMode)
	if err != nil {
		t.Fatal(err)
	}
	if selectedKeyID != providerKeyID || gotStatus != status || gotMode != executionMode || latency < 0 {
		t.Fatalf("latest Bifrost Log selected=%q status=%q mode=%q latency=%v", selectedKeyID, gotStatus, gotMode, latency)
	}
	if requireUsage && (promptTokens <= 0 || completionTokens <= 0) {
		t.Fatalf("latest Bifrost Log lacks token metrics: input=%d output=%d", promptTokens, completionTokens)
	}
}

func countQuery(t *testing.T, dsn, query string) int64 {
	t.Helper()
	connection, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(t.Context())
	var count int64
	if err := connection.QueryRow(t.Context(), query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func providerChatCalls(t *testing.T, providerURL string) int64 {
	t.Helper()
	stats := ProviderStats(t, providerURL)
	value, ok := stats["chat_calls"].(float64)
	if !ok {
		t.Fatal(fmt.Errorf("Provider stats lack chat_calls: %v", stats))
	}
	return int64(value)
}

func ProviderStats(t *testing.T, providerURL string) map[string]any {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, providerURL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Provider stats returned %d", response.StatusCode)
	}
	var stats map[string]any
	if err := json.NewDecoder(response.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	return stats
}
