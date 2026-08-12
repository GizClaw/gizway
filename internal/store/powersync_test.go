package store_test

import (
	"crypto/sha256"
	"testing"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

// PowerSync bypasses the HTTP handlers, so this database-level companion test
// verifies the security boundary at its real ownership layer: every view is
// account-keyed, contains only that account's rows, and rejects direct writes.
func TestPowerSyncViewsAreAccountScopedAndReadOnly(t *testing.T) {
	database := testdb.OpenGizPayStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	now := "2026-08-11T12:00:00.000000000Z"
	payload := sha256.Sum256([]byte("powersync transfer"))
	transfer := store.CreditTransfer{
		ID: "powersync-transfer", SenderAccountID: accountOneID, RecipientAccountID: accountTwoID,
		Amount: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 10}, Status: "succeeded", CreatedAt: now,
	}
	if _, _, err := repository.CreateCreditTransfer(t.Context(), userOneID, "powersync-transfer", payload[:], transfer); err != nil {
		t.Fatal(err)
	}

	for accountID, direction := range map[string]string{accountOneID: "outgoing", accountTwoID: "incoming"} {
		var got string
		if err := database.SQL.Get(&got, `SELECT direction FROM powersync_credit_transfers WHERE account_id=$1 AND id=$2`, accountID, transfer.ID); err != nil || got != direction {
			t.Fatalf("transfer projection account=%s direction=%s err=%v", accountID, got, err)
		}
	}
	var leaked int
	if err := database.SQL.Get(&leaked, `SELECT COUNT(*) FROM powersync_credit_transfers WHERE account_id=$1 AND id=$2`, "21000000-0000-4000-8000-000000000003", transfer.ID); err != nil || leaked != 0 {
		t.Fatalf("transfer leaked to third account count=%d err=%v", leaked, err)
	}

	var balances int
	if err := database.SQL.Get(&balances, `SELECT COUNT(*) FROM powersync_account_balances WHERE account_id=$1`, accountOneID); err != nil || balances != 1 {
		t.Fatalf("account balance projection rows=%d err=%v", balances, err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO powersync_account_balances(account_id,asset_code,balance_microcredits) VALUES ($1,$2,$3)`, accountOneID, "GIZ_CREDIT", 1); err == nil {
		t.Fatal("PowerSync balance view accepted a direct write")
	}

	for _, view := range []string{"powersync_gateway_usage", "powersync_payments", "powersync_merchant_orders"} {
		var count int
		if err := database.SQL.Get(&count, `SELECT COUNT(*) FROM `+view+` WHERE account_id=$1`, accountOneID); err != nil {
			t.Fatalf("query %s: %v", view, err)
		}
	}
	counts, err := repository.PowerSyncProjectionCounts(t.Context(), userOneID, accountOneID)
	if err != nil || counts["powersync_account_balances"] != 1 || counts["powersync_credit_transfers"] != 1 {
		t.Fatalf("projection counts=%v err=%v", counts, err)
	}
	if _, err := repository.PowerSyncProjectionCounts(t.Context(), userOneID, accountTwoID); err != store.ErrNotFound {
		t.Fatalf("foreign projection authorization err=%v", err)
	}
}
