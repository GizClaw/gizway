package gizpay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/gizway/internal/testdb"
	"github.com/google/uuid"
)

func TestMilestone04ZITADELWebhookVerifiesAndInitializesOnlyHumans(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required")
	}
	db := testdb.OpenGizPay(t).SQL
	now := time.Date(2026, 8, 16, 4, 0, 0, 0, time.UTC)
	handler := &Handler{config: Config{DB: db, Now: func() time.Time { return now }}}
	setM04ConfigField(t, &handler.config, "ZITADELIssuer", "https://identity.example.test")
	setM04ConfigField(t, &handler.config, "ActionSigningKey", []byte("m04-action-signing-key"))

	human := []byte(`{"user":{"id":"human-m04","human":{}},"userinfo":{"email":"human@example.test","name":"Human M04","preferred_username":"human"}}`)
	for _, expectedStatus := range []int{http.StatusNoContent, http.StatusNoContent} {
		recorder := serveSignedWebhook(handler, human, now, []byte("m04-action-signing-key"))
		if recorder.Code != expectedStatus {
			t.Fatalf("Human webhook status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	var users, accounts, ledgers, profiles, balances, merchants int
	err := db.QueryRowx(`
		SELECT
		  (SELECT count(*) FROM users WHERE identity_subject='human-m04'),
		  (SELECT count(*) FROM accounts a JOIN users u ON u.id=a.owner_user_id WHERE u.identity_subject='human-m04'),
		  (SELECT count(*) FROM ledger_accounts l JOIN accounts a ON a.id=l.owner_account_id JOIN users u ON u.id=a.owner_user_id WHERE u.identity_subject='human-m04'),
		  (SELECT count(*) FROM client_sync.user_profiles p WHERE p.owner_identity_subject='human-m04' AND length(p.merchant_id)>0),
		  (SELECT count(*) FROM client_sync.account_balances b WHERE b.owner_identity_subject='human-m04'),
		  (SELECT count(*) FROM merchants m JOIN accounts a ON a.id=m.settlement_account_id JOIN users u ON u.id=a.owner_user_id WHERE u.identity_subject='human-m04')
	`).Scan(&users, &accounts, &ledgers, &profiles, &balances, &merchants)
	if err != nil {
		t.Fatal(err)
	}
	if users != 1 || accounts != 1 || ledgers != 1 || profiles != 1 || balances != 1 || merchants != 1 {
		t.Fatalf("idempotent Human initialization counts=%d,%d,%d,%d,%d,%d", users, accounts, ledgers, profiles, balances, merchants)
	}
	var email, displayName string
	if err := db.QueryRowx(`SELECT email,display_name FROM users WHERE identity_subject='human-m04'`).Scan(&email, &displayName); err != nil {
		t.Fatal(err)
	}
	if email != "human@example.test" || displayName != "Human M04" {
		t.Fatalf("profile=(%q,%q)", email, displayName)
	}
	var generatedMerchantID string
	if err := db.Get(&generatedMerchantID, `SELECT merchant_id FROM client_sync.user_profiles WHERE owner_identity_subject='human-m04'`); err != nil {
		t.Fatal(err)
	}
	parsedMerchantID, err := uuid.Parse(generatedMerchantID)
	if err != nil || parsedMerchantID.String() != generatedMerchantID {
		t.Fatalf("generated default Merchant ID=%q is not a canonical UUID", generatedMerchantID)
	}

	service := []byte(`{"user":{"id":"service-m04"},"userinfo":{}}`)
	recorder := serveSignedWebhookWithHeader(handler, service, now, []byte("m04-action-signing-key"), "ZITADEL-Signature")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("Service Account webhook status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var serviceUsers int
	if err := db.Get(&serviceUsers, `SELECT count(*) FROM users WHERE identity_subject='service-m04'`); err != nil {
		t.Fatal(err)
	}
	if serviceUsers != 0 {
		t.Fatal("Service Account created a GizPay personal User")
	}
}

func TestMilestone04ZITADELWebhookRejectsMissingOrStaleSignature(t *testing.T) {
	handler := &Handler{config: Config{Now: func() time.Time { return time.Unix(2_000, 0).UTC() }}}
	setM04ConfigField(t, &handler.config, "ActionSigningKey", []byte("m04-action-signing-key"))
	body := []byte(`{"user":{"id":"human-m04","human":{}}}`)
	for name, signature := range map[string]string{
		"missing": "",
		"invalid": "t=2000,v1=deadbeef",
		"stale":   webhookSignature(body, time.Unix(1_000, 0), []byte("m04-action-signing-key")),
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/webhooks/v1/zitadel/user-authenticated", bytes.NewReader(body))
			request.Header.Set("X-ZITADEL-Signature", signature)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestMilestone04ZITADELWebhookHonorsFixedMerchantUUID(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required")
	}
	db := testdb.OpenGizPay(t).SQL
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	handler := &Handler{config: Config{DB: db, Now: func() time.Time { return now }}}
	setM04ConfigField(t, &handler.config, "ZITADELIssuer", "https://identity.example.test")
	setM04ConfigField(t, &handler.config, "ActionSigningKey", []byte("fixed-merchant-signing-key"))

	fixed := "7c891eb4-38ff-4726-8ea3-e59211cbd191"
	payload := []byte(`{"merchant_id":"` + fixed + `","user":{"id":"fixed-merchant-human","human":{}},"userinfo":{"name":"Fixed Merchant Human"}}`)
	for range 2 {
		recorder := serveSignedWebhook(handler, payload, now, []byte("fixed-merchant-signing-key"))
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("fixed Merchant webhook status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	var merchantID string
	if err := db.Get(&merchantID, `SELECT m.id FROM merchants m JOIN accounts a ON a.id=m.settlement_account_id JOIN users u ON u.id=a.owner_user_id WHERE u.identity_subject='fixed-merchant-human' AND m.is_default=true`); err != nil {
		t.Fatal(err)
	}
	if merchantID != fixed {
		t.Fatalf("default Merchant ID=%q want=%q", merchantID, fixed)
	}

	collision := []byte(`{"merchant_id":"` + fixed + `","user":{"id":"other-fixed-merchant-human","human":{}},"userinfo":{"name":"Other Fixed Merchant Human"}}`)
	recorder := serveSignedWebhook(handler, collision, now, []byte("fixed-merchant-signing-key"))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"identity_initialization_conflict"`) {
		t.Fatalf("Merchant UUID collision status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var collisionUserCount int
	if err := db.Get(&collisionUserCount, `SELECT count(*) FROM users WHERE identity_subject='other-fixed-merchant-human'`); err != nil || collisionUserCount != 0 {
		t.Fatalf("Merchant UUID collision created %d users: %v", collisionUserCount, err)
	}

	conflict := []byte(`{"merchant_id":"f5698fc7-f31b-4a3f-9827-47a288d1d427","user":{"id":"fixed-merchant-human","human":{}},"userinfo":{"name":"Changed Name"}}`)
	recorder = serveSignedWebhook(handler, conflict, now, []byte("fixed-merchant-signing-key"))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"identity_initialization_conflict"`) {
		t.Fatalf("conflicting replay status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var displayName string
	if err := db.Get(&displayName, `SELECT display_name FROM users WHERE identity_subject='fixed-merchant-human'`); err != nil {
		t.Fatal(err)
	}
	if displayName != "Fixed Merchant Human" {
		t.Fatalf("conflicting replay changed display name to %q", displayName)
	}
}

func TestMilestone04ZITADELWebhookRejectsInvalidMerchantUUIDBeforeMutation(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required")
	}
	db := testdb.OpenGizPay(t).SQL
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	handler := &Handler{config: Config{DB: db, Now: func() time.Time { return now }}}
	setM04ConfigField(t, &handler.config, "ZITADELIssuer", "https://identity.example.test")
	setM04ConfigField(t, &handler.config, "ActionSigningKey", []byte("invalid-merchant-signing-key"))
	payload := []byte(`{"merchant_id":"not-a-uuid","user":{"id":"invalid-merchant-human","human":{}},"userinfo":{"name":"Invalid Merchant Human"}}`)
	recorder := serveSignedWebhook(handler, payload, now, []byte("invalid-merchant-signing-key"))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid Merchant UUID status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM users WHERE identity_subject='invalid-merchant-human'`); err != nil || count != 0 {
		t.Fatalf("invalid Merchant UUID created %d users: %v", count, err)
	}
}

func TestMilestone04ZITADELWebhookSerializesCompetingFixedMerchantInitializations(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required")
	}
	for name, testCase := range map[string]struct {
		firstPayload  string
		secondPayload string
		assertState   func(*testing.T, *Handler)
	}{
		"same Human with different UUIDs": {
			firstPayload:  `{"merchant_id":"663926ef-df6a-4ef4-a322-2b21a386878a","user":{"id":"competing-human","human":{}},"userinfo":{"name":"First Delivery"}}`,
			secondPayload: `{"merchant_id":"cc857ab5-1c38-4a87-9144-9ce5322470fb","user":{"id":"competing-human","human":{}},"userinfo":{"name":"Second Delivery"}}`,
			assertState: func(t *testing.T, handler *Handler) {
				t.Helper()
				var counts struct {
					Users     int `db:"users"`
					Accounts  int `db:"accounts"`
					Ledgers   int `db:"ledgers"`
					Merchants int `db:"merchants"`
					Profiles  int `db:"profiles"`
					Balances  int `db:"balances"`
				}
				if err := handler.config.DB.Get(&counts, `SELECT (SELECT count(*) FROM users WHERE identity_subject='competing-human') users,(SELECT count(*) FROM accounts a JOIN users u ON u.id=a.owner_user_id WHERE u.identity_subject='competing-human') accounts,(SELECT count(*) FROM ledger_accounts l JOIN accounts a ON a.id=l.owner_account_id JOIN users u ON u.id=a.owner_user_id WHERE u.identity_subject='competing-human') ledgers,(SELECT count(*) FROM merchants m JOIN accounts a ON a.id=m.settlement_account_id JOIN users u ON u.id=a.owner_user_id WHERE u.identity_subject='competing-human') merchants,(SELECT count(*) FROM client_sync.user_profiles WHERE owner_identity_subject='competing-human') profiles,(SELECT count(*) FROM client_sync.account_balances WHERE owner_identity_subject='competing-human') balances`); err != nil {
					t.Fatal(err)
				}
				if counts.Users != 1 || counts.Accounts != 1 || counts.Ledgers != 1 || counts.Merchants != 1 || counts.Profiles != 1 || counts.Balances != 1 {
					t.Fatalf("competing same-Human initialization left state %+v", counts)
				}
			},
		},
		"different Humans with the same UUID": {
			firstPayload:  `{"merchant_id":"df67ff37-35b2-49bc-af3d-c50e3af19a5f","user":{"id":"competing-human-a","human":{}},"userinfo":{"name":"Competing Human A"}}`,
			secondPayload: `{"merchant_id":"df67ff37-35b2-49bc-af3d-c50e3af19a5f","user":{"id":"competing-human-b","human":{}},"userinfo":{"name":"Competing Human B"}}`,
			assertState: func(t *testing.T, handler *Handler) {
				t.Helper()
				var counts struct {
					Users     int `db:"users"`
					Accounts  int `db:"accounts"`
					Ledgers   int `db:"ledgers"`
					Merchants int `db:"merchants"`
					Profiles  int `db:"profiles"`
					Balances  int `db:"balances"`
				}
				if err := handler.config.DB.Get(&counts, `SELECT (SELECT count(*) FROM users WHERE identity_subject IN ('competing-human-a','competing-human-b')) users,(SELECT count(*) FROM accounts a JOIN users u ON u.id=a.owner_user_id WHERE u.identity_subject IN ('competing-human-a','competing-human-b')) accounts,(SELECT count(*) FROM ledger_accounts l JOIN accounts a ON a.id=l.owner_account_id JOIN users u ON u.id=a.owner_user_id WHERE u.identity_subject IN ('competing-human-a','competing-human-b')) ledgers,(SELECT count(*) FROM merchants WHERE id='df67ff37-35b2-49bc-af3d-c50e3af19a5f') merchants,(SELECT count(*) FROM client_sync.user_profiles WHERE owner_identity_subject IN ('competing-human-a','competing-human-b')) profiles,(SELECT count(*) FROM client_sync.account_balances WHERE owner_identity_subject IN ('competing-human-a','competing-human-b')) balances`); err != nil {
					t.Fatal(err)
				}
				if counts.Users != 1 || counts.Accounts != 1 || counts.Ledgers != 1 || counts.Merchants != 1 || counts.Profiles != 1 || counts.Balances != 1 {
					t.Fatalf("competing cross-Human initialization left state %+v", counts)
				}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := testdb.OpenGizPay(t).SQL
			now := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
			handler := &Handler{config: Config{DB: db, Now: func() time.Time { return now }}}
			setM04ConfigField(t, &handler.config, "ZITADELIssuer", "https://identity.example.test")
			setM04ConfigField(t, &handler.config, "ActionSigningKey", []byte("competing-merchant-signing-key"))

			lock, err := db.Connx(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if _, err := db.Exec(`CREATE SEQUENCE identity_bootstrap_barrier`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`CREATE FUNCTION block_identity_bootstrap() RETURNS trigger LANGUAGE plpgsql AS $function$ BEGIN PERFORM nextval('identity_bootstrap_barrier'); PERFORM pg_advisory_xact_lock(hashtextextended(current_database(),0)); RETURN NEW; END $function$`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`CREATE TRIGGER block_identity_bootstrap BEFORE INSERT ON users FOR EACH ROW EXECUTE FUNCTION block_identity_bootstrap()`); err != nil {
				t.Fatal(err)
			}
			if _, err := lock.ExecContext(t.Context(), `SELECT pg_advisory_lock(hashtextextended(current_database(),0))`); err != nil {
				t.Fatal(err)
			}
			locked := true
			defer func() {
				if locked {
					_, _ = lock.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended(current_database(),0))`)
				}
			}()

			responses := make(chan *httptest.ResponseRecorder, 2)
			for _, payload := range []string{testCase.firstPayload, testCase.secondPayload} {
				go func() {
					responses <- serveSignedWebhook(handler, []byte(payload), now, []byte("competing-merchant-signing-key"))
				}()
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				var barrierCount int64
				if err := db.Get(&barrierCount, `SELECT CASE WHEN is_called THEN last_value ELSE 0 END FROM identity_bootstrap_barrier`); err != nil {
					t.Fatal(err)
				}
				if barrierCount == 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("competing bootstrap requests did not reach the mutation barrier")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if _, err := lock.ExecContext(t.Context(), `SELECT pg_advisory_unlock(hashtextextended(current_database(),0))`); err != nil {
				t.Fatal(err)
			}
			locked = false

			statuses := make([]int, 0, 2)
			for range 2 {
				select {
				case response := <-responses:
					statuses = append(statuses, response.Code)
				case <-time.After(10 * time.Second):
					t.Fatal("competing bootstrap request did not complete")
				}
			}
			sort.Ints(statuses)
			if !reflect.DeepEqual(statuses, []int{http.StatusNoContent, http.StatusConflict}) {
				t.Fatalf("competing bootstrap statuses=%v", statuses)
			}
			testCase.assertState(t, handler)
		})
	}
}

func serveSignedWebhook(handler *Handler, body []byte, now time.Time, secret []byte) *httptest.ResponseRecorder {
	return serveSignedWebhookWithHeader(handler, body, now, secret, "X-ZITADEL-Signature")
}

func serveSignedWebhookWithHeader(handler *Handler, body []byte, now time.Time, secret []byte, header string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/webhooks/v1/zitadel/user-authenticated", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(header, webhookSignature(body, now, secret))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func webhookSignature(body []byte, timestamp time.Time, secret []byte) string {
	seconds := fmt.Sprint(timestamp.Unix())
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(seconds + "."))
	_, _ = mac.Write(body)
	return "t=" + seconds + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func setM04ConfigField(t *testing.T, config any, name string, value any) {
	t.Helper()
	field := reflect.ValueOf(config).Elem().FieldByName(name)
	if !field.IsValid() || !field.CanSet() {
		t.Fatalf("GizPay Config lacks M04 field %s", name)
	}
	field.Set(reflect.ValueOf(value))
}
