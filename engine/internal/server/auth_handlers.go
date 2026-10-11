// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/json"
	"net/http"

	"github.com/SenkjM/light-whisper/engine/internal/auth"
)

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	role, id := s.roleFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           id,
		"role":         role,
		"capabilities": auth.Capabilities(role),
	})
}

func (s *Server) handleAuthCapabilities(w http.ResponseWriter, r *http.Request) {
	role, _ := s.roleFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"role":         role,
		"capabilities": auth.Capabilities(role),
	})
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, auth.CapTokenList) {
		return
	}
	if s.opts.Auth == nil {
		writeJSON(w, http.StatusOK, map[string]any{"tokens": []any{}})
		return
	}
	list, err := s.opts.Auth.ListTokens()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": list})
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, auth.CapTokenManage) {
		return
	}
	if s.opts.Auth == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", "auth store not enabled")
		return
	}
	var body struct {
		Role  string `json:"role"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	role := auth.Role(body.Role)
	id, plain, err := s.opts.Auth.CreateToken(role, body.Label)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":    id,
		"role":  role,
		"label": body.Label,
		"token": plain, // shown once
	})
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, auth.CapTokenManage) {
		return
	}
	if s.opts.Auth == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", "auth store not enabled")
		return
	}
	id := r.PathValue("id")
	err := s.opts.Auth.DeleteToken(id)
	if err == auth.ErrImmutable {
		writeError(w, http.StatusForbidden, "immutable_root", "root token cannot be deleted")
		return
	}
	if err == auth.ErrNotFound {
		writeError(w, http.StatusNotFound, "not_found", "token not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if !s.requireCap(w, r, auth.CapStatsRead) {
		return
	}
	if s.opts.Auth == nil {
		writeJSON(w, http.StatusOK, auth.Stats{})
		return
	}
	writeJSON(w, http.StatusOK, s.opts.Auth.SnapshotStats())
}
