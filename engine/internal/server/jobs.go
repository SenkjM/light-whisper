// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/manager"
)

// jobRequest is the JSON form of POST /v1/jobs (a WAV file on disk).
type jobRequest struct {
	Path     string   `json:"path"`
	Language string   `json:"language"`
	Context  string   `json:"context"`
	HotWords []string `json:"hot_words"`
	Priority string   `json:"priority"`
	Segments bool     `json:"segments"`
}

func parseBool(s string) bool {
	b, err := strconv.ParseBool(s)
	return err == nil && b
}

// handleCreateJob: POST /v1/jobs. Either the audio itself (PCM s16le or WAV
// body, options in the query like /v1/asr/transcribe plus priority and
// segments) or JSON naming an absolute path to a 16 kHz mono 16-bit WAV.
func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var spec manager.JobSpec
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt == "application/json" {
		var req jobRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "body must be {path, language?, context?, hot_words?, priority?, segments?}: "+err.Error())
			return
		}
		if req.Path == "" || !filepath.IsAbs(req.Path) {
			writeError(w, http.StatusBadRequest, "bad_request", "path must be an absolute path to a WAV file")
			return
		}
		f, err := os.Open(req.Path)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_audio", err.Error())
			return
		}
		data, err := io.ReadAll(io.LimitReader(f, s.opts.MaxJobBytes+1))
		f.Close()
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_audio", err.Error())
			return
		}
		if int64(len(data)) > s.opts.MaxJobBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "file exceeds the job size limit")
			return
		}
		pcm, err := decodeWAV(data)
		if err != nil {
			writeError(w, http.StatusUnsupportedMediaType, "bad_audio", err.Error())
			return
		}
		spec = manager.JobSpec{
			PCM:      pcm,
			Options:  asr.TranscribeOptions{Language: req.Language, Context: req.Context, HotWords: req.HotWords},
			Priority: req.Priority,
			Segments: req.Segments,
		}
	} else {
		pcm, ok := readAudioBody(w, r, s.opts.MaxJobBytes)
		if !ok {
			return
		}
		q := r.URL.Query()
		spec = manager.JobSpec{
			PCM:      pcm,
			Options:  asr.TranscribeOptions{Language: q.Get("language"), Context: q.Get("context"), HotWords: q["hot_word"]},
			Priority: q.Get("priority"),
			Segments: parseBool(q.Get("segments")),
		}
	}
	info, err := s.opts.Manager.SubmitJob(spec)
	if err != nil {
		writeManagerError(w, err)
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+info.ID)
	writeJSON(w, http.StatusAccepted, info)
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": s.opts.Manager.Jobs()})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	info, err := s.opts.Manager.Job(r.PathValue("id"))
	if err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleDeleteJob(w http.ResponseWriter, r *http.Request) {
	if _, err := s.opts.Manager.CancelJob(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, manager.ErrJobNotFound) {
			writeManagerError(w, err)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
