package creditspy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSpyCountsWithoutChangingRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer machine" {
			t.Errorf("authorization was changed: %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(target, nil))
	defer server.Close()

	for _, path := range []string{"/service/v1/subscription-credit-checks", "/service/v1/payg-charges", "/other"} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+path, strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer machine")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("%s status=%d", path, response.StatusCode)
		}
	}

	response, err := server.Client().Get(server.URL + "/test/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var counters Counters
	if json.NewDecoder(response.Body).Decode(&counters) != nil || counters.Total != 3 || counters.CreditChecks != 1 || counters.Charges != 1 {
		t.Fatalf("counters=%+v", counters)
	}
}

func TestSpyCanExposeARealDuplicateConflictWithoutServingTheOriginalCharge(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("forced conflict request unexpectedly reached upstream")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(target, nil))
	defer server.Close()

	request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/test/force-charge-conflict", nil)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	request, _ = http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/service/v1/payg-charges", strings.NewReader(`{}`))
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("forced Charge status=%d", response.StatusCode)
	}

	response, err = server.Client().Get(server.URL + "/service/v1/payg-charges/order-collision")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("forced Charge GET status=%d", response.StatusCode)
	}
}

func TestSpyCanTemporarilyFailChargeRecoveryGET(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(target, nil))
	defer server.Close()

	request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/test/fail-charge-get", nil)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	response, err = server.Client().Get(server.URL + "/service/v1/payg-charges/order-recovery")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("forced Charge GET status=%d", response.StatusCode)
	}

	request, _ = http.NewRequestWithContext(t.Context(), http.MethodDelete, server.URL+"/test/fail-charge-get", nil)
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response, err = server.Client().Get(server.URL + "/service/v1/payg-charges/order-recovery")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("restored Charge GET status=%d", response.StatusCode)
	}
}

func TestSpyCanCommitChargeAndDropItsResponse(t *testing.T) {
	var upstreamCharges atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/service/v1/payg-charges" {
			t.Errorf("upstream path=%q", r.URL.Path)
		}
		upstreamCharges.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"charge_id":"committed"}`))
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(target, nil))
	defer server.Close()

	request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/test/drop-next-charge-response", nil)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	request, _ = http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/service/v1/payg-charges", strings.NewReader(`{}`))
	response, err = server.Client().Do(request)
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("dropped Charge response unexpectedly reached the client")
	}
	if upstreamCharges.Load() != 1 {
		t.Fatalf("upstream Charges=%d, want committed once", upstreamCharges.Load())
	}
}
