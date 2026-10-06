// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package qwen3

import (
	"errors"
	"fmt"
	"runtime"
)

// CLib is the part of the transcribe.cpp C ABI (include/transcribe.h,
// library version 0.1.x) the engine uses. The production implementation
// (capi_lwnative.go) loads the shared library at run time; tests plug in
// fakes. Like the Python binding, the library must not be used from two
// threads at once: the engine only calls it from the inference thread.
type CLib interface {
	// Version is transcribe_version().
	Version() string
	// Open loads a model on the requested backend ("auto", "cpu", "cuda",
	// "vulkan", ...) and opens one session on it (transcribe.Model(...)
	// followed by model.session(...)).
	Open(modelPath, backend string, cfg SessionConfig) (Session, error)
}

// SessionConfig mirrors the session parameters the Python server passes.
type SessionConfig struct {
	NThreads int    // 0 = library default
	KVType   string // "auto", "f32", "f16"
	NCtx     int    // 0 = library default
}

// Session is one loaded model plus its decoding session.
type Session interface {
	// Backend is transcribe_model_backend(), the backend actually in use.
	Backend() string
	// Run transcribes 16 kHz mono float PCM with timestamps "none" and the
	// library's auto language detection, returning transcribe_full_text and
	// transcribe_detected_language as-is (not trimmed). When the abort flag
	// is raised during the call it returns an error wrapping ErrAborted.
	Run(pcm []float32) (text, language string, err error)
	// SetAbort raises or clears the abort flag that the library polls at
	// chunk / decode boundaries (transcribe_set_abort_callback). Safe to call
	// from any goroutine, unlike every other method.
	SetAbort(on bool)
	// Close frees the session, then the model.
	Close()
}

// ErrAborted is returned by Session.Run when the abort flag stopped the
// call (TRANSCRIBE_ERR_ABORTED).
var ErrAborted = errors.New("transcribe.cpp run aborted")

// StatusError is a non-OK transcribe.cpp status.
type StatusError struct {
	Op      string
	Status  int
	Message string // transcribe_status_string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: %s (status %d)", e.Op, e.Message, e.Status)
}

// StatusAborted is TRANSCRIBE_ERR_ABORTED.
const StatusAborted = 13

// Is lets errors.Is(err, ErrAborted) match an aborted status.
func (e *StatusError) Is(target error) bool { return target == ErrAborted && e.Status == StatusAborted }

// LibraryVersion is the transcribe.cpp base version this binding was
// written against (transcribe-cpp 0.1.3 in uv.lock / build_engine.py; the
// Python binding requires the same MAJOR.MINOR.PATCH before 1.0).
const LibraryVersion = "0.1.3"

// DefaultLibraryName is the transcribe.cpp shared library name on this OS.
func DefaultLibraryName() string {
	switch runtime.GOOS {
	case "windows":
		return "transcribe.dll"
	case "darwin":
		return "libtranscribe.dylib"
	}
	return "libtranscribe.so"
}
