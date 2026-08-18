package gizpay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminAPIRequiresOnlyConfiguredAdminKey(t *testing.T) {
	handler := &Handler{config: Config{AdminKey: []byte("configured-admin-key")}}
	for name, key := range map[string]string{"missing": "", "wrong": "bearer-token"} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/admin/v1/products", nil)
			request.Header.Set("X-GizWay-Admin-Key", key)
			request.Header.Set("Authorization", "Bearer configured-admin-key")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), "invalid_admin_key") || strings.Contains(recorder.Body.String(), "configured-admin-key") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
