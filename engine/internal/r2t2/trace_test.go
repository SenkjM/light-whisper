// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package r2t2

// Python reference traces (testdata/gen_reference.py) and a checker that
// sits where audio.cpp and FireRedVAD sit. In replay mode it answers every
// native/VAD call from the trace (default build); in live mode it forwards to
// the real library and detector and compares (lwnative build). Either way
// every call must match the Python call sequence: op, stream offset, push
// length and SHA-256 of the float32 samples, VAD window length and hash.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

type traceEntry struct {
	Op            string        `json:"op"`
	Context       string        `json:"context"`
	Language      *string       `json:"language"`
	Offset        int64         `json:"offset"`
	N             int           `json:"n"`
	SHA           string        `json:"sha"`
	Status        int           `json:"status"`
	Event         bool          `json:"event"`
	TextStatus    *int          `json:"text_status"`
	Text          string        `json:"text"`
	PreviewStatus *int          `json:"preview_status"`
	Preview       string        `json:"preview"`
	Regions       []vad.Segment `json:"regions"`
}

type traceResponse struct {
	Samples   int    `json:"samples"`
	Text      string `json:"text"`
	Tentative string `json:"tentative"`
}

type traceRun struct {
	Responses []traceResponse `json:"responses"`
	Final     struct {
		Text        string `json:"text"`
		Language    string `json:"language"`
		SampleCount int    `json:"sample_count"`
	} `json:"final"`
	Text     string       `json:"text"`
	Language string       `json:"language"`
	Log      []traceEntry `json:"log"`
}

type trace struct {
	Name   string `json:"-"`
	Dir    string `json:"-"`
	Source struct {
		WAV     string `json:"wav"`
		Zeros   int    `json:"zeros"`
		Samples int    `json:"samples"`
	} `json:"source"`
	Samples      int      `json:"samples"`
	Backend      string   `json:"backend"`
	ChunkSamples int      `json:"chunk_samples"`
	FeedSamples  int      `json:"feed_samples"`
	Threads      int      `json:"threads"`
	Context      string   `json:"context"`
	HotWords     []string `json:"hot_words"`
	Stream       traceRun `json:"stream"`
	Transcribe   traceRun `json:"transcribe"`
}

func loadTraces(t *testing.T, dir string) []*trace {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.trace.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out []*trace
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		tr := &trace{Name: strings.TrimSuffix(filepath.Base(p), ".trace.json"), Dir: dir}
		if err := json.Unmarshal(data, tr); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		out = append(out, tr)
	}
	return out
}

