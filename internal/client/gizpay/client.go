// Package gizpay implements the only control-plane boundary available to a
// regional GizWay process.
package gizpay

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/idy/gizway/internal/service/quotaexchange"
)

const exchangeTimeout = 3 * time.Second
const maximumExchangeBody = 256 << 10

var (
	ErrInvalidAPIKey          = errors.New("GizPay rejected customer API key")
	ErrInvalidNodeIdentity    = errors.New("GizPay rejected Gateway node identity")
	ErrInvalidExchangePayload = errors.New("GizPay rejected quota exchange payload")
	ErrUsageConflict          = errors.New("GizPay rejected conflicting UCGID")
	ErrUsageUnpriceable       = errors.New("GizPay could not price Usage")
	ErrTemporarilyUnavailable = errors.New("GizPay is temporarily unavailable")
)

// IsPermanentExchangeError identifies only responses that prove retrying the
// same request cannot succeed. Unknown errors are deliberately not permanent:
// callers may also surface local database or runtime failures through the same
// control flow, and those must not discard the in-memory customer context.
func IsPermanentExchangeError(err error) bool {
	return errors.Is(err, ErrInvalidAPIKey) ||
		errors.Is(err, ErrInvalidNodeIdentity) ||
		errors.Is(err, ErrInvalidExchangePayload) ||
		errors.Is(err, ErrUsageConflict) ||
		errors.Is(err, ErrUsageUnpriceable)
}

type CreditAmount struct {
	Asset        string `json:"asset"`
	Microcredits int64  `json:"microcredits"`
}

type ExchangeResponse struct {
	Status              string       `json:"status"`
	Quota               CreditAmount `json:"quota"`
	CheckedAt           string       `json:"checked_at"`
	RecheckAfterSeconds int          `json:"recheck_after_seconds"`
}

type PublishedPrice struct {
	ModelVariantID            string `json:"model_variant_id"`
	PublicModel               string `json:"public_model"`
	Metric                    string `json:"metric"`
	UnitSize                  int64  `json:"unit_size"`
	BasePriceMicrocredits     int64  `json:"base_price_microcredits"`
	CustomerPriceMicrocredits int64  `json:"customer_price_microcredits"`
	DiscountBPS               int    `json:"discount_bps"`
}

