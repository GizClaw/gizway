package riskprovider

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFixtureAuthenticationValidationAndProviderIdempotency(t *testing.T) {
	server := httptest.NewServer(Handler("risk-secret"))
	defer server.Close()

	request := func(path, authorization, idempotencyKey, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	assertStatus := func(response *http.Response, want int) {
		t.Helper()
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("status=%d want=%d", response.StatusCode, want)
		}
	}

	health, err := server.Client().Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(health, http.StatusNoContent)
	assertStatus(request("/v1/assessments", "Bearer wrong", "bad", `{}`), http.StatusUnauthorized)
	assertStatus(request("/v1/assessments", "Bearer risk-secret", "bad", `{`), http.StatusBadRequest)
	assertStatus(request("/v1/assessments", "Bearer risk-secret", "wrong", `{"assessment_id":"allow"}`), http.StatusBadRequest)

	for _, test := range []struct {
		id, serviceCode, decision string
	}{
		{"allow", "vpn", "allow"},
		// Reusing the same provider idempotency key is a request, but it must
		// not create a second unique assessment.
		{"allow", "vpn", "allow"},
		{"blocked", "blocked", "deny"},
		{"review", "review", "review"},
	} {
		body, err := json.Marshal(map[string]string{"assessment_id": test.id, "service_code": test.serviceCode})
		if err != nil {
			t.Fatal(err)
		}
		response := request("/v1/assessments", "Bearer risk-secret", test.id, string(body))
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			t.Fatalf("assessment %s status=%d", test.id, response.StatusCode)
		}
		var payload struct {
			Decision string `json:"decision"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			response.Body.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		if payload.Decision != test.decision {
			t.Fatalf("assessment %s decision=%q want=%q", test.id, payload.Decision, test.decision)
		}
	}

	events, err := server.Client().Get(server.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	var counts struct {
		Requests    int64 `json:"requests"`
		Assessments int64 `json:"assessments"`
	}
	if err := json.NewDecoder(events.Body).Decode(&counts); err != nil {
		t.Fatal(err)
	}
	if counts.Requests != 6 || counts.Assessments != 3 {
		t.Fatalf("counts=%+v", counts)
	}
}
