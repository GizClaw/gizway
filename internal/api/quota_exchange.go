package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/idy/gizway/internal/service/quotaexchange"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/timetext"
)

const (
	maximumQuotaExchangeBody = 256 << 10
	maximumUsageBatch        = 200
)

type quotaExchangeRequest struct {
	APIKey string                      `json:"api_key"`
	Usage  []quotaexchange.UsageRecord `json:"usage,omitempty"`
}

// exchangeQuota treats Usage as optional input and current quota as mandatory
// output. Successful calls always answer with the balance calculated after the
// supplied Usage transaction; an empty batch performs no financial mutation.
func (s *Server) exchangeQuota(w http.ResponseWriter, r *http.Request) {
	var request quotaExchangeRequest
	if err := decodeQuotaExchangeJSON(r, &request); err != nil || request.APIKey == "" || len(request.Usage) > maximumUsageBatch {
		writeError(w, http.StatusBadRequest, "invalid_request", "request does not match the Quota Exchange contract")
		return
	}
	for index := range request.Usage {
		if err := normalizeAndValidateUsage(&request.Usage[index]); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "usage does not match the Quota Exchange contract")
			return
		}
	}

	result, err := s.store.ExchangeQuota(
		r.Context(),
		request.APIKey,
		contextString(r.Context(), gatewayNodeIDKey),
		contextString(r.Context(), gatewayRegionKey),
		request.Usage,
	)
	// Drop the only in-process reference held by the decoded request before
	// constructing any response. Go cannot guarantee immediate memory erasure,
	// but no error or response path receives the secret value.
	request.APIKey = ""
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "invalid_api_key", "API key is invalid or inactive")
		return
	}
	if errors.Is(err, store.ErrUCGIDConflict) {
		writeError(w, http.StatusConflict, "ucgid_conflict", "UCGID conflicts with previously received usage")
		return
	}
	if errors.Is(err, store.ErrUnpriceableUsage) {
		writeError(w, http.StatusUnprocessableEntity, "unpriceable_usage", "usage cannot be priced from the referenced publication")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	status := "denied"
	recheckSeconds := s.deniedRecheckSeconds
	if result.Allowed {
		status = "allowed"
		recheckSeconds = s.quotaRecheckSeconds
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": status,
		"quota": map[string]any{
			"asset":        "GIZ_CREDIT",
			"microcredits": result.QuotaMicrocredits,
		},
		"checked_at":            timetext.Format(s.now()),
		"recheck_after_seconds": recheckSeconds,
	})
}

func decodeQuotaExchangeJSON(r *http.Request, destination any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maximumQuotaExchangeBody+1))
	if err != nil {
		return fmt.Errorf("read JSON: %w", err)
	}
	if len(body) > maximumQuotaExchangeBody {
		return errors.New("request body exceeds 256 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON value")
	}
	return nil
}

func normalizeAndValidateUsage(record *quotaexchange.UsageRecord) error {
	if record == nil || strings.TrimSpace(record.UCGID) == "" || strings.TrimSpace(record.OperationID) == "" ||
		strings.TrimSpace(record.PublicModel) == "" || strings.TrimSpace(record.ModelVariantID) == "" ||
		strings.TrimSpace(record.RatePublicationID) == "" || len(record.Metrics) == 0 {
		return errors.New("missing required usage field")
	}
	if len(record.UCGID) > 255 || len(record.OperationID) > 255 || len(record.PublicModel) > 255 ||
		len(record.ModelVariantID) > 255 || len(record.RatePublicationID) > 255 {
		return errors.New("usage field exceeds maximum length")
	}
	for metric, quantity := range record.Metrics {
		if strings.TrimSpace(metric) == "" || quantity < 0 {
			return errors.New("invalid usage metric")
		}
	}
	startedAt, err := time.Parse(time.RFC3339Nano, record.StartedAt)
	if err != nil {
		return errors.New("invalid usage start time")
	}
	completedAt, err := time.Parse(time.RFC3339Nano, record.CompletedAt)
	if err != nil || completedAt.Before(startedAt) {
		return errors.New("invalid usage completion time")
	}
	record.StartedAt = timetext.Format(startedAt)
	record.CompletedAt = timetext.Format(completedAt)
	return nil
}

// requireGatewayNode binds the internal caller identity to the TLS transport,
// never to request JSON. Tests use the same verified client certificate path
// as production; there is no story-only node identity bypass.
func (s *Server) requireGatewayNode(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
			writeError(w, http.StatusUnauthorized, "invalid_node_identity", "verified Gateway node certificate is required")
			return
		}
		identity, err := s.store.AuthenticateGatewayNode(r.Context(), r.TLS.PeerCertificates[0])
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_node_identity", "Gateway node certificate identity is invalid")
			return
		}
		ctx := context.WithValue(r.Context(), gatewayRegionKey, identity.Region)
		ctx = context.WithValue(ctx, gatewayNodeIDKey, identity.NodeID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
