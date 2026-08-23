package gizpay

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/gizway/internal/testdb"
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
	concurrentCreate := strings.Replace(create, "admin-product", "admin-product-concurrent", 1)
	codes := make([]int, 2)
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range codes {
		group.Go(func() {
			<-start
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request(http.MethodPost, "/admin/v1/products", concurrentCreate))
			codes[index] = recorder.Code
		})
	}
	close(start)
	group.Wait()
	sort.Ints(codes)
	if codes[0] != http.StatusOK || codes[1] != http.StatusCreated {
		t.Fatalf("concurrent equivalent create statuses=%v", codes)
	}
	conflictingCreates := []string{
		strings.Replace(create, `"admin-product"`, `"admin-product-conflict"`, 1),
		strings.Replace(strings.Replace(create, `"admin-product"`, `"admin-product-conflict"`, 1), `"Product"`, `"Different Product"`, 1),
	}
	start = make(chan struct{})
	for index := range codes {
		group.Go(func() {
			<-start
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request(http.MethodPost, "/admin/v1/products", conflictingCreates[index]))
			codes[index] = recorder.Code
		})
	}
	close(start)
	group.Wait()
	sort.Ints(codes)
	if codes[0] != http.StatusCreated || codes[1] != http.StatusConflict {
		t.Fatalf("concurrent conflicting create statuses=%v", codes)
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
