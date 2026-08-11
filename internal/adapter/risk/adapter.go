// Package risk owns the outbound compliance/risk provider HTTP boundary.
package risk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	credential string
	http       *http.Client
}

func New(baseURL, credential string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), credential: credential, http: &http.Client{Timeout: 10 * time.Second}}
}

type AssessmentRequest struct {
	AssessmentID      string   `json:"assessment_id"`
	MerchantAccountID string   `json:"merchant_account_id"`
	ServiceCode       string   `json:"service_code"`
	CountryCode       string   `json:"country_code"`
	WebsiteURL        string   `json:"website_url"`
	InterfaceSet      []string `json:"interface_set"`
}

type Assessment struct {
	ProviderReference          string `json:"provider_reference"`
	Decision                   string `json:"decision"`
	KYCStatus                  string `json:"kyc_status"`
	KYBStatus                  string `json:"kyb_status"`
	SanctionsStatus            string `json:"sanctions_status"`
	AnomalyScore               int    `json:"anomaly_score"`
	MaxTransactionMicrocredits int64  `json:"max_transaction_microcredits"`
	DailyLimitMicrocredits     int64  `json:"daily_limit_microcredits"`
	Reason                     string `json:"reason"`
}

func (c *Client) Assess(ctx context.Context, request AssessmentRequest) (Assessment, error) {
	var assessment Assessment
	body, err := json.Marshal(request)
	if err != nil {
		return assessment, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/assessments", bytes.NewReader(body))
	if err != nil {
		return assessment, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.credential)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", request.AssessmentID)
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return assessment, fmt.Errorf("risk provider request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return assessment, fmt.Errorf("risk provider status %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&assessment); err != nil {
		return assessment, fmt.Errorf("decode risk assessment: %w", err)
	}
	if assessment.ProviderReference == "" || !oneOf(assessment.Decision, "allow", "deny", "review") ||
		!oneOf(assessment.KYCStatus, "verified", "failed", "pending") ||
		!oneOf(assessment.KYBStatus, "verified", "failed", "pending") ||
		!oneOf(assessment.SanctionsStatus, "clear", "match", "pending") ||
		assessment.AnomalyScore < 0 || assessment.AnomalyScore > 100 ||
		assessment.MaxTransactionMicrocredits <= 0 || assessment.DailyLimitMicrocredits <= 0 {
		return Assessment{}, errors.New("risk provider returned an invalid assessment")
	}
	return assessment, nil
}

func oneOf(value string, candidates ...string) bool {
	return slices.Contains(candidates, value)
}
