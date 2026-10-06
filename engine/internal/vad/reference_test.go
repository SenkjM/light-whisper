// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package vad

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Reference data produced by testdata/gen_reference.py from the Python
// runtime (src-tauri/resources/firered_vad.py, kaldi-native-fbank 1.22.3,
// onnxruntime).

var fixtureNames = []string{"speech_mixed", "tones_clip"}

type fixtureRef struct {
	Samples  int       `json:"samples"`
	Frames   int       `json:"frames"`
	Segments []Segment `json:"segments"`
}

func readWAV16(t *testing.T, path string) []int16 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatalf("%s: not a WAV file", path)
	}
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		body := data[pos+8 : min(len(data), pos+8+size)]
		switch id {
		case "fmt ":
			if binary.LittleEndian.Uint16(body[0:2]) != 1 || binary.LittleEndian.Uint16(body[2:4]) != 1 ||
				binary.LittleEndian.Uint32(body[4:8]) != SampleRate || binary.LittleEndian.Uint16(body[14:16]) != 16 {
				t.Fatalf("%s: want PCM s16 mono 16 kHz", path)
			}
		case "data":
			out := make([]int16, len(body)/2)
			for i := range out {
				out[i] = int16(binary.LittleEndian.Uint16(body[2*i:]))
			}
			return out
		}
		pos += 8 + size + size%2
	}
	t.Fatalf("%s: no data chunk", path)
	return nil
}

func readF32(t *testing.T, path string) []float32 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]float32, len(data)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[4*i:]))
	}
	return out
}

func readRef(t *testing.T, name string) fixtureRef {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".ref.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ref fixtureRef
	if err := json.Unmarshal(data, &ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

func fixtureAudio(t *testing.T, name string) []float32 {
	return PCM16ToFloat(readWAV16(t, filepath.Join("testdata", name+".wav")))
}

// bundledAsset returns the path of a model asset under src-tauri/resources.
func bundledAsset(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join("..", "..", "..", "src-tauri", "resources", name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("bundled asset %s not found: %v", name, err)
	}
	return p
}

type diffStats struct {
	maxAbs, meanAbs float64
	maxAt           int
	n               int
}

func compare(a, b []float32) diffStats {
	var s diffStats
	for i := range a {
		d := math.Abs(float64(a[i]) - float64(b[i]))
		s.meanAbs += d
		if d > s.maxAbs {
			s.maxAbs, s.maxAt = d, i
		}
	}
	s.n = len(a)
	if s.n > 0 {
		s.meanAbs /= float64(s.n)
	}
	return s
}

// Log-mel values are O(1..25). The kaldi-native-fbank wheel builds kissfft
// with -ffast-math (compiler-specific rounding), so the features differ from
// this port by float32 rounding noise, largest in low-energy bins.
const fbankTolerance = 2e-3

func TestFbankMatchesKaldiNativeFbank(t *testing.T) {
	fb := NewFbank()
	for _, name := range fixtureNames {
		t.Run(name, func(t *testing.T) {
			audio := fixtureAudio(t, name)
			ref := readRef(t, name)
			want := readF32(t, filepath.Join("testdata", name+".fbank.f32"))
			if len(audio) != ref.Samples {
				t.Fatalf("samples = %d, reference %d", len(audio), ref.Samples)
			}
			got, frames := fb.Compute(ScaleToPCM(audio))
			if frames != ref.Frames || len(got) != len(want) {
				t.Fatalf("frames = %d (%d values), reference %d (%d values)", frames, len(got), ref.Frames, len(want))
			}
			s := compare(got, want)
			t.Logf("raw fbank vs kaldi-native-fbank: %d values, max |Δ| = %.3g (frame %d, bin %d), mean |Δ| = %.3g",
				s.n, s.maxAbs, s.maxAt/NumMelBins, s.maxAt%NumMelBins, s.meanAbs)
			if s.maxAbs > fbankTolerance {
				t.Fatalf("max |Δ| %.3g exceeds %.g", s.maxAbs, fbankTolerance)
			}
		})
	}
}

func TestNormalizedFeaturesMatchPython(t *testing.T) {
	cmvn, err := LoadCMVN(bundledAsset(t, CMVNFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range fixtureNames {
		want := readF32(t, filepath.Join("testdata", name+".fbank.f32"))
		cmvn.Apply(want) // Python: (raw - mean) * inverse_std in float32
		v, err := New(fakeModel{}, cmvn, DefaultOptions())
		if err != nil {
			t.Fatal(err)
		}
		got, _ := v.Features(fixtureAudio(t, name))
		s := compare(got, want)
		t.Logf("%s normalized features: max |Δ| = %.3g, mean |Δ| = %.3g", name, s.maxAbs, s.meanAbs)
		// inverse_std is < 0.5 for every bin, so the normalized error is smaller.
		if s.maxAbs > fbankTolerance {
			t.Fatalf("%s: max |Δ| %.3g", name, s.maxAbs)
		}
	}
}

// Python's post-processing applied to the real model outputs must give the
// same segments in Go.
func TestSegmentsFromPythonProbabilities(t *testing.T) {
	for _, name := range fixtureNames {
		ref := readRef(t, name)
		probs := readF32(t, filepath.Join("testdata", name+".probs.f32"))
		if len(probs) != ref.Frames {
			t.Fatalf("%s: %d probabilities for %d frames", name, len(probs), ref.Frames)
		}
		got := TimestampsFromProbabilities(probs, ref.Samples, DefaultOptions())
		if !segmentsEqual(got, ref.Segments) {
			t.Fatalf("%s: segments %v, Python %v", name, got, ref.Segments)
		}
	}
}

func segmentsEqual(a, b []Segment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
