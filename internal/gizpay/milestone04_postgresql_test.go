package gizpay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idy/gizway/internal/testdb"
)

func TestMilestone04CreditCheckReturnsSubscriptionKeyID(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required")
	}
	db := testdb.OpenGizPay(t).SQL
	_, err := db.ExecContext(t.Context(), `
		INSERT INTO users(id,identity_issuer,identity_subject,email,display_name) VALUES ('user-credit-m04','issuer','subject','','User');
		INSERT INTO accounts(id,owner_user_id) VALUES ('account-credit-m04','user-credit-m04');
		INSERT INTO ledger_accounts(id,owner_account_id,asset_code) VALUES ('ledger-credit-m04','account-credit-m04','credit');
		INSERT INTO merchants(id,settlement_account_id,legal_name,public_name,is_default) VALUES ('merchant-credit-m04','account-credit-m04','Merchant','Merchant',true);
		INSERT INTO products(id,merchant_id,name,terms_version) VALUES ('product-credit-m04','merchant-credit-m04','PAYG','v1');
		INSERT INTO subscriptions(id,account_id,product_id,terms_version) VALUES ('subscription-credit-m04','account-credit-m04','product-credit-m04','v1');
		INSERT INTO subscription_keys(id,subscription_id,name,key,subscription_key_hmac) VALUES ('key-credit-m04','subscription-credit-m04','CLI','giz_sk_credit_m04','credit-m04-hmac');
		INSERT INTO ledger_transactions(id,transaction_type,status) VALUES ('txn-credit-m04','topup','pending');
		INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,direction,amount_microcredits) VALUES
		  ('entry-credit-m04-debit','txn-credit-m04','led_clearing','debit',100),
		  ('entry-credit-m04-credit','txn-credit-m04','ledger-credit-m04','credit',100);
		UPDATE ledger_transactions SET status='posted' WHERE id='txn-credit-m04';
	`)
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{config: Config{DB: db, Now: time.Now, RecheckInterval: 5 * time.Minute}}
	request := httptest.NewRequest("POST", "/service/v1/subscription-credit-checks", nil)
	request.Body = io.NopCloser(strings.NewReader(`{"subscription_key_hmac":"credit-m04-hmac"}`))
	recorder := httptest.NewRecorder()
	handler.creditCheck(recorder, request)
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["subscription_key_id"] != "key-credit-m04" {
		t.Fatalf("subscription_key_id=%v, want key-credit-m04; body=%s", body["subscription_key_id"], recorder.Body.String())
	}
}

func TestMilestone04ConcurrentTopupRetryPostsOnce(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required")
	}
	db := testdb.OpenGizPay(t).SQL
	_, err := db.ExecContext(t.Context(), `
		INSERT INTO users(id,identity_issuer,identity_subject,email,display_name) VALUES ('user-topup-race','issuer','subject','','User');
		INSERT INTO accounts(id,owner_user_id) VALUES ('account-topup-race','user-topup-race');
		INSERT INTO ledger_accounts(id,owner_account_id,asset_code) VALUES ('ledger-topup-race','account-topup-race','credit');
	`)
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{config: Config{DB: db, Now: time.Now}}
	const attempts = 16
	statuses := make(chan int, attempts)
	var group sync.WaitGroup
	for range attempts {
		group.Go(func() {
			request := httptest.NewRequest(http.MethodPost, "/account/v1/accounts/account-topup-race/topups", strings.NewReader(`{"id":"topup-race","channel":"fake","external_reference":"race-ref","amount_microcredits":100}`))
			recorder := httptest.NewRecorder()
			handler.accountTopups(recorder, request, "account-topup-race")
			statuses <- recorder.Code
		})
	}
	group.Wait()
	close(statuses)
	created := 0
	for status := range statuses {
		if status == http.StatusCreated {
			created++
			continue
		}
		if status != http.StatusOK {
			t.Fatalf("concurrent top-up status=%d, want 200 or 201", status)
		}
	}
	if created != 1 {
		t.Fatalf("created responses=%d, want 1", created)
	}
	var topups, transactions, entries int
	if err = db.QueryRow(`SELECT count(*) FROM topups WHERE id='topup-race'`).Scan(&topups); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM ledger_transactions WHERE transaction_type='topup'`).Scan(&transactions); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM ledger_entries e JOIN ledger_transactions t ON t.id=e.transaction_id WHERE t.transaction_type='topup'`).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if topups != 1 || transactions != 1 || entries != 2 {
		t.Fatalf("topups=%d transactions=%d entries=%d, want 1/1/2", topups, transactions, entries)
	}
}

func TestMilestone04ConcurrentSubscriptionRetryCreatesOnce(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required")
	}
	db := testdb.OpenGizPay(t).SQL
	_, err := db.ExecContext(t.Context(), `
		INSERT INTO users(id,identity_issuer,identity_subject,email,display_name) VALUES ('user-sub-race','issuer','subject','','User');
		INSERT INTO accounts(id,owner_user_id) VALUES ('account-sub-race','user-sub-race');
		INSERT INTO ledger_accounts(id,owner_account_id,asset_code) VALUES ('ledger-sub-race','account-sub-race','credit');
		INSERT INTO merchants(id,settlement_account_id,legal_name,public_name,is_default) VALUES ('merchant-sub-race','account-sub-race','Merchant','Merchant',true);
		INSERT INTO products(id,merchant_id,name,terms_version,published) VALUES ('product-sub-race','merchant-sub-race','PAYG','v1',true);
	`)
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{config: Config{DB: db, Now: time.Now}}
	const attempts = 16
	statuses := make(chan int, attempts)
	var group sync.WaitGroup
	for range attempts {
		group.Go(func() {
			request := httptest.NewRequest(http.MethodPost, "/account/v1/products/product-sub-race/subscriptions", strings.NewReader(`{"id":"subscription-race","account_id":"account-sub-race","terms_version":"v1"}`))
			recorder := httptest.NewRecorder()
			handler.createSubscription(recorder, request, "product-sub-race", "user-sub-race", "account-sub-race")
			statuses <- recorder.Code
		})
	}
	group.Wait()
	close(statuses)
	created := 0
	for status := range statuses {
		if status == http.StatusCreated {
			created++
			continue
		}
		if status != http.StatusOK {
			t.Fatalf("concurrent subscription status=%d, want 200 or 201", status)
		}
	}
	if created != 1 {
		t.Fatalf("created responses=%d, want 1", created)
	}
	var subscriptions int
	if err = db.QueryRow(`SELECT count(*) FROM subscriptions WHERE account_id='account-sub-race' AND product_id='product-sub-race'`).Scan(&subscriptions); err != nil {
		t.Fatal(err)
	}
	if subscriptions != 1 {
		t.Fatalf("subscriptions=%d, want 1", subscriptions)
	}
}
