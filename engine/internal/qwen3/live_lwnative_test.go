// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build lwnative && cgo

package qwen3

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

// liveChecker wraps the real FireRedVAD and transcribe.cpp session and
// compares every call (input samples by SHA-256, VAD regions, decoded text
// and language) with the Python trace being replayed.
type liveChecker struct {
	t     *testing.T
	det   Detector
	sess  Session
	tr    *trace
	pos   int
	diffs []string
}

func (c *liveChecker) expect(op string, audio []float32) (traceCall, bool) {
	if c.tr == nil {
		return traceCall{}, false
	}
	if c.pos >= len(c.tr.Log) {
		c.diffs = append(c.diffs, fmt.Sprintf("extra %s call (n=%d)", op, len(audio)))
		return traceCall{}, false
	}
	e := c.tr.Log[c.pos]
	c.pos++
	if e.Op != op || e.N != len(audio) || e.SHA256 != floatSHA(audio) {
		c.diffs = append(c.diffs, fmt.Sprintf("call %d: Go %s(n=%d) vs Python %s(n=%d) (or samples differ)", c.pos-1, op, len(audio), e.Op, e.N))
		return e, false
	}
	return e, true
}

func (c *liveChecker) SpeechTimestamps(audio []float32) ([]vad.Segment, error) {
	got, err := c.det.SpeechTimestamps(audio)
	if e, ok := c.expect("vad", audio); ok && !reflect.DeepEqual(got, e.Regions) && !(len(got) == 0 && len(e.Regions) == 0) {
		c.diffs = append(c.diffs, fmt.Sprintf("VAD regions: Go %v, Python %v", got, e.Regions))
	}
	return got, err
}
func (c *liveChecker) Close() error { return nil }

type liveSession struct{ c *liveChecker }

func (s liveSession) Backend() string { return s.c.sess.Backend() }
func (s liveSession) Run(pcm []float32) (string, string, error) {
	text, lang, err := s.c.sess.Run(pcm)
	if e, ok := s.c.expect("run", pcm); ok && (text != e.Text || lang != e.Language) {
		s.c.diffs = append(s.c.diffs, fmt.Sprintf("transcribe.cpp: Go %q (%s), Python %q (%s)", text, lang, e.Text, e.Language))
	}
	return text, lang, err
}
func (s liveSession) SetAbort(v bool) { s.c.sess.SetAbort(v) }
func (s liveSession) Close()          { s.c.sess.Close() }

type liveLib struct {
	c   *liveChecker
	lib CLib
}

func (l liveLib) Version() string { return l.lib.Version() }
func (l liveLib) Open(model, backend string, cfg SessionConfig) (Session, error) {
	s, err := l.lib.Open(model, backend, cfg)
	if err != nil {
		return nil, err
	}
	l.c.sess = s
	return liveSession{l.c}, nil
}

