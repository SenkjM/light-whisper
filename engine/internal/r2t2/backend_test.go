// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: GPL-3.0-only
//
// Go translation of src-tauri/resources/test_r2t2_asr_server.py,
// test_r2t2_server_wiring.py and the warmup cases of test_r2t2_startup.py
// (inherited, GPL-3.0-only; PLAN §10.4), adapted to the asr.Backend seam.

package r2t2

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

type nopDetector struct {
	SpeechDetector
	closed *int
}

func (d nopDetector) Close() error {
	if d.closed != nil {
		*d.closed++
	}
	return nil
}

// shellBackend is a loaded backend whose live session runs over native
// (FakeStreamRuntime in the Python tests).
func shellBackend(native Native) (*Backend, *fakeLib) {
	lib := newFakeLib()
	b := NewBackend(BackendOptions{})
	b.spec = loadSpec("cpu")
	b.native = &Runtime{lib: lib, backend: "cpu", chunkSamples: 4, open: true}
	b.detector = nopDetector{SpeechDetector: fixedVAD{}}
	b.stream = NewStreamSession(native)
	return b, lib
}

func TestStreamCommandsDecodePCM16AndPreserveContextLanguageAndCancel(t *testing.T) {
	rt := &streamRuntime{feedResults: [][3]string{{"hello", "zh"}, {"hello!", ""}, {"discard", "de"}}, finishResult: [2]string{"hello!", ""}}
	b, _ := shellBackend(rt)
	if info := b.Info(); !info.ModelLoaded || info.Device != "cpu" || info.Kind != asr.KindR2T2 {
		t.Fatalf("%+v", info)
	}
	s, err := b.NewStream(asr.StreamOptions{SessionID: 1, Context: "base", HotWords: []string{" foo ", "", "bar"}, Language: "   "})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rt.startCalls, [][2]string{{"base\nHotwords: foo, bar", ""}}) {
		t.Fatal(rt.startCalls)
	}
	if p, err := s.Push([]int16{0, 16384}); err != nil || p.Committed != "hello" {
		t.Fatal(p, err)
	}
	if p, err := s.Push([]int16{32767}); err != nil || p.Committed != "hello!" {
		t.Fatal(p, err)
	}
	final, err := s.Finish()
	if err != nil || final != (asr.Result{Text: "hello!", Language: "zh", SampleCount: 3}) {
		t.Fatal(final, err)
	}
	s.Close() // already finished: no cancel
	if rt.resetCalls != 0 {
		t.Fatal(rt.resetCalls)
	}

	s, err = b.NewStream(asr.StreamOptions{SessionID: 2})
	if err != nil {
		t.Fatal(err)
	}
	if p, err := s.Push([]int16{100}); err != nil || p.Committed != "discard" {
		t.Fatal(p, err)
	}
	s.Close()
	if rt.resetCalls != 1 || b.stream.Active() {
		t.Fatal("close must cancel the active session")
	}
	if rt.startCalls[1] != [2]string{"", ""} {
		t.Fatal(rt.startCalls)
	}
	if !slices.Equal(rt.feedInputs[0], []float32{0, 0.5}) || !slices.Equal(rt.feedInputs[1], []float32{32767.0 / 32768.0}) {
		t.Fatal(rt.feedInputs)
	}
}

