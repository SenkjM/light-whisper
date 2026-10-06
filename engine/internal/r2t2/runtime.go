// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: GPL-3.0-only
//
// Go port of NativeRuntime from src-tauri/resources/r2t2_native.py (inherited
// light-whisper code, GPL-3.0-only): chunked pushes (CUDA 160 ms / CPU
// 320 ms), committed-delta accumulation, preview prefix checks, the ABI 0.4
// one-shot decode of the first VAD prefix on CUDA, and the 320 ms of zeros
// pushed before stream_finish. The C ABI itself is behind CLib.
//
// Per PLAN §10.4, logic translated from inherited GPL code is not labelled
// as newly written AGPL code; this file stays GPL-3.0-only (combinable with
// the AGPL-3.0-only engine, PLAN §10.3).

package r2t2

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	// SampleRate is the only rate the runtime accepts (16 kHz mono).
	SampleRate = 16000
	// FinishContextSamples is the acoustic end context pushed before
	// stream_finish: 80/160 ms still lost Chinese tails in paired GPU checks;
	// 320 ms also aligns both native chunk sizes.
	FinishContextSamples = SampleRate * 320 / 1000
	// ModelFamily is the audio.cpp family the model is loaded as.
	ModelFamily = "confucius4_r2t2"
)

// ErrInvalidArgument marks caller errors (Python ValueError).
var ErrInvalidArgument = errors.New("invalid argument")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidArgument, fmt.Sprintf(format, args...))
}

// RuntimeOptions mirror NativeRuntime's keyword arguments.
type RuntimeOptions struct {
	Backend string // "cpu" or "cuda"
	ChunkMs int    // 80..2000
	Threads int    // 0 = 4
	Rolling bool
}

// Runtime is the resident native R2T2 stream (NativeRuntime).
type Runtime struct {
	lib               CLib
	backend           string
	rolling           bool
	chunkSamples      int
	batchInitialAudio bool

	open      bool
	active    bool
	committed string
	preview   string
	language  string
	offset    int64
}

// NewRuntime binds lib, loads the model and creates the streaming session.
// lib must come straight from OpenLibrary; on error it is closed.
func NewRuntime(lib CLib, modelPath string, opts RuntimeOptions) (*Runtime, error) {
	if opts.Backend != "cpu" && opts.Backend != "cuda" {
		lib.Close()
		return nil, invalid("Unsupported R2T2 backend")
	}
	if opts.ChunkMs < 80 || opts.ChunkMs > 2000 {
		lib.Close()
		return nil, invalid("chunk_ms must be an integer from 80 to 2000")
	}
	if opts.Threads == 0 {
		opts.Threads = 4
	}
	r := &Runtime{
		lib:          lib,
		backend:      opts.Backend,
		rolling:      opts.Rolling,
		chunkSamples: opts.ChunkMs * 16,
	}
	if err := r.bind(); err != nil {
		lib.Close()
		return nil, err
	}
	// Keep the upstream validated conservative rollback defaults.
	options := [][2]string{{"confucius4_r2t2.chunk_size_ms", strconv.Itoa(opts.ChunkMs)}}
	if opts.Rolling {
		// Commit sentence endings using R2T2's native policy, so a completed
		// suffix is not kept behind rollback at a roll.
		options = append(options,
			[2]string{"confucius4_r2t2.rolling_window", "true"},
			[2]string{"confucius4_r2t2.rollback_punctuation", "true"})
	}
	if err := lib.Open(SessionConfig{ModelPath: modelPath, Backend: opts.Backend, Threads: opts.Threads, Options: options}); err != nil {
		lib.Close()
		return nil, err
	}
	r.open = true
	return r, nil
}

// bind checks the ABI before any newer symbol is used.
func (r *Runtime) bind() error {
	abi := r.lib.ABIVersion()
	minor := (abi >> 8) & 0xff
	if abi>>16 != 0 || minor < 3 {
		return fmt.Errorf("Unsupported audio.cpp ABI %#x; preview requires 0.3 or newer", abi)
	}
	r.batchInitialAudio = r.backend == "cuda" && r.rolling && minor >= 4
	return nil
}

// Backend is "cpu" or "cuda".
func (r *Runtime) Backend() string { return r.backend }

