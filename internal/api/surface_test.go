package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProductionSurfacesRejectRoutesOwnedByTheOtherBinary(t *testing.T) {
	t.Parallel()
	now := func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) }
	for _, test := range []struct {
		name    string
		surface Surface
		method  string
		path    string
	}{
		{name: "GizPay rejects regional model API", surface: SurfaceGizPay, method: http.MethodGet, path: "/v1/models"},
		{name: "GizWay rejects central account API", surface: SurfaceGizWay, method: http.MethodGet, path: "/account/v1/me"},
		{name: "GizWay rejects internal center mutation before idempotency", surface: SurfaceGizWay, method: http.MethodPost, path: "/internal/v1/quota/exchanges"},
		{name: "GizPay rejects provider admin mutation before idempotency", surface: SurfaceGizPay, method: http.MethodPost, path: "/admin/v1/providers"},
		{name: "GizPay rejects regional rate publish", surface: SurfaceGizPay, method: http.MethodPost, path: "/admin/v1/rate_publications"},
		{name: "GizWay rejects center rate disable", surface: SurfaceGizWay, method: http.MethodPost, path: "/admin/v1/rate_publications/ratepub/disable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := NewWithServicesAndClockSurface(nil, nil, nil, nil, now, nil, test.surface)
			request := httptest.NewRequest(test.method, test.path, nil)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", response.Code)
			}
		})
	}
}

func TestOperationalEndpointsExposeOnlyCurrentReadinessFacts(t *testing.T) {
	server := NewWithServicesAndClockSurface(nil, nil, nil, nil, time.Now, nil, SurfaceGizPay)
	for _, path := range []string{"/livez", "/readyz"} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	server.ConfigureReadiness(func(context.Context, bool) (map[string]any, error) {
		return map[string]any{"status": "not_ready", "checks": map[string]string{"catalog": "pending"}}, nil
	})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "not_ready") {
		t.Fatalf("not-ready response=%d %s", response.Code, response.Body.String())
	}
	server.ConfigureReadiness(func(context.Context, bool) (map[string]any, error) { return nil, errors.New("database down") })
	response = httptest.NewRecorder()
	server.bootstrapStatus(response, httptest.NewRequest(http.MethodGet, "/bootstrap", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "failed") {
		t.Fatalf("failed readiness response=%d %s", response.Code, response.Body.String())
	}
	server.ConfigureQuotaRecheckPolicy(2*time.Second, 3*time.Second)
	server.ConfigureQuotaRecheckPolicy(time.Millisecond, time.Millisecond)
	if server.quotaRecheckSeconds != 2 || server.deniedRecheckSeconds != 3 {
		t.Fatalf("quota readiness policy=%d/%d", server.quotaRecheckSeconds, server.deniedRecheckSeconds)
	}
}

func TestReceivedUsageListQueryValidation(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/usage?cursor=next&limit=25", nil)
	query, err := receivedUsageListQuery(request)
	if err != nil || query.Cursor != "next" || query.Limit != 25 {
		t.Fatalf("query=%+v err=%v", query, err)
	}
	for _, raw := range []string{"0", "101", "not-a-number"} {
		request := httptest.NewRequest(http.MethodGet, "/usage?limit="+raw, nil)
		if _, err := receivedUsageListQuery(request); err == nil {
			t.Fatalf("accepted limit %q", raw)
		}
	}
	request = httptest.NewRequest(http.MethodGet, "/usage?cursor="+strings.Repeat("x", 256), nil)
	if _, err := receivedUsageListQuery(request); err == nil {
		t.Fatal("accepted oversized cursor")
	}
}
