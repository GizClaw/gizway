package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerFromArgs(t *testing.T) {
	t.Parallel()
	server, err := serverFromArgs([]string{"-address", "127.0.0.1:29100", "-callback-secret", "callback-key", "-fixed-now", "2026-08-11T00:00:00Z"})
	if err != nil {
		t.Fatalf("serverFromArgs: %v", err)
	}
	if server.Addr != "127.0.0.1:29100" || server.Handler == nil {
		t.Fatalf("unexpected server configuration: %+v", server)
	}
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("health status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestServerFromArgsRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"-unknown"}, {"-fixed-now", "tomorrow"}} {
		if _, err := serverFromArgs(args); err == nil {
			t.Fatalf("serverFromArgs(%q) unexpectedly succeeded", args)
		}
	}
}
