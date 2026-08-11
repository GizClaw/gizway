package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServerFromArgs(t *testing.T) {
	t.Parallel()
	server, err := serverFromArgs([]string{"-address", "127.0.0.1:29000", "-credential", "test-key", "-callback-secret", "callback-key"})
	if err != nil {
		t.Fatalf("serverFromArgs: %v", err)
	}
	if server.Addr != "127.0.0.1:29000" || server.ReadHeaderTimeout != 5*time.Second || server.Handler == nil {
		t.Fatalf("unexpected server configuration: %+v", server)
	}
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("health status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestServerFromArgsRejectsInvalidFlag(t *testing.T) {
	t.Parallel()
	if _, err := serverFromArgs([]string{"-unknown"}); err == nil {
		t.Fatal("expected invalid flag error")
	}
}
