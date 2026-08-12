package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
	"github.com/idy/gizway/internal/timetext"
	"github.com/jmoiron/sqlx"
)

func TestStoreRebindsPostgreSQLPlaceholders(t *testing.T) {
	database := testdb.OpenGizPayStory(t)
	defer database.Close()
	// Wrap the live PostgreSQL handle explicitly to prove Store routes its
	// readable question-mark queries through sqlx.Rebind.
	postgresStyle := sqlx.NewDb(database.SQL.DB, "postgres")
	user, err := store.New(postgresStyle).GetUser(t.Context(), userOneID)
	if err != nil || user.Email != "active-one@gizway.test" {
		t.Fatalf("GetUser through PostgreSQL binder = %+v, %v", user, err)
	}
}

func TestAuthenticationUsesCanonicalFractionalSecondOrdering(t *testing.T) {
	database := testdb.OpenGizPayStory(t)
	defer database.Close()
	if _, err := database.SQL.Exec(`UPDATE api_keys SET expires_at=$1 WHERE key_prefix='giz_story_user_active_1'`, "2026-08-10T00:00:00.500000000Z"); err != nil {
		t.Fatal(err)
	}
	repository := store.New(database.SQL)
	hash := sha256.Sum256([]byte(userOneKey))
	if _, err := repository.AuthenticateGatewayKey(t.Context(), hash[:], "2026-08-10T00:00:00.000000000Z"); err != nil {
		t.Fatalf("key expired before fractional boundary: %v", err)
	}
	if _, err := repository.AuthenticateGatewayKey(t.Context(), hash[:], "2026-08-10T00:00:00.500000000Z"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("key remained active at expiry: %v", err)
	}
}

func TestAdminWebhookRetryCommandReplaysAndConflicts(t *testing.T) {
	database := testdb.OpenGizPayStory(t)
	defer database.Close()
	for _, statement := range []string{
		`INSERT INTO webhook_endpoints (id,merchant_account_id,url,events,signing_secret,status,created_at,updated_at) VALUES ('e1000000-0000-4000-8000-000000000001','22000000-0000-4000-8000-000000000002','http://127.0.0.1:1/unreachable','["payment_intent.succeeded"]','fixture-secret','active','2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z')`,
		`INSERT INTO webhook_events (id,merchant_account_id,event_type,resource_id,payload,created_at) VALUES ('e2000000-0000-4000-8000-000000000001','22000000-0000-4000-8000-000000000002','payment_intent.succeeded','seeded-payment-intent','{}','2026-08-10T00:00:00.000000000Z')`,
		`INSERT INTO webhook_deliveries (id,event_id,endpoint_id,attempt,status,error,created_at,completed_at) VALUES ('e3000000-0000-4000-8000-000000000001','e2000000-0000-4000-8000-000000000001','e1000000-0000-4000-8000-000000000001',1,'failed','fixture connection failure','2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:01.000000000Z')`,
	} {
		if _, err := database.SQL.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	repository := store.New(database.SQL)
	const (
		adminID  = "41000000-0000-4000-8000-000000000001"
		delivery = "e3000000-0000-4000-8000-000000000001"
	)
	created, err := repository.RetryWebhookDelivery(t.Context(), adminID, delivery, "retry-command", "2026-08-10T01:00:00.000000000Z")
	if err != nil || created == "" {
		t.Fatalf("RetryWebhookDelivery = %q, %v", created, err)
	}
	replayed, err := repository.RetryWebhookDelivery(t.Context(), adminID, delivery, "retry-command", "2026-08-10T01:00:01.000000000Z")
	if err != nil || replayed != created {
		t.Fatalf("retry replay = %q, want %q, err=%v", replayed, created, err)
	}
	if _, err := repository.RetryWebhookDelivery(t.Context(), adminID, "different-delivery", "retry-command", "2026-08-10T01:00:02.000000000Z"); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("retry payload mismatch err=%v", err)
	}
	if _, err := repository.RetryWebhookDelivery(t.Context(), adminID, delivery, "parallel-retry-command", "2026-08-10T01:00:03.000000000Z"); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("parallel retry chain err=%v, want idempotency conflict", err)
	}
	deliveries, err := repository.AdminRows(t.Context(), "webhook_deliveries", "")
	if err != nil || len(deliveries) != 2 {
		t.Fatalf("delivery rows=%d err=%v", len(deliveries), err)
	}
	audits, err := repository.AdminRows(t.Context(), "audit_events", "")
	if err != nil || len(audits) != 1 || audits[0]["action"] != "webhook_delivery.retried" {
		t.Fatalf("audit rows=%v err=%v", audits, err)
	}
}

