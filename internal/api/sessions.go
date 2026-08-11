package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/timetext"
)

const interactiveSessionTTL = 8 * time.Hour

func sessionResponse(subjectField string, subject any, secret, expiresAt string) map[string]any {
	return map[string]any{
		subjectField: subject, "access_token": secret, "token_type": "Bearer",
		"expires_at": expiresAt,
		// Sessions are presented only in Authorization, never ambient cookies.
		// Browser callers therefore do not need a separate CSRF token.
		"csrf_protection": "not_required_bearer_header",
	}
}

func (s *Server) loginUser(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &request); err != nil || request.Email == "" || request.Password == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "email and password are required")
		return
	}
	if !s.consumeRateLimit(w, r, authenticationRateLimitScope(request.Email), "user.login", 20, 5) {
		return
	}
	secret, hash, err := randomSecret("gizusr_")
	if err != nil {
		writeDataError(w, err)
		return
	}
	created := s.now().UTC()
	expiresAt := timetext.Format(created.Add(interactiveSessionTTL))
	user, err := s.store.LoginUser(r.Context(), request.Email, request.Password, uuid.NewString(), hash, timetext.Format(created), expiresAt)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
		} else {
			writeDataError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse("user", user, secret, expiresAt))
}

func (s *Server) refreshUserSession(w http.ResponseWriter, r *http.Request) {
	oldSecret, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "user bearer session is required")
		return
	}
	secret, hash, err := randomSecret("gizusr_")
	if err != nil {
		writeDataError(w, err)
		return
	}
	now := s.now().UTC()
	expiresAt := timetext.Format(now.Add(interactiveSessionTTL))
	userID, err := s.store.RefreshUserSession(r.Context(), oldSecret, uuid.NewString(), hash, timetext.Format(now), expiresAt)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "user session cannot be refreshed")
		return
	}
	user, err := s.store.GetUser(r.Context(), userID)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse("user", user, secret, expiresAt))
}

func (s *Server) logoutUser(w http.ResponseWriter, r *http.Request) {
	secret, ok := bearerToken(r)
	if !ok || s.store.RevokeUserSession(r.Context(), secret, timetext.Format(s.now())) != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "user session is not active")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) refreshAdministratorSession(w http.ResponseWriter, r *http.Request) {
	oldSecret, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "administrator bearer session is required")
		return
	}
	secret, hash, err := randomSecret("gizadms_")
	if err != nil {
		writeDataError(w, err)
		return
	}
	now := s.now().UTC()
	expiresAt := timetext.Format(now.Add(interactiveSessionTTL))
	administratorID, err := s.store.RefreshAdminSession(r.Context(), oldSecret, uuid.NewString(), hash, timetext.Format(now), expiresAt)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "administrator session cannot be refreshed")
		return
	}
	administrator, err := s.store.GetAdministrator(r.Context(), administratorID)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse("administrator", administrator, secret, expiresAt))
}

func (s *Server) logoutAdministrator(w http.ResponseWriter, r *http.Request) {
	secret, ok := bearerToken(r)
	if !ok || s.store.RevokeAdminSession(r.Context(), secret, timetext.Format(s.now())) != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "administrator session is not active")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