func TestStreamRejectsStaleAndInvalidSessionIDs(t *testing.T) {
	b, _ := shellBackend(&streamRuntime{})
	if _, err := b.NewStream(asr.StreamOptions{SessionID: 0}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	s, err := b.NewStream(asr.StreamOptions{SessionID: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.NewStream(asr.StreamOptions{SessionID: 3}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if _, err := s.Push(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
}

func TestStreamBusyBlocksOfflineTranscriptionWithoutTouchingStream(t *testing.T) {
	rt := &streamRuntime{}
	b, lib := shellBackend(rt)
	if _, err := b.NewStream(asr.StreamOptions{SessionID: 1, Language: "en"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Transcribe([]int16{1, 2, 3}, asr.TranscribeOptions{}); !errors.Is(err, ErrStreamBusy) {
		t.Fatal(err)
	}
	if len(rt.feedInputs) != 0 || rt.resetCalls != 0 || len(lib.calls) != 0 {
		t.Fatal(rt.events, lib.calls)
	}
}

func TestOfflineTranscriptionFeedsTrueTailOnceAndFinishesOnce(t *testing.T) {
	rt := &streamRuntime{}
	b, lib := shellBackend(rt)
	b.detector = nopDetector{SpeechDetector: fixedVAD{[]vad.Segment{{Start: 0, End: 10}}}}
	lib.finish = FinishResult{TextStatus: StatusOK, Text: "offline", Language: "en"}
	if _, err := b.NewStream(asr.StreamOptions{SessionID: 5, Language: "en"}); err != nil {
		t.Fatal(err)
	}
	_, _ = b.stream.Cancel(5)
	pcm := make([]int16, 10)
	for i := range pcm {
		pcm[i] = int16(i * 1000)
	}
	res, err := b.Transcribe(pcm, asr.TranscribeOptions{Context: "offline", HotWords: []string{" foo ", "", "bar"}, Language: "en"})
	if err != nil || res != (asr.Result{Text: "offline", Language: "en", SampleCount: 10}) {
		t.Fatal(res, err)
	}
	if !slices.Equal(lib.starts, [][2]string{{"offline\nHotwords: foo, bar", "en"}}) || lib.finishes != 1 {
		t.Fatal(lib.starts, lib.finishes)
	}
	audio := vad.PCM16ToFloat(pcm)
	if len(lib.pushes) < 3 || !slices.Equal(lib.pushes[0].pcm, audio[:4]) || !slices.Equal(lib.pushes[1].pcm, audio[4:8]) ||
		!slices.Equal(lib.pushes[2].pcm, audio[8:]) || lib.pushes[2].offset != 8 {
		t.Fatal("clip must reach native once, in native-sized chunks")
	}
	if lib.pushedSamples() != 10+FinishContextSamples {
		t.Fatal(lib.pushedSamples())
	}
	if _, err := b.NewStream(asr.StreamOptions{SessionID: 5}); err == nil {
		t.Fatal("session ids stay consumed")
	}
	if _, err := b.NewStream(asr.StreamOptions{SessionID: 6, Language: "en"}); err != nil {
		t.Fatal(err)
	}
}

func TestOfflineNativeErrorResetsSegmented(t *testing.T) {
	b, lib := shellBackend(&streamRuntime{})
	b.detector = nopDetector{SpeechDetector: fixedVAD{[]vad.Segment{{Start: 0, End: 4}}}}
	lib.onPush = func([]float32, int64) (PushEvent, error) { return PushEvent{}, errors.New("native push failed") }
	if _, err := b.Transcribe([]int16{1, 2, 3, 4}, asr.TranscribeOptions{Context: "offline", Language: "en"}); err == nil {
		t.Fatal("want error")
	}
	if lib.resets != 1 || b.native.active {
		t.Fatal("failed transcription must reset the native stream", lib.resets)
	}
}

func TestInitializedStreamUsesVADBeforeNativeInference(t *testing.T) {
	lib := newFakeLib()
	v := &markerVAD{}
	b := NewBackend(BackendOptions{
		ModelPath:   writeTemp(t, "model", nil),
		OpenLibrary: func(string, []string) (CLib, error) { return lib, nil },
		NewDetector: func() (Detector, error) { return nopDetector{SpeechDetector: v}, nil },
	})
	if err := b.Load(loadSpec("cpu")); err != nil {
		t.Fatal(err)
	}
	s, err := b.NewStream(asr.StreamOptions{SessionID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Push(make([]int16, 5120)); err != nil {
		t.Fatal(err)
	}
	final, err := s.Finish()
	if err != nil || final.Text != "" || final.SampleCount != 5120 {
		t.Fatal(final, err)
	}
	if len(v.inputs) != 1 || len(lib.starts) != 0 || len(lib.pushes) != 0 || lib.finishes != 0 {
		t.Fatal(len(v.inputs), lib.calls)
	}
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeOpener hands out one fakeLib per backend directory.
type fakeOpener struct {
	libs   map[string]*fakeLib
	fail   map[string]error
	opened []string
	dirs   [][]string
}

func (o *fakeOpener) open(path string, dirs []string) (CLib, error) {
	backend := filepath.Base(filepath.Dir(path))
	o.opened = append(o.opened, path)
	o.dirs = append(o.dirs, dirs)
	if err := o.fail[backend]; err != nil {
		return nil, err
	}
	lib := newFakeLib()
	if o.libs == nil {
		o.libs = map[string]*fakeLib{}
	}
	o.libs[backend] = lib
	return lib, nil
}

func newLoadBackend(t *testing.T, o *fakeOpener) (*Backend, *int) {
	closed := new(int)
	return NewBackend(BackendOptions{
		RuntimeDir:  filepath.FromSlash("/res/r2t2-native"),
		DLLDirs:     []string{filepath.FromSlash("/res")},
		ModelPath:   writeTemp(t, "model.gguf", nil),
		OpenLibrary: o.open,
		NewDetector: func() (Detector, error) { return nopDetector{SpeechDetector: fixedVAD{}, closed: closed}, nil },
	}), closed
}

func TestCUDAUsesMeasured160msChunksAndSharedDLLDirectory(t *testing.T) {
	o := &fakeOpener{}
	b, _ := newLoadBackend(t, o)
	if err := b.Load(loadSpec("auto")); err != nil {
		t.Fatal(err)
	}
	cuda := filepath.FromSlash("/res/r2t2-native/cuda")
	if !slices.Equal(o.opened, []string{filepath.Join(cuda, DefaultLibraryName())}) ||
		!slices.Equal(o.dirs[0], []string{cuda, filepath.FromSlash("/res")}) {
		t.Fatal(o.opened, o.dirs)
	}
	if b.native.ChunkSamples() != 2560 || b.Info().Device != "cuda" {
		t.Fatal(b.native.ChunkSamples(), b.Info())
	}
	if opts := o.libs["cuda"].opened[0].Options; opts[0] != [2]string{"confucius4_r2t2.chunk_size_ms", "160"} {
		t.Fatal(opts)
	}
}

func TestCPUFallbackKeeps320msChunks(t *testing.T) {
	o := &fakeOpener{fail: map[string]error{"cuda": errors.New("CUDA unavailable")}}
	b, _ := newLoadBackend(t, o)
	if err := b.Load(loadSpec("auto")); err != nil {
		t.Fatal(err)
	}
	if len(o.opened) != 2 || b.native.ChunkSamples() != 5120 || b.Info().Device != "cpu" {
		t.Fatal(o.opened, b.native.ChunkSamples())
	}
	if len(o.libs["cpu"].pushes) != 0 {
		t.Fatal("CPU runtime is not warmed up")
	}
}

func TestDeviceCPUSkipsCUDA(t *testing.T) {
	o := &fakeOpener{}
	b, _ := newLoadBackend(t, o)
	if err := b.Load(loadSpec("cpu")); err != nil {
		t.Fatal(err)
	}
	if len(o.opened) != 1 || !strings.Contains(o.opened[0], "cpu") {
		t.Fatal(o.opened)
	}
}

func TestCUDAWarmupIsExactAndReadyRuntimeIsAssignedAfterReset(t *testing.T) {
	o := &fakeOpener{}
	b, _ := newLoadBackend(t, o)
	if err := b.Load(loadSpec("auto")); err != nil {
		t.Fatal(err)
	}
	lib := o.libs["cuda"]
	if !slices.Equal(lib.calls, []string{"abi_version", "open", "stream_start", "stream_push", "stream_reset"}) {
		t.Fatal(lib.calls)
	}
	if p := lib.pushes[0]; p.offset != 0 || len(p.pcm) != 2560 || !zerosOK(p.pcm) {
		t.Fatal(p.offset, len(p.pcm))
	}
	if lib.starts[0] != [2]string{"", ""} || b.native.active || b.native.offset != 0 {
		t.Fatal("warmup must leave an idle runtime")
	}
}

func TestCUDAWarmupFailuresCloseCUDAThenUseUnwarmedCPU(t *testing.T) {
	for stage, want := range map[string][]string{
		"start": {"abi_version", "open", "stream_start", "close"},
		"feed":  {"abi_version", "open", "stream_start", "stream_push", "stream_reset", "close"},
		"reset": {"abi_version", "open", "stream_start", "stream_push", "stream_reset", "close"},
	} {
		o := &fakeOpener{}
		inner := o.open
		var cudaLib *fakeLib
		b, _ := newLoadBackend(t, o)
		b.opts.OpenLibrary = func(path string, dirs []string) (CLib, error) {
			lib, err := inner(path, dirs)
			if err == nil && strings.Contains(path, "cuda") {
				cudaLib = lib.(*fakeLib)
				fail := errors.New("warmup " + stage + " failed")
				switch stage {
				case "start":
					cudaLib.startErr = fail
				case "feed":
					cudaLib.onPush = func([]float32, int64) (PushEvent, error) { return PushEvent{}, fail }
				case "reset":
					cudaLib.resetErr = fail
				}
			}
			return lib, err
		}
		if err := b.Load(loadSpec("auto")); err != nil {
			t.Fatal(stage, err)
		}
		if !slices.Equal(cudaLib.calls, want) {
			t.Errorf("%s: cuda calls %v, want %v", stage, cudaLib.calls, want)
		}
		if cpu := o.libs["cpu"]; !slices.Equal(cpu.calls, []string{"abi_version", "open"}) || b.Info().Device != "cpu" {
			t.Errorf("%s: cpu calls %v", stage, cpu.calls)
		}
	}
}

func TestInitializeMissingModelReturnsModelsNotDownloaded(t *testing.T) {
	o := &fakeOpener{}
	b := NewBackend(BackendOptions{HubCache: t.TempDir(), OpenLibrary: o.open,
		NewDetector: func() (Detector, error) { t.Fatal("no VAD for a missing model"); return nil, nil }})
	if err := b.Load(loadSpec("auto")); !errors.Is(err, ErrModelMissing) {
		t.Fatal(err)
	}
	info := b.Info()
	if info.ModelLoaded || !slices.Equal(info.MissingModels, []string{EngineID}) || len(o.opened) != 0 {
		t.Fatalf("%+v %v", info, o.opened)
	}
	if _, err := b.NewStream(asr.StreamOptions{SessionID: 1}); !errors.Is(err, asr.ErrNotLoaded) {
		t.Fatal(err)
	}
	if _, err := b.Transcribe([]int16{1}, asr.TranscribeOptions{}); !errors.Is(err, asr.ErrNotLoaded) {
		t.Fatal(err)
	}
}

func TestPinnedModelIsVerifiedBySizeAndSHA256(t *testing.T) {
	saved := Model
	defer func() { Model = saved }()
	data := []byte("not really a GGUF")
	sum := sha256.Sum256(data)
	Model.Size, Model.SHA256 = int64(len(data)), hex.EncodeToString(sum[:])
	hub := t.TempDir()
	path := ModelPath(hub)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.ToSlash(path), "models--davidxifeng--Confucius4-R2T2-gguf/snapshots/"+saved.Revision+"/r2t2-q8_0.gguf") {
		t.Fatal(path)
	}
	o := &fakeOpener{}
	b := NewBackend(BackendOptions{OpenLibrary: o.open, NewDetector: func() (Detector, error) { return nopDetector{SpeechDetector: fixedVAD{}}, nil }})
	if err := b.Load(asr.LoadSpec{Engine: EngineID, Kind: asr.KindR2T2, ModelsDir: hub, Device: "cpu"}); err != nil {
		t.Fatal(err)
	}
	if o.libs["cpu"].opened[0].ModelPath != path {
		t.Fatal(o.libs["cpu"].opened[0].ModelPath)
	}
	Model.SHA256 = strings.Repeat("0", 64)
	b2 := NewBackend(BackendOptions{OpenLibrary: o.open, NewDetector: func() (Detector, error) { return nopDetector{SpeechDetector: fixedVAD{}}, nil }})
	if err := b2.Load(asr.LoadSpec{Engine: EngineID, Kind: asr.KindR2T2, ModelsDir: hub, Device: "cpu"}); !errors.Is(err, ErrModelMissing) {
		t.Fatal(err)
	}
}

func TestHubCacheRootPrecedence(t *testing.T) {
	t.Setenv("HF_HUB_CACHE", "/hub")
	t.Setenv("HF_HOME", "/home-hf")
	if HubCacheRoot() != "/hub" {
		t.Fatal(HubCacheRoot())
	}
	t.Setenv("HF_HUB_CACHE", "")
	if HubCacheRoot() != filepath.Join("/home-hf", "hub") {
		t.Fatal(HubCacheRoot())
	}
}

func TestPartialInitializeClosesNative(t *testing.T) {
	o := &fakeOpener{}
	b := NewBackend(BackendOptions{ModelPath: writeTemp(t, "m", nil), OpenLibrary: o.open,
		NewDetector: func() (Detector, error) { return nil, errors.New("vad unavailable") }})
	if err := b.Load(loadSpec("cpu")); err == nil || !strings.Contains(err.Error(), "vad unavailable") {
		t.Fatal(err)
	}
	if o.libs["cpu"].closed != 1 || b.Info().ModelLoaded {
		t.Fatal(o.libs["cpu"].closed, b.Info())
	}
}

func TestUnloadClosesRuntimeAndVADAndReloads(t *testing.T) {
	o := &fakeOpener{}
	b, closed := newLoadBackend(t, o)
	if err := b.Load(loadSpec("cpu")); err != nil {
		t.Fatal(err)
	}
	if err := b.Load(loadSpec("cpu")); err != nil || len(o.opened) != 1 {
		t.Fatal("same spec must not reload", err, o.opened)
	}
	lib := o.libs["cpu"]
	if err := b.Unload(); err != nil {
		t.Fatal(err)
	}
	if lib.closed != 1 || *closed != 1 || b.Info().ModelLoaded {
		t.Fatal(lib.closed, *closed)
	}
	if err := b.Load(loadSpec("cpu")); err != nil || len(o.opened) != 2 {
		t.Fatal(err, o.opened)
	}
}

func TestQwen3IsNotImplementedYet(t *testing.T) {
	b := NewBackend(BackendOptions{})
	if err := b.Load(asr.LoadSpec{Engine: "qwen3-asr", Kind: asr.KindQwen3}); !errors.Is(err, ErrQwen3NotImplemented) {
		t.Fatal(err)
	}
}

func TestComposeContextAndNormalizeLanguage(t *testing.T) {
	for _, tc := range []struct {
		ctx   string
		words []string
		want  string
	}{
		{"", nil, ""},
		{" base ", nil, "base"},
		{"", []string{" a ", "", "b"}, "Hotwords: a, b"},
		{"base", []string{"x"}, "base\nHotwords: x"},
	} {
		if got := ComposeContext(tc.ctx, tc.words); got != tc.want {
			t.Errorf("%q %q → %q", tc.ctx, tc.words, got)
		}
	}
	if NormalizeLanguage("  zh ") != "zh" || NormalizeLanguage("   ") != "" {
		t.Fatal("language trim")
	}
}