func TestLedgerReversalRejectsProductTransaction(t *testing.T) {
	repository := newStore(t)
	_, err := repository.ReverseLedgerTransaction(t.Context(), "41000000-0000-4000-8000-000000000001", "c1000000-0000-4000-8000-000000000001", "reverse-topup", "must use refund workflow", "2026-08-10T01:00:00.000000000Z")
	if !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("ReverseLedgerTransaction(topup) err=%v, want idempotency conflict", err)
	}
}

func TestJSONValue(t *testing.T) {
	var value store.JSON
	for _, source := range []any{`{"ok":true}`, []byte(`[1,2]`), nil} {
		if err := value.Scan(source); err != nil {
			t.Fatalf("Scan(%T): %v", source, err)
		}
	}
	if err := value.Scan(42); err == nil {
		t.Fatal("Scan(integer) succeeded")
	}
	if err := value.Scan("not-json"); err == nil {
		t.Fatal("Scan(invalid JSON) succeeded")
	}
	if err := json.Unmarshal([]byte(`{"a":1}`), &value); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if _, err := json.Marshal(value); err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if err := value.UnmarshalJSON([]byte(`broken`)); err == nil {
		t.Fatal("UnmarshalJSON(invalid) succeeded")
	}
	invalid := store.JSON(`broken`)
	if _, err := invalid.MarshalJSON(); err == nil {
		t.Fatal("MarshalJSON(invalid) succeeded")
	}
	var empty store.JSON
	encoded, err := empty.MarshalJSON()
	if err != nil || string(encoded) != "null" {
		t.Fatalf("MarshalJSON(empty) = %q, %v", encoded, err)
	}
}

func TestIdentityAccountAndKeyStore(t *testing.T) {
	repository := newStore(t)
	ctx := context.Background()

	userKeyHash := sha256.Sum256([]byte(userOneKey))
	principal, err := repository.AuthenticateGatewayKey(ctx, userKeyHash[:], timetext.Format(time.Now()))
	if err != nil || principal.UserID != userOneID || principal.AccountID != accountOneID {
		t.Fatalf("AuthenticateGatewayKey = %+v, %v", principal, err)
	}
	sessionUserID, sessionAccountID, err := repository.AuthenticateUserSession(ctx, "gzs_story_user_active_1")
	if err != nil || sessionUserID != userOneID || sessionAccountID != accountOneID {
		t.Fatalf("AuthenticateUserSession = %q, %q, %v", sessionUserID, sessionAccountID, err)
	}
	for _, secret := range []string{"invalid", "gzs_story_user_suspended"} {
		if _, _, err := repository.AuthenticateUserSession(ctx, secret); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("AuthenticateUserSession(%q) error = %v", secret, err)
		}
	}
	for _, secret := range []string{"invalid", "giz_story_user_suspended"} {
		hash := sha256.Sum256([]byte(secret))
		if _, err := repository.AuthenticateGatewayKey(ctx, hash[:], timetext.Format(time.Now())); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("AuthenticateGatewayKey(%q) error = %v", secret, err)
		}
	}
	if adminID, err := repository.AuthenticateAdminKey(ctx, "gizadm_story_admin"); err != nil || adminID == "" {
		t.Fatalf("AuthenticateAdminKey = %q, %v", adminID, err)
	}
	if _, err := repository.AuthenticateAdminKey(ctx, "invalid"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("AuthenticateAdminKey(invalid) = %v", err)
	}

	user, err := repository.GetUser(ctx, userOneID)
	if err != nil || user.Email != "active-one@gizway.test" {
		t.Fatalf("GetUser = %+v, %v", user, err)
	}
	if _, err := repository.GetUser(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetUser(missing) = %v", err)
	}
	user, err = repository.UpdateUser(ctx, userOneID, "Store Test User")
	if err != nil || user.DisplayName != "Store Test User" {
		t.Fatalf("UpdateUser = %+v, %v", user, err)
	}
	if _, err := repository.UpdateUser(ctx, "missing", "Nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateUser(missing) = %v", err)
	}
	accounts, err := repository.ListAccounts(ctx, userOneID)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("ListAccounts = %d, %v", len(accounts), err)
	}
	balance, err := repository.GetBalance(ctx, userOneID, accountOneID)
	if err != nil || balance.Amount != 100_000_000 {
		t.Fatalf("GetBalance = %+v, %v", balance, err)
	}
	if _, err := repository.GetBalance(ctx, userOneID, accountTwoID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetBalance(other) = %v", err)
	}

	keys, err := repository.ListAPIKeys(ctx, userOneID, accountOneID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("ListAPIKeys = %d, %v", len(keys), err)
	}
	if _, err := repository.ListAPIKeys(ctx, userOneID, accountTwoID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ListAPIKeys(other) = %v", err)
	}
	secret := "giz_store_test_secret"
	hash := sha256.Sum256([]byte(secret))
	now := timetext.Format(time.Now())
	payloadHash := sha256.Sum256([]byte("store-key-payload"))
	created, replayed, err := repository.CreateAPIKey(ctx, userOneID, "store-create-key", payloadHash[:], hash[:], store.APIKey{
		ID: "31000000-0000-4000-8000-000000000099", AccountID: accountOneID,
		Name: "Store test", Kind: "gateway", KeyPrefix: "giz_store_te",
		Scopes: store.JSON(`["gateway:invoke"]`), Status: "active", CreatedAt: now,
	})
	if err != nil || replayed || created.ID == "" {
		t.Fatalf("CreateAPIKey = %+v, %v, %v", created, replayed, err)
	}
	if _, _, err := repository.CreateAPIKey(ctx, userOneID, "store-other-account", payloadHash[:], hash[:], store.APIKey{
		ID: "31000000-0000-4000-8000-000000000098", AccountID: accountTwoID,
		Name: "Not owned", Kind: "gateway", KeyPrefix: "not_owned", Scopes: store.JSON(`[]`), CreatedAt: now,
	}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CreateAPIKey(other account) = %v", err)
	}
	if _, err := repository.AuthenticateGatewayKey(ctx, hash[:], timetext.Format(time.Now())); err != nil {
		t.Fatalf("authenticate created key: %v", err)
	}
	if err := repository.RevokeAPIKey(ctx, userOneID, accountOneID, created.ID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	if err := repository.RevokeAPIKey(ctx, userOneID, accountOneID, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RevokeAPIKey(repeated) = %v", err)
	}
	if err := repository.RevokeAPIKey(ctx, userOneID, accountTwoID, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RevokeAPIKey(other account) = %v", err)
	}
}

