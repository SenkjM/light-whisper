// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: GPL-3.0-only
//
// Ports of src-tauri/resources/test_qwen3_asr_server.py (inherited
// light-whisper tests, GPL-3.0-only) plus tests of the other ported
// qwen3_asr_server.py / hf_cache_utils.py logic.

package qwen3

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

type harness struct {
	b       *Backend
	lib     *fakeLib
	vad     *fakeVAD
	vadNews int
}

func newHarness(t *testing.T, chunks []vad.Segment) *harness {
	t.Helper()
	h := &harness{lib: &fakeLib{text: "测试文本", lang: "zh"}, vad: &fakeVAD{chunks: chunks}}
	model := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(model, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.b = NewBackend(BackendOptions{
		LibraryPath: "/lib/libtranscribe.so",
		ModelPath:   model,
		OpenLibrary: func(string, []string) (CLib, error) { return h.lib, nil },
		NewDetector: func() (Detector, error) { h.vadNews++; return h.vad, nil },
		HasNVIDIA:   func() bool { return true },
	})
	return h
}

var spec = asr.LoadSpec{Engine: EngineID, Kind: asr.KindQwen3, Device: "auto"}

func pcmRange(n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(i)
	}
	return out
}

// test_reuses_one_model_and_session_for_inline_pcm_requests
func TestReusesOneModelAndSessionForRequests(t *testing.T) {
	h := newHarness(t, []vad.Segment{{Start: 0, End: 16000}})
	if err := h.b.Load(spec); err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, 16000)
	first, err1 := h.b.Transcribe(pcm, asr.TranscribeOptions{})
	second, err2 := h.b.Transcribe(pcm, asr.TranscribeOptions{})
	if err1 != nil || err2 != nil || first.Text != "测试文本" || second.Text != "测试文本" || first.Language != "zh" {
		t.Fatal(first, second, err1, err2)
	}
	if len(h.lib.opens) != 1 || h.lib.configs[0] != (SessionConfig{KVType: "f16", NCtx: 32_768}) {
		t.Fatalf("opens %v configs %+v", h.lib.opens, h.lib.configs)
	}
	// warmup + two requests on the one session
	if s := h.lib.sessions[0]; len(s.inputs) != 3 || len(s.inputs[0]) != 16000 {
		t.Fatalf("runs %d", len(s.inputs))
	}
	if h.vad.warm != 1 {
		t.Fatal("VAD not warmed up")
	}
	if info := h.b.Info(); !info.ModelLoaded || info.Engine != EngineID || info.Device != "cuda" || info.Kind != asr.KindQwen3 {
		t.Fatalf("%+v", info)
	}
}

// test_vad_rejects_silence_without_running_qwen
func TestVADRejectsSilenceWithoutRunningQwen(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.b.Load(spec); err != nil {
		t.Fatal(err)
	}
	res, err := h.b.Transcribe(make([]int16, 16000), asr.TranscribeOptions{})
	if err != nil || res.Text != "" || res.Language != "unknown" || res.SampleCount != 16000 {
		t.Fatal(res, err)
	}
	if n := len(h.lib.sessions[0].inputs); n != 1 { // warmup only
		t.Fatalf("Qwen ran %d times", n-1)
	}
	if len(h.vad.calls) != 1 {
		t.Fatal("VAD not consulted")
	}
}

// test_vad_trims_only_outer_silence_before_qwen
func TestVADTrimsOnlyOuterSilenceBeforeQwen(t *testing.T) {
	h := newHarness(t, []vad.Segment{{Start: 1600, End: 6400}, {Start: 9600, End: 14400}})
	if err := h.b.Load(spec); err != nil {
		t.Fatal(err)
	}
	if _, err := h.b.Transcribe(pcmRange(16000), asr.TranscribeOptions{}); err != nil {
		t.Fatal(err)
	}
	sent := h.lib.sessions[0].inputs[1]
	if len(sent) != 12_800 {
		t.Fatalf("sent %d samples", len(sent))
	}
	if math.Abs(float64(sent[0])-1600.0/32768) > 1e-9 || math.Abs(float64(sent[len(sent)-1])-14399.0/32768) > 1e-9 {
		t.Fatal(sent[0], sent[len(sent)-1])
	}
}

// test_idle_unload_closes_runtime_and_keeps_vad
func TestIdleUnloadClosesRuntimeAndKeepsVAD(t *testing.T) {
	h := newHarness(t, []vad.Segment{{Start: 0, End: 16000}})
	if err := h.b.Load(spec); err != nil {
		t.Fatal(err)
	}
	s := h.lib.sessions[0]
	if err := h.b.Unload(); err != nil {
		t.Fatal(err)
	}
	if !s.closed || h.vad.closed || h.b.Info().ModelLoaded {
		t.Fatalf("closed=%v vadClosed=%v", s.closed, h.vad.closed)
	}
	if _, err := h.b.Transcribe(make([]int16, 16000), asr.TranscribeOptions{}); !errors.Is(err, asr.ErrNotLoaded) {
		t.Fatal(err)
	}
	if err := h.b.Load(spec); err != nil {
		t.Fatal(err)
	}
	if h.vadNews != 1 || len(h.lib.opens) != 2 {
		t.Fatalf("VAD recreated %d times, opens %v", h.vadNews, h.lib.opens)
	}
	_ = h.b.Close()
	if !h.vad.closed {
		t.Fatal("Close must free the VAD")
	}
}

