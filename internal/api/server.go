// Package api owns the Milestone 02 HTTP transport.
package api

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"strings"
	"time"
)

// Surface selects the service-owned route set.
type Surface uint8

const (
	SurfaceGizPay Surface = iota
	SurfaceGizWay
)

// Server is intentionally small: authentication and business handlers are
// composed behind the route manifest without retaining the pre-refactor API.
type Server struct {
	handler   http.Handler
	business  http.Handler
	surface   Surface
	name      string
	now       func() time.Time
	advance   func(time.Duration) time.Time
	readiness func(context.Context, bool) (map[string]any, error)
}

// NewMilestone02 composes a service-owned business handler behind the single
// route manifest and the local-only health probe.
func NewMilestone02(surface Surface, name string, business http.Handler, advance func(time.Duration) time.Time) *Server {
	server := &Server{surface: surface, name: name, business: business, now: time.Now, advance: advance}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.milestone02Health)
	if advance != nil {
		mux.HandleFunc("POST /test/v1/clock/advance", server.advanceStoryClock)
	}
	server.registerMilestone02Routes(mux)
	server.handler = recoverMiddleware(requestIDMiddleware(surfaceHandler(surface, mux)))
	return server
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) Shutdown(context.Context) error                 { return nil }
func (s *Server) CloseRealtimeConnections(context.Context) error { return nil }

func (s *Server) ConfigureReadiness(check func(context.Context, bool) (map[string]any, error)) {
	s.readiness = check
}

func (s *Server) milestone02Health(w http.ResponseWriter, r *http.Request) {
	result := map[string]any{"status": "degraded", "service": map[string]any{"kind": s.surface.String()}, "server": map[string]any{"name": s.name}}
	if s.readiness != nil {
		checks, err := s.readiness(r.Context(), false)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status":  "unhealthy",
				"service": map[string]any{"kind": s.surface.String()},
				"server":  map[string]any{"name": s.name},
			})
			return
		}
		maps.Copy(result, checks)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) milestone02NotImplemented(w http.ResponseWriter, r *http.Request) {
	if s.business != nil {
		s.business.ServeHTTP(w, r)
		return
	}
	writeError(w, http.StatusNotImplemented, "not_implemented", "Milestone 02 handler is not implemented yet")
}

func (s *Server) advanceStoryClock(w http.ResponseWriter, r *http.Request) {
	if s.advance == nil {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Duration string `json:"duration"`
		Seconds  int64  `json:"seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	duration := time.Duration(body.Seconds) * time.Second
	var err error
	if body.Duration != "" {
		duration, err = time.ParseDuration(body.Duration)
	}
	if err != nil || duration < 0 {
		writeError(w, http.StatusBadRequest, "invalid_duration", "duration must be non-negative")
		return
	}
	s.advance(duration)
	w.WriteHeader(http.StatusNoContent)
}

func surfaceHandler(surface Surface, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		allowed := path == "/healthz" || strings.HasPrefix(path, "/test/")
		if surface == SurfaceGizPay {
			allowed = allowed || strings.HasPrefix(path, "/account/") || strings.HasPrefix(path, "/service/")
		} else {
			allowed = allowed || strings.HasPrefix(path, "/admin/") || strings.HasPrefix(path, "/v1/") ||
				strings.HasPrefix(path, "/v1beta/")
		}
		if !allowed {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestIDMiddleware(next http.Handler) http.Handler { return next }
