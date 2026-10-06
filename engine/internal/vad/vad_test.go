// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package vad

import (
	"errors"
	"math"
	"testing"
)

// fakeModel marks a frame as speech when its mean normalized feature is
// above zero (enough to exercise the pipeline without onnxruntime).
type fakeModel struct{ short bool }

func (m fakeModel) Probabilities(features []float32, frames int) ([]float32, error) {
	out := make([]float32, frames)
	for f := range frames {
		var sum float32
		for _, x := range features[f*NumMelBins : (f+1)*NumMelBins] {
			sum += x
		}
		if sum > 0 {
			out[f] = 1
		}
	}
	if m.short {
		return out[:frames-1], nil
	}
	return out, nil
}

func (fakeModel) Close() error { return nil }

func testCMVN() *CMVN {
	c := &CMVN{}
	for i := range NumMelBins {
		c.Mean[i] = 8
		c.InverseStd[i] = 0.25
	}
	return c
}

func TestNumFrames(t *testing.T) {
	cases := map[int]int{0: 0, 399: 0, 400: 1, 559: 1, 560: 2, 16000: 98}
	for n, want := range cases {
		if got := NumFrames(n); got != want {
			t.Errorf("NumFrames(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestPoveyWindowAndMelBanks(t *testing.T) {
	f := NewFbank()
	if f.window[0] != 0 || f.window[FrameLengthSamples-1] > 1e-6 {
		t.Fatalf("povey window must start/end at 0: %v %v", f.window[0], f.window[FrameLengthSamples-1])
	}
	if math.Abs(float64(f.window[199])-1) > 1e-4 || f.window[100] != f.window[FrameLengthSamples-1-100] {
		t.Fatalf("povey window not symmetric/peaked: %v %v %v", f.window[199], f.window[100], f.window[299])
	}
	prevOffset := -1
	for b, bin := range f.bins {
		if bin.offset < prevOffset || len(bin.weights) == 0 || bin.offset+len(bin.weights) > numFFTBins {
			t.Fatalf("bin %d: offset %d, %d weights", b, bin.offset, len(bin.weights))
		}
		for _, w := range bin.weights {
			if w <= 0 || w > 1 {
				t.Fatalf("bin %d weight %v out of (0,1]", b, w)
			}
		}
		prevOffset = bin.offset
	}
	// Lowest bin starts just above 20 Hz (bin width 31.25 Hz).
	if f.bins[0].offset != 1 {
		t.Fatalf("first mel bin offset = %d", f.bins[0].offset)
	}
}

func TestDigitalSilenceIsFloored(t *testing.T) {
	feats, frames := NewFbank().Compute(make([]float32, 16000))
	if frames != 98 {
		t.Fatal(frames)
	}
	want := logf(floatEpsilon)
	for i, x := range feats {
		if x != want {
			t.Fatalf("feature %d = %v, want log(FLT_EPSILON) = %v", i, x, want)
		}
	}
}

func TestScaleToPCMClips(t *testing.T) {
	got := ScaleToPCM([]float32{-2, -1, 0, 0.5, 1, 2})
	want := []float32{-32768, -32768, 0, 16384, 32767, 32767}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ScaleToPCM = %v, want %v", got, want)
		}
	}
	if f := PCM16ToFloat([]int16{-32768, 16384}); f[0] != -1 || f[1] != 0.5 {
		t.Fatalf("PCM16ToFloat = %v", f)
	}
}

func TestParseCMVN(t *testing.T) {
	if _, err := ParseCMVN([]byte(`{"mean":[1],"inverse_std":[1]}`)); err == nil {
		t.Fatal("short cmvn accepted")
	}
	if _, err := ParseCMVN([]byte(`{`)); err == nil {
		t.Fatal("bad json accepted")
	}
	c, err := LoadCMVN(bundledAsset(t, CMVNFileName))
	if err != nil {
		t.Fatal(err)
	}
	if c.Mean[0] != float32(10.42295174919564) {
		t.Fatalf("mean[0] = %v", c.Mean[0])
	}
}

func TestPipelineWithFakeModel(t *testing.T) {
	v, err := New(fakeModel{}, testCMVN(), DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	// Under 400 samples: no frames, no segments, model not called.
	if p, err := v.Probabilities(make([]float32, 399)); err != nil || len(p) != 0 {
		t.Fatalf("short audio: %v %v", p, err)
	}
	// 1 s silence, 1 s loud white noise, 1 s silence.
	audio := make([]float32, 3*SampleRate)
	seed := uint32(1)
	for i := SampleRate; i < 2*SampleRate; i++ {
		seed = seed*1664525 + 1013904223
		audio[i] = 0.3 * (float32(seed>>8)/float32(1<<24)*2 - 1)
	}
	segs, err := v.SpeechTimestamps(audio)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 || segs[0].Start > SampleRate || segs[0].End < 2*SampleRate-FrameLengthSamples {
		t.Fatalf("segments = %v", segs)
	}
	if err := v.Warmup(); err != nil {
		t.Fatal(err)
	}

	bad, _ := New(fakeModel{short: true}, testCMVN(), DefaultOptions())
	if _, err := bad.Probabilities(audio); err == nil {
		t.Fatal("frame count mismatch not reported")
	}
	if _, err := New(nil, testCMVN(), DefaultOptions()); err == nil {
		t.Fatal("nil model accepted")
	}
}

func TestOpenONNXWithoutNative(t *testing.T) {
	if NativeAvailable {
		t.Skip("native build")
	}
	if _, err := OpenONNX("x.onnx", ONNXOptions{}); !errors.Is(err, ErrNativeUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
