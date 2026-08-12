package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/timetext"
)

type ratePublicationRequest struct {
	SourcePublicationID string                 `json:"source_publication_id"`
	Revision            int64                  `json:"revision"`
	EffectiveAt         string                 `json:"effective_at"`
	Prices              []store.PublishedPrice `json:"prices"`
}

func (s *Server) publishRatePublication(w http.ResponseWriter, r *http.Request) {
	var request ratePublicationRequest
	if decodeJSON(r, &request) != nil || strings.TrimSpace(request.SourcePublicationID) == "" || len(request.SourcePublicationID) > 255 || request.Revision <= 0 || len(request.Prices) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "source publication, revision, effective time, and prices are required")
		return
	}
	effectiveAt, err := timetext.Normalize(request.EffectiveAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "publication effective_at is invalid")
		return
	}
	request.EffectiveAt = effectiveAt
	slices.SortFunc(request.Prices, func(left, right store.PublishedPrice) int {
		if compared := strings.Compare(left.ModelVariantID, right.ModelVariantID); compared != 0 {
			return compared
		}
		if compared := strings.Compare(left.PublicModel, right.PublicModel); compared != 0 {
			return compared
		}
		return strings.Compare(left.Metric, right.Metric)
	})
	for index, price := range request.Prices {
		if price.ModelVariantID == "" || price.PublicModel == "" || !publishedMetric(price.Metric) || price.UnitSize <= 0 ||
			price.BasePriceMicrocredits < 0 || price.CustomerPriceMicrocredits < 0 || price.CustomerPriceMicrocredits > price.BasePriceMicrocredits ||
			price.DiscountBPS < 0 || price.DiscountBPS > 10000 {
			writeError(w, http.StatusBadRequest, "invalid_request", "publication contains an invalid price")
			return
		}
		if index > 0 && price.ModelVariantID == request.Prices[index-1].ModelVariantID && price.Metric == request.Prices[index-1].Metric {
			writeError(w, http.StatusBadRequest, "invalid_request", "publication contains a duplicate metric price")
			return
		}
	}
	// The source publication ID is the idempotency identity, while the digest
	// covers only the immutable price snapshot. This lets GizWay compare the
	// GizPay receipt with the exact local snapshot before activating it.
	canonical, _ := json.Marshal(request.Prices)
	digest := sha256.Sum256(canonical)
	publication, replayed, err := s.store.PublishRatePublication(r.Context(), contextString(r.Context(), gatewayNodeIDKey), contextString(r.Context(), gatewayRegionKey),
		request.SourcePublicationID, request.Revision, request.EffectiveAt, digest[:], request.Prices)
	if errors.Is(err, store.ErrIdempotencyConflict) {
		writeError(w, http.StatusConflict, "publication_conflict", "publication ID already has different content")
		return
	}
	if err != nil {
		log.Printf("commit rate publication: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "rate publication could not be committed")
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeRatePublication(w, status, publication)
}

func (s *Server) getRatePublication(w http.ResponseWriter, r *http.Request) {
	publication, err := s.store.GetRatePublication(r.Context(), contextString(r.Context(), gatewayNodeIDKey), r.PathValue("source_publication_id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "rate publication was not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "rate publication could not be read")
		return
	}
	writeRatePublication(w, http.StatusOK, publication)
}

func writeRatePublication(w http.ResponseWriter, status int, publication store.RatePublication) {
	writeJSON(w, status, map[string]any{
		"id":                    publication.ID,
		"source_publication_id": publication.SourcePublicationID,
		"revision":              publication.Revision,
		"status":                publication.Status,
		"effective_at":          publication.EffectiveAt,
		"content_sha256":        hex.EncodeToString(publication.PayloadHash),
		"created_at":            publication.CreatedAt,
	})
}

func publishedMetric(metric string) bool {
	return slices.Contains([]string{"input_token", "output_token", "cached_input_token", "input_audio_token", "output_audio_token", "audio_second", "image", "video_second", "request"}, metric)
}
