package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMilestone03GizPayRouteBoundary(t *testing.T) {
	business := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	server := NewMilestone03(SurfaceGizPay, "gizpay", business, nil)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/account/v1/accounts"},
		{http.MethodPost, "/account/v1/accounts/account-1/topups"},
		{http.MethodGet, "/account/v1/accounts/account-1/topups"},
		{http.MethodPost, "/service/v1/subscription-credit-checks"},
		{http.MethodPost, "/service/v1/payg-charges"},
	} {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(route.method, route.path, nil))
		if recorder.Code != http.StatusNoContent {
			t.Errorf("%s %s status=%d, want business handler", route.method, route.path, recorder.Code)
		}
	}
}

func TestMilestone03RejectsUndeclaredMethodsBeforeBusinessHandler(t *testing.T) {
	called := false
	business := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	server := NewMilestone03(SurfaceGizPay, "gizpay", business, nil)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/account/v1/subscriptions/sub-1/keys/key-1/revoke"},
		{http.MethodDelete, "/account/v1/merchants/merchant-1"},
		{http.MethodPost, "/account/v1/accounts/account-1/balance"},
	} {
		called = false
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(route.method, route.path, nil))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s status=%d, want 405", route.method, route.path, recorder.Code)
		}
		if called {
			t.Errorf("%s %s reached the business handler", route.method, route.path)
		}
	}
}

func TestMilestone03GizWayExposesCommandsAndRejectsAdmin(t *testing.T) {
	business := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	server := NewMilestone03(SurfaceGizWay, "gizway", business, nil)
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/user/v1/providers/provider-1/keys"},
		{http.MethodPut, "/user/v1/provider-keys/key-1/prices"},
		{http.MethodPost, "/user/v1/provider-keys/key-1/disable"},
	} {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(route.method, route.path, nil))
		if recorder.Code != http.StatusNoContent {
			t.Errorf("%s %s status=%d, want business handler", route.method, route.path, recorder.Code)
		}
	}
	for _, path := range []string{"/admin/v1/models", "/admin/v1/providers", "/admin/v1/ai-orders"} {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("GET %s status=%d, want deleted route", path, recorder.Code)
		}
	}
}

func TestMilestone03HealthIsProcessOnly(t *testing.T) {
	for _, surface := range []Surface{SurfaceGizPay, SurfaceGizWay} {
		server := NewMilestone03(surface, surface.String(), nil, nil)
		server.now = func() time.Time { return time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC) }
		server.version = "test"
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s health status=%d", surface.String(), recorder.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"status": "healthy", "service": surface.String(),
			"version": "test", "server_time": "2026-08-14T12:00:00Z",
		}
		for key, value := range want {
			if body[key] != value {
				t.Errorf("%s health %s=%v, want %v", surface.String(), key, body[key], value)
			}
		}
		for _, forbidden := range []string{"degraded", "dependencies", "bootstrap"} {
			if _, exists := body[forbidden]; exists {
				t.Errorf("%s health contains %s", surface.String(), forbidden)
			}
		}
	}
}
