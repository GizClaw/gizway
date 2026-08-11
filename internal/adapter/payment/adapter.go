// Package payment owns the outbound fiat-provider HTTP protocol. It never
// mutates Gizway balances; the service/store layer owns financial state.
package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	credential string
	providerID string
	http       *http.Client
}

func New(baseURL, credential string) *Client {
	return NewNamed(baseURL, credential, "payment-provider")
}

func NewNamed(baseURL, credential, providerID string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), credential: credential, providerID: providerID, http: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) ProviderID() string { return c.providerID }

type Checkout struct {
	ProviderReference string `json:"provider_reference"`
	CheckoutURL       string `json:"checkout_url"`
}

func (c *Client) CreateCheckout(ctx context.Context, topupID, currency string, amount int64) (Checkout, error) {
	var result Checkout
	err := c.post(ctx, "/v1/checkouts", topupID, map[string]any{"topup_id": topupID, "currency": currency, "amount_minor": amount}, &result)
	if err == nil {
		checkoutURL, parseErr := url.Parse(result.CheckoutURL)
		if result.ProviderReference == "" || parseErr != nil || checkoutURL.Host == "" || (checkoutURL.Scheme != "https" && checkoutURL.Scheme != "http") {
			err = errors.New("payment provider returned an invalid checkout reference")
		}
	}
	return result, err
}

type Refund struct {
	ProviderRefundID string `json:"provider_refund_id"`
	Status           string `json:"status"`
}

func (c *Client) Refund(ctx context.Context, providerReference, refundID, currency string, amount int64) (Refund, error) {
	var result Refund
	err := c.post(ctx, "/v1/refunds", refundID, map[string]any{
		"provider_reference": providerReference, "refund_id": refundID,
		"currency": currency, "amount_minor": amount,
	}, &result)
	if err == nil {
		switch result.Status {
		case "succeeded":
			if result.ProviderRefundID == "" {
				err = errors.New("payment provider returned an empty successful refund id")
			}
		case "failed", "pending":
		default:
			err = errors.New("payment provider returned an invalid refund status")
		}
	}
	return result, err
}

func (c *Client) post(ctx context.Context, path, idempotencyKey string, body any, result any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.credential)
	req.Header.Set("Content-Type", "application/json")
	// The stable Gizway resource ID is the provider idempotency key. Retrying
	// after an ambiguous network/commit failure therefore cannot create a
	// second checkout or original-route refund at a compliant provider.
	req.Header.Set("Idempotency-Key", idempotencyKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("payment provider request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("payment provider status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode payment provider response: %w", err)
	}
	return nil
}
