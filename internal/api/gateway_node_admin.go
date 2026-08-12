package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/idy/gizway/internal/store"
)

func (s *Server) createGatewayNode(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID     string `json:"id"`
		Region string `json:"region"`
		Name   string `json:"name"`
	}
	if decodeJSON(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "node id, region, and name are required")
		return
	}
	node, replayed, err := s.store.CreateGatewayNode(r.Context(), request.ID, request.Region, request.Name, s.nowText())
	if errors.Is(err, store.ErrIdempotencyConflict) {
		writeError(w, http.StatusConflict, "node_conflict", "node ID already has different configuration")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, node)
}

func (s *Server) getGatewayNode(w http.ResponseWriter, r *http.Request) {
	node, err := s.store.GetGatewayNode(r.Context(), r.PathValue("node_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, node)
}

func (s *Server) registerGatewayNodeCertificate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Fingerprint string `json:"fingerprint_sha256"`
		Serial      string `json:"serial_number"`
		SubjectDN   string `json:"subject_dn"`
		SANURI      string `json:"san_uri"`
		NotBefore   string `json:"not_before"`
		NotAfter    string `json:"not_after"`
	}
	if decodeJSON(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "certificate metadata is required")
		return
	}
	certificate, replayed, err := s.store.RegisterGatewayNodeCertificate(r.Context(), r.PathValue("node_id"),
		strings.ToLower(request.Fingerprint), request.Serial, request.SubjectDN, request.SANURI,
		request.NotBefore, request.NotAfter, s.nowText())
	if errors.Is(err, store.ErrIdempotencyConflict) {
		writeError(w, http.StatusConflict, "certificate_conflict", "certificate fingerprint already has different metadata")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, certificate)
}

func (s *Server) activateGatewayNodeCertificate(w http.ResponseWriter, r *http.Request) {
	certificate, err := s.store.ActivateGatewayNodeCertificate(r.Context(), r.PathValue("node_id"), r.PathValue("certificate_id"), s.nowText())
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, certificate)
}

func (s *Server) revokeGatewayNodeCertificate(w http.ResponseWriter, r *http.Request) {
	certificate, err := s.store.RevokeGatewayNodeCertificate(r.Context(), r.PathValue("node_id"), r.PathValue("certificate_id"), s.nowText())
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, certificate)
}