// extraTraceDirs lists LW_R2T2_TRACE_DIRS (path-list separated).
func extraTraceDirs() []string {
	var dirs []string
	for _, d := range filepath.SplitList(os.Getenv("LW_R2T2_TRACE_DIRS")) {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

func (tr *trace) audio(t *testing.T) []int16 {
	t.Helper()
	var pcm []int16
	switch {
	case tr.Source.WAV != "":
		pcm = readWAV16(t, filepath.Join(tr.Dir, tr.Source.WAV))
		if tr.Source.Samples > 0 {
			pcm = pcm[:tr.Source.Samples]
		}
	default:
		pcm = make([]int16, tr.Source.Zeros)
	}
	if len(pcm) != tr.Samples {
		t.Fatalf("%s: %d samples, trace says %d", tr.Name, len(pcm), tr.Samples)
	}
	return pcm
}

func readWAV16(t *testing.T, path string) []int16 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatalf("%s: not a WAV file", path)
	}
	for pos := 12; pos+8 <= len(data); {
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

func sha32(pcm []float32) string {
	buf := make([]byte, 4*len(pcm))
	for i, x := range pcm {
		binary.LittleEndian.PutUint32(buf[4*i:], math.Float32bits(x))
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

// checker implements CLib and Detector against an expected call log.
type checker struct {
	lib  CLib           // live mode: real audio.cpp
	det  SpeechDetector // live mode: real FireRedVAD
	log  []traceEntry
	pos  int
	free bool // accept unlogged calls (load-time CUDA warmup)

	// mismatch is the first structural divergence (op/offset/length/hash);
	// it fails the call so the Go code stops there.
	mismatch error
	// textDiffs counts live-mode pushes whose decoded event differs from
	// Python while the audio matched.
	textDiffs []string
	pushes    int
	vadCalls  int
}

func (c *checker) expect(what string) (*traceEntry, error) {
	if c.mismatch != nil {
		return nil, c.mismatch
	}
	if c.pos >= len(c.log) {
		c.mismatch = fmt.Errorf("call %d: unexpected %s (Python log has %d calls)", c.pos, what, len(c.log))
		return nil, c.mismatch
	}
	e := &c.log[c.pos]
	c.pos++
	return e, nil
}

func (c *checker) fail(format string, args ...any) error {
	c.mismatch = fmt.Errorf("call %d: %s", c.pos-1, fmt.Sprintf(format, args...))
	return c.mismatch
}

func (c *checker) load(log []traceEntry) {
	c.log, c.pos, c.mismatch, c.textDiffs, c.pushes, c.vadCalls = log, 0, nil, nil, 0, 0
}

func (c *checker) done() error {
	if c.mismatch != nil {
		return c.mismatch
	}
	if c.pos != len(c.log) {
		return fmt.Errorf("Go made %d calls, Python %d (next Python call: %+v)", c.pos, len(c.log), c.log[c.pos])
	}
	return nil
}

func (c *checker) ABIVersion() uint32 {
	if c.lib != nil {
		return c.lib.ABIVersion()
	}
	return 0x0400
}

func (c *checker) Open(cfg SessionConfig) error {
	if c.lib != nil {
		return c.lib.Open(cfg)
	}
	return nil
}

func (c *checker) Close() {
	if c.lib != nil {
		c.lib.Close()
	}
}

func (c *checker) StreamStart(context, language string) error {
	if c.free {
		return c.forwardStart(context, language)
	}
	e, err := c.expect("stream start")
	if err != nil {
		return err
	}
	lang := e.lang()
	if e.Op != "start" || e.Context != context || lang != language {
		return c.fail("stream start(%q, %q), Python %s(%q, %q)", context, language, e.Op, e.Context, lang)
	}
	return c.forwardStart(context, language)
}

func (c *checker) forwardStart(context, language string) error {
	if c.lib != nil {
		return c.lib.StreamStart(context, language)
	}
	return nil
}

func eventFromEntry(e *traceEntry) PushEvent {
	ev := PushEvent{HasEvent: e.Event, TextStatus: StatusNotAvailable, PreviewStatus: StatusNotAvailable}
	if e.TextStatus != nil {
		ev.TextStatus = *e.TextStatus
		ev.Delta = e.Text
		ev.Language = e.lang()
	}
	if e.PreviewStatus != nil {
		ev.PreviewStatus = *e.PreviewStatus
		ev.Preview = e.Preview
	}
	return ev
}

// lang is the logged language ("" when null).
func (e *traceEntry) lang() string {
	if e.Language == nil {
		return ""
	}
	return *e.Language
}

func (c *checker) StreamPush(pcm []float32, offset int64) (PushEvent, error) {
	if c.free {
		if c.lib != nil {
			return c.lib.StreamPush(pcm, offset)
		}
		return PushEvent{}, nil
	}
	e, err := c.expect("push")
	if err != nil {
		return PushEvent{}, err
	}
	c.pushes++
	if e.Op != "push" || e.Offset != offset || e.N != len(pcm) {
		return PushEvent{}, c.fail("push(offset %d, n %d), Python %s(offset %d, n %d)", offset, len(pcm), e.Op, e.Offset, e.N)
	}
	if h := sha32(pcm); h != e.SHA {
		return PushEvent{}, c.fail("push at %d: samples differ (sha %s, Python %s)", offset, h[:12], e.SHA[:12])
	}
	want := eventFromEntry(e)
	if c.lib == nil {
		return want, nil
	}
	got, err := c.lib.StreamPush(pcm, offset)
	if err != nil {
		return got, err
	}
	if !samePush(got, want) {
		c.textDiffs = append(c.textDiffs, fmt.Sprintf("push at %d: Go %+v, Python %+v", offset, got, want))
	}
	return got, nil
}

func samePush(a, b PushEvent) bool {
	if a.HasEvent != b.HasEvent || a.TextStatus != b.TextStatus {
		return false
	}
	if a.HasEvent && a.TextStatus == StatusOK && (a.Delta != b.Delta || a.Language != b.Language) {
		return false
	}
	if a.HasEvent && a.TextStatus == StatusOK {
		if a.PreviewStatus != b.PreviewStatus || (a.PreviewStatus == StatusOK && a.Preview != b.Preview) {
			return false
		}
	}
	return true
}

func (c *checker) StreamFinish() (FinishResult, error) {
	e, err := c.expect("finish")
	if err != nil {
		return FinishResult{}, err
	}
	if e.Op != "finish" {
		return FinishResult{}, c.fail("finish, Python %s", e.Op)
	}
	want := FinishResult{TextStatus: StatusNotAvailable}
	if e.TextStatus != nil {
		want = FinishResult{TextStatus: *e.TextStatus, Text: e.Text, Language: e.lang()}
	}
	if c.lib == nil {
		return want, nil
	}
	got, err := c.lib.StreamFinish()
	if err == nil && (got.TextStatus != want.TextStatus || got.Text != want.Text || got.Language != want.Language) {
		c.textDiffs = append(c.textDiffs, fmt.Sprintf("finish: Go %q/%q, Python %q/%q", got.Text, got.Language, want.Text, want.Language))
	}
	return got, err
}

func (c *checker) StreamReset() error {
	if c.free {
		if c.lib != nil {
			return c.lib.StreamReset()
		}
		return nil
	}
	e, err := c.expect("reset")
	if err != nil {
		return err
	}
	if e.Op != "reset" {
		return c.fail("reset, Python %s", e.Op)
	}
	if c.lib != nil {
		return c.lib.StreamReset()
	}
	return nil
}

func (c *checker) SpeechTimestamps(pcm []float32) ([]vad.Segment, error) {
	e, err := c.expect("VAD")
	if err != nil {
		return nil, err
	}
	c.vadCalls++
	if e.Op != "vad" || e.N != len(pcm) {
		return nil, c.fail("VAD(n %d), Python %s(n %d)", len(pcm), e.Op, e.N)
	}
	if h := sha32(pcm); h != e.SHA {
		return nil, c.fail("VAD window samples differ (sha %s, Python %s)", h[:12], e.SHA[:12])
	}
	if c.det == nil {
		return e.Regions, nil
	}
	got, err := c.det.SpeechTimestamps(pcm)
	if err != nil {
		return nil, err
	}
	if !sameRegions(got, e.Regions) {
		// Segmentation would diverge from Python from here on.
		return nil, c.fail("VAD regions %v, Python %v", got, e.Regions)
	}
	return got, nil
}

func sameRegions(a, b []vad.Segment) bool {
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

// checkerDetector adapts checker to Detector.
type checkerDetector struct{ *checker }

func (d checkerDetector) Close() error { return nil }

// newTraceBackend loads a Backend whose native library and VAD are c.
func newTraceBackend(t *testing.T, c *checker, backend string, threads int) *Backend {
	t.Helper()
	model := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(model, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	b := NewBackend(BackendOptions{
		RuntimeDir:  t.TempDir(),
		Threads:     threads,
		ModelPath:   model,
		OpenLibrary: func(string, []string) (CLib, error) { return c, nil },
		NewDetector: func() (Detector, error) { return checkerDetector{c}, nil },
	})
	c.free = true
	defer func() { c.free = false }()
	if err := b.Load(loadSpec(backend)); err != nil {
		t.Fatal(err)
	}
	return b
}

func loadSpec(backend string) asr.LoadSpec {
	return asr.LoadSpec{Engine: EngineID, Kind: asr.KindR2T2, Device: backend}
}

// traceResult summarises one compared run.
type traceResult struct {
	responses, pushes, vadCalls int
	textDiffs                   []string
}

// runStream feeds tr's audio like the Rust shell and compares every
// response and the final result with Python.
func runStream(t *testing.T, b *Backend, c *checker, tr *trace, sessionID uint64) traceResult {
	t.Helper()
	pcm := tr.audio(t)
	c.load(tr.Stream.Log)
	s, err := b.NewStream(asr.StreamOptions{SessionID: sessionID, Context: tr.Context, HotWords: tr.HotWords})
	if err != nil {
		t.Fatalf("%s stream start: %v", tr.Name, err)
	}
	defer s.Close()
	res := traceResult{}
	samples := 0
	for i, start := 0, 0; start < len(pcm); i, start = i+1, start+tr.FeedSamples {
		block := pcm[start:min(start+tr.FeedSamples, len(pcm))]
		p, err := s.Push(block)
		if err != nil {
			t.Fatalf("%s stream block %d (sample %d): %v", tr.Name, i, start, err)
		}
		samples += len(block)
		want := tr.Stream.Responses[i]
		if c.lib == nil || len(c.textDiffs) == 0 {
			if samples != want.Samples || p.Committed != want.Text || p.Tentative != want.Tentative {
				t.Fatalf("%s stream block %d: Go %q + %q at %d, Python %q + %q at %d",
					tr.Name, i, p.Committed, p.Tentative, samples, want.Text, want.Tentative, want.Samples)
			}
		}
		res.responses++
	}
	if res.responses != len(tr.Stream.Responses) {
		t.Fatalf("%s: %d responses, Python %d", tr.Name, res.responses, len(tr.Stream.Responses))
	}
	final, err := s.Finish()
	if err != nil {
		t.Fatalf("%s stream finish: %v", tr.Name, err)
	}
	if err := c.done(); err != nil {
		t.Fatalf("%s stream: %v", tr.Name, err)
	}
	if len(c.textDiffs) == 0 {
		w := tr.Stream.Final
		if final.Text != w.Text || final.Language != w.Language || final.SampleCount != w.SampleCount {
			t.Fatalf("%s stream final: Go %q (%q, %d), Python %q (%q, %d)", tr.Name,
				final.Text, final.Language, final.SampleCount, w.Text, w.Language, w.SampleCount)
		}
	}
	res.pushes, res.vadCalls, res.textDiffs = c.pushes, c.vadCalls, c.textDiffs
	return res
}

// runTranscribe compares whole-clip transcription with Python.
func runTranscribe(t *testing.T, b *Backend, c *checker, tr *trace) traceResult {
	t.Helper()
	pcm := tr.audio(t)
	c.load(tr.Transcribe.Log)
	got, err := b.Transcribe(pcm, asr.TranscribeOptions{Context: tr.Context, HotWords: tr.HotWords})
	if err != nil {
		t.Fatalf("%s transcribe: %v", tr.Name, err)
	}
	if err := c.done(); err != nil {
		t.Fatalf("%s transcribe: %v", tr.Name, err)
	}
	if len(c.textDiffs) == 0 && (got.Text != tr.Transcribe.Text || got.Language != tr.Transcribe.Language) {
		t.Fatalf("%s transcribe: Go %q (%q), Python %q (%q)", tr.Name, got.Text, got.Language, tr.Transcribe.Text, tr.Transcribe.Language)
	}
	return traceResult{pushes: c.pushes, vadCalls: c.vadCalls, textDiffs: c.textDiffs}
}
