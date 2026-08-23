package gizway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	bifrostadapter "github.com/GizClaw/gizway/internal/adapter/bifrost"
	"github.com/GizClaw/gizway/internal/storage"
	"github.com/GizClaw/gizway/internal/testdb"
	"gorm.io/gorm"
)

func TestAdminProviderPostgreSQLRollsBackBifrostWhenProjectionFails(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required")
	}
	dsn := testdb.NewDatabase(t)
	serviceDSN := testdb.SearchPathDSN(t, dsn, "public")
	database, err := storage.OpenGizWayPostgreSQL(serviceDSN, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	stores, err := bifrostadapter.OpenStores(t.Context(), bifrostadapter.StoreConfig{Type: "postgresql", DSN: dsn, Schema: "bifrost_config"}, bifrostadapter.StoreConfig{Type: "postgresql", DSN: dsn, Schema: "bifrost_logs"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stores.Close(context.Background()) })
	if _, err := database.SQL.Exec(`INSERT INTO client_sync.providers(id,name,kind,status) VALUES('provider-rollback','Existing','openai','active')`); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{config: Config{AdminKey: []byte("admin"), DB: database.SQL, DatabaseSchema: "gizway", Now: time.Now}, stores: stores}
	request := httptest.NewRequest(http.MethodPost, "/admin/v1/providers", strings.NewReader(`{"id":"provider-rollback","name":"Provider","kind":"openai","base_url":"https://provider.test","status":"active"}`))
	request.Header.Set("X-GizWay-Admin-Key", "admin")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if _, err := stores.Provider(t.Context(), "provider-rollback"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("Bifrost Provider survived rolled-back projection: %v", err)
	}
	adminRequest := func(method, path, body string) *http.Request {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("X-GizWay-Admin-Key", "admin")
		request.Header.Set("Content-Type", "application/json")
		return request
	}
	for _, resource := range []struct {
		path string
		body string
	}{
		{"/admin/v1/providers", `{"id":"provider-concurrent","name":"Provider","kind":"openai","base_url":"https://provider.test","status":"active"}`},
		{"/admin/v1/models", `{"id":"model-concurrent","provider_id":"provider-concurrent","name":"Model","provider_model":"upstream-model","status":"active"}`},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, adminRequest(http.MethodPost, resource.path, resource.body))
		if recorder.Code != http.StatusCreated {
			t.Fatalf("create %s status=%d body=%s", resource.path, recorder.Code, recorder.Body.String())
		}
	}
	priceBodies := []string{
		`{"prices":[{"metric":"input_tokens","unit_size":1,"price_microcredits":10}]}`,
		`{"prices":[{"metric":"input_tokens","unit_size":1,"price_microcredits":20}]}`,
	}
	codes := make([]int, len(priceBodies))
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range priceBodies {
		group.Go(func() {
			<-start
			request := adminRequest(http.MethodPut, "/admin/v1/models/model-concurrent/customer-prices", priceBodies[index])
			request.SetPathValue("model_id", "model-concurrent")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			codes[index] = recorder.Code
		})
	}
	close(start)
	group.Wait()
	for _, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("concurrent price replacement statuses=%v", codes)
		}
	}
	var price, count int64
	if err := database.SQL.QueryRowx(`SELECT min(price_microcredits),count(*) FROM model_customer_prices WHERE model_id='model-concurrent'`).Scan(&price, &count); err != nil || count != 1 || price != 10 && price != 20 {
		t.Fatalf("concurrent price final state price=%d count=%d error=%v", price, count, err)
	}
}
