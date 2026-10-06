// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Package asr defines the seam between the Go engine and the native
// inference libraries.
//
// Backend is implemented today by Mock (deterministic, no native code). The
// real implementation will live behind the "lwnative" build tag and bind via
// cgo / LoadLibrary to:
//   - audio.cpp C ABI for Confucius4-R2T2 (streaming, PLAN §2.1 / migration step 3),
//     (also whole-clip batch transcription when R2T2 is the active engine),
//   - transcribe.cpp for Qwen3-ASR (batch, migration step 4),
//   - onnxruntime for FireRedVAD (migration step 2).
//
// Threading contract: every Backend and Stream method is called only from the
// scheduler's single inference goroutine (locked OS thread), never
// concurrently. Implementations therefore need no internal locking for
// native state.
package asr

import (
	"errors"
)

// SampleRate is the only PCM rate accepted on the API (16 kHz mono s16le).
const SampleRate = 16000

// Kind identifies a local engine family.
type Kind string

const (
	KindR2T2  Kind = "r2t2"  // realtime with live subtitles; batch too
	KindQwen3 Kind = "qwen3" // batch only / fallback, no live subtitles
)

// LoadSpec is what a backend needs to load a model.
type LoadSpec struct {
	Engine    string // config engine id, e.g. "confucius4-r2t2"
	Kind      Kind
	ModelsDir string // "" = default HF cache
	Device    string // auto | cpu | cuda | vulkan
}

// Info describes the loaded state.
type Info struct {
	Engine        string   `json:"engine,omitempty"`
	Kind          Kind     `json:"kind,omitempty"`
	Device        string   `json:"device,omitempty"`
	ModelLoaded   bool     `json:"model_loaded"`
	GPUName       string   `json:"gpu_name,omitempty"`
	GPUMemoryGB   float64  `json:"gpu_memory_total,omitempty"`
	MissingModels []string `json:"missing_models"`
}

// TranscribeOptions are per-request options (hot words and language are sent
// by the shell with every request, PLAN §3.1).
type TranscribeOptions struct {
	Language string
	Context  string
	HotWords []string
}

// Result is a final transcription.
type Result struct {
	Text        string `json:"text"`
	Language    string `json:"language,omitempty"`
	SampleCount int    `json:"sample_count"`
}

// Partial is a streaming update: Committed only grows; Tentative may change.
type Partial struct {
	Committed string `json:"committed"`
	Tentative string `json:"tentative"`
}

// StreamOptions configure a realtime session.
type StreamOptions struct {
	SessionID uint64
	Language  string
	Context   string
	HotWords  []string
}

// Stream is one realtime R2T2 session.
type Stream interface {
	// Push feeds PCM samples (already offset-checked by the caller).
	Push(pcm []int16) (Partial, error)
	// Finish flushes (the native impl pads 320 ms of silence) and returns the result.
	Finish() (Result, error)
	// Close releases the session; safe after Finish.
	Close()
}

// Backend is a loaded-or-unloaded inference backend.
type Backend interface {
	// Name is "mock" or "native".
	Name() string
	Load(spec LoadSpec) error
	Unload() error
	Info() Info
	// Transcribe runs whole-clip recognition with the loaded engine: Qwen3
	// runs once over the VAD-trimmed clip; R2T2 feeds the clip through a
	// fresh segmented session in chunks and finishes it (like
	// transcribe_audio in r2t2_asr_server.py). The caller guarantees no live
	// R2T2 session is active.
	Transcribe(pcm []int16, opts TranscribeOptions) (Result, error)
	// NewStream starts a realtime session (R2T2 path).
	NewStream(opts StreamOptions) (Stream, error)
}

// Errors shared by implementations.
var (
	ErrNotLoaded         = errors.New("model not loaded")
	ErrWrongKind         = errors.New("operation not supported by the loaded engine")
	ErrNativeUnavailable = errors.New("native backend not compiled in (build with -tags lwnative)")
)

// KindForEngine maps a config engine id to a local engine kind.
func KindForEngine(engine string) (Kind, bool) {
	switch engine {
	case "confucius4-r2t2":
		return KindR2T2, true
	case "qwen3-asr-0.6b":
		return KindQwen3, true
	}
	return "", false
}
