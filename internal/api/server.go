// Package api owns the Milestone 03 HTTP transport.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/idy/gizway/internal/buildinfo"
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
	handler  http.Handler
	business http.Handler
	surface  Surface
	name     string
	build    buildinfo.Info
	now      func() time.Time
	advance  func(time.Duration) time.Time
}

// NewMilestone03 composes a service-owned business handler behind the single
// route manifest and the local-only health probe.
func NewMilestone03(surface Surface, name string, business http.Handler, advance func(time.Duration) time.Time) *Server {
	server := &Server{surface: surface, name: name, build: buildinfo.Current(), business: business, now: time.Now, advance: advance}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.milestone03Health)
	if advance != nil {
		mux.HandleFunc("POST /test/v1/clock/advance", server.advanceStoryClock)
	}
	server.registerMilestone03Routes(mux)
	server.handler = recoverMiddleware(requestIDMiddleware(surfaceHandler(surface, mux)))
	return server
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) Shutdown(context.Context) error                 { return nil }
func (s *Server) CloseRealtimeConnections(context.Context) error { return nil }

func (s *Server) milestone03Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "healthy", "service": s.surface.String(),
		"version": s.build.Version, "revision": s.build.Revision, "build_time": s.build.BuildTime,
		"server_time": s.now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) milestone03NotImplemented(w http.ResponseWriter, r *http.Request) {
	if s.business != nil {
		s.business.ServeHTTP(w, r)
		return
	}
	writeError(w, http.StatusNotImplemented, "not_implemented", "Milestone 03 handler is not implemented yet")
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
		if surface == SurfaceGizPay && path == "/account/v1/initialize" {
			writeError(w, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		allowed := path == "/healthz" || strings.HasPrefix(path, "/test/")
		if surface == SurfaceGizPay {
			allowed = allowed || strings.HasPrefix(path, "/account/") || strings.HasPrefix(path, "/service/") ||
				strings.HasPrefix(path, "/webhooks/")
		} else {
			allowed = allowed || strings.HasPrefix(path, "/user/") || strings.HasPrefix(path, "/v1/") ||
				strings.HasPrefix(path, "/v1beta/") || strings.HasPrefix(path, "/auth/")
		}
		if !allowed {
			writeError(w, http.StatusNotFound, "not_found", "resource not found")
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
