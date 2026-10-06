// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Package vad is the Go port of the FireRedVAD runtime used by the Python
// engines (src-tauri/resources/firered_vad.py), PLAN 步骤 2.
//
// Pipeline (identical to Python):
//
//	16 kHz float audio → ×32768, clip → Kaldi fbank (80 bins, 25/10 ms, povey,
//	snip_edges, no dither; pure Go, see fbank.go) → CMVN (cmvn.json) →
//	FireRedVAD ONNX model (input "feat" [1,T,80] → output "probs" [1,T,1]) →
//	post-processing (5-frame smoothing, threshold 0.5, min speech 150 ms,
//	min silence 300 ms, 120 ms padding, overlap merge; see postprocess.go).
//
// The model runs through onnxruntime only in builds with the `lwnative` tag
// (onnx_lwnative.go, cgo). The default build is pure Go: [Model] is an
// interface, so callers and tests can plug in a fake.
//
// The Python runtime is stateless (each call processes the whole buffer it
// is given); callers do their own windowing (r2t2_segmented.py feeds a
// sliding 1 s window). This package keeps that contract.
package vad

import (
	"errors"
	"fmt"
)

const (
	// SampleRate is the only supported input rate.
	SampleRate = 16000
	// FrameShiftSamples is the hop between feature frames (10 ms).
	FrameShiftSamples = 160
	// FrameLengthSamples is the analysis window (25 ms).
	FrameLengthSamples = 400
	// NumMelBins is the feature dimension.
	NumMelBins = 80

	// ModelFileName and CMVNFileName are the bundled asset names
	// (src-tauri/resources).
	ModelFileName = "fireredvad_vad.onnx"
	CMVNFileName  = "fireredvad_cmvn.json"
)

// ErrNativeUnavailable is returned when the ONNX model is requested from a
// build without the `lwnative` tag (or without cgo).
var ErrNativeUnavailable = errors.New("vad: onnxruntime backend not compiled in (build with -tags lwnative and cgo)")

// Segment is a speech region in samples, [Start, End).
type Segment struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Model maps normalized features (frames × NumMelBins, row-major) to one
// speech probability per frame.
type Model interface {
	Probabilities(features []float32, frames int) ([]float32, error)
	Close() error
}

// VAD is what the rest of the engine depends on.
type VAD interface {
	// Probabilities returns one speech probability per 10 ms frame of audio
	// (float samples in [-1, 1], 16 kHz mono).
	Probabilities(audio []float32) ([]float32, error)
	// SpeechTimestamps returns the speech regions of audio in samples.
	SpeechTimestamps(audio []float32) ([]Segment, error)
	Close() error
}

// FireRedVAD implements [VAD] with the FireRedVAD feature pipeline.
// It is not safe for concurrent use (the engine calls it from the single
// inference thread, like Python).
type FireRedVAD struct {
	fbank *Fbank
	cmvn  *CMVN
	model Model
	opts  Options
}

var _ VAD = (*FireRedVAD)(nil)

// New builds a detector from its parts. opts is validated.
func New(model Model, cmvn *CMVN, opts Options) (*FireRedVAD, error) {
	if model == nil || cmvn == nil {
		return nil, errors.New("vad: model and cmvn are required")
	}
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	return &FireRedVAD{fbank: NewFbank(), cmvn: cmvn, model: model, opts: opts}, nil
}

// Options returns the post-processing options in use.
func (v *FireRedVAD) Options() Options { return v.opts }

// Features returns the CMVN-normalized fbank features of audio
// (frames × NumMelBins) — the model input.
func (v *FireRedVAD) Features(audio []float32) ([]float32, int) {
	feats, frames := v.fbank.Compute(ScaleToPCM(audio))
	v.cmvn.Apply(feats)
	return feats, frames
}

// Probabilities implements [VAD].
func (v *FireRedVAD) Probabilities(audio []float32) ([]float32, error) {
	feats, frames := v.Features(audio)
	if frames == 0 {
		return []float32{}, nil
	}
	probs, err := v.model.Probabilities(feats, frames)
	if err != nil {
		return nil, err
	}
	if len(probs) != frames {
		return nil, fmt.Errorf("vad: model returned %d probabilities for %d frames", len(probs), frames)
	}
	return probs, nil
}

// SpeechTimestamps implements [VAD].
func (v *FireRedVAD) SpeechTimestamps(audio []float32) ([]Segment, error) {
	probs, err := v.Probabilities(audio)
	if err != nil {
		return nil, err
	}
	return TimestampsFromProbabilities(probs, len(audio), v.opts), nil
}

// Warmup runs one second of silence through the pipeline (same as Python).
func (v *FireRedVAD) Warmup() error {
	_, err := v.Probabilities(make([]float32, SampleRate))
	return err
}

// Close releases the model.
func (v *FireRedVAD) Close() error { return v.model.Close() }

// ScaleToPCM converts float audio in [-1, 1] to the int16-scaled values the
// fbank expects: x·32768 clipped to [-32768, 32767] (float32, as in Python).
func ScaleToPCM(audio []float32) []float32 {
	out := make([]float32, len(audio))
	for i, s := range audio {
		x := s * 32768
		switch {
		case x < -32768:
			x = -32768
		case x > 32767:
			x = 32767
		}
		out[i] = x
	}
	return out
}

// PCM16ToFloat converts s16 samples to float32 as the Python servers do
// (int16 / 32768).
func PCM16ToFloat(pcm []int16) []float32 {
	out := make([]float32, len(pcm))
	for i, s := range pcm {
		out[i] = float32(s) / 32768
	}
	return out
}
