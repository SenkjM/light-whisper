// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build lwnative && cgo

package vad

import (
	"os"
	"path/filepath"
	"testing"
)

// Python's FireRedVAD probabilities vs the Go pipeline (Go fbank + CMVN +
// onnxruntime). Needs LW_ONNXRUNTIME_LIB (or a loader-visible onnxruntime);
// skipped otherwise.

const probabilityTolerance = 1e-4 // PLAN §6 #5

func openNative(t *testing.T) *FireRedVAD {
	t.Helper()
	if os.Getenv("LW_ONNXRUNTIME_LIB") == "" {
		t.Skip("LW_ONNXRUNTIME_LIB not set")
	}
	model, err := OpenONNX(bundledAsset(t, ModelFileName), ONNXOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cmvn, err := LoadCMVN(bundledAsset(t, CMVNFileName))
	if err != nil {
		t.Fatal(err)
	}
	v, err := New(model, cmvn, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

func TestONNXProbabilitiesMatchPython(t *testing.T) {
	v := openNative(t)
	for _, name := range fixtureNames {
		t.Run(name, func(t *testing.T) {
			ref := readRef(t, name)
			want := readF32(t, filepath.Join("testdata", name+".probs.f32"))
			audio := fixtureAudio(t, name)
			got, err := v.Probabilities(audio)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(want) {
				t.Fatalf("%d probabilities, Python %d", len(got), len(want))
			}
			s := compare(got, want)
			t.Logf("probabilities vs Python: %d frames, max |Δ| = %.3g (frame %d), mean |Δ| = %.3g",
				s.n, s.maxAbs, s.maxAt, s.meanAbs)
			if s.maxAbs > probabilityTolerance {
				t.Errorf("max |Δ| %.3g exceeds %.g", s.maxAbs, probabilityTolerance)
			}
			segs, err := v.SpeechTimestamps(audio)
			if err != nil {
				t.Fatal(err)
			}
			if !segmentsEqual(segs, ref.Segments) {
				t.Errorf("segments %v, Python %v", segs, ref.Segments)
			}
		})
	}
}

// Exact features from Python through the Go ONNX wrapper isolate the model
// runtime from fbank differences.
func TestONNXOnPythonFeatures(t *testing.T) {
	v := openNative(t)
	for _, name := range fixtureNames {
		feats := readF32(t, filepath.Join("testdata", name+".fbank.f32"))
		v.cmvn.Apply(feats)
		got, err := v.model.Probabilities(feats, len(feats)/NumMelBins)
		if err != nil {
			t.Fatal(err)
		}
		s := compare(got, readF32(t, filepath.Join("testdata", name+".probs.f32")))
		t.Logf("%s: model on Python features: max |Δ| = %.3g", name, s.maxAbs)
		if s.maxAbs > 1e-6 {
			t.Errorf("%s: onnxruntime wrapper differs from Python by %.3g", name, s.maxAbs)
		}
	}
}

func TestONNXSilence(t *testing.T) {
	v := openNative(t)
	p, err := v.Probabilities(make([]float32, SampleRate))
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 98 {
		t.Fatalf("%d probabilities for 1 s", len(p))
	}
	if segs, _ := v.SpeechTimestamps(make([]float32, SampleRate)); len(segs) != 0 {
		t.Fatalf("silence segments: %v", segs)
	}
}
