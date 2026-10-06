// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package vad

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type f32List []float32

// UnmarshalJSON parses numbers with 32-bit precision (the generator writes
// the shortest float32 representation).
func (l *f32List) UnmarshalJSON(data []byte) error {
	var nums []json.Number
	if err := json.Unmarshal(data, &nums); err != nil {
		return err
	}
	out := make([]float32, len(nums))
	for i, n := range nums {
		v, err := strconv.ParseFloat(string(n), 32)
		if err != nil {
			return err
		}
		out[i] = float32(v)
	}
	*l = out
	return nil
}

type postprocessCase struct {
	Name    string `json:"name"`
	Options struct {
		Threshold            float64 `json:"threshold"`
		SmoothWindowFrames   int     `json:"smooth_window_frames"`
		MinSpeechDurationMs  int     `json:"min_speech_duration_ms"`
		MinSilenceDurationMs int     `json:"min_silence_duration_ms"`
		SpeechPadMs          int     `json:"speech_pad_ms"`
	} `json:"options"`
	AudioLength   int       `json:"audio_length"`
	Probabilities f32List   `json:"probabilities"`
	Smoothed      *f32List  `json:"smoothed"`
	Expected      []Segment `json:"expected"`
}

func TestPostprocessMatchesPython(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "postprocess_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []postprocessCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) < 50 {
		t.Fatalf("only %d cases", len(file.Cases))
	}
	smoothedChecked := 0
	for _, c := range file.Cases {
		opts := Options{
			// numpy compares a float32 array with a Python float in float32 (NEP 50).
			Threshold:            float32(c.Options.Threshold),
			SmoothWindowFrames:   c.Options.SmoothWindowFrames,
			MinSpeechDurationMs:  c.Options.MinSpeechDurationMs,
			MinSilenceDurationMs: c.Options.MinSilenceDurationMs,
			SpeechPadMs:          c.Options.SpeechPadMs,
		}
		got := TimestampsFromProbabilities(c.Probabilities, c.AudioLength, opts)
		if !segmentsEqual(got, c.Expected) {
			t.Errorf("%s: got %v, Python %v", c.Name, got, c.Expected)
		}
		if c.Smoothed != nil {
			smoothedChecked++
			s := Smooth(c.Probabilities, opts.SmoothWindowFrames)
			for i := range s {
				if math.Float32bits(s[i]) != math.Float32bits((*c.Smoothed)[i]) {
					t.Errorf("%s: smoothed[%d] = %v, numpy %v", c.Name, i, s[i], (*c.Smoothed)[i])
					break
				}
			}
		}
	}
	if smoothedChecked == 0 {
		t.Fatal("no smoothed references checked")
	}
	t.Logf("%d post-processing cases, %d with bitwise smoothing checks", len(file.Cases), smoothedChecked)
}

func TestTimestampsHandWritten(t *testing.T) {
	ones := func(n int) []float32 {
		s := make([]float32, n)
		for i := range s {
			s[i] = 1
		}
		return s
	}
	zeros := func(n int) []float32 { return make([]float32, n) }
	cat := func(parts ...[]float32) []float32 {
		var out []float32
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	opts := Options{Threshold: 0.5, SmoothWindowFrames: 1, MinSpeechDurationMs: 100, MinSilenceDurationMs: 200}

	// Same as test_firered_vad.py.
	if got := TimestampsFromProbabilities(cat(zeros(10), ones(9), zeros(30)), 8000, opts); len(got) != 0 {
		t.Fatalf("short spike: %v", got)
	}
	opts.SpeechPadMs = 120
	got := TimestampsFromProbabilities(cat(zeros(20), ones(15), zeros(20), ones(15), zeros(30)), 16000, opts)
	if !segmentsEqual(got, []Segment{{1280, 13120}}) {
		t.Fatalf("merge: %v", got)
	}
	if got := TimestampsFromProbabilities(nil, 100, DefaultOptions()); got == nil || len(got) != 0 {
		t.Fatalf("empty: %#v", got)
	}
}
