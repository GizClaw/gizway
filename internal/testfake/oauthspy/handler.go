// Package oauthspy provides a transparent ZITADEL token-endpoint observer for
// Milestone 03 E2E. It records only decoded JWT metadata and never exposes the
// client assertion itself.
package oauthspy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
)

type Snapshot struct {
	TotalRequests       int64          `json:"total_requests"`
	TokenRequests       int64          `json:"token_requests"`
	GrantType           string         `json:"grant_type,omitempty"`
	ClientAssertionType string         `json:"client_assertion_type,omitempty"`
	Scope               string         `json:"scope,omitempty"`
	AssertionHeader     map[string]any `json:"assertion_header,omitempty"`
	AssertionClaims     map[string]any `json:"assertion_claims,omitempty"`
}

type Spy struct {
	proxy *httputil.ReverseProxy
	mu    sync.RWMutex
	state Snapshot
}

func New(upstream *url.URL, transport http.RoundTripper) *Spy {
	proxy := &httputil.ReverseProxy{Rewrite: func(request *httputil.ProxyRequest) {
		request.SetURL(upstream)
		request.Out.Host = upstream.Host
	}}
	if transport != nil {
		proxy.Transport = transport
	}
	return &Spy{proxy: proxy}
}

func (s *Spy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/test/stats":
		s.mu.RLock()
		state := s.state
		s.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/test/reset":
		s.mu.Lock()
		s.state = Snapshot{}
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}

	isTokenRequest := r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/token")
	var form url.Values
	var header map[string]any
	var claims map[string]any
	if isTokenRequest {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "cannot observe token request", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		form, _ = url.ParseQuery(string(raw))
		assertion := form.Get("client_assertion")
		if assertion == "" {
			assertion = form.Get("assertion")
		}
		header, claims = decodeAssertion(assertion)
	}
	s.mu.Lock()
	s.state.TotalRequests++
	if isTokenRequest {
		s.state.TokenRequests++
		s.state.GrantType = form.Get("grant_type")
		s.state.ClientAssertionType = form.Get("client_assertion_type")
		s.state.Scope = form.Get("scope")
		s.state.AssertionHeader = header
		s.state.AssertionClaims = claims
	}
	s.mu.Unlock()
	s.proxy.ServeHTTP(w, r)
}

func decodeAssertion(assertion string) (map[string]any, map[string]any) {
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		return nil, nil
	}
	decode := func(part string) map[string]any {
		raw, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil {
			return nil
		}
		var value map[string]any
		if json.Unmarshal(raw, &value) != nil {
			return nil
		}
		return value
	}
	return decode(parts[0]), decode(parts[1])
}