func TestTopupCommandLookupBeforeProviderCall(t *testing.T) {
	repository := newStore(t)
	ctx := context.Background()
	payload := sha256.Sum256([]byte("topup-payload"))
	if _, found, err := repository.LookupTopupCommand(ctx, userOneID, accountOneID, "topup-lookup", payload[:]); err != nil || found {
		t.Fatalf("initial LookupTopupCommand found=%v err=%v", found, err)
	}
	checkoutURL := "https://checkout.gizway.test/topup"
	createdAt := "2026-08-10T01:00:00.000000000Z"
	topup := store.Topup{
		ID: "topup-lookup-id", AccountID: accountOneID, PaymentProvider: "storypay",
		ProviderReference: "pay_topup_lookup", FiatCurrency: "USD", FiatAmountMinor: 900,
		Rate: store.TopupRateSnapshot{
			Base:      store.TopupRate{FiatMinor: 100, CreditMicrocredits: 1_000_000},
			Effective: store.TopupRate{FiatMinor: 90, CreditMicrocredits: 1_000_000}, DiscountBPS: 1000,
		},
		CreditAmount:     store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 10_000_000},
		RefundableAmount: store.CreditAmount{Asset: "GIZ_CREDIT"}, Status: "pending",
		CheckoutURL: &checkoutURL, CreatedAt: createdAt,
	}
	if _, replayed, err := repository.CreateTopup(ctx, userOneID, "topup-lookup", payload[:], topup); err != nil || replayed {
		t.Fatalf("CreateTopup replayed=%v err=%v", replayed, err)
	}
	stored, found, err := repository.LookupTopupCommand(ctx, userOneID, accountOneID, "topup-lookup", payload[:])
	if err != nil || !found || stored.ID != topup.ID || stored.ProviderReference != topup.ProviderReference {
		t.Fatalf("replay LookupTopupCommand=%+v found=%v err=%v", stored, found, err)
	}
	changed := sha256.Sum256([]byte("changed"))
	if _, _, err := repository.LookupTopupCommand(ctx, userOneID, accountOneID, "topup-lookup", changed[:]); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("changed LookupTopupCommand err=%v", err)
	}
	if _, _, err := repository.LookupTopupCommand(ctx, userOneID, accountTwoID, "missing", payload[:]); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unauthorized LookupTopupCommand err=%v", err)
	}
}

