package paymentprovider

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFixtureRejectsMalformedAndUnauthorizedControlTraffic(t *testing.T) {
	server := httptest.NewServer(Handler("callback-secret"))
	defer server.Close()
	request := func(method, path, authorization, idempotency, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		if idempotency != "" {
			req.Header.Set("Idempotency-Key", idempotency)
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	if response := request(http.MethodGet, "/healthz", "", "", ""); response.StatusCode != http.StatusNoContent {
		t.Fatalf("health status=%d", response.StatusCode)
	}
	for _, path := range []string{"/v1/checkouts", "/v1/refunds"} {
		if response := request(http.MethodPost, path, "Bearer wrong", "x", `{}`); response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s unauthorized status=%d", path, response.StatusCode)
		}
		if response := request(http.MethodPost, path, "Bearer story-payment-key", "x", `{`); response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s malformed status=%d", path, response.StatusCode)
		}
	}
	if response := request(http.MethodPost, "/v1/checkouts", "Bearer story-payment-key", "wrong", `{"topup_id":"topup"}`); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("checkout idempotency status=%d", response.StatusCode)
	}
	if response := request(http.MethodPost, "/v1/refunds", "Bearer story-payment-key", "wrong", `{"refund_id":"refund"}`); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("refund idempotency status=%d", response.StatusCode)
	}
	if response := request(http.MethodPost, "/v1/test/confirm", "", "", `{`); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("confirm malformed status=%d", response.StatusCode)
	}
	if response := request(http.MethodPost, "/v1/test/confirm", "", "", `{"callback_url":"://bad","event_id":"event"}`); response.StatusCode != http.StatusBadGateway {
		t.Fatalf("confirm unreachable callback status=%d", response.StatusCode)
	}
	if response := request(http.MethodPost, "/merchant-webhook", "", "", `{`); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("webhook malformed status=%d", response.StatusCode)
	}
	if response := request(http.MethodGet, "/events", "", "", ""); response.StatusCode != http.StatusOK {
		t.Fatalf("events status=%d", response.StatusCode)
	}
}
