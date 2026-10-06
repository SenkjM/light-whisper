// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package qwen3

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

// trace is one <name>.trace.json written by testdata/gen_reference.py.
type trace struct {
	Name        string `json:"name"`
	Samples     int    `json:"samples"`
	PCMSHA256   string `json:"pcm_sha256"`
	Backend     string `json:"backend"`
	DerivedFrom string `json:"derived_from"`
	Result      struct {
		Text     string `json:"text"`
		Language string `json:"language"`
	} `json:"result"`
	Log []traceCall `json:"log"`
	dir string
}

type traceCall struct {
	Op       string        `json:"op"` // "vad" or "run"
	N        int           `json:"n"`
	SHA256   string        `json:"sha256"`
	Regions  []vad.Segment `json:"regions"`
	Text     string        `json:"text"`
	Language string        `json:"language"`
}

func extraTraceDirs() []string {
	var out []string
	for _, d := range filepath.SplitList(os.Getenv("LW_QWEN3_TRACE_DIRS")) {
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

func loadTraces(t *testing.T, dir string) []*trace {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(dir, "*.trace.json"))
	sort.Strings(paths)
	var out []*trace
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		tr := &trace{dir: dir}
		if err := json.Unmarshal(b, tr); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		out = append(out, tr)
	}
	return out
}

func readWAV(t *testing.T, path string) []int16 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Fixtures are canonical 44-byte-header PCM16 mono 16 kHz WAVs; find "data".
	i := strings.Index(string(b), "data")
	if i < 0 || binary.LittleEndian.Uint32(b[24:28]) != 16000 {
		t.Fatalf("%s: not a 16 kHz PCM WAV", path)
	}
	n := int(binary.LittleEndian.Uint32(b[i+4:])) / 2
	data := b[i+8:]
	out := make([]int16, min(n, len(data)/2))
	for k := range out {
		out[k] = int16(binary.LittleEndian.Uint16(data[2*k:]))
	}
	return out
}

func findWAV(t *testing.T, tr *trace, name string) string {
	t.Helper()
	for _, d := range []string{tr.dir, filepath.Join("..", "r2t2", "testdata"), filepath.Join("..", "vad", "testdata")} {
		p := filepath.Join(d, name+".wav")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatalf("no WAV for %s", name)
	return ""
}

// tracePCM rebuilds the clip a trace was recorded on (gen_reference.py's
// derived clips included) and checks its hash.
func tracePCM(t *testing.T, tr *trace) []int16 {
	t.Helper()
	var pcm []int16
	switch {
	case tr.Name == "silence":
		pcm = make([]int16, 32000)
	case strings.HasSuffix(tr.Name, "-tiny"):
		pcm = readWAV(t, findWAV(t, tr, tr.DerivedFrom))[:6400]
	case strings.HasSuffix(tr.Name, "-padded"):
		src := readWAV(t, findWAV(t, tr, tr.DerivedFrom))
		pcm = append(append(make([]int16, 24000), src...), make([]int16, 24000)...)
	default:
		pcm = readWAV(t, findWAV(t, tr, tr.Name))
	}
	b := make([]byte, 2*len(pcm))
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	sum := sha256.Sum256(b)
	if len(pcm) != tr.Samples || hex.EncodeToString(sum[:]) != tr.PCMSHA256 {
		t.Fatalf("%s: rebuilt clip differs from the recorded one", tr.Name)
	}
	return pcm
}

func floatSHA(a []float32) string {
	b := make([]byte, 4*len(a))
	for i, x := range a {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// replayer serves the recorded VAD and transcribe.cpp answers in order,
// failing on any input that differs from the Python run.
type replayer struct {
	t   *testing.T
	tr  *trace
	pos int
}

func (r *replayer) next(op string, audio []float32) traceCall {
	r.t.Helper()
	if r.pos >= len(r.tr.Log) {
		r.t.Fatalf("%s: unexpected extra %s call", r.tr.Name, op)
	}
	c := r.tr.Log[r.pos]
	r.pos++
	if c.Op != op || c.N != len(audio) || c.SHA256 != floatSHA(audio) {
		r.t.Fatalf("%s: call %d: Go %s(n=%d) differs from Python %s(n=%d)", r.tr.Name, r.pos-1, op, len(audio), c.Op, c.N)
	}
	return c
}

func (r *replayer) SpeechTimestamps(audio []float32) ([]vad.Segment, error) {
	return r.next("vad", audio).Regions, nil
}
func (r *replayer) Close() error { return nil }

type replaySession struct{ r *replayer }

func (s replaySession) Backend() string { return s.r.tr.Backend }
func (s replaySession) Run(pcm []float32) (string, string, error) {
	if s.r.tr == nil { // warmup
		return "", "", nil
	}
	c := s.r.next("run", pcm)
	return c.Text, c.Language, nil
}
func (s replaySession) SetAbort(bool) {}
func (s replaySession) Close()        {}

type replayLib struct{ r *replayer }

func (l replayLib) Version() string { return LibraryVersion }
func (l replayLib) Open(string, string, SessionConfig) (Session, error) {
	return replaySession(l), nil
}

// TestReplayMatchesPythonTraces feeds every recorded clip through the Go
// backend with the recorded VAD regions and transcribe.cpp outputs: the
// short-clip floor, the VAD calls (same samples), the trimmed audio handed
// to transcribe.cpp (same samples, by SHA-256) and the final text and
// language must all match the Python server.
func TestReplayMatchesPythonTraces(t *testing.T) {
	traces := loadTraces(t, "testdata")
	for _, d := range extraTraceDirs() {
		traces = append(traces, loadTraces(t, d)...)
	}
	if len(traces) < 7 {
		t.Fatalf("only %d traces", len(traces))
	}
	for _, tr := range traces {
		t.Run(tr.Name, func(t *testing.T) {
			pcm := tracePCM(t, tr)
			r := &replayer{t: t}
			model := filepath.Join(t.TempDir(), "m.gguf")
			_ = os.WriteFile(model, []byte("x"), 0o644)
			b := NewBackend(BackendOptions{
				ModelPath:   model,
				OpenLibrary: func(string, []string) (CLib, error) { return replayLib{r}, nil },
				NewDetector: func() (Detector, error) { return r, nil },
				HasNVIDIA:   func() bool { return false },
			})
			if err := b.Load(spec); err != nil {
				t.Fatal(err)
			}
			r.tr = tr // warmup done
			res, err := b.Transcribe(pcm, asr.TranscribeOptions{Language: "zh", HotWords: []string{"ignored"}})
			if err != nil {
				t.Fatal(err)
			}
			if r.pos != len(tr.Log) {
				t.Fatalf("Go made %d of %d recorded calls", r.pos, len(tr.Log))
			}
			if res.Text != tr.Result.Text || res.Language != tr.Result.Language {
				t.Fatalf("Go %q (%s), Python %q (%s)", res.Text, res.Language, tr.Result.Text, tr.Result.Language)
			}
		})
	}
}
