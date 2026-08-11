// Package riskprovider implements the deterministic external risk fixture.
package riskprovider

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
)

func Handler(credential string) http.Handler {
	mux := http.NewServeMux()
	var assessments atomic.Int64
	var requests atomic.Int64
	var keys sync.Map
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			// requests counts every authorized provider call. assessments only
			// counts unique provider-side idempotency keys. Keeping both makes
			// Hurl able to distinguish a true Gizway replay (no provider call)
			// from a duplicate call hidden by the fake provider's own replay.
			"requests":    requests.Load(),
			"assessments": assessments.Load(),
		})
	})
	mux.HandleFunc("POST /v1/assessments", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+credential {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		requests.Add(1)
		var request struct {
			AssessmentID string `json:"assessment_id"`
			ServiceCode  string `json:"service_code"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.AssessmentID == "" || r.Header.Get("Idempotency-Key") != request.AssessmentID {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		if _, loaded := keys.LoadOrStore(request.AssessmentID, struct{}{}); !loaded {
			assessments.Add(1)
		}
		result := map[string]any{
			"provider_reference": "risk_" + request.AssessmentID,
			"decision":           "allow", "kyc_status": "verified", "kyb_status": "verified",
			"sanctions_status": "clear", "anomaly_score": 5,
			"max_transaction_microcredits": 10_000_000,
			"daily_limit_microcredits":     50_000_000,
			"reason":                       "deterministic checks passed",
		}
		if request.ServiceCode == "blocked" {
			result["decision"] = "deny"
			result["sanctions_status"] = "match"
			result["reason"] = "deterministic sanctions match"
		} else if request.ServiceCode == "review" {
			result["decision"] = "review"
			result["reason"] = "manual review required"
		}
		_ = json.NewEncoder(w).Encode(result)
	})
	return mux
}