// TestLiveMatchesPythonTraces runs the real transcribe.cpp library, the
// pinned Qwen3-ASR GGUF and FireRedVAD under the Go backend and compares
// every VAD and transcribe.cpp call and the final result with the Python
// reference traces (testdata/gen_reference.py). It needs:
//
//	LW_TRANSCRIBE_LIBRARY  transcribe.cpp 0.1.x shared library
//	LW_QWEN3_MODEL         Qwen3-ASR-0.6B-Q8_0.gguf
//	LW_ONNXRUNTIME_LIB     onnxruntime shared library (≥ 1.24)
//	LW_QWEN3_TRACE_DIRS    optional extra trace directories
//
// GPU traces must come from the same transcribe.cpp backend.
func TestLiveMatchesPythonTraces(t *testing.T) {
	libPath, model := os.Getenv("LW_TRANSCRIBE_LIBRARY"), os.Getenv("LW_QWEN3_MODEL")
	if libPath == "" || model == "" || os.Getenv("LW_ONNXRUNTIME_LIB") == "" {
		t.Skip("set LW_TRANSCRIBE_LIBRARY, LW_QWEN3_MODEL and LW_ONNXRUNTIME_LIB")
	}
	if testing.Short() {
		t.Skip("slow: real Qwen3-ASR inference")
	}
	traces := loadTraces(t, "testdata")
	for _, d := range extraTraceDirs() {
		traces = append(traces, loadTraces(t, d)...)
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
	lib, err := OpenLibrary(libPath, []string{filepath.Dir(libPath)})
	if err != nil {
		t.Fatal(err)
	}
	c := &liveChecker{t: t, det: det}
	b := NewBackend(BackendOptions{
		ModelPath:   model,
		OpenLibrary: func(string, []string) (CLib, error) { return liveLib{c, lib}, nil },
		NewDetector: func() (Detector, error) { return c, nil },
		HasNVIDIA:   func() bool { return false },
	})
	if err := b.Load(spec); err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if got := b.Info().Device; got != "cpu" {
		t.Logf("transcribe.cpp backend %q", got)
	}
	for _, tr := range traces {
		t.Run(tr.Name, func(t *testing.T) {
			// Python records the requested backend ("auto" resolves to the
			// CPU here); only a GPU trace needs a matching native backend.
			if tr.Backend != "auto" && !strings.EqualFold(tr.Backend, c.sess.Backend()) {
				t.Skipf("trace recorded on %s, running on %s", tr.Backend, c.sess.Backend())
			}
			pcm := tracePCM(t, tr)
			c.tr, c.pos, c.diffs = tr, 0, nil
			start := time.Now()
			res, err := b.Transcribe(pcm, asr.TranscribeOptions{})
			c.tr = nil
			if err != nil {
				t.Fatal(err)
			}
			if c.pos != len(tr.Log) {
				c.diffs = append(c.diffs, fmt.Sprintf("Go made %d of %d recorded calls", c.pos, len(tr.Log)))
			}
			if res.Text != tr.Result.Text || res.Language != tr.Result.Language {
				c.diffs = append(c.diffs, fmt.Sprintf("result: Go %q (%s), Python %q (%s)", res.Text, res.Language, tr.Result.Text, tr.Result.Language))
			}
			t.Logf("%.1fs audio in %v: %q (%s)", float64(len(pcm))/16000, time.Since(start).Round(time.Millisecond), res.Text, res.Language)
			for _, d := range c.diffs {
				t.Error(d)
			}
		})
	}
}

func liveBackend(t *testing.T) *Backend {
	t.Helper()
	libPath, model := os.Getenv("LW_TRANSCRIBE_LIBRARY"), os.Getenv("LW_QWEN3_MODEL")
	if libPath == "" || model == "" || os.Getenv("LW_ONNXRUNTIME_LIB") == "" {
		t.Skip("set LW_TRANSCRIBE_LIBRARY, LW_QWEN3_MODEL and LW_ONNXRUNTIME_LIB")
	}
	if testing.Short() {
		t.Skip("slow: real Qwen3-ASR inference")
	}
	resources := filepath.Join("..", "..", "..", "src-tauri", "resources")
	b := NewBackend(BackendOptions{
		LibraryPath: libPath,
		ModelPath:   model,
		HasNVIDIA:   func() bool { return false },
		NewDetector: func() (Detector, error) {
			cmvn, err := vad.LoadCMVN(filepath.Join(resources, vad.CMVNFileName))
			if err != nil {
				return nil, err
			}
			onnx, err := vad.OpenONNX(filepath.Join(resources, vad.ModelFileName), vad.ONNXOptions{LibraryPath: os.Getenv("LW_ONNXRUNTIME_LIB")})
			if err != nil {
				return nil, err
			}
			return vad.New(onnx, cmvn, vad.DefaultOptions())
		},
	})
	if err := b.Load(spec); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// TestLiveInterruptDuringNativeRun interrupts a transcribe.cpp call in
// flight. The abort callback is installed, but transcribe.cpp 0.1.3 does not
// poll it inside a Qwen3 run (measured on CPU, also from Python's
// Session.cancel), so the call normally completes and its result is kept;
// an ErrInterrupted return is accepted too in case a build honours it. Either
// way the session must stay usable and produce the same text afterwards.
func TestLiveInterruptDuringNativeRun(t *testing.T) {
	b := liveBackend(t)
	pcm := readWAV(t, filepath.Join("..", "r2t2", "testdata", "dictation_long.wav"))
	start := time.Now()
	full, err := b.Transcribe(pcm, asr.TranscribeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fullTime := time.Since(start)

	interrupt := make(chan struct{})
	time.AfterFunc(fullTime/4, func() { close(interrupt) })
	start = time.Now()
	res, err := b.Transcribe(pcm, asr.TranscribeOptions{Interrupt: interrupt})
	took := time.Since(start)
	switch {
	case errors.Is(err, asr.ErrInterrupted):
		t.Logf("aborted mid-run after %v (full run %v)", took.Round(time.Millisecond), fullTime.Round(time.Millisecond))
	case err != nil:
		t.Fatal(err)
	case res.Text != full.Text:
		t.Fatalf("interrupted run returned %q, want %q", res.Text, full.Text)
	default:
		t.Logf("abort not honoured mid-run: call completed after %v (full run %v), result kept", took.Round(time.Millisecond), fullTime.Round(time.Millisecond))
	}
	again, err := b.Transcribe(pcm, asr.TranscribeOptions{})
	if err != nil || again.Text != full.Text {
		t.Fatalf("after interrupt: %q, %v; want %q", again.Text, err, full.Text)
	}
}

// TestLiveSentenceStream feeds real speech in 100 ms frames through the
// Qwen3 whole-sentence realtime mode and logs the committed captions.
func TestLiveSentenceStream(t *testing.T) {
	b := liveBackend(t)
	for _, name := range []string{"dictation_long", "speech_mixed"} {
		t.Run(name, func(t *testing.T) {
			dir := "r2t2"
			if name == "speech_mixed" {
				dir = "vad"
			}
			pcm := readWAV(t, filepath.Join("..", dir, "testdata", name+".wav"))
			s, err := b.NewStream(asr.StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			last := ""
			for off := 0; off < len(pcm); off += 1600 {
				p, err := s.Push(pcm[off:min(off+1600, len(pcm))])
				if err != nil {
					t.Fatal(err)
				}
				if p.Committed != last {
					t.Logf("%5.1fs committed %q", float64(off)/16000, p.Committed)
					last = p.Committed
				}
			}
			res, err := s.Finish()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("final %q (%s)", res.Text, res.Language)
			if res.Text == "" || !strings.HasPrefix(res.Text, last) {
				t.Fatalf("final %q does not extend committed %q", res.Text, last)
			}
		})
	}
}
