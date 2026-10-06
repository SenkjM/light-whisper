// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Package r2t2 runs Confucius4-R2T2 through audio.cpp's C ABI: the native
// stream runtime, the outer VAD segmentation and the realtime session
// controller (PLAN §2.1, migration step 3).
//
// The native library is reached only through [CLib]. Builds with the
// "lwnative" tag and cgo load audio.cpp dynamically (dlopen / LoadLibrary,
// capi_lwnative.go); every other build gets [ErrNativeUnavailable] from
// [OpenLibrary], and tests drive the same logic through fake CLib
// implementations that replay scripted or recorded native events.
//
// Threading: like the Python runtime, nothing here is safe for concurrent
// use. The engine calls it only from the scheduler's inference thread.
package r2t2

import (
	"errors"
	"fmt"
)

// audio.cpp status codes used by the runtime (audiocpp_status).
const (
	StatusOK           = 0
	StatusNotAvailable = 7 // AUDIOCPP_ERR_NOT_AVAILABLE: no text / no decode yet
)

// ErrNativeUnavailable is returned by OpenLibrary in builds without the
// "lwnative" tag or without cgo.
var ErrNativeUnavailable = errors.New("r2t2: audio.cpp binding not compiled in (build with -tags lwnative and CGO_ENABLED=1)")

// StatusError is a failed C ABI call: "R2T2 <call> failed (<status>): <detail>".
type StatusError struct {
	Call   string
	Status int
	Detail string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("R2T2 %s failed (%d): %s", e.Call, e.Status, e.Detail)
}

// SessionConfig is what CLib.Open needs to create the streaming session.
type SessionConfig struct {
	ModelPath string
	Backend   string // "cpu" or "cuda"
	Threads   int
	// Options are audio.cpp session options, applied in order.
	Options [][2]string
}

// PushEvent is what one audiocpp_stream_push produced, already copied out of
// the native event (which the CLib frees before returning).
type PushEvent struct {
	// HasEvent is false when the push produced no event.
	HasEvent bool
	// TextStatus is audiocpp_result_text's status; Delta / Language are set
	// when it is StatusOK.
	TextStatus int
	Delta      string
	Language   string
	// PreviewStatus is audiocpp_result_preview_text's status; Preview is set
	// when it is StatusOK.
	PreviewStatus int
	Preview       string
	// Detail is audiocpp_last_error() captured right after a failing
	// result_text call.
	Detail string
}

// FinishResult is what audiocpp_stream_finish returned.
type FinishResult struct {
	TextStatus int
	Text       string
	Language   string
	Detail     string
}

// CLib is the subset of audio.cpp's C ABI the R2T2 runtime uses. One CLib
// owns one registry, model and streaming session.
type CLib interface {
	// ABIVersion is audiocpp_abi_version(), packed (major<<16)|(minor<<8)|patch.
	ABIVersion() uint32
	// Open creates the registry, loads the model (family confucius4_r2t2)
	// and creates an "asr" / "streaming" session with the given options.
	Open(cfg SessionConfig) error
	// StreamStart sends a fresh request (context text, optional forced
	// language; "" means NULL) and starts the stream.
	StreamStart(context, language string) error
	// StreamPush feeds one chunk (16 kHz mono) at start sample offset.
	StreamPush(pcm []float32, offset int64) (PushEvent, error)
	// StreamFinish ends the stream and reads its text.
	StreamFinish() (FinishResult, error)
	// StreamReset drops the stream.
	StreamReset() error
	// Close frees session, model and registry and unloads the library.
	Close()
}
