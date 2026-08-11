package risk

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/idy/gizway/internal/testfake/riskprovider"
)

func TestAssessValidatesExternalRiskProtocol(t *testing.T) {
	provider := httptest.NewServer(riskprovider.Handler("risk-key"))
	defer provider.Close()
	client := New(provider.URL+"/", "risk-key")

	allowed, err := client.Assess(t.Context(), AssessmentRequest{
		AssessmentID: "assessment-1", MerchantAccountID: "merchant-1",
		ServiceCode: "vpn", InterfaceSet: []string{"checkout", "webhook"},
	})
	if err != nil || allowed.Decision != "allow" || allowed.SanctionsStatus != "clear" {
		t.Fatalf("allow assessment = %+v, %v", allowed, err)
	}
	// The fixture and adapter both require the stable Gizway assessment ID as
	// the provider idempotency key. Replays do not increment external work.
	if _, err := client.Assess(t.Context(), AssessmentRequest{AssessmentID: "assessment-1", MerchantAccountID: "merchant-1", ServiceCode: "vpn", InterfaceSet: []string{"checkout"}}); err != nil {
		t.Fatalf("idempotent assessment replay: %v", err)
	}

	denied, err := client.Assess(t.Context(), AssessmentRequest{AssessmentID: "assessment-2", MerchantAccountID: "merchant-1", ServiceCode: "blocked", InterfaceSet: []string{"checkout"}})
	if err != nil || denied.Decision != "deny" || denied.SanctionsStatus != "match" {
		t.Fatalf("deny assessment = %+v, %v", denied, err)
	}
	review, err := client.Assess(t.Context(), AssessmentRequest{AssessmentID: "assessment-3", MerchantAccountID: "merchant-1", ServiceCode: "review", InterfaceSet: []string{"checkout"}})
	if err != nil || review.Decision != "review" {
		t.Fatalf("review assessment = %+v, %v", review, err)
	}

	unauthorized := New(provider.URL, "wrong-key")
	if _, err := unauthorized.Assess(t.Context(), AssessmentRequest{AssessmentID: "assessment-4"}); err == nil {
		t.Fatal("assessment with wrong credential succeeded")
	}
}

func TestAssessRejectsInvalidProviderResponses(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{`},
		{name: "missing reference", body: `{"decision":"allow"}`},
		{name: "bad enum", body: `{"provider_reference":"r","decision":"maybe","kyc_status":"verified","kyb_status":"verified","sanctions_status":"clear","anomaly_score":1,"max_transaction_microcredits":1,"daily_limit_microcredits":1}`},
		{name: "bad score", body: `{"provider_reference":"r","decision":"allow","kyc_status":"verified","kyb_status":"verified","sanctions_status":"clear","anomaly_score":101,"max_transaction_microcredits":1,"daily_limit_microcredits":1}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			if _, err := New(server.URL, "key").Assess(t.Context(), AssessmentRequest{AssessmentID: "id"}); err == nil {
				t.Fatal("invalid provider response succeeded")
			}
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer server.Close()
	if _, err := New(server.URL, "key").Assess(t.Context(), AssessmentRequest{AssessmentID: "id"}); err == nil {
		t.Fatal("provider error status succeeded")
	}
}
