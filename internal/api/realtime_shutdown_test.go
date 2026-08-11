package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestCloseRealtimeConnectionsWaitsForHijackedHandler(t *testing.T) {
	apiServer := &Server{realtimeConns: make(map[*websocket.Conn]struct{})}
	handlerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		if !apiServer.trackRealtimeConnection(connection) {
			_ = connection.CloseNow()
			return
		}
		defer close(handlerDone)
		defer apiServer.untrackRealtimeConnection(connection)
		_, _, _ = connection.Read(r.Context())
	}))
	defer server.Close()
	client, _, err := websocket.Dial(t.Context(), server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := apiServer.CloseRealtimeConnections(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handlerDone:
	default:
		t.Fatal("Realtime shutdown returned before hijacked handler finished")
	}
	if apiServer.trackRealtimeConnection(client) {
		t.Fatal("new Realtime connection accepted after shutdown began")
	}
}
