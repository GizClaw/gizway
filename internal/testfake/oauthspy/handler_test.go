package oauthspy

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSpyObservesPrivateKeyJWTMetadataAndForwardsTheOriginalBody(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"key-1"}`))
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"machine","sub":"machine","aud":"https://auth.test/token"}`))
	assertion := header + "." + claims + ".signature"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("client_assertion") != assertion {
			t.Error("proxy changed client assertion")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"short-lived","token_type":"Bearer"}`))
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(target, nil))
	defer server.Close()

	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {assertion},
	}
	response, err := server.Client().Post(server.URL+"/oauth/v2/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	stats, err := server.Client().Get(server.URL + "/test/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer stats.Body.Close()
	var snapshot Snapshot
	if err := json.NewDecoder(stats.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.TotalRequests != 1 || snapshot.TokenRequests != 1 || snapshot.AssertionHeader["kid"] != "key-1" || snapshot.AssertionClaims["sub"] != "machine" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestSpyCountsEveryNonTestRequestIncludingJWKS(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(target, nil))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/oauth/v2/keys")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	stats, err := server.Client().Get(server.URL + "/test/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer stats.Body.Close()
	var snapshot Snapshot
	if err := json.NewDecoder(stats.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.TotalRequests != 1 || snapshot.TokenRequests != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}