type RatePublicationResponse struct {
	ID                  string `json:"id"`
	SourcePublicationID string `json:"source_publication_id"`
	Revision            int64  `json:"revision"`
	Status              string `json:"status"`
	EffectiveAt         string `json:"effective_at"`
	ContentSHA256       string `json:"content_sha256"`
	CreatedAt           string `json:"created_at"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewMTLS builds the production transport from one node certificate and the
// GizPay server CA. Certificate rotation is performed by constructing a new
// client after deployment updates the files; no node identity enters JSON.
func NewMTLS(baseURL, certificateFile, privateKeyFile, serverCAFile string) (*Client, error) {
	certificate, err := tls.LoadX509KeyPair(certificateFile, privateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load GizPay client certificate: %w", err)
	}
	serverCAPEM, err := os.ReadFile(serverCAFile)
	if err != nil {
		return nil, fmt.Errorf("read GizPay server CA: %w", err)
	}
	serverCAs := x509.NewCertPool()
	if !serverCAs.AppendCertsFromPEM(serverCAPEM) {
		return nil, errors.New("GizPay server CA file contains no certificates")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		RootCAs:      serverCAs,
	}}
	return New(baseURL, &http.Client{Transport: transport})
}

// New requires an HTTPS endpoint. The supplied HTTP client must own the
// deployment's trusted CA and node client certificate; keeping TLS material in
// transport configuration prevents business requests from carrying node IDs.
func New(baseURL string, httpClient *http.Client) (*Client, error) {
	return newClient(baseURL, httpClient, false)
}

// NewForTest permits loopback HTTP only for process-local contract tests.
func NewForTest(baseURL string, httpClient *http.Client) *Client {
	client, err := newClient(baseURL, httpClient, true)
	if err != nil {
		panic(err)
	}
	return client
}

func newClient(baseURL string, httpClient *http.Client, allowHTTP bool) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http")) {
		return nil, errors.New("GizPay base URL must be absolute HTTPS")
	}
	if httpClient == nil {
		return nil, errors.New("GizPay HTTP client is required")
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), httpClient: httpClient}, nil
}

func (c *Client) Exchange(ctx context.Context, rawAPIKey string, usage []quotaexchange.UsageRecord) (ExchangeResponse, error) {
	requestBody := struct {
		APIKey string                      `json:"api_key"`
		Usage  []quotaexchange.UsageRecord `json:"usage,omitempty"`
	}{APIKey: rawAPIKey, Usage: usage}
	encoded, err := json.Marshal(requestBody)
	requestBody.APIKey = ""
	if err != nil {
		return ExchangeResponse{}, fmt.Errorf("%w: encode request", ErrInvalidExchangePayload)
	}
	// The HTTP request body necessarily contains the raw customer credential,
	// but the backing byte slice does not need to survive this call.
	defer clear(encoded)
	if len(encoded) > maximumExchangeBody {
		return ExchangeResponse{}, fmt.Errorf("%w: request exceeds 256 KiB", ErrInvalidExchangePayload)
	}
	requestContext, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, c.baseURL+"/internal/v1/quota/exchanges", bytes.NewReader(encoded))
	if err != nil {
		return ExchangeResponse{}, errors.New("create quota exchange request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return ExchangeResponse{}, fmt.Errorf("%w: quota exchange transport failed: %v", ErrTemporarilyUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// Never include an untrusted response body: intermediaries may echo the
		// secret-bearing request, and callers may log this error.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumExchangeBody))
		switch response.StatusCode {
		case http.StatusUnauthorized:
			if response.Header.Get("X-Gizway-Error-Code") == "invalid_node_identity" {
				return ExchangeResponse{}, ErrInvalidNodeIdentity
			}
			return ExchangeResponse{}, ErrInvalidAPIKey
		case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
			return ExchangeResponse{}, ErrInvalidExchangePayload
		case http.StatusConflict:
			return ExchangeResponse{}, ErrUsageConflict
		case http.StatusUnprocessableEntity:
			return ExchangeResponse{}, ErrUsageUnpriceable
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return ExchangeResponse{}, ErrTemporarilyUnavailable
		default:
			if response.StatusCode >= 500 {
				return ExchangeResponse{}, ErrTemporarilyUnavailable
			}
			return ExchangeResponse{}, fmt.Errorf("%w: HTTP %d", ErrInvalidExchangePayload, response.StatusCode)
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumExchangeBody+1))
	if err != nil || len(body) > maximumExchangeBody {
		return ExchangeResponse{}, fmt.Errorf("%w: decode response", ErrTemporarilyUnavailable)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var result ExchangeResponse
	if err := decoder.Decode(&result); err != nil {
		return ExchangeResponse{}, fmt.Errorf("%w: decode response", ErrTemporarilyUnavailable)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ExchangeResponse{}, fmt.Errorf("%w: decode response", ErrTemporarilyUnavailable)
	}
	if result.Quota.Asset != "GIZ_CREDIT" || result.Quota.Microcredits < 0 || result.RecheckAfterSeconds <= 0 ||
		(result.Status != "allowed" && result.Status != "denied") || (result.Status == "denied" && result.Quota.Microcredits != 0) {
		return ExchangeResponse{}, fmt.Errorf("%w: invalid response", ErrTemporarilyUnavailable)
	}
	return result, nil
}

func (c *Client) PublishRatePublication(ctx context.Context, sourceID string, revision int64, effectiveAt string, prices []PublishedPrice) (RatePublicationResponse, error) {
	encoded, err := json.Marshal(struct {
		SourcePublicationID string           `json:"source_publication_id"`
		Revision            int64            `json:"revision"`
		EffectiveAt         string           `json:"effective_at"`
		Prices              []PublishedPrice `json:"prices"`
	}{SourcePublicationID: sourceID, Revision: revision, EffectiveAt: effectiveAt, Prices: prices})
	if err != nil || len(encoded) > maximumExchangeBody {
		return RatePublicationResponse{}, errors.New("encode rate publication request")
	}
	requestContext, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, c.baseURL+"/internal/v1/rate-publications", bytes.NewReader(encoded))
	if err != nil {
		return RatePublicationResponse{}, errors.New("create rate publication request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return RatePublicationResponse{}, fmt.Errorf("rate publication transport failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumExchangeBody))
		if response.StatusCode == http.StatusUnauthorized {
			return RatePublicationResponse{}, ErrInvalidNodeIdentity
		}
		if response.StatusCode == http.StatusServiceUnavailable || response.StatusCode == http.StatusBadGateway || response.StatusCode == http.StatusGatewayTimeout {
			return RatePublicationResponse{}, ErrTemporarilyUnavailable
		}
		return RatePublicationResponse{}, fmt.Errorf("rate publication returned HTTP %d", response.StatusCode)
	}
	return decodeRatePublicationResponse(response.Body)
}

// GetRatePublication resolves an ambiguous POST result using the same source
// publication ID. It is deliberately a read, not a second publication command.
func (c *Client) GetRatePublication(ctx context.Context, id string) (RatePublicationResponse, error) {
	requestContext, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, c.baseURL+"/internal/v1/rate-publications/"+url.PathEscape(id), nil)
	if err != nil {
		return RatePublicationResponse{}, errors.New("create rate publication query")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return RatePublicationResponse{}, fmt.Errorf("rate publication query transport failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumExchangeBody))
		if response.StatusCode == http.StatusUnauthorized {
			return RatePublicationResponse{}, ErrInvalidNodeIdentity
		}
		if response.StatusCode == http.StatusServiceUnavailable || response.StatusCode == http.StatusBadGateway || response.StatusCode == http.StatusGatewayTimeout {
			return RatePublicationResponse{}, ErrTemporarilyUnavailable
		}
		return RatePublicationResponse{}, fmt.Errorf("rate publication query returned HTTP %d", response.StatusCode)
	}
	return decodeRatePublicationResponse(response.Body)
}

// CheckReadiness proves that the mounted client certificate is currently
// accepted by GizPay and that its internal dependencies are ready. Merely
// constructing a TLS client cannot establish either fact.
func (c *Client) CheckReadiness(ctx context.Context, expectedNodeID, expectedRegion string) error {
	requestContext, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, c.baseURL+"/internal/v1/readyz", nil)
	if err != nil {
		return errors.New("create GizPay readiness request")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("GizPay readiness transport failed: %w", err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maximumExchangeBody+1))
	if readErr != nil || len(body) > maximumExchangeBody {
		return ErrTemporarilyUnavailable
	}
	if response.StatusCode == http.StatusUnauthorized {
		return ErrInvalidNodeIdentity
	}
	if response.StatusCode != http.StatusOK {
		return ErrTemporarilyUnavailable
	}
	var result struct {
		Status string `json:"status"`
		NodeID string `json:"node_id"`
		Region string `json:"region"`
	}
	if json.Unmarshal(body, &result) != nil || result.Status != "ready" || result.NodeID == "" || result.Region == "" {
		return ErrTemporarilyUnavailable
	}
	// TLS proves which certificate called GizPay; this comparison proves that
	// deployment configuration mounted the certificate intended for this
	// regional process. A valid Global certificate must not make CN ready.
	if result.NodeID != expectedNodeID || result.Region != expectedRegion {
		return ErrInvalidNodeIdentity
	}
	return nil
}

func decodeRatePublicationResponse(reader io.Reader) (RatePublicationResponse, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maximumExchangeBody+1))
	if err != nil || len(body) > maximumExchangeBody {
		return RatePublicationResponse{}, errors.New("decode rate publication response")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var result RatePublicationResponse
	if err := decoder.Decode(&result); err != nil || result.ID == "" || result.SourcePublicationID == "" || result.Revision <= 0 ||
		result.EffectiveAt == "" || (result.Status != "active" && result.Status != "retired") || len(result.ContentSHA256) != 64 {
		return RatePublicationResponse{}, errors.New("decode rate publication response")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return RatePublicationResponse{}, errors.New("decode rate publication response")
	}
	return result, nil
}
