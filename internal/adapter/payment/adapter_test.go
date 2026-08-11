package payment

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientProviderProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Idempotency-Key") == "" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/checkouts":
			_, _ = w.Write([]byte(`{"provider_reference":"pay-1","checkout_url":"https://checkout.test/1"}`))
		case "/v1/refunds":
			_, _ = w.Write([]byte(`{"provider_refund_id":"refund-1","status":"succeeded"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := New(server.URL+"/", "secret")
	checkout, err := client.CreateCheckout(t.Context(), "topup", "USD", 900)
	if err != nil || checkout.ProviderReference != "pay-1" {
		t.Fatalf("CreateCheckout = %+v, %v", checkout, err)
	}
	refund, err := client.Refund(t.Context(), "pay-1", "refund", "USD", 90)
	if err != nil || refund.ProviderRefundID != "refund-1" {
		t.Fatalf("Refund = %+v, %v", refund, err)
	}
}

func TestClientProviderFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler http.Handler
		want    string
	}{
		{name: "status", handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }), want: "status 502"},
		{name: "json", handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{`)) }), want: "decode"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			_, err := New(server.URL, "secret").CreateCheckout(t.Context(), "t", "USD", 1)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New("http://127.0.0.1:1", "secret").CreateCheckout(cancelled, "t", "USD", 1); err == nil || !strings.Contains(err.Error(), "payment provider request") {
		t.Fatalf("cancelled request error = %v", err)
	}
	client := New("://bad", "secret")
	if err := client.post(t.Context(), "/x", "operation", map[string]string{}, &struct{}{}); err == nil {
		t.Fatal("invalid request URL succeeded")
	}
	if err := New("http://example.test", "secret").post(t.Context(), "/x", "operation", make(chan int), &struct{}{}); err == nil {
		t.Fatal("unencodable request succeeded")
	}
}

func TestClientRejectsIncompleteSuccessfulResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		path   string
		body   string
		refund bool
	}{
		{name: "checkout missing reference", path: "/v1/checkouts", body: `{"checkout_url":"https://checkout.test/x"}`},
		{name: "checkout relative url", path: "/v1/checkouts", body: `{"provider_reference":"p","checkout_url":"/relative"}`},
		{name: "checkout unsafe scheme", path: "/v1/checkouts", body: `{"provider_reference":"p","checkout_url":"file:///tmp/x"}`},
		{name: "refund missing id", path: "/v1/refunds", body: `{"status":"succeeded"}`, refund: true},
		{name: "refund unknown status", path: "/v1/refunds", body: `{"provider_refund_id":"r","status":"maybe"}`, refund: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := NewNamed(server.URL, "secret", "fixture-provider")
			if client.ProviderID() != "fixture-provider" {
				t.Fatalf("ProviderID = %q", client.ProviderID())
			}
			var err error
			if test.refund {
				_, err = client.Refund(t.Context(), "provider-ref", "refund", "USD", 1)
			} else {
				_, err = client.CreateCheckout(t.Context(), "topup", "USD", 1)
			}
			if err == nil {
				t.Fatal("incomplete provider response succeeded")
			}
		})
	}

	// Pending/failed are valid provider states even though the orchestration
	// layer decides whether they are terminal or retryable.
	for _, status := range []string{"pending", "failed"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"` + status + `"}`))
		}))
		_, err := New(server.URL, "secret").Refund(t.Context(), "p", "r", "USD", 1)
		server.Close()
		if err != nil {
			t.Fatalf("status %s rejected: %v", status, err)
		}
	}
}
