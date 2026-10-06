// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: GPL-3.0-only
//
// Go translation of src-tauri/resources/test_r2t2_segmented.py,
// test_r2t2_rolling_segmentation.py, test_r2t2_segment_spacing.py and the
// segmented cases of test_r2t2_preview.py (inherited, GPL-3.0-only; PLAN §10.4).

package r2t2

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

func segOpts(over func(*SegmentOptions)) SegmentOptions {
	o := SegmentOptions{ChunkSamples: 4, WindowSamples: 6, MinSegmentSamples: 100, MaxSegmentSamples: 100, PauseSamples: 100}
	if over != nil {
		over(&o)
	}
	return o
}

func repeat(r [2]string, n int) [][2]string {
	out := make([][2]string, n)
	for i := range out {
		out[i] = r
	}
	return out
}

func mustFeed(t *testing.T, s *Segmented, pcm []float32) (string, string) {
	t.Helper()
	text, lang, err := s.Feed(pcm)
	if err != nil {
		t.Fatal(err)
	}
	return text, lang
}

func mustFinish(t *testing.T, s *Segmented) (string, string) {
	t.Helper()
	text, lang, err := s.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return text, lang
}

func concat(parts [][]float32) []float32 {
	var out []float32
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestVADRunsOnlyWhenItCanStartOrEndASegment(t *testing.T) {
	native := &recordingNative{feedResults: repeat([2]string{"speech", "en"}, 4), finishResults: [][2]string{{"speech", "en"}}}
	v := &markerVAD{}
	s := NewSegmented(native, v, segOpts(func(o *SegmentOptions) { o.MinSegmentSamples = 16; o.PauseSamples = 4 }))
	_ = s.Start("", "")
	mustFeed(t, s, audio(1, 1, 1, 1))
	mustFeed(t, s, make([]float32, 8))
	if len(v.inputs) != 1 || native.finishCalls != 0 {
		t.Fatalf("vad calls %d finish %d", len(v.inputs), native.finishCalls)
	}
	mustFeed(t, s, make([]float32, 4))
	if len(v.inputs) != 2 || !slices.Equal(v.inputs[1], make([]float32, 6)) || native.finishCalls != 1 {
		t.Fatalf("vad %v finish %d", v.inputs, native.finishCalls)
	}
	if got := concat(native.feedInputs); !slices.Equal(got, append(ones(4), make([]float32, 12)...)) {
		t.Fatalf("native inputs %v", got)
	}
	if text, lang := mustFinish(t, s); text != "speech" || lang != "en" {
		t.Fatal(text, lang)
	}
}

func TestPrerollAndIncompleteTailAreForwardedOnceWithoutPadding(t *testing.T) {
	native := &recordingNative{feedResults: [][2]string{{"stable", "en"}, {"stable tail", ""}}, finishResults: [][2]string{{"stable tail", ""}}}
	v := &markerVAD{}
	s := NewSegmented(native, v, segOpts(nil))
	_ = s.Start("ctx", "hint")
	mustFeed(t, s, audio(0, 0, 0, 0))
	if text, lang := mustFeed(t, s, audio(0, 0, 1, 1, 1, 1)); text != "stable" || lang != "en" {
		t.Fatal(text, lang)
	}
	text, lang := mustFinish(t, s)
	if !slices.Equal(native.startCalls, [][2]string{{"ctx", "hint"}}) {
		t.Fatal(native.startCalls)
	}
	var ev []string
	for _, e := range native.events {
		if e != "reset" {
			ev = append(ev, e)
		}
	}
	if !slices.Equal(ev, []string{"start", "feed", "feed", "finish"}) {
		t.Fatal(ev)
	}
	if len(native.feedInputs) != 2 || !slices.Equal(native.feedInputs[0], audio(0, 0, 0, 0, 1, 1)) || !slices.Equal(native.feedInputs[1], audio(1, 1)) {
		t.Fatal(native.feedInputs)
	}
	if native.finishCalls != 1 || text != "stable tail" || lang != "en" {
		t.Fatal(native.finishCalls, text, lang)
	}
	for _, in := range v.inputs {
		if len(in) > 6 {
			t.Fatalf("VAD window %d > 6", len(in))
		}
	}
}

func TestNoSpeechSuppressesNativeFinish(t *testing.T) {
	native := &recordingNative{}
	s := NewSegmented(native, &markerVAD{}, segOpts(func(o *SegmentOptions) { o.WindowSamples = 4; o.MinSegmentSamples = 4 }))
	_ = s.Start("old", "en")
	if text, _ := mustFeed(t, s, audio(0, 0, 0, 0)); text != "" {
		t.Fatal(text)
	}
	if text, _ := mustFinish(t, s); text != "" {
		t.Fatal(text)
	}
	if len(native.startCalls) != 0 || len(native.feedInputs) != 0 || native.finishCalls != 0 {
		t.Fatal(native.events)
	}
}

func TestResetAndNewStartClearOldTextAndContext(t *testing.T) {
	native := &recordingNative{feedResults: [][2]string{{"old", "en"}, {"new", "fr"}}, finishResults: [][2]string{{"new", "fr"}}}
	s := NewSegmented(native, &markerVAD{}, segOpts(func(o *SegmentOptions) { o.WindowSamples = 4 }))
	_ = s.Reset()
	_ = s.Reset()
	if len(native.startCalls) != 0 || native.resetCalls < 2 {
		t.Fatal(native.events)
	}
	if _, _, err := s.Feed(ones(4)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if _, _, err := s.Finish(); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	_ = s.Start("old context", "en")
	mustFeed(t, s, ones(4))
	before := native.resetCalls
	_ = s.Start("new context", "de")
	if native.resetCalls <= before {
		t.Fatal("start must reset the native stream")
	}
	mustFeed(t, s, ones(4))
	text, lang := mustFinish(t, s)
	if !slices.Equal(native.startCalls, [][2]string{{"old context", "en"}, {"new context", "de"}}) {
		t.Fatal(native.startCalls)
	}
	if text != "new" || lang != "fr" || native.finishCalls != 1 {
		t.Fatal(text, lang, native.finishCalls)
	}
	if _, _, err := s.Feed(ones(4)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if _, _, err := s.Finish(); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
}

func TestPauseSplitsSegmentsPreservesContextAndJoinsWithoutRewriting(t *testing.T) {
	native := &recordingNative{
		feedResults:   [][2]string{{"alpha", "en"}, {"alpha", ""}, {"beta!", "fr"}, {"beta!", ""}, {"(tail", ""}},
		finishResults: [][2]string{{"alpha", ""}, {"beta!", ""}, {"(tail", ""}},
	}
	s := NewSegmented(native, &markerVAD{}, segOpts(func(o *SegmentOptions) {
		o.WindowSamples = 4
		o.MinSegmentSamples = 4
		o.PauseSamples = 4
	}))
	_ = s.Start("same context", "hint")
	speech, silence := ones(4), make([]float32, 4)

	short := &recordingNative{feedResults: [][2]string{{"short", "en"}, {"short", "en"}}}
	shortSession := NewSegmented(short, &markerVAD{}, segOpts(func(o *SegmentOptions) {
		o.WindowSamples = 4
		o.MinSegmentSamples = 12
		o.PauseSamples = 4
	}))
	_ = shortSession.Start("", "")
	mustFeed(t, shortSession, speech)
	mustFeed(t, shortSession, silence)
	if short.finishCalls != 0 {
		t.Fatal("a segment shorter than the minimum must not end")
	}

	mustFeed(t, s, speech)
	if text, lang := mustFeed(t, s, silence); text != "alpha" || lang != "en" {
		t.Fatal(text, lang)
	}
	mustFeed(t, s, speech)
	mustFeed(t, s, silence)
	mustFeed(t, s, speech)
	text, lang := mustFinish(t, s)
	if !slices.Equal(native.startCalls, repeatPair([2]string{"same context", "hint"}, 3)) || native.finishCalls != 3 {
		t.Fatal(native.startCalls, native.finishCalls)
	}
	want := [][]float32{speech, silence, speech, silence, speech}
	if len(native.feedInputs) != 5 {
		t.Fatal(native.feedInputs)
	}
	for i := range want {
		if !slices.Equal(native.feedInputs[i], want[i]) {
			t.Fatalf("feed %d: %v", i, native.feedInputs[i])
		}
	}
	if text != "alpha beta!(tail" || lang != "fr" {
		t.Fatalf("%q %q", text, lang)
	}
}

func repeatPair(p [2]string, n int) [][2]string { return repeat(p, n) }

func TestMaxSegmentLimitSplitsAfterABlockAndKeepsNativeInputsBounded(t *testing.T) {
	native := &recordingNative{
		feedResults:   [][2]string{{"one", "en"}, {"one", "en"}, {"two", "en"}},
		finishResults: [][2]string{{"one", "en"}, {"two", "en"}},
	}
	s := NewSegmented(native, &markerVAD{}, segOpts(func(o *SegmentOptions) {
		o.WindowSamples = 4
		o.MaxSegmentSamples = 8
		o.PauseSamples = 100
	}))
	_ = s.Start("ctx", "en")
	mustFeed(t, s, ones(4))
	if text, lang := mustFeed(t, s, ones(4)); text != "one" || lang != "en" {
		t.Fatal(text, lang)
	}
	mustFeed(t, s, ones(4))
	text, lang := mustFinish(t, s)
	if len(native.startCalls) != 2 || native.finishCalls != 2 || len(native.feedInputs) != 3 {
		t.Fatal(native.events)
	}
	for _, in := range native.feedInputs {
		if !slices.Equal(in, ones(4)) {
			t.Fatal(in)
		}
	}
	if text != "one two" || lang != "en" {
		t.Fatal(text, lang)
	}
}

func TestFinishPrefixViolationResetsAndRaisesWithoutPublishingRewrite(t *testing.T) {
	native := &recordingNative{feedResults: [][2]string{{"abc", "en"}}, finishResults: [][2]string{{"xbc", "en"}}}
	s := NewSegmented(native, &markerVAD{}, segOpts(nil))
	_ = s.Start("", "")
	mustFeed(t, s, ones(4))
	before := native.resetCalls
	if _, _, err := s.Finish(); err == nil || errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want runtime error, got %v", err)
	}
	if native.finishCalls != 1 || native.resetCalls <= before || len(native.feedInputs) != 1 {
		t.Fatal(native.events)
	}
	if _, _, err := s.Feed(ones(4)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if _, _, err := s.Finish(); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
}

func TestNativeFeedErrorPropagatesWithoutRetry(t *testing.T) {
	native := &recordingNative{feedErrors: []error{errors.New("native feed failed")}}
	s := NewSegmented(native, &markerVAD{}, segOpts(nil))
	_ = s.Start("", "")
	if _, _, err := s.Feed(ones(4)); err == nil || !strings.Contains(err.Error(), "native feed failed") {
		t.Fatal(err)
	}
	if len(native.feedInputs) != 1 || native.finishCalls != 0 {
		t.Fatal(native.events)
	}
}

// test_r2t2_rolling_segmentation.py

func TestContinuousAudioCrossesOldLimitWithoutResetOrSampleLoss(t *testing.T) {
	in := ones(480001)
	native := &recordingNative{feedResults: repeat([2]string{"", ""}, 94), finishResults: [][2]string{{"complete", "en"}}}
	s := NewSegmented(native, &markerVAD{}, DefaultSegmentOptions(5120))
	_ = s.Start("", "")
	mustFeed(t, s, in)
	if native.finishCalls != 0 || len(native.startCalls) != 1 {
		t.Fatal(native.finishCalls, native.startCalls)
	}
	if text, lang := mustFinish(t, s); text != "complete" || lang != "en" {
		t.Fatal(text, lang)
	}
	if native.finishCalls != 1 || !slices.Equal(concat(native.feedInputs), in) {
		t.Fatal("native must receive every sample exactly once")
	}
}

func TestDisablingCapacityCutPreservesRealPauseSegmentation(t *testing.T) {
	native := &recordingNative{feedResults: repeat([2]string{"", ""}, 3), finishResults: [][2]string{{"first", "en"}}}
	s := NewSegmented(native, &markerVAD{}, SegmentOptions{ChunkSamples: 4, WindowSamples: 4, MinSegmentSamples: 8, PauseSamples: 2})
	_ = s.Start("", "")
	mustFeed(t, s, ones(8))
	if native.finishCalls != 0 {
		t.Fatal("no pause yet")
	}
	if text, lang := mustFeed(t, s, make([]float32, 4)); text != "first" || lang != "en" || native.finishCalls != 1 {
		t.Fatal(text, lang, native.finishCalls)
	}
	if text, lang := mustFinish(t, s); text != "first" || lang != "en" || native.finishCalls != 1 {
		t.Fatal(text, lang)
	}
}

// test_r2t2_segment_spacing.py

func TestEnglishSentenceBoundaryKeepsWordSeparation(t *testing.T) {
	native := &recordingNative{
		feedResults:   [][2]string{{"years.", ""}, {"22,500 times", ""}},
		finishResults: [][2]string{{"years.", ""}, {"22,500 times", ""}},
	}
	s := NewSegmented(native, fixedVAD{[]vad.Segment{{Start: 0, End: 4}}},
		SegmentOptions{ChunkSamples: 4, WindowSamples: 4, MinSegmentSamples: 4, MaxSegmentSamples: 4, PauseSamples: 4800})
	_ = s.Start("", "")
	first, _ := mustFeed(t, s, ones(4))
	second, _ := mustFeed(t, s, ones(4))
	final, _ := mustFinish(t, s)
	if first != "years." || second != "years. 22,500 times" || final != second || native.finishCalls != 2 {
		t.Fatalf("%q %q %q %d", first, second, final, native.finishCalls)
	}
}

func TestJoinSpacingRules(t *testing.T) {
	s := &Segmented{}
	for _, tc := range []struct {
		pieces []string
		want   string
	}{
		{[]string{"hello", "world"}, "hello world"},
		{[]string{"done!", "Next"}, "done! Next"},
		{[]string{"你好", "world"}, "你好world"},
		{[]string{"hello", "世界"}, "hello世界"},
		{[]string{"a,", "b"}, "a,b"},
		{[]string{"x:", "9"}, "x: 9"},
		{[]string{"é", "a"}, "éa"},
	} {
		s.segments = tc.pieces
		if got := s.joinedText(false, ""); got != tc.want {
			t.Errorf("%q → %q, want %q", tc.pieces, got, tc.want)
		}
	}
}

// test_r2t2_preview.py (segmented cases)

func TestSegmentedPreviewTracksNativeRevisionsAndKeepsJoinSpacing(t *testing.T) {
	native := &previewNative{recordingNative{
		feedResults:   [][2]string{{"hello", "en"}, {"hello", ""}, {"world", "en"}, {"world", ""}},
		previews:      []string{"hello draft", "hello", "world 世界", "world 世界!"},
		finishResults: [][2]string{{"hello", ""}},
	}}
	s := NewSegmented(native, &markerVAD{}, SegmentOptions{ChunkSamples: 4, WindowSamples: 4, MinSegmentSamples: 4, PauseSamples: 4})
	_ = s.Start("", "")
	speech, silence := ones(4), make([]float32, 4)
	steps := []struct {
		pcm           []float32
		text, preview string
	}{
		{speech, "hello", "hello draft"},
		{silence, "hello", "hello"},
		{speech, "hello world", "hello world 世界"},
		{speech, "hello world", "hello world 世界!"},
	}
	for i, st := range steps {
		text, lang := mustFeed(t, s, st.pcm)
		if text != st.text || lang != "en" || s.PreviewText() != st.preview {
			t.Fatalf("step %d: %q %q preview %q", i, text, lang, s.PreviewText())
		}
	}
}

func TestSegmentedLegacyNativePreviewDefaultsToCommitted(t *testing.T) {
	native := &recordingNative{feedResults: [][2]string{{"legacy", "en"}}}
	s := NewSegmented(native, &markerVAD{}, SegmentOptions{ChunkSamples: 4, WindowSamples: 4, MinSegmentSamples: 100, PauseSamples: 100})
	_ = s.Start("", "")
	if text, lang := mustFeed(t, s, ones(4)); text != "legacy" || lang != "en" || s.PreviewText() != "legacy" {
		t.Fatal(text, lang, s.PreviewText())
	}
}

func TestTrailingSilence(t *testing.T) {
	if trailingSilence(10, nil) != 10 {
		t.Fatal("no regions: whole window is silence")
	}
	if got := trailingSilence(10, []vad.Segment{{Start: 0, End: 3}, {Start: 5, End: 7}}); got != 3 {
		t.Fatal(got)
	}
	if got := trailingSilence(10, []vad.Segment{{Start: 5, End: 12}}); got != 0 {
		t.Fatal(got)
	}
}
