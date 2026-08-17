package gizpay

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/idy/gizway/internal/testdb"
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
