package gizway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	bifrostadapter "github.com/idy/gizway/internal/adapter/bifrost"
	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/testdb"
	"gorm.io/gorm"
)

func TestAdminProviderPostgreSQLRollsBackBifrostWhenProjectionFails(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required")
	}
	dsn := testdb.NewDatabase(t)
	database, err := storage.OpenGizWayPostgreSQL(dsn, true)
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
}
