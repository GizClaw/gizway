package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	bifrostadapter "github.com/idy/gizway/internal/adapter/bifrost"
	gatewayservice "github.com/idy/gizway/internal/service/gateway"
	"github.com/idy/gizway/internal/service/gatewayquota"
	"github.com/idy/gizway/internal/service/localadmission"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
	"github.com/idy/gizway/internal/testfake/aiprovider"
)

// TestRegionalRealtimeWebSocketProxy covers the upgraded HTTP connection and
// bidirectional provider proxy with a GizWay-only database. Customer identity
// remains the raw API Key used by Quota Exchange; it is never copied locally.
func TestRegionalRealtimeWebSocketProxy(t *testing.T) {
	database := testdb.OpenGizWayStory(t)
	defer database.Close()
	fakeProvider := httptest.NewServer(aiprovider.Handler())
	defer fakeProvider.Close()
	if _, err := database.SQL.Exec(`UPDATE provider_endpoints SET base_url=$1,credential_ref='story-provider-key'`, fakeProvider.URL); err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) }
	repository := store.New(database.SQL)
	repository.ConfigureClock(now)
	executor, err := bifrostadapter.NewOpenAI(t.Context(), fakeProvider.URL, "story-provider-key")
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Shutdown()
	quota := gatewayquota.New(regionalAllowedQuota{}, localadmission.New(now), repository, now)
	gateway := gatewayservice.NewWithRealtimeProviderCallback(repository, executor, "", "regional-realtime-secret")
	gateway.ConfigureClock(now)
	gateway.ConfigureRegionalQuota(quota)
	server := NewWithServicesAndClockSurface(repository, gateway, nil, nil, now, nil, SurfaceGizWay)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	created := apiJSON(t, httpServer, http.MethodPost, "/v1/realtime/client_secrets", "giz-regional-websocket", "go-realtime-websocket", map[string]any{
		"model": "story-text", "transport": "websocket",
	}, http.StatusCreated)
	secret := requiredString(t, created["client_secret"].(map[string]any), "value")
	sessionID := requiredString(t, created["session"].(map[string]any), "session_id")
	websocketURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v1/realtime?session_id=" + sessionID
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, websocketURL, &websocket.DialOptions{HTTPHeader: http.Header{
		"Authorization": []string{"Bearer " + secret},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"event_id":"go-response","type":"response.create","response":{"modalities":["text","audio"]}}`)); err != nil {
		t.Fatal(err)
	}
	foundDone := false
	for !foundDone {
		_, payload, err := connection.Read(ctx)
		if err != nil {
			t.Fatalf("read provider event: %v", err)
		}
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(payload, &event) == nil && event.Type == "response.done" {
			foundDone = true
		}
	}
	if err := connection.Close(websocket.StatusNormalClosure, "complete"); err != nil {
		t.Fatal(err)
	}
}
