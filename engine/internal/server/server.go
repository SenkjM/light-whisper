// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Package server exposes the engine over local HTTP + WebSocket (PLAN §4).
//
// Security (PLAN §4, §6 #9): the listener must be loopback-only (enforced by
// cmd/lw-engine); every request needs "Authorization: Bearer <token>"; the
// Host header must name a loopback address (DNS-rebinding guard); requests
// carrying an Origin header are rejected unless the origin is explicitly
// allowed (browsers always send Origin on cross-site and WebSocket requests).
// No CORS headers are ever emitted.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/SenkjM/light-whisper/engine/internal/config"
	"github.com/SenkjM/light-whisper/engine/internal/events"
	"github.com/SenkjM/light-whisper/engine/internal/manager"
	"github.com/SenkjM/light-whisper/engine/internal/version"
)

// Options configure the HTTP handler.
type Options struct {
	Token          string
	Store          *config.Store
	Manager        *manager.Manager
	Hub            *events.Hub
	Logger         *slog.Logger
	AllowedOrigins []string
	// MaxAudioBytes bounds POST /v1/asr/transcribe bodies (default 64 MiB).
	MaxAudioBytes int64
	// MaxJobBytes bounds POST /v1/jobs audio (default 512 MiB, about 4.6 h
	// of 16 kHz mono s16).
	MaxJobBytes int64
}

// Server is the HTTP handler.
type Server struct {
	opts Options
	mux  *http.ServeMux
	log  *slog.Logger
}

// New builds the handler.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.MaxAudioBytes <= 0 {
		opts.MaxAudioBytes = 64 << 20
	}
	if opts.MaxJobBytes <= 0 {
		opts.MaxJobBytes = 512 << 20
	}
	s := &Server{opts: opts, mux: http.NewServeMux(), log: opts.Logger}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /v1/engine/status", s.handleStatus)
	s.mux.HandleFunc("POST /v1/engine/reload", s.handleReload)
	s.mux.HandleFunc("POST /v1/engine/load", s.handleLoad)
	s.mux.HandleFunc("POST /v1/engine/unload", s.handleUnload)
	s.mux.HandleFunc("GET /v1/config", s.handleGetConfig)
	s.mux.HandleFunc("PATCH /v1/config", s.handlePatchConfig)
	s.mux.HandleFunc("GET /v1/config/schema", s.handleSchema)
	s.mux.HandleFunc("GET /v1/events", s.handleEvents)
	s.mux.HandleFunc("POST /v1/asr/transcribe", s.handleTranscribe)
	s.mux.HandleFunc("GET /v1/asr/stream", s.handleStream)
	s.mux.HandleFunc("POST /v1/jobs", s.handleCreateJob)
	s.mux.HandleFunc("GET /v1/jobs", s.handleListJobs)
	s.mux.HandleFunc("GET /v1/jobs/{id}", s.handleGetJob)
	s.mux.HandleFunc("DELETE /v1/jobs/{id}", s.handleDeleteJob)
}

// ServeHTTP applies the security checks, then routes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !loopbackHost(r.Host) {
		writeError(w, http.StatusForbidden, "bad_host", "Host must be a loopback address")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(origin) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "cross-origin requests are not allowed")
		return
	}
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="light-whisper-engine"`)
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) authorized(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) || s.opts.Token == "" {
		return false
	}
	got := strings.TrimSpace(h[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.opts.Token)) == 1
}

func (s *Server) originAllowed(origin string) bool {
	for _, o := range s.opts.AllowedOrigins {
		if o == origin {
			return true
		}
	}
	return false
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// --- helpers -------------------------------------------------------------------

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Fields  any    `json:"fields,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeErrorFields(w, status, code, msg, nil)
}

func writeErrorFields(w http.ResponseWriter, status int, code, msg string, fields any) {
	var b errorBody
	b.Error.Code, b.Error.Message, b.Error.Fields = code, msg, fields
	writeJSON(w, status, b)
}

// writeManagerError maps manager errors to HTTP statuses.
func writeManagerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, manager.ErrBusy):
		writeError(w, http.StatusConflict, "realtime_session_active", err.Error())
	case errors.Is(err, manager.ErrSessionActive):
		writeError(w, http.StatusConflict, "realtime_session_active", err.Error())
	case errors.Is(err, manager.ErrNoLocalEngine):
		writeError(w, http.StatusConflict, "no_local_engine", err.Error())
	case errors.Is(err, manager.ErrWrongEngine):
		writeError(w, http.StatusConflict, "wrong_engine", err.Error())
	case errors.Is(err, manager.ErrBadPriority):
		writeError(w, http.StatusBadRequest, "invalid_priority", err.Error())
	case errors.Is(err, manager.ErrJobNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "engine_error", err.Error())
	}
}

// --- health / status / engine ------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"version":     version.Version,
		"api_version": version.APIVersion,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.opts.Manager.Status())
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	applied, err := s.opts.Manager.Reload(r.Context())
	if err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": applied, "status": s.opts.Manager.Status()})
}

func (s *Server) handleLoad(w http.ResponseWriter, r *http.Request) {
	if err := s.opts.Manager.Load(r.Context()); err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.opts.Manager.Status())
}

func (s *Server) handleUnload(w http.ResponseWriter, r *http.Request) {
	if err := s.opts.Manager.Unload(r.Context()); err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.opts.Manager.Status())
}

// --- config ---------------------------------------------------------------------

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	snap, err := s.opts.Store.Snapshot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_read_failed", err.Error())
		return
	}
	w.Header().Set("ETag", `"`+snap.Revision+`"`)
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/schema+json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(config.SchemaJSON())
}

func (s *Server) handlePatchConfig(w http.ResponseWriter, r *http.Request) {
	ifMatch := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
	ifMatch = strings.TrimPrefix(ifMatch, `W/"`)
	var patch map[string]json.RawMessage
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&patch); err != nil || patch == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "body must be a JSON object of key -> value (null resets to default)")
		return
	}
	res, err := s.opts.Store.Patch(ifMatch, patch)
	var verr *config.ValidationError
	switch {
	case errors.Is(err, config.ErrMissingRevision):
		writeError(w, http.StatusPreconditionRequired, "revision_required", "If-Match: <revision> header is required")
		return
	case errors.Is(err, config.ErrRevisionMismatch):
		writeError(w, http.StatusPreconditionFailed, "revision_mismatch", "config changed; GET /v1/config and retry")
		return
	case errors.As(err, &verr):
		writeErrorFields(w, http.StatusBadRequest, "invalid_config", verr.Error(), verr.Fields)
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "config_write_failed", err.Error())
		return
	}
	s.opts.Manager.OnConfigPatched(res)
	w.Header().Set("ETag", `"`+res.Revision+`"`)
	writeJSON(w, http.StatusOK, res)
}
