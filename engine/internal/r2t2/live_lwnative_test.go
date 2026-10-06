// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build lwnative && cgo

package r2t2

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

// TestLiveMatchesPythonTraces runs the real audio.cpp library, model and
// FireRedVAD under the Go backend and compares every native/VAD call, every
// streaming response, the final text and the whole-clip transcription with
// the Python reference traces. It needs:
//
//	LW_R2T2_LIBRARY     audio.cpp shared library (libaudiocpp.so / audiocpp.dll)
//	LW_R2T2_MODEL       r2t2-q8_0.gguf
//	LW_ONNXRUNTIME_LIB  onnxruntime shared library (≥ 1.24)
//	LW_R2T2_TRACE_DIRS  optional extra trace directories
//
// The traces must come from the same backend (cpu/cuda) and thread count.
func TestLiveMatchesPythonTraces(t *testing.T) {
	libPath, model := os.Getenv("LW_R2T2_LIBRARY"), os.Getenv("LW_R2T2_MODEL")
	if libPath == "" || model == "" || os.Getenv("LW_ONNXRUNTIME_LIB") == "" {
		t.Skip("set LW_R2T2_LIBRARY, LW_R2T2_MODEL and LW_ONNXRUNTIME_LIB")
	}
	if testing.Short() {
		t.Skip("slow: real R2T2 inference")
	}
	traces := loadTraces(t, "testdata")
	for _, dir := range extraTraceDirs() {
		traces = append(traces, loadTraces(t, dir)...)
	}
	if len(traces) == 0 {
		t.Fatal("no traces")
	}
	resources := filepath.Join("..", "..", "..", "src-tauri", "resources")
	cmvn, err := vad.LoadCMVN(filepath.Join(resources, vad.CMVNFileName))
	if err != nil {
		t.Fatal(err)
	}
	onnx, err := vad.OpenONNX(filepath.Join(resources, vad.ModelFileName), vad.ONNXOptions{LibraryPath: os.Getenv("LW_ONNXRUNTIME_LIB")})
	if err != nil {
		t.Fatal(err)
	}
	det, err := vad.New(onnx, cmvn, vad.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer det.Close()

	type config struct {
		backend string
		threads int
	}
	groups := map[config][]*trace{}
	for _, tr := range traces {
		k := config{tr.Backend, tr.Threads}
		groups[k] = append(groups[k], tr)
	}
	for k, group := range groups {
		backend, threads := k.backend, k.threads
		lib, err := OpenLibrary(libPath, []string{filepath.Dir(libPath)})
		if err != nil {
			t.Fatal(err)
		}
		c := &checker{lib: lib, det: det}
		b := NewBackend(BackendOptions{
			Threads:     threads,
			ModelPath:   model,
			OpenLibrary: func(string, []string) (CLib, error) { return c, nil },
			NewDetector: func() (Detector, error) { return checkerDetector{c}, nil },
		})
		c.free = true
		if err := b.Load(loadSpec(backend)); err != nil {
			t.Fatal(err)
		}
		c.free = false
		for i, tr := range group {
			t.Run(tr.Name, func(t *testing.T) {
				s := runStream(t, b, c, tr, uint64(i+1))
				x := runTranscribe(t, b, c, tr)
				t.Logf("stream: %d responses, %d pushes, %d VAD calls; transcribe: %d pushes, %d VAD calls",
					s.responses, s.pushes, s.vadCalls, x.pushes, x.vadCalls)
				for _, d := range append(s.textDiffs, x.textDiffs...) {
					t.Errorf("decoded output differs from Python: %s", d)
				}
			})
		}
		_ = b.Unload()
	}
}
