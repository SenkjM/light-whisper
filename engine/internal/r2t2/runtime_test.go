// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: GPL-3.0-only
//
// Go translation of src-tauri/resources/test_r2t2_native_preview.py and the
// runtime cases of test_r2t2_startup.py (inherited, GPL-3.0-only; PLAN §10.4).

package r2t2

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
)

func zerosOK(pcm []float32) bool {
	for _, x := range pcm {
		if x != 0 {
			return false
		}
	}
	return true
}

func finishingRuntime(chunk int, captured int64) (*Runtime, *fakeLib) {
	lib := newFakeLib()
	r := activeRuntime(lib, chunk, false)
	r.offset = captured
	if captured != 0 {
		r.committed = "之前有顾客"
	}
	r.preview = r.committed
	r.language = "Chinese"
	lib.onPush = func([]float32, int64) (PushEvent, error) { return PushEvent{}, nil }
	lib.finish = FinishResult{TextStatus: StatusOK, Language: "Chinese"}
	return r, lib
}

func TestFinishDecodesSentenceTailWithoutWaitingForNewMicrophoneAudio(t *testing.T) {
	for _, chunk := range []int{2560, 5120} {
		r, lib := finishingRuntime(chunk, 9000)
		lib.finish.Text = "之前有顾客自己带"
		text, lang, err := r.Finish()
		if err != nil || text != "之前有顾客自己带" || lang != "Chinese" {
			t.Fatal(chunk, text, lang, err)
		}
		if lib.pushedSamples() != FinishContextSamples {
			t.Fatal(lib.pushedSamples())
		}
		offset := int64(9000)
		for _, p := range lib.pushes {
			if p.offset != offset || len(p.pcm) > chunk || !zerosOK(p.pcm) {
				t.Fatalf("chunk %d push at %d len %d", chunk, p.offset, len(p.pcm))
			}
			offset += int64(len(p.pcm))
		}
		if lib.calls[len(lib.calls)-1] != "stream_finish" || r.active || r.PreviewText() != "" || lib.finishes != 1 {
			t.Fatal(lib.calls, r.active)
		}
	}
}

func TestEmptyFinishDoesNotInventAudio(t *testing.T) {
	r, lib := finishingRuntime(2560, 0)
	text, lang, err := r.Finish()
	if err != nil || text != "" || lang != "Chinese" {
		t.Fatal(text, lang, err)
	}
	if len(lib.pushes) != 0 || !slices.Equal(lib.calls, []string{"stream_finish"}) {
		t.Fatal(lib.calls)
	}
}

func TestFinishDoesNotPublishAStaleResultAfterFlushFailure(t *testing.T) {
	r, lib := finishingRuntime(2560, 9000)
	lib.onPush = func([]float32, int64) (PushEvent, error) { return PushEvent{}, errors.New("audio flush failed") }
	if _, _, err := r.Finish(); err == nil || !strings.Contains(err.Error(), "audio flush failed") {
		t.Fatal(err)
	}
	if !slices.Equal(lib.calls, []string{"stream_push"}) {
		t.Fatal(lib.calls)
	}
}

func TestRejectsOldABIBeforeResolvingNewSymbols(t *testing.T) {
	lib := newFakeLib()
	lib.abi = 0x0200
	_, err := NewRuntime(lib, "model.gguf", RuntimeOptions{Backend: "cuda", ChunkMs: 160, Rolling: true})
	if err == nil || !strings.Contains(err.Error(), "Unsupported audio.cpp ABI") {
		t.Fatal(err)
	}
	if !slices.Equal(lib.calls, []string{"abi_version", "close"}) {
		t.Fatal(lib.calls)
	}
	lib = newFakeLib()
	lib.abi = 0x010400
	if _, err := NewRuntime(lib, "m", RuntimeOptions{Backend: "cpu", ChunkMs: 320}); err == nil {
		t.Fatal("major != 0 must be rejected")
	}
}

func TestPartialAudioWithoutADecodedEventRetainsPreview(t *testing.T) {
	lib := newFakeLib()
	r := activeRuntime(lib, 5120, false)
	r.committed, r.preview, r.language = "明天", "明天去上海", "Chinese"
	lib.onPush = func([]float32, int64) (PushEvent, error) {
		return PushEvent{HasEvent: true, TextStatus: StatusNotAvailable, PreviewStatus: StatusNotAvailable}, nil
	}
	text, _, err := r.Feed(make([]float32, 1))
	if err != nil || text != "明天" || r.PreviewText() != "明天去上海" {
		t.Fatal(text, r.PreviewText(), err)
	}
}