func TestMerchantUsageAndLedgerProjections(t *testing.T) {
	repository := newStore(t)
	ctx := context.Background()
	now := timetext.Format(time.Now())
	country := "SG"
	merchant, err := repository.CreateMerchantAccount(ctx, userOneID, store.MerchantAccount{
		Account:   store.Account{ID: "22000000-0000-4000-8000-000000000099", Name: "Store Merchant", Kind: "merchant", Status: "active", CreatedAt: now},
		LegalName: "Store Merchant Pte Ltd", PublicName: "Store Merchant",
		ReviewLevel: "basic", MerchantStatus: "pending", CountryCode: &country,
	})
	if err != nil || merchant.MerchantStatus != "pending" {
		t.Fatalf("CreateMerchantAccount = %+v, %v", merchant, err)
	}
	if _, err := repository.CreateMerchantAccount(ctx, userOneID, merchant); err == nil {
		t.Fatal("duplicate CreateMerchantAccount succeeded")
	}
	usage, err := repository.ListReceivedGatewayUsagePage(ctx, userOneID, accountOneID, "2026-08-10T00:00:00.000000000Z", "2026-08-11T00:00:00.000000000Z", store.AccountListQuery{})
	if err != nil || len(usage.Items) != 0 {
		t.Fatalf("ListReceivedGatewayUsagePage = %d, %v", len(usage.Items), err)
	}
	if _, err := repository.ListReceivedGatewayUsagePage(ctx, userOneID, accountTwoID, "2026-08-10T00:00:00.000000000Z", "2026-08-11T00:00:00.000000000Z", store.AccountListQuery{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ListReceivedGatewayUsagePage(other) = %v", err)
	}
	transactions, err := repository.ListAccountTransactionsPage(ctx, userOneID, accountOneID, store.AccountListQuery{})
	if err != nil || len(transactions.Items) != 1 {
		t.Fatalf("ListAccountTransactionsPage = %d, %v", len(transactions.Items), err)
	}
	if _, err := repository.ListAccountTransactionsPage(ctx, userOneID, accountTwoID, store.AccountListQuery{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ListAccountTransactionsPage(other) = %v", err)
	}
}

func TestCreditTransferStore(t *testing.T) {
	repository := newStore(t)
	ctx := context.Background()
	now := timetext.Format(time.Now())
	payload := sha256.Sum256([]byte("payload"))
	transfer := store.CreditTransfer{
		ID: "e1000000-0000-4000-8000-000000000001", SenderAccountID: accountOneID,
		RecipientAccountID: accountTwoID,
		Amount:             store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 25_000_000},
		Status:             "succeeded", Note: "store transfer", CreatedAt: now, CompletedAt: &now,
	}
	created, replayed, err := repository.CreateCreditTransfer(ctx, userOneID, "store-transfer", payload[:], transfer)
	if err != nil || replayed || created.ID != transfer.ID {
		t.Fatalf("CreateCreditTransfer = %+v, %v, %v", created, replayed, err)
	}
	created, replayed, err = repository.CreateCreditTransfer(ctx, userOneID, "store-transfer", payload[:], transfer)
	if err != nil || !replayed || created.ID != transfer.ID {
		t.Fatalf("replay CreateCreditTransfer = %+v, %v, %v", created, replayed, err)
	}
	changed := sha256.Sum256([]byte("changed"))
	if _, _, err := repository.CreateCreditTransfer(ctx, userOneID, "store-transfer", changed[:], transfer); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("changed replay error = %v", err)
	}
	transfer.ID = "e1000000-0000-4000-8000-000000000002"
	transfer.Amount.Microcredits = 100_000_000
	if _, _, err := repository.CreateCreditTransfer(ctx, userOneID, "too-much", payload[:], transfer); !errors.Is(err, store.ErrInsufficientBalance) {
		t.Fatalf("insufficient transfer error = %v", err)
	}
	transfer.ID = "e1000000-0000-4000-8000-000000000003"
	transfer.SenderAccountID = accountTwoID
	if _, _, err := repository.CreateCreditTransfer(ctx, userOneID, "not-owned", payload[:], transfer); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("not-owned transfer error = %v", err)
	}
	transfers, err := repository.ListCreditTransfersPage(ctx, userOneID, accountOneID, store.AccountListQuery{})
	if err != nil || len(transfers.Items) != 1 || transfers.Items[0].Direction != "outgoing" {
		t.Fatalf("ListCreditTransfers(sender) = %+v, %v", transfers, err)
	}
	transfers, err = repository.ListCreditTransfersPage(ctx, "11000000-0000-4000-8000-000000000002", accountTwoID, store.AccountListQuery{})
	if err != nil || len(transfers.Items) != 1 || transfers.Items[0].Direction != "incoming" {
		t.Fatalf("ListCreditTransfers(recipient) = %+v, %v", transfers, err)
	}
	if _, err := repository.ListCreditTransfersPage(ctx, userOneID, accountTwoID, store.AccountListQuery{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ListCreditTransfers(other) = %v", err)
	}
}