func TestShortClipSkipsVADAndModel(t *testing.T) {
	h := newHarness(t, []vad.Segment{{Start: 0, End: 7999}})
	_ = h.b.Load(spec)
	res, err := h.b.Transcribe(make([]int16, MinClipSamples-1), asr.TranscribeOptions{})
	if err != nil || res.Text != "" || res.Language != "" || len(h.vad.calls) != 0 || len(h.lib.sessions[0].inputs) != 1 {
		t.Fatal(res, err, len(h.vad.calls))
	}
	if _, err := h.b.Transcribe(make([]int16, MinClipSamples), asr.TranscribeOptions{}); err != nil || len(h.vad.calls) != 1 {
		t.Fatal("0.5 s exactly must be recognized", err)
	}
}

func TestTextStrippedAndLanguageDefaults(t *testing.T) {
	h := newHarness(t, []vad.Segment{{Start: 0, End: 16000}})
	h.lib.text, h.lib.lang = "  你好。\n", ""
	_ = h.b.Load(spec)
	res, err := h.b.Transcribe(make([]int16, 16000), asr.TranscribeOptions{Language: "en"})
	if err != nil || res.Text != "你好。" || res.Language != "unknown" {
		t.Fatal(res, err)
	}
}

func TestPreferredBackendsAndFallback(t *testing.T) {
	for _, tc := range []struct {
		device string
		nvidia bool
		want   []string
	}{
		{"auto", true, []string{"cuda", "vulkan", "cpu"}},
		{"auto", false, []string{"auto", "cpu"}},
		{"cuda", false, []string{"cuda", "vulkan", "cpu"}},
		{"vulkan", true, []string{"vulkan", "cpu"}},
		{"cpu", true, []string{"cpu"}},
	} {
		got := PreferredBackends(tc.device, tc.nvidia)
		if len(got) != len(tc.want) {
			t.Fatalf("%v: %v", tc, got)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%v: %v", tc, got)
			}
		}
	}
	h := newHarness(t, nil)
	h.lib.fail = map[string]error{"cuda": errors.New("no cuda")}
	if err := h.b.Load(spec); err != nil {
		t.Fatal(err)
	}
	if h.b.Info().Device != "vulkan" || len(h.lib.opens) != 2 {
		t.Fatal(h.lib.opens)
	}
	_ = h.b.Unload()
	h.lib.fail = map[string]error{"cuda": errors.New("a"), "vulkan": errors.New("b"), "cpu": errors.New("c")}
	err := h.b.Load(spec)
	if err == nil || h.b.Info().ModelLoaded {
		t.Fatal("expected failure")
	}
	for _, s := range []string{"cuda: a", "vulkan: b", "cpu: c"} {
		if !contains(err.Error(), s) {
			t.Fatalf("%v lacks %q", err, s)
		}
	}
	if err := h.b.Load(asr.LoadSpec{Kind: asr.KindR2T2}); !errors.Is(err, asr.ErrWrongKind) {
		t.Fatal(err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, p string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindSnapshotFile(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "models--handy-computer--Qwen3-ASR-0.6B-gguf")
	if _, ok := FindSnapshotFile(root, Model.RepoID, Model.Filename); ok {
		t.Fatal("found in empty cache")
	}
	// Too small to be a weight file.
	writeFile(t, filepath.Join(repo, "snapshots", "aaa", Model.Filename), 100)
	if _, ok := FindSnapshotFile(root, Model.RepoID, Model.Filename); ok {
		t.Fatal("accepted a tiny file")
	}
	// Legacy snapshot without manifest.
	writeFile(t, filepath.Join(repo, "snapshots", "bbb", Model.Filename), minWeightSize)
	if p, ok := FindSnapshotFile(root, Model.RepoID, Model.Filename); !ok || filepath.Base(filepath.Dir(p)) != "bbb" {
		t.Fatal(p, ok)
	}
	// refs/main wins, but its manifest must match the size.
	writeFile(t, filepath.Join(repo, "snapshots", "ccc", Model.Filename), minWeightSize+1)
	writeFile(t, filepath.Join(repo, "refs", "main"), 0)
	_ = os.WriteFile(filepath.Join(repo, "refs", "main"), []byte("ccc\n"), 0o644)
	manifest := filepath.Join(repo, "snapshots", "ccc", completeManifest)
	_ = os.WriteFile(manifest, []byte(`{"files":[{"path":"`+Model.Filename+`","size":5}]}`), 0o644)
	if p, _ := FindSnapshotFile(root, Model.RepoID, Model.Filename); filepath.Base(filepath.Dir(p)) != "bbb" {
		t.Fatalf("size-mismatched manifest accepted: %s", p)
	}
	_ = os.WriteFile(manifest, []byte(`{"files":[{"path":"`+Model.Filename+`","size":1000001}]}`), 0o644)
	if p, _ := FindSnapshotFile(root, Model.RepoID, Model.Filename); filepath.Base(filepath.Dir(p)) != "ccc" {
		t.Fatalf("refs/main snapshot not preferred: %s", p)
	}

	b := NewBackend(BackendOptions{HubCache: t.TempDir(), OpenLibrary: func(string, []string) (CLib, error) { return &fakeLib{}, nil }})
	if err := b.Load(spec); !errors.Is(err, ErrModelMissing) || len(b.Info().MissingModels) != 1 {
		t.Fatal(err, b.Info())
	}
	spec2 := spec
	spec2.ModelsDir = root
	b.opts.NewDetector = func() (Detector, error) { return &fakeVAD{}, nil }
	if err := b.Load(spec2); err != nil || len(b.Info().MissingModels) != 0 {
		t.Fatal(err)
	}
}
