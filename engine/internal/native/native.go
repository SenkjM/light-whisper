// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Package native assembles the native inference backend: R2T2 through
// audio.cpp (internal/r2t2), Qwen3-ASR through transcribe.cpp
// (internal/qwen3) and FireRedVAD through onnxruntime (internal/vad). All
// need the "lwnative" build tag and cgo; other builds get
// asr.ErrNativeUnavailable from New. Only one engine is resident at a time:
// loading one kind unloads the other.
package native

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/qwen3"
	"github.com/SenkjM/light-whisper/engine/internal/r2t2"
	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

// Available reports whether this build contains the native bindings.
const Available = r2t2.NativeAvailable && qwen3.NativeAvailable && vad.NativeAvailable

// Options locate the native files.
type Options struct {
	// RuntimeDir holds audio.cpp builds in cpu/ and cuda/ subdirectories.
	RuntimeDir string
	// ResourcesDir holds fireredvad_vad.onnx and fireredvad_cmvn.json.
	ResourcesDir string
	// ONNXRuntimeLib is the onnxruntime shared library ("" = $LW_ONNXRUNTIME_LIB,
	// then the OS loader's default name).
	ONNXRuntimeLib string
	// Threads for the R2T2 session (0 = 4).
	Threads int
	// R2T2Model, when set, is used as the R2T2 GGUF without the pinned
	// size/SHA-256 check (development only; "" = pinned model in the
	// Hugging Face cache).
	R2T2Model string
	// TranscribeLibrary is the transcribe.cpp shared library; the ggml
	// backend modules must sit next to it.
	TranscribeLibrary string
	// TranscribeDLLDirs are extra Windows dependency directories for
	// transcribe.cpp (e.g. the CUDA runtime).
	TranscribeDLLDirs []string
	// Qwen3Model, when set, is used as the Qwen3-ASR GGUF instead of the
	// Hugging Face cache lookup (development only).
	Qwen3Model string
}

// OptionsFromEnv uses LW_R2T2_RUNTIME_DIR, LW_RESOURCES_DIR,
// LW_ONNXRUNTIME_LIB, LW_R2T2_MODEL, LW_TRANSCRIBE_LIBRARY and
// LW_QWEN3_MODEL, defaulting to the Tauri resources layout next to the
// executable (<dir>/r2t2-native, <dir>/transcribe-native/<lib>, <dir>).
func OptionsFromEnv(exeDir string) Options {
	o := Options{
		RuntimeDir:        os.Getenv("LW_R2T2_RUNTIME_DIR"),
		ResourcesDir:      os.Getenv("LW_RESOURCES_DIR"),
		ONNXRuntimeLib:    os.Getenv("LW_ONNXRUNTIME_LIB"),
		R2T2Model:         os.Getenv("LW_R2T2_MODEL"),
		TranscribeLibrary: os.Getenv("LW_TRANSCRIBE_LIBRARY"),
		Qwen3Model:        os.Getenv("LW_QWEN3_MODEL"),
	}
	if o.ResourcesDir == "" {
		o.ResourcesDir = exeDir
	}
	if o.RuntimeDir == "" {
		o.RuntimeDir = filepath.Join(o.ResourcesDir, "r2t2-native")
	}
	if o.TranscribeLibrary == "" {
		o.TranscribeLibrary = filepath.Join(o.ResourcesDir, "transcribe-native", qwen3.DefaultLibraryName())
	}
	return o
}

// NewDetector opens FireRedVAD with the Python defaults.
func NewDetector(o Options) (*vad.FireRedVAD, error) {
	cmvn, err := vad.LoadCMVN(filepath.Join(o.ResourcesDir, vad.CMVNFileName))
	if err != nil {
		return nil, err
	}
	model, err := vad.OpenONNX(filepath.Join(o.ResourcesDir, vad.ModelFileName), vad.ONNXOptions{LibraryPath: o.ONNXRuntimeLib})
	if err != nil {
		return nil, err
	}
	det, err := vad.New(model, cmvn, vad.DefaultOptions())
	if err != nil {
		_ = model.Close()
		return nil, err
	}
	return det, nil
}

// New returns the native backend (R2T2 and Qwen3).
func New(o Options) (asr.Backend, error) {
	if !Available {
		return nil, asr.ErrNativeUnavailable
	}
	if o.RuntimeDir == "" || o.ResourcesDir == "" {
		return nil, errors.New("native: runtime and resources directories are required")
	}
	if _, err := os.Stat(filepath.Join(o.ResourcesDir, vad.ModelFileName)); err != nil {
		return nil, fmt.Errorf("native: FireRedVAD model: %w", err)
	}
	r := r2t2.NewBackend(r2t2.BackendOptions{
		RuntimeDir:  o.RuntimeDir,
		DLLDirs:     []string{filepath.Dir(o.RuntimeDir)},
		Threads:     o.Threads,
		ModelPath:   o.R2T2Model,
		NewDetector: func() (r2t2.Detector, error) { return NewDetector(o) },
	})
	q := qwen3.NewBackend(qwen3.BackendOptions{
		LibraryPath: o.TranscribeLibrary,
		DLLDirs:     o.TranscribeDLLDirs,
		ModelPath:   o.Qwen3Model,
		NewDetector: func() (qwen3.Detector, error) { return NewDetector(o) },
	})
	return NewMulti(map[asr.Kind]asr.Backend{asr.KindR2T2: r, asr.KindQwen3: q}), nil
}

// Multi routes to one backend per engine kind; at most one is loaded.
// Like every Backend, it is used only from the inference thread.
type Multi struct {
	backends map[asr.Kind]asr.Backend
	active   asr.Kind // kind of the last Load attempt ("" = none)
}

// NewMulti combines per-kind backends.
func NewMulti(backends map[asr.Kind]asr.Backend) *Multi { return &Multi{backends: backends} }

func (m *Multi) Name() string { return "native" }

func (m *Multi) current() asr.Backend {
	if b, ok := m.backends[m.active]; ok {
		return b
	}
	return nil
}

// Load unloads the other kind first (one resident model; GPU memory).
func (m *Multi) Load(spec asr.LoadSpec) error {
	b, ok := m.backends[spec.Kind]
	if !ok {
		return asr.ErrWrongKind
	}
	if m.active != spec.Kind {
		if cur := m.current(); cur != nil {
			if err := cur.Unload(); err != nil {
				return err
			}
		}
		m.active = spec.Kind
	}
	return b.Load(spec)
}

func (m *Multi) Unload() error {
	if cur := m.current(); cur != nil {
		return cur.Unload()
	}
	return nil
}

func (m *Multi) Info() asr.Info {
	if cur := m.current(); cur != nil {
		return cur.Info()
	}
	return asr.Info{MissingModels: []string{}}
}

func (m *Multi) Transcribe(pcm []int16, opts asr.TranscribeOptions) (asr.Result, error) {
	if cur := m.current(); cur != nil {
		return cur.Transcribe(pcm, opts)
	}
	return asr.Result{}, asr.ErrNotLoaded
}

func (m *Multi) NewStream(opts asr.StreamOptions) (asr.Stream, error) {
	if cur := m.current(); cur != nil {
		return cur.NewStream(opts)
	}
	return nil, asr.ErrNotLoaded
}

func (m *Multi) SpeechSegments(pcm []int16) ([]asr.Span, error) {
	if cur := m.current(); cur != nil {
		return cur.SpeechSegments(pcm)
	}
	return nil, asr.ErrNotLoaded
}
