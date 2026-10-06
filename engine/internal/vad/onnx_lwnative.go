// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build lwnative && cgo

package vad

import (
	"errors"
	"fmt"
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// ONNXOptions configures the onnxruntime-backed model.
type ONNXOptions struct {
	// LibraryPath is the onnxruntime shared library (onnxruntime.dll,
	// libonnxruntime.so.*, libonnxruntime.*.dylib). Empty: $LW_ONNXRUNTIME_LIB,
	// then the platform default name resolved by the OS loader.
	LibraryPath string
}

// NativeAvailable reports whether this build can run the ONNX model.
const NativeAvailable = true

var (
	envMu      sync.Mutex
	envLibrary string
)

// initEnvironment initializes the process-wide onnxruntime environment once.
// The environment lives until the process exits.
func initEnvironment(lib string) error {
	envMu.Lock()
	defer envMu.Unlock()
	if lib == "" {
		lib = os.Getenv("LW_ONNXRUNTIME_LIB")
	}
	if ort.IsInitialized() {
		if lib != "" && envLibrary != "" && lib != envLibrary {
			return fmt.Errorf("vad: onnxruntime already initialized from %s", envLibrary)
		}
		return nil
	}
	if lib != "" {
		ort.SetSharedLibraryPath(lib)
	}
	if err := ort.InitializeEnvironment(ort.WithLogLevelFatal()); err != nil {
		return fmt.Errorf("vad: initialize onnxruntime: %w", err)
	}
	envLibrary = lib
	return nil
}

type onnxModel struct {
	session *ort.DynamicAdvancedSession
}

// OpenONNX loads the FireRedVAD model with the same session settings as
// Python: CPU provider, 1 intra-op and 1 inter-op thread, no CPU memory arena.
func OpenONNX(modelPath string, opts ONNXOptions) (Model, error) {
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("vad: model: %w", err)
	}
	if err := initEnvironment(opts.LibraryPath); err != nil {
		return nil, err
	}
	so, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("vad: session options: %w", err)
	}
	defer so.Destroy()
	if err := errors.Join(
		so.SetIntraOpNumThreads(1),
		so.SetInterOpNumThreads(1),
		so.SetCpuMemArena(false),
		so.SetLogSeverityLevel(ort.LoggingLevelFatal),
	); err != nil {
		return nil, fmt.Errorf("vad: session options: %w", err)
	}
	session, err := ort.NewDynamicAdvancedSession(modelPath, []string{"feat"}, []string{"probs"}, so)
	if err != nil {
		return nil, fmt.Errorf("vad: load %s: %w", modelPath, err)
	}
	return &onnxModel{session: session}, nil
}

func (m *onnxModel) Probabilities(features []float32, frames int) ([]float32, error) {
	if len(features) != frames*NumMelBins {
		return nil, fmt.Errorf("vad: %d feature values for %d frames", len(features), frames)
	}
	input, err := ort.NewTensor(ort.NewShape(1, int64(frames), NumMelBins), features)
	if err != nil {
		return nil, fmt.Errorf("vad: input tensor: %w", err)
	}
	defer input.Destroy()
	outputs := []ort.Value{nil}
	if err := m.session.Run([]ort.Value{input}, outputs); err != nil {
		return nil, fmt.Errorf("vad: run model: %w", err)
	}
	defer outputs[0].Destroy()
	t, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("vad: unexpected output type %T", outputs[0])
	}
	return append([]float32(nil), t.GetData()...), nil
}

func (m *onnxModel) Close() error { return m.session.Destroy() }
