package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMilestone04RouteBoundary(t *testing.T) {
	business := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	t.Run("GizPay webhook replaces initialize", func(t *testing.T) {
		server := NewMilestone03(SurfaceGizPay, "gizpay", business, nil)
		for _, test := range []struct {
			method string
			path   string
			want   int
		}{
			{http.MethodPost, "/webhooks/v1/zitadel/user-authenticated", http.StatusNoContent},
			{http.MethodPost, "/account/v1/initialize", http.StatusNotFound},
		} {
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
			if recorder.Code != test.want {
				t.Errorf("%s %s status=%d, want %d", test.method, test.path, recorder.Code, test.want)
			}
		}
	})

	t.Run("GizWay exposes public catalog token", func(t *testing.T) {
		server := NewMilestone03(SurfaceGizWay, "gizway", business, nil)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/auth/catalog-token", nil))
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("GET /auth/catalog-token status=%d, want business handler", recorder.Code)
		}
	})
}
