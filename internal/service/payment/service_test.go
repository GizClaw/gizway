package payment

import (
	"context"
	"crypto/sha256"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	paymentadapter "github.com/idy/gizway/internal/adapter/payment"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
	"github.com/idy/gizway/internal/testfake/paymentprovider"
)

func TestPolicyAndSignatureValidation(t *testing.T) {
	service := New(nil, nil, "callback-secret")
	for _, request := range []CreateTopupRequest{
		{},
		{FiatCurrency: "EUR", FiatAmountMinor: 100},
		{FiatCurrency: "USD", FiatAmountMinor: math.MaxInt64},
	} {
		if _, _, err := service.CreateTopup(context.Background(), "u", "a", "k", request); err == nil {
			t.Fatalf("invalid top-up %+v succeeded", request)
		}
	}
	for _, signature := range []string{"", "v1=not-hex", "v1=00"} {
		if _, _, err := service.CompleteProviderEvent(context.Background(), []byte(`{}`), signature); err == nil {
			t.Fatalf("signature %q succeeded", signature)
		}
	}
	if _, _, err := service.Refund(context.Background(), "u", "a", "t", "k", store.CreditAmount{}); err == nil {
		t.Fatal("invalid refund succeeded")
	}
}

func TestRecoverPendingRefundsCompletesDurableProviderCommand(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	provider := httptest.NewServer(paymentprovider.Handler("unused-callback-secret"))
	defer provider.Close()
	service := New(repository, paymentadapter.New(provider.URL, "story-payment-key"), "unused-callback-secret")

	const (
		accountID = "21000000-0000-4000-8000-000000000001"
		userID    = "11000000-0000-4000-8000-000000000001"
		topupID   = "payment-recovery-topup"
		now       = "2026-08-10T03:00:00.000000000Z"
	)
	if _, err := database.SQL.Exec(`INSERT INTO topups(id,account_id,payment_provider,provider_reference,fiat_currency,fiat_amount_minor,base_fiat_minor,base_credit_microcredits,effective_fiat_minor,effective_credit_microcredits,discount_bps,credit_microcredits,refundable_microcredits,status,idempotency_key,payload_hash,created_at,completed_at) VALUES ($1,$2,'fixture','provider-payment-recovery','USD',900,90,1000000,90,1000000,0,10000000,10000000,'succeeded','payment-recovery-topup',decode('01','hex'),$3,$4)`, topupID, accountID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO credit_lots(id,account_id,topup_id,original_microcredits,remaining_microcredits,created_at) VALUES ('payment-recovery-lot',$1,$2,10000000,10000000,$3)`, accountID, topupID, now); err != nil {
		t.Fatal(err)
	}
	payload := sha256.Sum256([]byte("payment recovery refund"))
	refund, _, _, replayed, err := repository.CreateRefund(t.Context(), userID, accountID, topupID, "payment-recovery-refund", payload[:], 5_000_000, now)
	if err != nil || replayed || refund.Status != "pending" {
		t.Fatalf("CreateRefund=%+v replayed=%v err=%v", refund, replayed, err)
	}
	if err := service.RecoverPendingRefunds(t.Context(), 0); err != nil {
		t.Fatalf("zero-limit recovery: %v", err)
	}
	if err := service.RecoverPendingRefunds(t.Context(), 32); err != nil {
		t.Fatalf("RecoverPendingRefunds: %v", err)
	}
	if commands, err := repository.RecoverableRefunds(t.Context(), 32); err != nil || len(commands) != 0 {
		t.Fatalf("commands after recovery=%+v err=%v", commands, err)
	}
	createPending := func(key string, amount int64) store.RefundRecord {
		t.Helper()
		fingerprint := sha256.Sum256([]byte(key))
		refund, _, _, replayed, err := repository.CreateRefund(t.Context(), userID, accountID, topupID, key, fingerprint[:], amount, now)
		if err != nil || replayed || refund.Status != "pending" {
			t.Fatalf("CreateRefund(%s)=%+v replayed=%v err=%v", key, refund, replayed, err)
		}
		return refund
	}
	providerControl := func(path string) {
		t.Helper()
		response, err := http.Post(provider.URL+path, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("provider control %s status=%d", path, response.StatusCode)
		}
	}

	failedRefund := createPending("payment-recovery-failed", 1_000_000)
	providerControl("/v1/test/fail_next_refund")
	if err := service.RecoverPendingRefunds(t.Context(), 32); err != nil {
		t.Fatalf("failed-state recovery: %v", err)
	}
	if replay, _, _, replayed, err := repository.CreateRefund(t.Context(), userID, accountID, topupID, "payment-recovery-failed", sha256Bytes("payment-recovery-failed"), 1_000_000, now); err != nil || !replayed || replay.ID != failedRefund.ID || replay.Status != "failed" {
		t.Fatalf("failed refund replay=%+v replayed=%v err=%v", replay, replayed, err)
	}

	pendingRefund := createPending("payment-recovery-pending", 1_000_000)
	providerControl("/v1/test/pend_next_refund")
	if err := service.RecoverPendingRefunds(t.Context(), 32); err != nil {
		t.Fatalf("pending-state recovery: %v", err)
	}
	commands, err := repository.RecoverableRefunds(t.Context(), 32)
	if err != nil || len(commands) != 1 || commands[0].Refund.ID != pendingRefund.ID {
		t.Fatalf("provider-pending commands=%+v err=%v", commands, err)
	}
	if err := service.RecoverPendingRefunds(t.Context(), 32); err != nil {
		t.Fatalf("pending second recovery: %v", err)
	}
	if err := service.RecoverPendingRefunds(t.Context(), 32); err != nil {
		t.Fatalf("idempotent empty recovery: %v", err)
	}
	if err := New(repository, nil, "").RecoverPendingRefunds(t.Context(), 32); err != nil {
		t.Fatalf("unconfigured recovery: %v", err)
	}
}

func sha256Bytes(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}
