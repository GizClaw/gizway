package gizpay

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/idy/gizway/internal/testdb"
)

func TestAdminProductPostgreSQLIdempotencyDependencyAndSoftDelete(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required")
	}
	db := testdb.OpenGizPay(t).SQL
	if _, err := db.Exec(`
		INSERT INTO users(id,identity_issuer,identity_subject,email,display_name) VALUES ('admin-owner','issuer','owner','','Owner');
		INSERT INTO accounts(id,owner_user_id) VALUES ('admin-account','admin-owner');
		INSERT INTO ledger_accounts(id,owner_account_id,asset_code) VALUES ('admin-ledger','admin-account','credit');
		INSERT INTO merchants(id,settlement_account_id,legal_name,public_name,is_default) VALUES ('admin-merchant','admin-account','Merchant','Merchant',true);
	`); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{config: Config{AdminKey: []byte("admin"), DB: db, Now: time.Now}}
	request := func(method, path, body string) *http.Request {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-GizWay-Admin-Key", "admin")
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	create := `{"id":"admin-product","merchant_id":"admin-merchant","name":"Product","billing_mode":"pay_as_you_go","published":true,"status":"active","terms_version":"v1"}`
	for index, want := range []int{http.StatusCreated, http.StatusOK} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request(http.MethodPost, "/admin/v1/products", create))
		if recorder.Code != want {
			t.Fatalf("create pass %d status=%d body=%s", index+1, recorder.Code, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request(http.MethodPost, "/admin/v1/products", strings.Replace(create, "admin-merchant", "missing-merchant", 1)))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("conflicting create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	deleteRequest := request(http.MethodDelete, "/admin/v1/products/admin-product", "")
	deleteRequest.SetPathValue("product_id", "admin-product")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, deleteRequest)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var status string
	if err := db.Get(&status, `SELECT status FROM products WHERE id='admin-product'`); err != nil || status != "inactive" {
		t.Fatalf("soft-deleted Product status=%q error=%v", status, err)
	}
}
