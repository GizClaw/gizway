package store_test

import (
	"crypto/sha256"
	"testing"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

func TestAdminPagesAreBoundedFilteredAndCursorAddressable(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	repository := store.New(database.SQL)

	users, err := repository.AdminListUsersPage(t.Context(), store.AdminListQuery{Query: "gizway.test", Limit: 1})
	if err != nil || len(users.Items) != 1 || !users.HasMore || users.NextCursor == nil || *users.NextCursor != "1" {
		t.Fatalf("first user page=%+v err=%v", users, err)
	}
	nextUsers, err := repository.AdminListUsersPage(t.Context(), store.AdminListQuery{Query: "gizway.test", Cursor: *users.NextCursor, Limit: 1})
	if err != nil || len(nextUsers.Items) != 1 || nextUsers.Items[0].ID == users.Items[0].ID {
		t.Fatalf("next user page=%+v err=%v", nextUsers, err)
	}
	if _, err := repository.AdminListUsersPage(t.Context(), store.AdminListQuery{Cursor: "not-an-offset"}); err == nil {
		t.Fatal("invalid Admin cursor succeeded")
	}

	administrators, err := repository.ListAdministratorsPage(t.Context(), store.AdminListQuery{Status: "active"})
	if err != nil || len(administrators.Items) != 1 || administrators.Items[0].Status != "active" {
		t.Fatalf("administrator page=%+v err=%v", administrators, err)
	}
	models, err := repository.ListModelsPage(t.Context(), store.AdminListQuery{Status: "active", Limit: 100})
	if err != nil || len(models.Items) == 0 {
		t.Fatalf("model page=%+v err=%v", models, err)
	}
	keys, err := repository.AdminListAPIKeysPage(t.Context(), store.AdminListQuery{AccountID: accountOneID, Status: "active", KeyPrefix: "giz_story"})
	if err != nil || len(keys.Items) == 0 {
		t.Fatalf("API key page=%+v err=%v", keys, err)
	}
	merchants, err := repository.ListMerchantsPage(t.Context(), store.AdminListQuery{Status: "approved"})
	if err != nil || len(merchants.Items) != 1 || merchants.Items[0]["status"] != "approved" {
		t.Fatalf("merchant page=%+v err=%v", merchants, err)
	}

	now := "2026-08-10T03:00:00.000000000Z"
	command := store.GatewayCommand{
		ID: "admin-page-gateway", AccountID: accountOneID,
		APIKeyID: "31000000-0000-4000-8000-000000000001",
		ModelID:  "81000000-0000-4000-8000-000000000001", VariantID: "91000000-0000-4000-8000-000000000001",
		Operation: "chat.completions", IdempotencyKey: "admin-page-gateway", PayloadHash: []byte("page"),
		ReserveAmount: 1, Protocol: "https", StartedAt: now, ExecutionSnapshot: []byte(`{"plan":"page"}`),
	}
	if _, err := repository.BeginGatewayCommand(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	payload := sha256.Sum256([]byte("admin page transfer"))
	transfer := store.CreditTransfer{ID: "admin-page-transfer", SenderAccountID: accountOneID, RecipientAccountID: accountTwoID, Amount: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 1}, Status: "succeeded", CreatedAt: now, CompletedAt: &now}
	if _, _, err := repository.CreateCreditTransfer(t.Context(), userOneID, "admin-page-transfer", payload[:], transfer); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		kind  string
		query store.AdminListQuery
	}{
		{"gateway_requests", store.AdminListQuery{AccountID: accountOneID, APIKeyID: command.APIKeyID, ModelID: command.ModelID, Status: "started", From: now, To: "2026-08-10T04:00:00.000000000Z"}},
		{"payments", store.AdminListQuery{Type: "transfer", AccountID: accountOneID, Status: "succeeded", From: now, To: "2026-08-10T04:00:00.000000000Z"}},
		{"ledger_accounts", store.AdminListQuery{OwnerAccountID: accountOneID, Kind: "user_credit"}},
		{"ledger_transactions", store.AdminListQuery{TransactionType: "transfer", ReferenceID: transfer.ID, From: now, To: "2026-08-10T04:00:00.000000000Z"}},
		{"webhook_deliveries", store.AdminListQuery{MerchantID: "22000000-0000-4000-8000-000000000002", Status: "pending"}},
		{"audit_events", store.AdminListQuery{ActorID: userOneID, Action: "credit_transfer.created", ResourceType: "credit_transfer", ResourceID: transfer.ID, From: now, To: "2026-08-10T04:00:00.000000000Z"}},
	}
	for _, test := range cases {
		t.Run(test.kind, func(t *testing.T) {
			page, err := repository.AdminRowsPage(t.Context(), test.kind, test.query)
			if err != nil {
				t.Fatal(err)
			}
			if test.kind != "webhook_deliveries" && len(page.Items) == 0 {
				t.Fatalf("empty %s page", test.kind)
			}
		})
	}
	if _, err := repository.AdminRowsPage(t.Context(), "secrets", store.AdminListQuery{}); err == nil {
		t.Fatal("unsupported Admin query succeeded")
	}
}
