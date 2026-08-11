package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/idy/gizway/internal/store"
)

const rateLimitWindow = time.Minute

// consumeRateLimit applies a production DB-backed limit. Story mode uses a
// deliberately smaller allowance so Hurl can prove the same 429 transition
// without generating production-scale traffic; only the configured number is
// different, never the persistence or concurrency path.
func (s *Server) consumeRateLimit(w http.ResponseWriter, r *http.Request, scope, action string, productionLimit, storyLimit int64) bool {
	limit := productionLimit
	if s.advance != nil {
		limit = storyLimit
	}
	err := s.store.ConsumeRateLimit(r.Context(), scope, action, limit, rateLimitWindow, s.now())
	if err == nil {
		return true
	}
	if errors.Is(err, store.ErrRateLimited) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "request rate limit exceeded")
		return false
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "rate limit could not be checked")
	return false
}

func authenticationRateLimitScope(email string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return "login:" + hex.EncodeToString(digest[:])
}
