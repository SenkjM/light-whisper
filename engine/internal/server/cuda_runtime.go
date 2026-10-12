// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/SenkjM/light-whisper/engine/internal/auth"
	"github.com/SenkjM/light-whisper/engine/internal/cudartime"
)

func (s *Server) cudaStatusExtras() (devicePref string, usingCUDA, dllPresent bool) {
	if s.opts.Store != nil {
		devicePref = s.opts.Store.Effective().Device
	}
	if s.opts.Manager != nil {
		st := s.opts.Manager.Status()
		usingCUDA = st.Device == "cuda"
	}
	dllPresent = cudaR2T2DLLPresent()
	return
}

func cudaR2T2DLLPresent() bool {
	roots := []string{}
	if d := os.Getenv("LW_R2T2_RUNTIME_DIR"); d != "" {
		roots = append(roots, d)
	}
	if d := os.Getenv("LW_RESOURCES_DIR"); d != "" {
		roots = append(roots, filepath.Join(d, "r2t2-native"))
	}
	for _, r := range roots {
		for _, name := range []string{"audiocpp.dll", "libaudiocpp.so", "libaudiocpp.dylib"} {
			if st, err := os.Stat(filepath.Join(r, "cuda", name)); err == nil && !st.IsDir() {
				return true
			}
		}
	}
	return false
}

func (s *Server) requireEngineControl(w http.ResponseWriter, r *http.Request) bool {
	role, _ := s.roleFrom(r)
	if auth.Has(role, auth.CapEngineControl) || auth.Has(role, auth.CapConfigWrite) {
		return true
	}
	writeError(w, http.StatusForbidden, "forbidden", "missing capability: engine.control")
	return false
}

func (s *Server) handleCudaRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	if s.opts.CudaRuntime == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", "CUDA runtime manager not configured")
		return
	}
	dev, using, dll := s.cudaStatusExtras()
	writeJSON(w, http.StatusOK, s.opts.CudaRuntime.Status(dev, using, dll))
}

func (s *Server) handleCudaRuntimeDownload(w http.ResponseWriter, r *http.Request) {
	if s.opts.CudaRuntime == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", "CUDA runtime manager not configured")
		return
	}
	if !s.requireEngineControl(w, r) {
		return
	}
	if err := s.opts.CudaRuntime.StartDownload(r.Context()); err != nil {
		if errors.Is(err, cudartime.ErrDownloadInProgress) {
			writeError(w, http.StatusConflict, "download_in_progress", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "download_failed", err.Error())
		return
	}
	dev, using, dll := s.cudaStatusExtras()
	writeJSON(w, http.StatusAccepted, s.opts.CudaRuntime.Status(dev, using, dll))
}

func (s *Server) handleCudaRuntimeCancelDownload(w http.ResponseWriter, r *http.Request) {
	if s.opts.CudaRuntime == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", "CUDA runtime manager not configured")
		return
	}
	if !s.requireEngineControl(w, r) {
		return
	}
	s.opts.CudaRuntime.CancelDownload()
	w.WriteHeader(http.StatusNoContent)
}

type cudaEnableBody struct {
	PreferSystem               *bool `json:"prefer_system"`
	AcknowledgeVersionMismatch bool  `json:"acknowledge_version_mismatch"`
	Reload                     *bool `json:"reload"`
}

func (s *Server) handleCudaRuntimeEnable(w http.ResponseWriter, r *http.Request) {
	if s.opts.CudaRuntime == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", "CUDA runtime manager not configured")
		return
	}
	if !s.requireEngineControl(w, r) {
		return
	}
	var body cudaEnableBody
	if r.Body != nil {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		_ = dec.Decode(&body)
	}
	preferSystem := true
	if body.PreferSystem != nil {
		preferSystem = *body.PreferSystem
	}
	doReload := true
	if body.Reload != nil {
		doReload = *body.Reload
	}

	st, err := s.opts.CudaRuntime.Enable(preferSystem, body.AcknowledgeVersionMismatch)
	if errors.Is(err, cudartime.ErrVersionMismatch) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]any{
				"code":    "version_mismatch",
				"message": st.VersionWarning,
			},
			"status": st,
		})
		return
	}
	if errors.Is(err, cudartime.ErrNoNVIDIA) {
		writeError(w, http.StatusBadRequest, "nvidia_not_detected", err.Error())
		return
	}
	if errors.Is(err, cudartime.ErrDriverTooOld) {
		writeError(w, http.StatusBadRequest, "driver_too_old", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "enable_failed", err.Error())
		return
	}

	reloadApplied := []string{}
	if doReload && s.opts.Store != nil && s.opts.Manager != nil {
		snap, _ := s.opts.Store.Snapshot()
		res, perr := s.opts.Store.Patch(snap.Revision, map[string]json.RawMessage{"device": json.RawMessage(`"cuda"`)})
		if perr != nil {
			// try with quoted revision styles from tests
			writeError(w, http.StatusConflict, "config_patch_failed", perr.Error())
			return
		}
		applied, rerr := s.opts.Manager.Reload(r.Context())
		if rerr != nil {
			writeError(w, http.StatusConflict, "reload_failed", rerr.Error())
			return
		}
		reloadApplied = applied
		_ = res
	}
	dev, using, dll := s.cudaStatusExtras()
	out := s.opts.CudaRuntime.Status(dev, using, dll)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         out,
		"reload_applied": reloadApplied,
		"device":         "cuda",
	})
}

func (s *Server) handleCudaRuntimeDisable(w http.ResponseWriter, r *http.Request) {
	if s.opts.CudaRuntime == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", "CUDA runtime manager not configured")
		return
	}
	if !s.requireEngineControl(w, r) {
		return
	}
	_ = s.opts.CudaRuntime.Disable()
	if s.opts.Store != nil && s.opts.Manager != nil {
		snap, _ := s.opts.Store.Snapshot()
		_, _ = s.opts.Store.Patch(snap.Revision, map[string]json.RawMessage{"device": json.RawMessage(`"cpu"`)})
		_, _ = s.opts.Manager.Reload(r.Context())
	}
	dev, using, dll := s.cudaStatusExtras()
	writeJSON(w, http.StatusOK, s.opts.CudaRuntime.Status(dev, using, dll))
}
