package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

func TestQuotaExchangeHandlerResponsesAndValidation(t *testing.T) {
	database := testdb.OpenGizPayStory(t)
	defer database.Close()
	now := func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) }
	repository := store.New(database.SQL)
	repository.ConfigureClock(now)
	server := NewWithServicesAndClockSurface(repository, nil, nil, nil, now, nil, SurfaceGizPay)

	call := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/internal/v1/quota/exchanges", strings.NewReader(body))
		ctx := context.WithValue(request.Context(), gatewayNodeIDKey, "gw-global-e2e")
		ctx = context.WithValue(ctx, gatewayRegionKey, "global")
		response := httptest.NewRecorder()
		server.exchangeQuota(response, request.WithContext(ctx))
		return response
	}
	if response := call(`{"api_key":"giz_story_user_active_1"}`); response.Code != http.StatusOK {
		t.Fatalf("empty Usage status=%d body=%s", response.Code, response.Body.String())
	}
	valid := `{"api_key":"giz_story_user_active_1","usage":[{"ucgid":"go-quota-usage","operation_id":"go-operation","public_model":"story-text","model_variant_id":"91000000-0000-4000-8000-000000000001","rate_publication_id":"ratepub_story_global_1","metrics":{"request":1},"started_at":"2026-08-12T00:00:00Z","completed_at":"2026-08-12T00:00:01Z"}]}`
	if response := call(valid); response.Code != http.StatusOK {
		t.Fatalf("valid Usage status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(valid); response.Code != http.StatusOK {
		t.Fatalf("Usage replay status=%d body=%s", response.Code, response.Body.String())
	}
	conflict := strings.Replace(valid, `"request":1`, `"request":2`, 1)
	if response := call(conflict); response.Code != http.StatusConflict {
		t.Fatalf("conflicting UCGID status=%d body=%s", response.Code, response.Body.String())
	}
	unpriceable := strings.Replace(valid, "go-quota-usage", "go-unpriceable", 1)
	unpriceable = strings.Replace(unpriceable, "ratepub_story_global_1", "missing-publication", 1)
	if response := call(unpriceable); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unpriceable status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(`{"api_key":"invalid"}`); response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid key status=%d body=%s", response.Code, response.Body.String())
	}

	longValue := strings.Repeat("x", 256)
	invalidBodies := []string{
		`{`,
		`{"api_key":"giz_story_user_active_1","unknown":true}`,
		`{"api_key":""}`,
		`{"api_key":"giz_story_user_active_1","usage":[{}]}`,
		`{"api_key":"giz_story_user_active_1","usage":[{"ucgid":"u","operation_id":"o","public_model":"m","model_variant_id":"v","rate_publication_id":"r","metrics":{"request":-1},"started_at":"2026-08-12T00:00:00Z","completed_at":"2026-08-12T00:00:01Z"}]}`,
		`{"api_key":"giz_story_user_active_1","usage":[{"ucgid":"u","operation_id":"o","public_model":"m","model_variant_id":"v","rate_publication_id":"r","metrics":{"request":1},"started_at":"bad","completed_at":"2026-08-12T00:00:01Z"}]}`,
		`{"api_key":"giz_story_user_active_1","usage":[{"ucgid":"u","operation_id":"o","public_model":"m","model_variant_id":"v","rate_publication_id":"r","metrics":{"request":1},"started_at":"2026-08-12T00:00:02Z","completed_at":"2026-08-12T00:00:01Z"}]}`,
		`{"api_key":"giz_story_user_active_1","usage":[{"ucgid":"` + longValue + `","operation_id":"o","public_model":"m","model_variant_id":"v","rate_publication_id":"r","metrics":{"request":1},"started_at":"2026-08-12T00:00:00Z","completed_at":"2026-08-12T00:00:01Z"}]}`,
		`{"api_key":"giz_story_user_active_1"}` + strings.Repeat(" ", maximumQuotaExchangeBody),
	}
	for index, body := range invalidBodies {
		if response := call(body); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid body %d status=%d body=%s", index, response.Code, response.Body.String())
		}
	}
}
