// Package creditspy provides a transparent counting reverse proxy used only by
// Milestone 02 black-box tests. It observes whether GizWay contacted GizPay;
// it never interprets or changes business payloads.
package creditspy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
)

type Counters struct {
	Total        int64 `json:"total_requests"`
	CreditChecks int64 `json:"credit_check_requests"`
	Charges      int64 `json:"charge_requests"`
	ChargeGets   int64 `json:"charge_get_requests"`
}

type Spy struct {
	proxy         *httputil.ReverseProxy
	upstream      *url.URL
	client        *http.Client
	total         atomic.Int64
	creditChecks  atomic.Int64
	charges       atomic.Int64
	chargeGets    atomic.Int64
	forceConflict atomic.Bool
	failChargeGet atomic.Bool
	dropCharge    atomic.Bool
}

func New(upstream *url.URL, transport http.RoundTripper) *Spy {
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	if transport == nil {
		transport = http.DefaultTransport
	}
	proxy.Transport = transport
	return &Spy{proxy: proxy, upstream: upstream, client: &http.Client{Transport: transport}}
}

func (s *Spy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/test/stats":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Counters{
			Total: s.total.Load(), CreditChecks: s.creditChecks.Load(), Charges: s.charges.Load(), ChargeGets: s.chargeGets.Load(),
		})
		return
	case r.Method == http.MethodPost && r.URL.Path == "/test/reset":
		s.total.Store(0)
		s.creditChecks.Store(0)
		s.charges.Store(0)
		s.chargeGets.Store(0)
		s.forceConflict.Store(false)
		s.failChargeGet.Store(false)
		s.dropCharge.Store(false)
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/test/force-charge-conflict":
		s.forceConflict.Store(true)
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method == http.MethodDelete && r.URL.Path == "/test/force-charge-conflict":
		s.forceConflict.Store(false)
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/test/fail-charge-get":
		s.failChargeGet.Store(true)
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method == http.MethodDelete && r.URL.Path == "/test/fail-charge-get":
		s.failChargeGet.Store(false)
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/test/drop-next-charge-response":
		s.dropCharge.Store(true)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	s.total.Add(1)
	if r.URL.Path == "/service/v1/subscription-credit-checks" {
		s.creditChecks.Add(1)
	}
	if r.URL.Path == "/service/v1/payg-charges" {
		s.charges.Add(1)
		if s.dropCharge.CompareAndSwap(true, false) {
			s.forwardAndDropResponse(w, r)
			return
		}
		if s.forceConflict.Load() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"duplicate_external_order_id"}}`))
			return
		}
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/service/v1/payg-charges/") {
		s.chargeGets.Add(1)
		if s.failChargeGet.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if s.forceConflict.Load() {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"charge_id": "conflicting-charge"})
			return
		}
	}
	s.proxy.ServeHTTP(w, r)
}

func (s *Spy) forwardAndDropResponse(w http.ResponseWriter, r *http.Request) {
	outbound := r.Clone(r.Context())
	outbound.URL.Scheme = s.upstream.Scheme
	outbound.URL.Host = s.upstream.Host
	outbound.URL.Path = strings.TrimRight(s.upstream.Path, "/") + "/" + strings.TrimLeft(r.URL.Path, "/")
	outbound.Host = s.upstream.Host
	outbound.RequestURI = ""
	response, err := s.client.Do(outbound)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	connection, _, err := hijacker.Hijack()
	if err == nil {
		_ = connection.Close()
	}
}
