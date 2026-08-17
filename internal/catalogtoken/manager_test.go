package catalogtoken

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerCachesAndRefreshesJWT(t *testing.T) {
	now := time.Date(2026, 8, 16, 4, 0, 0, 0, time.UTC)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("scope") != "openid roles audience" {
			t.Fatalf("unexpected token request: %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "header.payload.signature", "token_type": "Bearer", "expires_in": 12 * 60 * 60,
		})
	}))
	defer server.Close()
	manager, err := New(Config{
		TokenURL: server.URL, ClientID: "catalog", ClientSecret: "secret", Scope: "openid roles audience",
		TTL: 12 * time.Hour, RefreshBefore: time.Hour, HTTPClient: server.Client(), Now: func() time.Time { return now },
		ValidateJWT: func(token string) error {
			if token != "header.payload.signature" {
				return errors.New("unexpected token")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first != second || requests.Load() != 1 {
		t.Fatalf("cache token equality=%t requests=%d", first == second, requests.Load())
	}
	now = now.Add(11*time.Hour + time.Second)
	if _, err := manager.Current(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("refresh requests=%d, want 2", requests.Load())
	}
}

func TestManagerRejectsOpaqueOrOverlongTokens(t *testing.T) {
	for name, response := range map[string]map[string]any{
		"opaque":   {"access_token": "opaque", "token_type": "Bearer", "expires_in": 3600},
		"overlong": {"access_token": "header.payload.signature", "token_type": "Bearer", "expires_in": 25 * 60 * 60},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(response) }))
			defer server.Close()
			manager, err := New(Config{
				TokenURL: server.URL, ClientID: "catalog", ClientSecret: "secret", Scope: "openid",
				TTL: 12 * time.Hour, RefreshBefore: time.Hour, HTTPClient: server.Client(), Now: time.Now,
				ValidateJWT: func(string) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Current(context.Background()); err == nil {
				t.Fatal("invalid Catalog token was cached")
			}
		})
	}
}

func TestManagerKeepsOnlyUnexpiredTokenWhenRefreshFails(t *testing.T) {
	now := time.Date(2026, 8, 16, 4, 0, 0, 0, time.UTC)
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "header.payload.signature", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer server.Close()
	manager, err := New(Config{
		TokenURL: server.URL, ClientID: "catalog", ClientSecret: "secret", Scope: "openid",
		TTL: time.Hour, RefreshBefore: 15 * time.Minute, HTTPClient: server.Client(), Now: func() time.Time { return now },
		ValidateJWT: func(string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	now = now.Add(50 * time.Minute)
	stillValid, err := manager.Current(context.Background())
	if err != nil || stillValid != first {
		t.Fatalf("unexpired fallback=(%v,%v)", stillValid, err)
	}
	now = now.Add(11 * time.Minute)
	if _, err := manager.Current(context.Background()); err == nil {
		t.Fatal("expired Catalog token was returned after refresh failure")
	}
}

func TestManagerStartFetchesBeforeReadyAndRefreshesProactively(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "header.payload.signature", "token_type": "Bearer", "expires_in": 1,
		})
	}))
	defer server.Close()
	manager, err := New(Config{
		TokenURL: server.URL, ClientID: "catalog", ClientSecret: "secret", Scope: "openid",
		TTL: time.Second, RefreshBefore: 900 * time.Millisecond, HTTPClient: server.Client(),
		ValidateJWT: func(string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if requests.Load() != 1 {
		t.Fatalf("startup token requests=%d, want 1", requests.Load())
	}
	deadline := time.Now().Add(time.Second)
	for requests.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if requests.Load() < 2 {
		t.Fatal("manager did not proactively refresh the Catalog token")
	}
}

func TestManagerStartFailureDoesNotLeaveLifecycleRunning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	manager, err := New(Config{
		TokenURL: server.URL, ClientID: "catalog", ClientSecret: "secret", Scope: "openid",
		TTL: time.Hour, RefreshBefore: time.Minute, HTTPClient: server.Client(), ValidateJWT: func(string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err == nil {
		t.Fatal("startup accepted an unavailable token endpoint")
	}
	closed := make(chan struct{})
	go func() { manager.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked after Start failed")
	}
}