// ChunkSamples is the native push size (chunk_ms × 16).
func (r *Runtime) ChunkSamples() int { return r.chunkSamples }

// PreviewText is the latest full revisable hypothesis (starts with the
// committed text).
func (r *Runtime) PreviewText() string { return r.preview }

// Start begins a stream with a fresh request.
func (r *Runtime) Start(context, language string) error {
	if !r.open {
		return errors.New("R2T2 runtime is closed")
	}
	// A fresh request avoids retaining the previous stream's forced language.
	if err := r.lib.StreamStart(context, language); err != nil {
		return err
	}
	r.active = true
	r.offset = 0
	r.committed = ""
	r.preview = ""
	r.language = language
	return nil
}

func finite(pcm []float32) bool {
	for _, x := range pcm {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return false
		}
	}
	return true
}

// Feed pushes pcm and returns the committed text and the latest language.
func (r *Runtime) Feed(pcm []float32) (string, string, error) {
	if !r.active {
		return "", "", errors.New("No active R2T2 stream")
	}
	if !finite(pcm) {
		return "", "", invalid("Expected finite 1-D float32 PCM")
	}
	pushSize := r.chunkSamples
	// ABI 0.4 decodes the bounded initial VAD prefix once on CUDA.
	if r.offset == 0 && r.batchInitialAudio && len(pcm) > r.chunkSamples &&
		len(pcm) <= SampleRate && len(pcm)%r.chunkSamples == 0 {
		pushSize = len(pcm)
	}
	// All other pushes must decode at most one internal chunk: the C ABI
	// event container otherwise retains only the last delta.
	for start := 0; start < len(pcm); start += pushSize {
		chunk := pcm[start:min(start+pushSize, len(pcm))]
		ev, err := r.lib.StreamPush(chunk, r.offset)
		if err != nil {
			return "", "", err
		}
		r.offset += int64(len(chunk))
		if !ev.HasEvent {
			continue
		}
		delta, language, err := readText(ev.TextStatus, ev.Delta, ev.Language, ev.Detail)
		if err != nil {
			return "", "", err
		}
		r.committed += delta
		switch ev.PreviewStatus {
		case StatusOK:
			r.preview = ev.Preview
		case StatusNotAvailable: // No decode yet for a partial audio chunk.
		default:
			return "", "", fmt.Errorf("R2T2 result_preview_text failed (%d)", ev.PreviewStatus)
		}
		if !strings.HasPrefix(r.preview, r.committed) {
			return "", "", errors.New("R2T2 preview conflicts with committed text")
		}
		if language != "" {
			r.language = language
		}
	}
	return r.committed, r.language, nil
}

// readText mirrors _read_text: empty events are normal before the first
// stable word.
func readText(status int, text, language, detail string) (string, string, error) {
	switch status {
	case StatusOK:
		return text, language, nil
	case StatusNotAvailable:
		return "", "", nil
	}
	return "", "", &StatusError{Call: "result_text", Status: status, Detail: detail}
}

// Finish flushes FinishContextSamples of zeros (when audio was pushed) and
// returns the final text and language.
func (r *Runtime) Finish() (string, string, error) {
	if !r.active {
		return "", "", errors.New("No active R2T2 stream")
	}
	if r.offset != 0 {
		// Decode the last spoken word without waiting for more microphone
		// packets. This context stays inside the native runtime; captured
		// sample counts, durations and live captions remain unchanged.
		if _, _, err := r.Feed(make([]float32, FinishContextSamples)); err != nil {
			return "", "", err
		}
	}
	res, err := r.lib.StreamFinish()
	if err != nil {
		return "", "", err
	}
	text, language, err := readText(res.TextStatus, res.Text, res.Language, res.Detail)
	if err != nil {
		return "", "", err
	}
	r.active = false
	r.preview = ""
	if language == "" {
		language = r.language
	}
	return text, language, nil
}

// Reset drops the current stream, if any.
func (r *Runtime) Reset() error {
	wasActive := r.active
	r.active = false
	r.committed = ""
	r.preview = ""
	r.language = ""
	r.offset = 0
	if r.open && wasActive {
		return r.lib.StreamReset()
	}
	return nil
}

// Close frees the native session, model and registry. Idempotent.
func (r *Runtime) Close() {
	r.active = false
	if r.open {
		r.open = false
		r.lib.Close()
	}
}