func TestDatabaseRateLimitIsAtomicAndWindowed(t *testing.T) {
	database := testdb.OpenGizPayStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	now := time.Date(2026, 8, 11, 12, 0, 30, 0, time.UTC)
	for attempt := range 2 {
		if err := repository.ConsumeRateLimit(t.Context(), "api-key:test", "gateway.invoke", 2, time.Minute, now); err != nil {
			t.Fatalf("allowed attempt %d: %v", attempt+1, err)
		}
	}
	if err := repository.ConsumeRateLimit(t.Context(), "api-key:test", "gateway.invoke", 2, time.Minute, now); !errors.Is(err, store.ErrRateLimited) {
		t.Fatalf("third attempt error=%v", err)
	}
	if err := repository.ConsumeRateLimit(t.Context(), "api-key:test", "gateway.invoke", 2, time.Minute, now.Add(time.Minute)); err != nil {
		t.Fatalf("next window attempt: %v", err)
	}
	if err := repository.ConsumeRateLimit(t.Context(), "", "gateway.invoke", 2, time.Minute, now); err == nil {
		t.Fatal("invalid rate limit configuration succeeded")
	}
}

func TestAPICommandAtomicallyCommitsBusinessMutationAndResponse(t *testing.T) {
	database := testdb.OpenGizWayStory(t)
	defer database.Close()
	repository, err := store.NewWithSecretKey(database.SQL, []byte("gizway-story-secret-key-32bytes!"))
	if err != nil {
		t.Fatal(err)
	}
	credentialHash := sha256.Sum256([]byte("admin"))
	payloadHash := sha256.Sum256([]byte("provider-create"))
	executions := 0
	execute := func(status int) func(context.Context) store.APICommandResponse {
		return func(ctx context.Context) store.APICommandResponse {
			executions++
			if _, err := repository.CreateProvider(ctx, "41000000-0000-4000-8000-000000000001", "atomic-provider", "Atomic Provider", "2026-08-10T03:00:00.000000000Z"); err != nil {
				t.Fatalf("CreateProvider in API command: %v", err)
			}
			return store.APICommandResponse{StatusCode: status, ContentType: "application/json", Body: []byte(`{"id":"atomic-provider"}`)}
		}
	}
	if response, replayed, err := repository.ExecuteAPICommand(t.Context(), credentialHash[:], "POST /admin/v1/providers", "atomic-rollback", payloadHash[:], execute(500)); err != nil || replayed || response.StatusCode != 500 {
		t.Fatalf("rollback response=%+v replayed=%v err=%v", response, replayed, err)
	}
	var count int
	if err := database.SQL.Get(&count, `SELECT COUNT(*) FROM providers WHERE slug='atomic-provider'`); err != nil || count != 0 {
		t.Fatalf("500 command provider count=%d err=%v", count, err)
	}
	response, replayed, err := repository.ExecuteAPICommand(t.Context(), credentialHash[:], "POST /admin/v1/providers", "atomic-success", payloadHash[:], execute(201))
	if err != nil || replayed || response.StatusCode != 201 {
		t.Fatalf("success response=%+v replayed=%v err=%v", response, replayed, err)
	}
	var storedResponse []byte
	if err := database.SQL.Get(&storedResponse, `SELECT response_body FROM api_idempotency_commands WHERE idempotency_key='atomic-success'`); err != nil || bytes.Contains(storedResponse, []byte("atomic-provider")) {
		t.Fatalf("stored API response leaked plaintext: %q err=%v", storedResponse, err)
	}
	response, replayed, err = repository.ExecuteAPICommand(t.Context(), credentialHash[:], "POST /admin/v1/providers", "atomic-success", payloadHash[:], func(context.Context) store.APICommandResponse {
		t.Fatal("completed command executed twice")
		return store.APICommandResponse{}
	})
	if err != nil || !replayed || response.StatusCode != 201 || executions != 2 {
		t.Fatalf("replay response=%+v replayed=%v executions=%d err=%v", response, replayed, executions, err)
	}
}

