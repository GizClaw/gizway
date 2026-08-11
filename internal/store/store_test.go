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

const (
	userOneID    = "11000000-0000-4000-8000-000000000001"
	userOneKey   = "giz_story_user_active_1"
	accountOneID = "21000000-0000-4000-8000-000000000001"
	accountTwoID = "21000000-0000-4000-8000-000000000002"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	database := testdb.OpenStory(t)
	t.Cleanup(func() { _ = database.Close() })
	return store.New(database.SQL)
}

func TestStoreRebindsPostgreSQLPlaceholders(t *testing.T) {
	database := testdb.OpenStory(t)
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
	database := testdb.OpenStory(t)
	defer database.Close()
	if _, err := database.SQL.Exec(`UPDATE api_keys SET expires_at=$1 WHERE key_prefix='giz_story_user_active_1'`, "2026-08-10T00:00:00.500000000Z"); err != nil {
		t.Fatal(err)
	}
	repository := store.New(database.SQL)
	if _, _, err := repository.AuthenticateUserKeyAt(t.Context(), userOneKey, "2026-08-10T00:00:00.000000000Z"); err != nil {
		t.Fatalf("key expired before fractional boundary: %v", err)
	}
	if _, _, err := repository.AuthenticateUserKeyAt(t.Context(), userOneKey, "2026-08-10T00:00:00.500000000Z"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("key remained active at expiry: %v", err)
	}
}

func TestRealtimeProviderUsageEventIsRecoverable(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	const sessionID = "realtime-recovery-session"
	if _, err := database.SQL.Exec(`INSERT INTO gateway_requests(id,account_id,api_key_id,model_id,model_variant_id,operation,idempotency_key,payload_hash,protocol,status,started_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'started',$10)`, "realtime-recovery-request", accountOneID, "31000000-0000-4000-8000-000000000001", "81000000-0000-4000-8000-000000000001", "91000000-0000-4000-8000-000000000001", "realtime", "realtime-recovery", []byte{1}, "webrtc", "2026-08-10T00:00:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO realtime_sessions(id,gateway_request_id,account_id,api_key_id,model_id,model_variant_id,public_model,provider_model,client_secret_hash,transport,status,idempotency_key,payload_hash,expires_at,deadline_at,created_at,connected_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'webrtc','connected',$10,$11,$12,$13,$14,$15)`, sessionID, "realtime-recovery-request", accountOneID, "31000000-0000-4000-8000-000000000001", "81000000-0000-4000-8000-000000000001", "91000000-0000-4000-8000-000000000001", "story-text", "fake-text-v1", []byte{1}, "realtime-recovery", []byte{1}, "2026-08-10T00:02:00.000000000Z", "2026-08-10T00:11:00.000000000Z", "2026-08-10T00:00:00.000000000Z", "2026-08-10T00:01:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("signed-event"))
	if replayed, err := repository.RecordRealtimeProviderEvent(t.Context(), "realtime-event", sessionID, hash[:], 12, 7, 2, 4, 2, "2026-08-10T00:01:00.000000000Z"); err != nil || replayed {
		t.Fatalf("RecordRealtimeProviderEvent replayed=%v err=%v", replayed, err)
	}
	if replayed, err := repository.RecordRealtimeProviderEvent(t.Context(), "realtime-event", sessionID, hash[:], 12, 7, 2, 4, 2, "2026-08-10T00:01:01.000000000Z"); err != nil || !replayed {
		t.Fatalf("exact provider replay replayed=%v err=%v", replayed, err)
	}
	differentHash := sha256.Sum256([]byte("changed-signed-event"))
	if _, err := repository.RecordRealtimeProviderEvent(t.Context(), "realtime-event", sessionID, differentHash[:], 13, 7, 2, 4, 2, "2026-08-10T00:01:02.000000000Z"); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("changed event replay err=%v", err)
	}
	if _, err := repository.RecordRealtimeProviderEvent(t.Context(), "second-event-id", sessionID, differentHash[:], 12, 7, 2, 4, 2, "2026-08-10T00:01:03.000000000Z"); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("second terminal event err=%v", err)
	}
	events, err := repository.RecoverableRealtimeProviderEvents(t.Context(), 10)
	if err != nil || len(events) != 1 || events[0].InputTokens != 12 || events[0].OutputTokens != 7 || events[0].CachedInputTokens != 2 || events[0].InputAudioTokens != 4 || events[0].OutputAudioTokens != 2 {
		t.Fatalf("recoverable events=%+v err=%v", events, err)
	}
	if _, err := repository.RecoverableRealtimeProviderEvents(t.Context(), 0); err == nil {
		t.Fatal("non-positive recovery limit succeeded")
	}
	if err := repository.MarkRealtimeProviderEventProcessed(t.Context(), "realtime-event", "2026-08-10T00:01:04.000000000Z"); err != nil {
		t.Fatal(err)
	}
	// Exact callback replay after processing is an audit-neutral no-op.
	if err := repository.MarkRealtimeProviderEventProcessed(t.Context(), "realtime-event", "2026-08-10T00:01:05.000000000Z"); err != nil {
		t.Fatal(err)
	}
	if events, err := repository.RecoverableRealtimeProviderEvents(t.Context(), 10); err != nil || len(events) != 0 {
		t.Fatalf("processed recoverable events=%+v err=%v", events, err)
	}
}