func TestReadsLatestPreviewEvenWhenCommittedDeltaIsEmpty(t *testing.T) {
	lib := newFakeLib()
	r := activeRuntime(lib, 5120, false)
	snapshots := [][2]string{{"", "明天去上海"}, {"明天去", "明天去上班"}, {"", "明天去"}}
	i := 0
	lib.onPush = func([]float32, int64) (PushEvent, error) {
		s := snapshots[i]
		i++
		return PushEvent{HasEvent: true, TextStatus: StatusOK, Delta: s[0], Language: "Chinese", PreviewStatus: StatusOK, Preview: s[1]}, nil
	}
	for _, want := range [][2]string{{"", "明天去上海"}, {"明天去", "明天去上班"}, {"明天去", "明天去"}} {
		text, _, err := r.Feed(make([]float32, 5120))
		if err != nil || text != want[0] || r.PreviewText() != want[1] {
			t.Fatal(text, r.PreviewText(), err)
		}
	}
}

func TestPreviewConflictAndPreviewStatusErrors(t *testing.T) {
	lib := newFakeLib()
	r := activeRuntime(lib, 5120, false)
	lib.onPush = func([]float32, int64) (PushEvent, error) {
		return PushEvent{HasEvent: true, TextStatus: StatusOK, Delta: "abc", PreviewStatus: StatusOK, Preview: "xbc"}, nil
	}
	if _, _, err := r.Feed(make([]float32, 10)); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatal(err)
	}
	lib.onPush = func([]float32, int64) (PushEvent, error) {
		return PushEvent{HasEvent: true, TextStatus: 3, Detail: "boom"}, nil
	}
	var se *StatusError
	if _, _, err := r.Feed(make([]float32, 10)); !errors.As(err, &se) || se.Status != 3 || se.Call != "result_text" {
		t.Fatal(err)
	}
}

// test_r2t2_startup.py

func TestConstructorPreservesBackendAndRealBindEnablesOnlySupportedCUDA(t *testing.T) {
	lib := newFakeLib()
	r, err := NewRuntime(lib, "model.gguf", RuntimeOptions{Backend: "cuda", ChunkMs: 160, Rolling: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Backend() != "cuda" || !r.batchInitialAudio || r.ChunkSamples() != 2560 {
		t.Fatal(r.Backend(), r.batchInitialAudio, r.ChunkSamples())
	}
	cfg := lib.opened[0]
	want := [][2]string{{"confucius4_r2t2.chunk_size_ms", "160"}, {"confucius4_r2t2.rolling_window", "true"}, {"confucius4_r2t2.rollback_punctuation", "true"}}
	if cfg.ModelPath != "model.gguf" || cfg.Backend != "cuda" || cfg.Threads != 4 || !slices.Equal(cfg.Options, want) {
		t.Fatalf("%+v", cfg)
	}
}

func TestRealBindAccepts03AndGatesInitialBatchByBackendAndRolling(t *testing.T) {
	for _, tc := range []struct {
		abi      uint32
		backend  string
		rolling  bool
		expected bool
	}{
		{0x0300, "cuda", true, false},
		{0x0400, "cuda", true, true},
		{0x0400, "cpu", true, false},
		{0x0400, "cuda", false, false},
	} {
		lib := newFakeLib()
		lib.abi = tc.abi
		r, err := NewRuntime(lib, "m", RuntimeOptions{Backend: tc.backend, ChunkMs: 160, Rolling: tc.rolling})
		if err != nil {
			t.Fatal(err)
		}
		if r.batchInitialAudio != tc.expected {
			t.Errorf("%+v: batch %v", tc, r.batchInitialAudio)
		}
		if !tc.rolling && len(lib.opened[0].Options) != 1 {
			t.Errorf("non-rolling options %v", lib.opened[0].Options)
		}
	}
}

func TestRuntimeRejectsBadOptions(t *testing.T) {
	for _, o := range []RuntimeOptions{{Backend: "vulkan", ChunkMs: 160}, {Backend: "cpu", ChunkMs: 79}, {Backend: "cpu", ChunkMs: 2001}} {
		lib := newFakeLib()
		if _, err := NewRuntime(lib, "m", o); !errors.Is(err, ErrInvalidArgument) || lib.closed != 1 {
			t.Errorf("%+v: %v closed=%d", o, err, lib.closed)
		}
	}
	lib := newFakeLib()
	lib.openErr = errors.New("model load failed")
	if _, err := NewRuntime(lib, "m", RuntimeOptions{Backend: "cpu", ChunkMs: 320}); err == nil || lib.closed != 1 {
		t.Fatal(err, lib.closed)
	}
}

// deltaLib mirrors RecordingPushFunctions: every push emits an event whose
// delta comes from deltas in order, with preview = committed so far.
func deltaLib(deltas ...string) *fakeLib {
	lib := newFakeLib()
	committed, i, resets := "", 0, 0
	lib.onPush = func([]float32, int64) (PushEvent, error) {
		if lib.resets != resets { // stream_reset clears the native hypothesis
			resets, committed = lib.resets, ""
		}
		d := ""
		if i < len(deltas) {
			d = deltas[i]
		}
		i++
		committed += d
		return PushEvent{HasEvent: true, TextStatus: StatusOK, Delta: d, Language: "en", PreviewStatus: StatusOK, Preview: committed}, nil
	}
	return lib
}

func arange(n int, base float32) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(i) + base
	}
	return out
}

