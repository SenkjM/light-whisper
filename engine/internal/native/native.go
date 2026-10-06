// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Package native assembles the native inference backend: R2T2 through
// audio.cpp (internal/r2t2) with FireRedVAD through onnxruntime
// (internal/vad). Both need the "lwnative" build tag and cgo; other builds
// get asr.ErrNativeUnavailable from New.
package native

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/r2t2"
	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

// Available reports whether this build contains the native bindings.
const Available = r2t2.NativeAvailable && vad.NativeAvailable

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
}

// OptionsFromEnv uses LW_R2T2_RUNTIME_DIR, LW_RESOURCES_DIR,
// LW_ONNXRUNTIME_LIB and LW_R2T2_MODEL, defaulting to the Tauri resources layout next to the
// executable (<dir>/r2t2-native, <dir>).
func OptionsFromEnv(exeDir string) Options {
	o := Options{
		RuntimeDir:     os.Getenv("LW_R2T2_RUNTIME_DIR"),
		ResourcesDir:   os.Getenv("LW_RESOURCES_DIR"),
		ONNXRuntimeLib: os.Getenv("LW_ONNXRUNTIME_LIB"),
		R2T2Model:      os.Getenv("LW_R2T2_MODEL"),
	}
	if o.ResourcesDir == "" {
		o.ResourcesDir = exeDir
	}
	if o.RuntimeDir == "" {
		o.RuntimeDir = filepath.Join(o.ResourcesDir, "r2t2-native")
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

// New returns the native backend (R2T2 only until migration step 4).
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
	return r2t2.NewBackend(r2t2.BackendOptions{
		RuntimeDir:  o.RuntimeDir,
		DLLDirs:     []string{filepath.Dir(o.RuntimeDir)},
		Threads:     o.Threads,
		ModelPath:   o.R2T2Model,
		NewDetector: func() (r2t2.Detector, error) { return NewDetector(o) },
	}), nil
}