func TestRealtimeCredentialCreationAndDurableExpiry(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	const (
		accountID = "21000000-0000-4000-8000-000000000001"
		apiKeyID  = "31000000-0000-4000-8000-000000000001"
		modelID   = "81000000-0000-4000-8000-000000000001"
		variantID = "91000000-0000-4000-8000-000000000001"
		expiredAt = "2026-08-10T00:01:00.000000000Z"
		sweepAt   = "2026-08-10T00:02:00.000000000Z"
	)
	create := func(id, transport string, connected bool) {
		requestID := "request-" + id
		if _, err := database.SQL.Exec(`INSERT INTO gateway_requests(id,account_id,api_key_id,model_id,model_variant_id,operation,idempotency_key,payload_hash,protocol,status,started_at) VALUES ($1,$2,$3,$4,$5,'realtime.test',$6,$7,$8,'started','2026-08-10T00:00:00.000000000Z')`, requestID, accountID, apiKeyID, modelID, variantID, "command-"+id, []byte{1}, transport); err != nil {
			t.Fatal(err)
		}
		if _, err := database.SQL.Exec(`INSERT INTO credit_reservations(id,account_id,api_key_id,amount_microcredits,status,idempotency_key,created_at) VALUES ($1,$2,$3,1000,'active',$4,'2026-08-10T00:00:00.000000000Z')`, "reserve-"+id, accountID, apiKeyID, requestID); err != nil {
			t.Fatal(err)
		}
		session := store.RealtimeSession{ID: id, GatewayRequestID: requestID, AccountID: accountID, APIKeyID: apiKeyID, ModelID: modelID, VariantID: variantID, PublicModel: "story-text", ProviderModel: "fake-text-v1", Transport: transport, ExpiresAt: expiredAt, CreatedAt: "2026-08-10T00:00:00.000000000Z"}
		if err := repository.CreateRealtimeSession(t.Context(), session, "command-"+id, []byte{1}, []byte("secret-"+id)); err != nil {
			t.Fatal(err)
		}
		if connected {
			if _, err := database.SQL.Exec(`UPDATE realtime_sessions SET status='connected',connected_at='2026-08-10T00:00:30.000000000Z' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	create("created-expiry", "websocket", false)
	create("connected-expiry", "webrtc", true)
	create("late-callback-expiry", "webrtc", true)

	// Same command payload never creates a second credential; a mismatched
	// payload is the stable idempotency conflict.
	replay := store.RealtimeSession{ID: "ignored", AccountID: accountID, APIKeyID: apiKeyID}
	if err := repository.CreateRealtimeSession(t.Context(), replay, "command-created-expiry", []byte{1}, []byte("ignored")); !errors.Is(err, store.ErrCredentialConsumed) {
		t.Fatalf("same-payload replay err=%v", err)
	}
	if err := repository.CreateRealtimeSession(t.Context(), replay, "command-created-expiry", []byte{2}, []byte("ignored")); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("mismatched replay err=%v", err)
	}

	lateHash := sha256.Sum256([]byte("late-provider-event"))
	if _, err := repository.RecordRealtimeProviderEvent(t.Context(), "late-provider-event", "late-callback-expiry", lateHash[:], 1, 1, 0, 0, 0, sweepAt); !errors.Is(err, store.ErrInvalidRealtimeProviderState) {
		t.Fatalf("late callback err=%v", err)
	}
	expired, err := repository.ExpireRealtimeSessions(t.Context(), sweepAt, 10)
	if err != nil || expired != 2 {
		t.Fatalf("expired=%d err=%v", expired, err)
	}
	for _, id := range []string{"created-expiry", "connected-expiry", "late-callback-expiry"} {
		session, err := repository.GetRealtimeSession(t.Context(), id)
		if err != nil || session.Status != "expired" {
			t.Fatalf("session %s = %+v err=%v", id, session, err)
		}
		var requestStatus, reservationStatus string
		if err := database.SQL.QueryRow(`SELECT g.status,r.status FROM gateway_requests g JOIN credit_reservations r ON r.idempotency_key=g.id WHERE g.id=$1`, "request-"+id).Scan(&requestStatus, &reservationStatus); err != nil || requestStatus != "failed" || reservationStatus != "released" {
			t.Fatalf("economic state %s request=%s reservation=%s err=%v", id, requestStatus, reservationStatus, err)
		}
	}
	if _, err := repository.ExpireRealtimeSessions(t.Context(), sweepAt, 0); err == nil {
		t.Fatal("zero expiry batch succeeded")
	}
}

func TestAdminWebhookRetryCommandReplaysAndConflicts(t *testing.T) {
	repository := newStore(t)
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

	userID, accountID, err := repository.AuthenticateUserKey(ctx, userOneKey)
	if err != nil || userID != userOneID || accountID != accountOneID {
		t.Fatalf("AuthenticateUserKey = %q, %q, %v", userID, accountID, err)
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
		if _, _, err := repository.AuthenticateUserKey(ctx, secret); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("AuthenticateUserKey(%q) error = %v", secret, err)
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
	if _, _, err := repository.AuthenticateUserKey(ctx, secret); err != nil {
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
	usage, err := repository.ListGatewayUsagePage(ctx, userOneID, accountOneID, "2026-08-10T00:00:00.000000000Z", "2026-08-11T00:00:00.000000000Z", store.AccountListQuery{})
	if err != nil || len(usage.Items) != 0 {
		t.Fatalf("ListGatewayUsagePage = %d, %v", len(usage.Items), err)
	}
	if _, err := repository.ListGatewayUsagePage(ctx, userOneID, accountTwoID, "2026-08-10T00:00:00.000000000Z", "2026-08-11T00:00:00.000000000Z", store.AccountListQuery{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ListGatewayUsagePage(other) = %v", err)
	}
	transactions, err := repository.ListAccountTransactionsPage(ctx, userOneID, accountOneID, store.AccountListQuery{})
	if err != nil || len(transactions.Items) != 1 {
		t.Fatalf("ListAccountTransactionsPage = %d, %v", len(transactions.Items), err)
	}
	if _, err := repository.ListAccountTransactionsPage(ctx, userOneID, accountTwoID, store.AccountListQuery{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ListAccountTransactionsPage(other) = %v", err)
	}
}

func TestGatewayUsageRowScanning(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	_, err := database.SQL.Exec(`
		INSERT INTO gateway_requests (
			id, account_id, api_key_id, model_id, model_variant_id, operation, protocol,
			idempotency_key, payload_hash, status, input_tokens, output_tokens,
			charged_microcredits, started_at, completed_at
		) VALUES ($1, $2, $3, $4, $5, 'chat.completions', 'https', 'store-usage', decode('01','hex'), 'succeeded', 10, 5, 27, $6, $7)
	`, "f1000000-0000-4000-8000-000000000001", accountOneID,
		"31000000-0000-4000-8000-000000000001",
		"81000000-0000-4000-8000-000000000001",
		"91000000-0000-4000-8000-000000000001",
		"2026-08-10T12:00:00.000000000Z", "2026-08-10T12:00:01.000000000Z")
	if err != nil {
		t.Fatalf("insert gateway request: %v", err)
	}
	usage, err := store.New(database.SQL).ListGatewayUsagePage(context.Background(), userOneID, accountOneID, "2026-08-10T00:00:00.000000000Z", "2026-08-11T00:00:00.000000000Z", store.AccountListQuery{})
	if err != nil || len(usage.Items) != 1 {
		t.Fatalf("ListGatewayUsage = %+v, %v", usage, err)
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

func TestReservationsAndPendingRefundsCannotBeDoubleSpent(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	now := "2026-08-10T02:00:00.000000000Z"
	if _, err := repository.BeginGatewayCommand(t.Context(), store.GatewayCommand{
		ID: "reserved-request", AccountID: accountOneID,
		APIKeyID: "31000000-0000-4000-8000-000000000001",
		ModelID:  "81000000-0000-4000-8000-000000000001", VariantID: "91000000-0000-4000-8000-000000000001",
		Operation: "chat.completions", IdempotencyKey: "reserved-request", PayloadHash: []byte{1},
		ReserveAmount: 99_990_000, Protocol: "https", StartedAt: now, ExecutionSnapshot: []byte(`{"plan":"reserved"}`),
	}); err != nil {
		t.Fatal(err)
	}
	payload := sha256.Sum256([]byte("reserved transfer"))
	transfer := store.CreditTransfer{ID: "reserved-transfer", SenderAccountID: accountOneID, RecipientAccountID: accountTwoID,
		Amount: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 20_000}, Status: "succeeded", CreatedAt: now, CompletedAt: &now}
	if _, _, err := repository.CreateCreditTransfer(t.Context(), userOneID, "reserved-transfer", payload[:], transfer); !errors.Is(err, store.ErrInsufficientBalance) {
		t.Fatalf("transfer spent active reservation: %v", err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO topups(id,account_id,payment_provider,provider_reference,fiat_currency,fiat_amount_minor,base_fiat_minor,base_credit_microcredits,effective_fiat_minor,effective_credit_microcredits,discount_bps,credit_microcredits,refundable_microcredits,status,idempotency_key,payload_hash,created_at,completed_at) VALUES ('reserved-topup',$1,'fake','reserved-provider','USD',100,100,100000000,100,100000000,0,100000000,100000000,'succeeded','reserved-topup',decode('01','hex'),$2,$3)`, accountOneID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO credit_lots(id,account_id,topup_id,original_microcredits,remaining_microcredits,created_at) VALUES ('reserved-lot',$1,'reserved-topup',100000000,100000000,$2)`, accountOneID, now); err != nil {
		t.Fatal(err)
	}
	refundHash := sha256.Sum256([]byte("reserved refund"))
	if _, _, _, _, err := repository.CreateRefund(t.Context(), userOneID, accountOneID, "reserved-topup", "reserved-refund", refundHash[:], 20_000, now); !errors.Is(err, store.ErrInsufficientBalance) {
		t.Fatalf("refund spent active reservation: %v", err)
	}
	if err := repository.ReleaseGatewayCommand(t.Context(), "reserved-request", "test_release"); err != nil {
		t.Fatal(err)
	}
	pendingRefund, _, _, replayed, err := repository.CreateRefund(t.Context(), userOneID, accountOneID, "reserved-topup", "pending-refund", refundHash[:], 90_000_000, now)
	if err != nil || replayed {
		t.Fatalf("create pending refund: replayed=%v err=%v", replayed, err)
	}
	if empty, err := repository.RecoverableRefunds(t.Context(), 0); err != nil || len(empty) != 0 {
		t.Fatalf("zero-limit recoverable refunds=%+v err=%v", empty, err)
	}
	recoverable, err := repository.RecoverableRefunds(t.Context(), 32)
	if err != nil || len(recoverable) != 1 || recoverable[0].Refund.ID != pendingRefund.ID || recoverable[0].ProviderReference != "reserved-provider" || recoverable[0].Currency != "USD" {
		t.Fatalf("recoverable refunds=%+v err=%v", recoverable, err)
	}
	transfer.ID = "pending-refund-transfer"
	transfer.Amount.Microcredits = 10_000_001
	if _, _, err := repository.CreateCreditTransfer(t.Context(), userOneID, "pending-refund-transfer", payload[:], transfer); !errors.Is(err, store.ErrInsufficientBalance) {
		t.Fatalf("transfer spent pending refund: %v", err)
	}
	failedAt := "2026-08-10T02:01:00.000000000Z"
	failed, err := repository.FailRefund(t.Context(), pendingRefund.ID, failedAt, "provider rejected fixture refund")
	if err != nil || failed.Status != "failed" || failed.CompletedAt == nil || *failed.CompletedAt != failedAt {
		t.Fatalf("FailRefund=%+v err=%v", failed, err)
	}
	// Terminal failure is idempotent and no longer returned to the recovery
	// worker; it also releases the available-Credit hold.
	if replay, err := repository.FailRefund(t.Context(), pendingRefund.ID, failedAt, "duplicate provider result"); err != nil || replay.Status != "failed" {
		t.Fatalf("replayed FailRefund=%+v err=%v", replay, err)
	}
	if recoverable, err := repository.RecoverableRefunds(t.Context(), 32); err != nil || len(recoverable) != 0 {
		t.Fatalf("terminal recoverable refunds=%+v err=%v", recoverable, err)
	}
	// A delayed or concurrent provider success cannot move a refund out of its
	// definitive failed terminal state. In particular it must not post a debit
	// or consume the purchased lot after FailRefund released the Credit hold.
	lateSuccess, err := repository.CompleteRefund(t.Context(), pendingRefund.ID, "late-provider-success", "2026-08-10T02:01:01.000000000Z")
	if err != nil || lateSuccess.Status != "failed" {
		t.Fatalf("late CompleteRefund=%+v err=%v", lateSuccess, err)
	}
	var refundLedgerCount int
	if err := database.SQL.Get(&refundLedgerCount, `SELECT COUNT(*) FROM ledger_transactions WHERE reference_type='refund' AND reference_id=$1`, pendingRefund.ID); err != nil || refundLedgerCount != 0 {
		t.Fatalf("late refund ledger count=%d err=%v", refundLedgerCount, err)
	}
	var remainingLot int64
	if err := database.SQL.Get(&remainingLot, `SELECT remaining_microcredits FROM credit_lots WHERE topup_id='reserved-topup'`); err != nil || remainingLot != 100_000_000 {
		t.Fatalf("late refund remaining lot=%d err=%v", remainingLot, err)
	}
	if _, _, err := repository.CreateCreditTransfer(t.Context(), userOneID, "released-refund-transfer", payload[:], transfer); err != nil {
		t.Fatalf("failed refund did not release Credit: %v", err)
	}
}

func TestGatewaySettlementOutboxRetainsAndRecoversReservation(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	now := "2026-08-10T03:00:00.000000000Z"
	if _, err := repository.BeginGatewayCommand(t.Context(), store.GatewayCommand{ID: "settlement-pending", AccountID: accountOneID,
		APIKeyID: "31000000-0000-4000-8000-000000000001", ModelID: "81000000-0000-4000-8000-000000000001",
		VariantID: "91000000-0000-4000-8000-000000000001", Operation: "chat.completions", IdempotencyKey: "settlement-pending",
		PayloadHash: []byte{2}, ReserveAmount: 100, Protocol: "https", StartedAt: now, ExecutionSnapshot: []byte(`{"plan":"settlement"}`)}); err != nil {
		t.Fatal(err)
	}
	metric := store.GatewayMetric{Metric: "input_token", Quantity: 1, Charge: 1, Price: store.GatewayPrice{ID: "missing-price", Metric: "input_token", UnitSize: 1, BasePrice: 1, EffectivePrice: 1}}
	if err := repository.SettleGatewayCommand(t.Context(), "settlement-pending", "provider-1", []store.GatewayMetric{metric}, []byte(`{"ok":true}`), now); err == nil {
		t.Fatal("settlement with missing price unexpectedly succeeded")
	}
	var requestStatus, reservationStatus, outboxStatus string
	if err := database.SQL.QueryRow(`SELECT status FROM gateway_requests WHERE id='settlement-pending'`).Scan(&requestStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.QueryRow(`SELECT status FROM credit_reservations WHERE idempotency_key='settlement-pending'`).Scan(&reservationStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.QueryRow(`SELECT status FROM gateway_settlement_outbox WHERE request_id='settlement-pending'`).Scan(&outboxStatus); err != nil {
		t.Fatal(err)
	}
	if requestStatus != "started" || reservationStatus != "active" || outboxStatus != "pending" {
		t.Fatalf("unsafe settlement failure state request=%s reservation=%s outbox=%s", requestStatus, reservationStatus, outboxStatus)
	}
	metric.Price.ID = "a1000000-0000-4000-8000-000000000001"
	encoded, _ := json.Marshal([]store.GatewayMetric{metric})
	if _, err := database.SQL.Exec(`UPDATE gateway_settlement_outbox SET metrics_json=$1 WHERE request_id='settlement-pending'`, string(encoded)); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecoverGatewaySettlements(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.QueryRow(`SELECT status FROM gateway_requests WHERE id='settlement-pending'`).Scan(&requestStatus); err != nil || requestStatus != "succeeded" {
		t.Fatalf("recovered request=%s err=%v", requestStatus, err)
	}
}

func TestExpiredGatewayLeaseRemainsRecoverableAndIsNeverGuessedFailed(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	repository, err := store.NewWithSecretKey(database.SQL, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	command := store.GatewayCommand{
		ID: "abandoned-command", AccountID: accountOneID,
		APIKeyID: "31000000-0000-4000-8000-000000000001",
		ModelID:  "81000000-0000-4000-8000-000000000001", VariantID: "91000000-0000-4000-8000-000000000001",
		Operation: "chat.completions", IdempotencyKey: "abandoned-command", PayloadHash: []byte("abandoned"),
		ReserveAmount: 100, Protocol: "https", StartedAt: "2026-08-10T03:00:00.000000000Z",
		ExecutionSnapshot: []byte(`{"plan":"immutable"}`),
		RecoveryRequest:   []byte(`{"method":"POST","request_uri":"/v1/chat/completions","body":"e30="}`),
	}
	if _, err := repository.BeginGatewayCommand(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	// Advancing wall time alone cannot distinguish "provider never called" from
	// "provider committed and Gizway crashed". The reservation therefore stays
	// active until the immutable plan is reclaimed with the same provider
	// idempotency identity.
	var requestStatus, reservationStatus string
	if err := database.SQL.QueryRow(`SELECT status FROM gateway_requests WHERE id=$1`, command.ID).Scan(&requestStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.QueryRow(`SELECT status FROM credit_reservations WHERE idempotency_key=$1`, command.ID).Scan(&reservationStatus); err != nil {
		t.Fatal(err)
	}
	if requestStatus != "started" || reservationStatus != "active" {
		t.Fatalf("ambiguous request=%s reservation=%s", requestStatus, reservationStatus)
	}
	recoverable, err := repository.RecoverableGatewayCommands(t.Context(), "2026-08-10T03:01:00.000000000Z", 1)
	if err != nil || len(recoverable) != 1 {
		t.Fatalf("recoverable commands=%+v err=%v", recoverable, err)
	}
	if recoverable[0].RequestID != command.ID || recoverable[0].Principal.APIKeyID != command.APIKeyID || !bytes.Equal(recoverable[0].RecoveryRequest, command.RecoveryRequest) {
		t.Fatalf("recoverable command=%+v", recoverable[0])
	}
	var protected []byte
	if err := database.SQL.QueryRow(`SELECT recovery_request FROM gateway_requests WHERE id=$1`, command.ID).Scan(&protected); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(protected, []byte("chat/completions")) {
		t.Fatal("Gateway recovery request was persisted in plaintext")
	}
	if err := repository.RecordGatewayRecoveryFailure(t.Context(), command.ID, "temporary provider outage", "2026-08-10T03:01:00.000000000Z", false); err != nil {
		t.Fatal(err)
	}
	if due, err := repository.RecoverableGatewayCommands(t.Context(), "2026-08-10T03:01:00.500000000Z", 1); err != nil || len(due) != 0 {
		t.Fatalf("command ignored recovery backoff: %+v err=%v", due, err)
	}
	if due, err := repository.RecoverableGatewayCommands(t.Context(), "2026-08-10T03:01:02.000000000Z", 1); err != nil || len(due) != 1 {
		t.Fatalf("command not due after recovery backoff: %+v err=%v", due, err)
	}
	if err := repository.RecordGatewayRecoveryFailure(t.Context(), command.ID, "stored envelope is invalid", "2026-08-10T03:01:02.000000000Z", true); err != nil {
		t.Fatal(err)
	}
	var recoveryStatus, recoveryError string
	var recoveryAttempts int
	if err := database.SQL.QueryRow(`SELECT recovery_status,recovery_attempts,recovery_last_error FROM gateway_requests WHERE id=$1`, command.ID).Scan(&recoveryStatus, &recoveryAttempts, &recoveryError); err != nil {
		t.Fatal(err)
	}
	if recoveryStatus != "reconciliation_required" || recoveryAttempts != 2 || recoveryError != "stored envelope is invalid" {
		t.Fatalf("recovery status=%s attempts=%d error=%q", recoveryStatus, recoveryAttempts, recoveryError)
	}
}

func TestGatewayCommandReclaimsOnlyExpiredExecutionLease(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	command := store.GatewayCommand{
		ID: "leased-command", AccountID: accountOneID,
		APIKeyID: "31000000-0000-4000-8000-000000000001",
		ModelID:  "81000000-0000-4000-8000-000000000001", VariantID: "91000000-0000-4000-8000-000000000001",
		Operation: "chat.completions", IdempotencyKey: "leased-command", PayloadHash: []byte("same"),
		ReserveAmount: 100, Protocol: "https", StartedAt: "2026-08-10T03:00:00.000000000Z",
		ExecutionLeaseUntil: "2026-08-10T03:00:10.000000000Z",
		ExecutionSnapshot:   []byte(`{"plan":"immutable"}`),
	}
	created, err := repository.BeginGatewayCommand(t.Context(), command)
	if err != nil || created.RequestID != command.ID || created.Resumed {
		t.Fatalf("initial BeginGatewayCommand = %+v, %v", created, err)
	}
	activeRetry := command
	activeRetry.ID = "ignored-active-retry-id"
	activeRetry.StartedAt = "2026-08-10T03:00:09.000000000Z"
	activeRetry.ExecutionLeaseUntil = "2026-08-10T03:00:19.000000000Z"
	if _, err := repository.BeginGatewayCommand(t.Context(), activeRetry); err == nil {
		t.Fatal("active execution lease was reclaimed")
	}
	expiredRetry := activeRetry
	expiredRetry.StartedAt = "2026-08-10T03:00:11.000000000Z"
	expiredRetry.ExecutionLeaseUntil = "2026-08-10T03:00:21.000000000Z"
	resumed, err := repository.BeginGatewayCommand(t.Context(), expiredRetry)
	if err != nil || !resumed.Resumed || resumed.RequestID != command.ID {
		t.Fatalf("expired lease resume = %+v, %v", resumed, err)
	}
	if string(resumed.ExecutionSnapshot) != string(command.ExecutionSnapshot) {
		t.Fatalf("resumed snapshot = %s", resumed.ExecutionSnapshot)
	}
	var attempts, reservations int
	if err := database.SQL.QueryRow(`SELECT execution_attempts FROM gateway_requests WHERE id=$1`, command.ID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.QueryRow(`SELECT COUNT(*) FROM credit_reservations WHERE idempotency_key=$1`, command.ID).Scan(&reservations); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || reservations != 1 {
		t.Fatalf("attempts=%d reservations=%d", attempts, reservations)
	}
}

func TestZeroPriceGatewayCommandSettlesWithoutReservationOrLedgerPosting(t *testing.T) {
	database := testdb.OpenStory(t)
	defer database.Close()
	repository := store.New(database.SQL)
	now := "2026-08-10T03:30:00.000000000Z"
	command := store.GatewayCommand{
		ID: "free-command", AccountID: accountOneID, APIKeyID: "31000000-0000-4000-8000-000000000001",
		ModelID: "81000000-0000-4000-8000-000000000001", VariantID: "91000000-0000-4000-8000-000000000001",
		Operation: "chat.completions", IdempotencyKey: "free-command", PayloadHash: []byte("free"),
		Protocol: "https", StartedAt: now, ExecutionSnapshot: []byte(`{"plan":"free"}`),
	}
	if _, err := repository.BeginGatewayCommand(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	var reservations int
	if err := database.SQL.QueryRow(`SELECT COUNT(*) FROM credit_reservations WHERE idempotency_key=$1`, command.ID).Scan(&reservations); err != nil || reservations != 0 {
		t.Fatalf("free reservations=%d err=%v", reservations, err)
	}
	metrics := []store.GatewayMetric{
		{Metric: "input_token", Quantity: 10, Price: store.GatewayPrice{ID: "a1000000-0000-4000-8000-000000000001", UnitSize: 1000, BasePrice: 2000, EffectivePrice: 0, DiscountBPS: 10_000}},
		{Metric: "output_token", Quantity: 5, Price: store.GatewayPrice{ID: "a1000000-0000-4000-8000-000000000002", UnitSize: 1000, BasePrice: 4000, EffectivePrice: 0, DiscountBPS: 10_000}},
	}
	if err := repository.SettleGatewayCommand(t.Context(), command.ID, "provider-free", metrics, []byte(`{"id":"free"}`), now); err != nil {
		t.Fatal(err)
	}
	var status string
	var charged, ledgerRows int
	if err := database.SQL.QueryRow(`SELECT status,charged_microcredits FROM gateway_requests WHERE id=$1`, command.ID).Scan(&status, &charged); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.QueryRow(`SELECT COUNT(*) FROM ledger_transactions WHERE reference_type='gateway_request' AND reference_id=$1`, command.ID).Scan(&ledgerRows); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || charged != 0 || ledgerRows != 0 {
		t.Fatalf("free command status=%s charged=%d ledger=%d", status, charged, ledgerRows)
	}
}

func TestDatabaseRateLimitIsAtomicAndWindowed(t *testing.T) {
	database := testdb.OpenStory(t)
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
	database := testdb.OpenStory(t)
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
	repository := newStore(t)
	ctx := context.Background()
	modelPage, err := repository.ListModelsPage(ctx, store.AdminListQuery{Limit: 100})
	models := modelPage.Items
	if err != nil || len(models) != 2 {
		t.Fatalf("ListModels = %d, %v", len(models), err)
	}
	public, err := repository.ListPublicModels(ctx, time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC))
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