func TestAlignedFirstFeedBatchesOnceThenLaterFeedsSplitAndKeepDeltas(t *testing.T) {
	lib := deltaLib("首段", "后半", "尾段")
	r := activeRuntime(lib, 4000, true)
	first, second := arange(8000, 0), arange(8000, 10000)
	if text, lang, err := r.Feed(first); err != nil || text != "首段" || lang != "en" {
		t.Fatal(text, lang, err)
	}
	if len(lib.pushes) != 1 || lib.pushes[0].offset != 0 || !slices.Equal(lib.pushes[0].pcm, first) {
		t.Fatal(len(lib.pushes))
	}
	if text, lang, err := r.Feed(second); err != nil || text != "首段后半尾段" || lang != "en" {
		t.Fatal(text, lang, err)
	}
	var offsets []int64
	var lens []int
	for _, p := range lib.pushes {
		offsets = append(offsets, p.offset)
		lens = append(lens, len(p.pcm))
	}
	if !slices.Equal(offsets, []int64{0, 8000, 12000}) || !slices.Equal(lens, []int{8000, 4000, 4000}) {
		t.Fatal(offsets, lens)
	}
	if !slices.Equal(lib.pushes[1].pcm, second[:4000]) || !slices.Equal(lib.pushes[2].pcm, second[4000:]) {
		t.Fatal("split pushes must carry the original samples")
	}
}

func TestBatchBoundariesAndLegacyKeepRegularSplitting(t *testing.T) {
	for _, tc := range []struct{ length, pushes int }{{4001, 2}, {8000, 1}, {16000, 1}, {16001, 5}} {
		lib := deltaLib()
		r := activeRuntime(lib, 4000, true)
		if _, _, err := r.Feed(make([]float32, tc.length)); err != nil {
			t.Fatal(err)
		}
		if len(lib.pushes) != tc.pushes {
			t.Errorf("length %d: %d pushes, want %d", tc.length, len(lib.pushes), tc.pushes)
		}
	}
	lib := deltaLib("a", "b")
	r := activeRuntime(lib, 4000, false)
	if text, lang, err := r.Feed(ones(8000)); err != nil || text != "ab" || lang != "en" {
		t.Fatal(text, lang, err)
	}
	if lib.pushes[0].offset != 0 || lib.pushes[1].offset != 4000 {
		t.Fatal(lib.pushes[0].offset, lib.pushes[1].offset)
	}
}

func TestInvalidPCMIsRejectedBeforePushAndDoesNotConsumeFirstBatch(t *testing.T) {
	lib := deltaLib("ok")
	r := activeRuntime(lib, 4000, true)
	for _, bad := range [][]float32{{float32(math.NaN())}, {float32(math.Inf(1))}} {
		if _, _, err := r.Feed(bad); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	if len(lib.pushes) != 0 || r.offset != 0 {
		t.Fatal(len(lib.pushes), r.offset)
	}
	if _, _, err := r.Feed(ones(8000)); err != nil || len(lib.pushes) != 1 || lib.pushes[0].offset != 0 {
		t.Fatal(err, len(lib.pushes))
	}
}

func TestResetAndStartReenableFirstOffsetZeroBatch(t *testing.T) {
	lib := deltaLib("first", "again")
	r := activeRuntime(lib, 4000, true)
	if _, _, err := r.Feed(ones(8000)); err != nil {
		t.Fatal(err)
	}
	if err := r.Reset(); err != nil {
		t.Fatal(err)
	}
	if err := r.Start("", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Feed(ones(8000)); err != nil {
		t.Fatal(err)
	}
	if lib.resets != 1 || len(lib.starts) != 1 {
		t.Fatal(lib.resets, lib.starts)
	}
	if len(lib.pushes) != 2 || lib.pushes[0].offset != 0 || lib.pushes[1].offset != 0 ||
		len(lib.pushes[0].pcm) != 8000 || len(lib.pushes[1].pcm) != 8000 {
		t.Fatal(len(lib.pushes))
	}
}

func TestInactiveAndClosedRuntimeErrors(t *testing.T) {
	lib := newFakeLib()
	r := activeRuntime(lib, 4000, false)
	r.active = false
	if _, _, err := r.Feed(ones(1)); err == nil {
		t.Fatal("feed without stream")
	}
	if _, _, err := r.Finish(); err == nil {
		t.Fatal("finish without stream")
	}
	if err := r.Reset(); err != nil || lib.resets != 0 {
		t.Fatal("reset of an inactive stream must not reach native")
	}
	r.Close()
	r.Close()
	if lib.closed != 1 {
		t.Fatal(lib.closed)
	}
	if err := r.Start("", ""); err == nil {
		t.Fatal("start after close")
	}
}
