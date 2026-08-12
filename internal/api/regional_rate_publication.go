package api

import (
	"encoding/hex"
	"errors"
	"net/http"

	gizpayclient "github.com/idy/gizway/internal/client/gizpay"
	"github.com/idy/gizway/internal/store"
)

func (s *Server) publishRegionalRatePublication(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	if s.publishRegionalRates == nil || s.region == "" {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "regional rate publication is not configured")
		return
	}
	var request struct {
		SourcePublicationID string `json:"source_publication_id"`
		EffectiveAt         string `json:"effective_at"`
	}
	if decodeJSON(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "effective_at is required")
		return
	}
	publication, err := s.store.PrepareRegionalRatePublication(r.Context(), s.region, request.SourcePublicationID, request.EffectiveAt)
	if err != nil {
		if errors.Is(err, store.ErrIdempotencyConflict) {
			writeError(w, http.StatusConflict, "publication_conflict", "source publication ID already has different content")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	gizPayID, contentSHA256, err := s.publishRegionalRates(r.Context(), publication.ID, publication.Revision, *publication.EffectiveAt, publication.Prices)
	if err != nil {
		// An HTTP failure can occur after GizPay committed the immutable
		// publication. Keep the local row in publishing state so the same source
		// ID and snapshot can be queried/retried; manufacturing a new publication
		// here could create two financial versions for one operator action.
		switch {
		case errors.Is(err, gizpayclient.ErrInvalidNodeIdentity):
			writeError(w, http.StatusUnauthorized, "invalid_node_identity", "Gateway node certificate is not authorized")
		case errors.Is(err, gizpayclient.ErrTemporarilyUnavailable):
			writeError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "GizPay is temporarily unavailable")
		default:
			writeError(w, http.StatusBadGateway, "publication_failed", "GizPay rejected the publication")
		}
		return
	}
	if contentSHA256 != hex.EncodeToString(publication.ContentHash) {
		writeError(w, http.StatusBadGateway, "publication_mismatch", "GizPay confirmed a different publication snapshot")
		return
	}
	active, err := s.store.ActivateRegionalRatePublication(r.Context(), publication.ID, gizPayID)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, active)
}