func TestCatalogStore(t *testing.T) {
	database := testdb.OpenGizWayStory(t)
	repository := store.New(database.SQL)
	ctx := context.Background()
	modelPage, err := repository.ListModelsPage(ctx, store.AdminListQuery{Limit: 100})
	models := modelPage.Items
	if err != nil || len(models) != 1 {
		t.Fatalf("ListModels = %d, %v", len(models), err)
	}
	public, err := repository.ListPublicModelsForAccount(ctx, "", "", time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC))
	if err != nil || len(public) != 1 || public[0].ID != "story-text" {
		t.Fatalf("ListPublicModels = %+v, %v", public, err)
	}
	model, err := repository.CreateModel(ctx, "90000000-0000-4000-8000-000000000001", store.Model{Slug: "store-model", Name: "Store Model", Modality: store.JSON(`["text"]`)})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if _, err := repository.CreateModel(ctx, "90000000-0000-4000-8000-000000000001", store.Model{Slug: "store-model", Name: "Duplicate", Modality: store.JSON(`["text"]`)}); err == nil {
		t.Fatal("CreateModel(duplicate) succeeded")
	}
	model, err = repository.UpdateModel(ctx, "90000000-0000-4000-8000-000000000001", model.ID, "Store Model Updated", "deprecated")
	if err != nil || model.Status != "deprecated" {
		t.Fatalf("UpdateModel = %+v, %v", model, err)
	}
	if _, err := repository.UpdateModel(ctx, "90000000-0000-4000-8000-000000000001", "missing", "Missing", "disabled"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateModel(missing) = %v", err)
	}
	variant, err := repository.CreateModelVariant(ctx, "90000000-0000-4000-8000-000000000001", store.ModelVariant{
		ModelID: model.ID, ProviderEndpointID: "71000000-0000-4000-8000-000000000001",
		ProviderModelName: "store-model-v1", VariantSlug: "store", Capabilities: store.JSON(`{"chat":true}`),
	})
	if err != nil {
		t.Fatalf("CreateModelVariant: %v", err)
	}
	variants, err := repository.ListModelVariants(ctx, model.ID)
	if err != nil || len(variants) != 1 {
		t.Fatalf("ListModelVariants = %d, %v", len(variants), err)
	}
	variant.Status = "disabled"
	variant, err = repository.UpdateModelVariant(ctx, "90000000-0000-4000-8000-000000000001", variant)
	if err != nil || variant.Status != "disabled" {
		t.Fatalf("UpdateModelVariant = %+v, %v", variant, err)
	}
	missingVariant := variant
	missingVariant.ID = "missing"
	if _, err := repository.UpdateModelVariant(ctx, "90000000-0000-4000-8000-000000000001", missingVariant); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateModelVariant(missing) = %v", err)
	}
	price, err := repository.CreateModelPrice(ctx, "90000000-0000-4000-8000-000000000001", store.ModelPrice{
		ModelVariantID: variant.ID, Metric: "input_token", UnitSize: 1000,
		UpstreamCostMicrocredits: 1000, BaseCustomerPriceMicrocredits: 2000,
		CustomerPriceMicrocredits: 1800, DiscountBPS: 1000, ValidFrom: "2026-08-11T00:00:00.000000000Z",
	})
	if err != nil || price.ID == "" {
		t.Fatalf("CreateModelPrice = %+v, %v", price, err)
	}
	prices, err := repository.ListModelPrices(ctx, variant.ID)
	if err != nil || len(prices) != 1 {
		t.Fatalf("ListModelPrices = %d, %v", len(prices), err)
	}
	price.ID = ""
	if _, err := repository.CreateModelPrice(ctx, "90000000-0000-4000-8000-000000000001", price); err == nil {
		t.Fatal("CreateModelPrice(overlap) succeeded")
	}
	audits, err := repository.AdminRows(ctx, "audit_events", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 5 {
		t.Fatalf("catalog audit count = %d, want 5", len(audits))
	}
	wantActions := []string{"model.created", "model.updated", "model_variant.created", "model_variant.updated", "model_price.created"}
	for i, action := range wantActions {
		if audits[i]["action"] != action {
			t.Fatalf("audit %d action = %v, want %s", i, audits[i]["action"], action)
		}
	}
}
