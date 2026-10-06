// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: GPL-3.0-only
//
// Go translation of src-tauri/resources/test_r2t2_stream.py and the session
// cases of test_r2t2_preview.py (inherited, GPL-3.0-only; PLAN §10.4).

package r2t2

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// streamRuntime mirrors RecordingRuntime / PreviewRuntime.
type streamRuntime struct {
	events       []string
	startCalls   [][2]string
	feedInputs   [][]float32
	feedResults  [][3]string // committed, language, preview ("" preview = committed)
	finishResult [2]string
	finishCalls  int
	resetCalls   int
	startErrors  []error
	feedErrors   []error
	finishErrors []error
	resetErrors  []error
	preview      string
}

func next(errs *[]error) error {
	if len(*errs) == 0 {
		return nil
	}
	err := (*errs)[0]
	*errs = (*errs)[1:]
	return err
}

func (r *streamRuntime) Start(context, language string) error {
	r.events = append(r.events, "start")
	r.startCalls = append(r.startCalls, [2]string{context, language})
	r.preview = ""
	return next(&r.startErrors)
}

func (r *streamRuntime) Feed(pcm []float32) (string, string, error) {
	r.events = append(r.events, "feed")
	r.feedInputs = append(r.feedInputs, slices.Clone(pcm))
	if err := next(&r.feedErrors); err != nil {
		return "", "", err
	}
	if len(r.feedResults) == 0 {
		return "", "", nil
	}
	res := r.feedResults[0]
	r.feedResults = r.feedResults[1:]
	r.preview = res[2]
	if r.preview == "" {
		r.preview = res[0]
	}
	return res[0], res[1], nil
}

func (r *streamRuntime) Finish() (string, string, error) {
	r.events = append(r.events, "finish")
	r.finishCalls++
	if err := next(&r.finishErrors); err != nil {
		return "", "", err
	}
	r.preview = r.finishResult[0]
	return r.finishResult[0], r.finishResult[1], nil
}

func (r *streamRuntime) Reset() error {
	r.events = append(r.events, "reset")
	r.resetCalls++
	r.preview = ""
	return next(&r.resetErrors)
}

// previewRuntime exposes PreviewText (PreviewRuntime); streamRuntime alone is
// the legacy seam without a preview.
type previewRuntime struct{ streamRuntime }

func (r *previewRuntime) PreviewText() string { return r.preview }

func pcm(v ...float32) []float32 { return v }

func wantResp(t *testing.T, got Response, err error, want Response) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("response %+v, want %+v", got, want)
	}
}

func wantInvalid(t *testing.T, _ Response, err error) {
	t.Helper()
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument, got %v", err)
	}
}

func wantRuntimeErr(t *testing.T, _ Response, err error, contains string) {
	t.Helper()
	if err == nil || errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), contains) {
		t.Fatalf("want runtime error %q, got %v", contains, err)
	}
}

func TestStartForwardsContextAndLanguage(t *testing.T) {
	rt := &streamRuntime{}
	s := NewStreamSession(rt)
	r, err := s.Start(7, "hotword: 你好\t", "zh")
	wantResp(t, r, err, Response{SessionID: 7, Language: "zh"})
	if !slices.Equal(rt.startCalls, [][2]string{{"hotword: 你好\t", "zh"}}) || !slices.Equal(rt.events, []string{"start"}) {
		t.Fatal(rt.events)
	}
}

func TestStartReplacesActiveStreamAndStaleCallsCannotTouchCurrent(t *testing.T) {
	rt := &streamRuntime{feedResults: [][3]string{{"旧", "zh"}, {"新", "zh"}}}
	s := NewStreamSession(rt)
	_, _ = s.Start(7, "", "")
	_, _ = s.Feed(7, pcm(0.1, 0.2), 0)
	r, err := s.Start(8, "", "")
	wantResp(t, r, err, Response{SessionID: 8})
	if rt.resetCalls != 1 {
		t.Fatal(rt.resetCalls)
	}
	r, err = s.Start(8, "", "")
	wantInvalid(t, r, err)
	r, err = s.Start(7, "", "")
	wantInvalid(t, r, err)
	r, err = s.Feed(7, pcm(0.3), 0)
	wantInvalid(t, r, err)
	r, err = s.Finish(7)
	wantInvalid(t, r, err)
	r, err = s.Cancel(7)
	wantInvalid(t, r, err)
	r, err = s.Feed(8, pcm(0.4), 0)
	wantResp(t, r, err, Response{SessionID: 8, Text: "新", Language: "zh", SampleCount: 1})
	if rt.resetCalls != 1 || len(rt.feedInputs) != 2 || !slices.Equal(rt.events, []string{"start", "feed", "reset", "start", "feed"}) {
		t.Fatal(rt.events)
	}
}

func TestSessionIDsValidateAndFailedStartConsumesID(t *testing.T) {
	rt := &streamRuntime{}
	s := NewStreamSession(rt)
	r, err := s.Start(0, "", "")
	wantInvalid(t, r, err)
	if len(rt.startCalls) != 0 {
		t.Fatal(rt.startCalls)
	}
	rt.startErrors = []error{errors.New("native start failed")}
	r, err = s.Start(1, "", "")
	wantRuntimeErr(t, r, err, "native start failed")
	if rt.resetCalls != 1 {
		t.Fatal(rt.resetCalls)
	}
	r, err = s.Start(1, "", "")
	wantInvalid(t, r, err)
	r, err = s.Start(2, "", "")
	wantResp(t, r, err, Response{SessionID: 2})
	if len(rt.startCalls) != 2 {
		t.Fatal(rt.startCalls)
	}
}

func TestFeedRejectsInvalidAudioAndOffsetsBeforeNativeCall(t *testing.T) {
	rt := &streamRuntime{}
	s := NewStreamSession(rt)
	_, _ = s.Start(3, "", "")
	for _, a := range [][]float32{{}, nil, {float32(nan())}, {float32(inf())}} {
		r, err := s.Feed(3, a, 0)
		wantInvalid(t, r, err)
	}
	for _, off := range []int{-1, 1} {
		r, err := s.Feed(3, pcm(0.1), off)
		wantInvalid(t, r, err)
	}
	if len(rt.feedInputs) != 0 || rt.resetCalls != 0 {
		t.Fatal(rt.events)
	}
	r, err := s.Feed(3, pcm(0.1, 0.2), 0)
	wantResp(t, r, err, Response{SessionID: 3, SampleCount: 2})
	for _, off := range []int{0, 1, 3} {
		r, err := s.Feed(3, pcm(0.3), off)
		wantInvalid(t, r, err)
	}
	r, err = s.Feed(3, pcm(0.3), 2)
	wantResp(t, r, err, Response{SessionID: 3, SampleCount: 3})
	if len(rt.feedInputs) != 2 {
		t.Fatal(len(rt.feedInputs))
	}
}

func TestFeedPreservesAudioUnicodeTextAndKnownLanguage(t *testing.T) {
	rt := &streamRuntime{feedResults: [][3]string{{"你🚀", "zh"}, {"你🚀好", "de"}, {"你🚀好!", ""}}}
	s := NewStreamSession(rt)
	_, _ = s.Start(4, "", "en")
	r, err := s.Feed(4, pcm(0.1, 0.2), 0)
	wantResp(t, r, err, Response{SessionID: 4, Text: "你🚀", Language: "zh", SampleCount: 2})
	r, err = s.Feed(4, pcm(0.3), 2)
	wantResp(t, r, err, Response{SessionID: 4, Text: "你🚀好", Language: "de", SampleCount: 3})
	r, err = s.Feed(4, pcm(0.4, 0.5), 3)
	wantResp(t, r, err, Response{SessionID: 4, Text: "你🚀好!", Language: "de", SampleCount: 5})
	if !slices.Equal(rt.feedInputs[0], pcm(0.1, 0.2)) {
		t.Fatal(rt.feedInputs[0])
	}
}

func TestFeedTextRegressionResetsAndInvalidatesWithoutRetry(t *testing.T) {
	for _, regressed := range []string{"ab", "xbc"} {
		rt := &streamRuntime{feedResults: [][3]string{{"abc", "en"}, {regressed, "en"}}}
		s := NewStreamSession(rt)
		_, _ = s.Start(5, "", "")
		_, _ = s.Feed(5, pcm(0.1), 0)
		r, err := s.Feed(5, pcm(0.2), 1)
		wantRuntimeErr(t, r, err, "regressed")
		if len(rt.feedInputs) != 2 || rt.resetCalls != 1 {
			t.Fatal(rt.events)
		}
		r, err = s.Feed(5, pcm(0.3), 0)
		wantInvalid(t, r, err)
		r, err = s.Finish(5)
		wantInvalid(t, r, err)
		r, err = s.Cancel(5)
		wantInvalid(t, r, err)
		if rt.resetCalls != 1 {
			t.Fatal(rt.resetCalls)
		}
	}
}

func TestFinishTextRegressionResetsAndInvalidatesSession(t *testing.T) {
	for _, regressed := range []string{"ab", "xbc"} {
		rt := &streamRuntime{feedResults: [][3]string{{"abc", "en"}}, finishResult: [2]string{regressed, "en"}}
		s := NewStreamSession(rt)
		_, _ = s.Start(6, "", "")
		_, _ = s.Feed(6, pcm(0.1), 0)
		r, err := s.Finish(6)
		wantRuntimeErr(t, r, err, "regressed")
		if rt.finishCalls != 1 || rt.resetCalls != 1 {
			t.Fatal(rt.events)
		}
		r, err = s.Feed(6, pcm(0.2), 1)
		wantInvalid(t, r, err)
		r, err = s.Finish(6)
		wantInvalid(t, r, err)
		r, err = s.Cancel(6)
		wantInvalid(t, r, err)
		if rt.finishCalls != 1 || rt.resetCalls != 1 {
			t.Fatal(rt.events)
		}
	}
}

func TestFinishFlushesOnceAndReturnsCompleteUnicodeText(t *testing.T) {
	rt := &streamRuntime{feedResults: [][3]string{{"你好", "zh"}}, finishResult: [2]string{"你好世界", "ja"}}
	s := NewStreamSession(rt)
	_, _ = s.Start(6, "", "")
	_, _ = s.Feed(6, pcm(0.1, 0.2, 0.3), 0)
	r, err := s.Finish(6)
	wantResp(t, r, err, Response{SessionID: 6, Text: "你好世界", Language: "ja", SampleCount: 3, Final: true})
	r, err = s.Finish(6)
	wantInvalid(t, r, err)
	r, err = s.Feed(6, pcm(0.4), 3)
	wantInvalid(t, r, err)
	r, err = s.Cancel(6)
	wantInvalid(t, r, err)
	if rt.finishCalls != 1 {
		t.Fatal(rt.finishCalls)
	}
}

func TestEmptyFinishAvoidsNativeFinishAndResetsStream(t *testing.T) {
	rt := &streamRuntime{finishErrors: []error{errors.New("finish must not be called")}}
	s := NewStreamSession(rt)
	_, _ = s.Start(7, "", "")
	r, err := s.Finish(7)
	wantResp(t, r, err, Response{SessionID: 7, Final: true})
	if rt.finishCalls != 0 || rt.resetCalls != 1 {
		t.Fatal(rt.events)
	}
	r, err = s.Cancel(7)
	wantInvalid(t, r, err)
}

func TestCancelReturnsEmptyFinalAndResetsOnce(t *testing.T) {
	rt := &streamRuntime{feedResults: [][3]string{{"spoken", "de"}}}
	s := NewStreamSession(rt)
	_, _ = s.Start(8, "", "en")
	_, _ = s.Feed(8, pcm(0.1, 0.2, 0.3, 0.4), 0)
	r, err := s.Cancel(8)
	wantResp(t, r, err, Response{SessionID: 8, Language: "de", SampleCount: 4, Final: true})
	if rt.resetCalls != 1 {
		t.Fatal(rt.resetCalls)
	}
	r, err = s.Cancel(8)
	wantInvalid(t, r, err)
	if rt.resetCalls != 1 {
		t.Fatal(rt.resetCalls)
	}
}

func TestCloseIsIdempotentAndDoesNotCloseNativeRuntime(t *testing.T) {
	rt := &streamRuntime{}
	s := NewStreamSession(rt)
	if err := s.Close(); err != nil || rt.resetCalls != 0 {
		t.Fatal(err, rt.resetCalls)
	}
	_, _ = s.Start(9, "", "")
	_, _ = s.Feed(9, pcm(0.1, 0.2), 0)
	_ = s.Close()
	_ = s.Close()
	if rt.resetCalls != 1 {
		t.Fatal(rt.resetCalls)
	}
	r, err := s.Feed(9, pcm(0.3), 2)
	wantInvalid(t, r, err)
	r, err = s.Start(10, "", "")
	wantResp(t, r, err, Response{SessionID: 10})
}

func TestFeedErrorPreservesOriginalErrorWhenResetAlsoFails(t *testing.T) {
	rt := &streamRuntime{feedErrors: []error{errors.New("feed failed")}, resetErrors: []error{errors.New("reset failed")}}
	s := NewStreamSession(rt)
	_, _ = s.Start(11, "", "")
	r, err := s.Feed(11, pcm(0.1), 0)
	wantRuntimeErr(t, r, err, "feed failed")
	if len(rt.feedInputs) != 1 || rt.resetCalls != 1 {
		t.Fatal(rt.events)
	}
	r, err = s.Finish(11)
	wantInvalid(t, r, err)
}

func TestFinishErrorPreservesOriginalErrorAndInvalidatesSession(t *testing.T) {
	rt := &streamRuntime{feedResults: [][3]string{{"text", "en"}}, finishErrors: []error{errors.New("finish failed")}, resetErrors: []error{errors.New("reset failed")}}
	s := NewStreamSession(rt)
	_, _ = s.Start(12, "", "")
	_, _ = s.Feed(12, pcm(0.1), 0)
	r, err := s.Finish(12)
	wantRuntimeErr(t, r, err, "finish failed")
	if rt.finishCalls != 1 || rt.resetCalls != 1 {
		t.Fatal(rt.events)
	}
	r, err = s.Cancel(12)
	wantInvalid(t, r, err)
}

func TestStartErrorPreservesOriginalErrorAndConsumesSessionID(t *testing.T) {
	rt := &streamRuntime{resetErrors: []error{nil, errors.New("reset failed")}}
	s := NewStreamSession(rt)
	_, _ = s.Start(13, "", "")
	_, _ = s.Feed(13, pcm(0.1), 0)
	rt.startErrors = []error{errors.New("start failed")}
	r, err := s.Start(14, "", "")
	wantRuntimeErr(t, r, err, "start failed")
	r, err = s.Feed(13, pcm(0.2), 1)
	wantInvalid(t, r, err)
	r, err = s.Finish(13)
	wantInvalid(t, r, err)
	r, err = s.Cancel(13)
	wantInvalid(t, r, err)
	r, err = s.Start(14, "", "")
	wantInvalid(t, r, err)
	r, err = s.Start(15, "", "")
	wantResp(t, r, err, Response{SessionID: 15})
}

func TestFinishRetainsLatestDetectedLanguageWhenNativeLanguageIsEmpty(t *testing.T) {
	rt := &streamRuntime{feedResults: [][3]string{{"Hallo", "de"}}, finishResult: [2]string{"Hallo!", ""}}
	s := NewStreamSession(rt)
	_, _ = s.Start(1, "", "en")
	_, _ = s.Feed(1, pcm(0.1), 0)
	r, err := s.Finish(1)
	wantResp(t, r, err, Response{SessionID: 1, Text: "Hallo!", Language: "de", SampleCount: 1, Final: true})
}

func TestResetErrorOnCancelInvalidatesAndPropagates(t *testing.T) {
	rt := &streamRuntime{resetErrors: []error{errors.New("cancel reset failed")}}
	s := NewStreamSession(rt)
	_, _ = s.Start(15, "", "")
	r, err := s.Cancel(15)
	wantRuntimeErr(t, r, err, "cancel reset failed")
	if rt.resetCalls != 1 {
		t.Fatal(rt.resetCalls)
	}
	r, err = s.Feed(15, pcm(0.1), 0)
	wantInvalid(t, r, err)
}

func TestInvalidActiveSessionIDsCannotAddressFeedFinishOrCancel(t *testing.T) {
	rt := &streamRuntime{}
	s := NewStreamSession(rt)
	_, _ = s.Start(1, "", "")
	r, err := s.Feed(0, pcm(0.1), 0)
	wantInvalid(t, r, err)
	r, err = s.Finish(0)
	wantInvalid(t, r, err)
	r, err = s.Cancel(0)
	wantInvalid(t, r, err)
	if len(rt.feedInputs) != 0 || rt.finishCalls != 0 || rt.resetCalls != 0 {
		t.Fatal(rt.events)
	}
	r, err = s.Feed(1, pcm(0.1), 0)
	wantResp(t, r, err, Response{SessionID: 1, SampleCount: 1})
	if s.ActiveID() != 1 || !s.Active() {
		t.Fatal(s.ActiveID())
	}
}

// test_r2t2_preview.py (session cases)

func TestSessionReportsRevisableUnicodePreviewSuffix(t *testing.T) {
	rt := &previewRuntime{streamRuntime{feedResults: [][3]string{
		{"你好", "zh", "你好世界"}, {"你好", "", "你好世代"}, {"你好", "", "你好世"}, {"你好", "", "你好"},
	}}}
	s := NewStreamSession(rt)
	r, err := s.Start(1, "", "zh")
	wantResp(t, r, err, Response{SessionID: 1, Language: "zh"})
	for i, tentative := range []string{"世界", "世代", "世", ""} {
		r, err := s.Feed(1, pcm(0.1), i)
		wantResp(t, r, err, Response{SessionID: 1, Text: "你好", TentativeText: tentative, Language: "zh", SampleCount: i + 1})
	}
}

func TestLegacyRuntimeWithoutPreviewReportsCommittedOnly(t *testing.T) {
	rt := &streamRuntime{feedResults: [][3]string{{"legacy", "en", "legacy draft"}}}
	s := NewStreamSession(rt)
	_, _ = s.Start(2, "", "")
	r, err := s.Feed(2, pcm(0.1), 0)
	wantResp(t, r, err, Response{SessionID: 2, Text: "legacy", Language: "en", SampleCount: 1})
}

func TestFinishAndCancelResponsesAlwaysClearTentativeText(t *testing.T) {
	rt := &previewRuntime{streamRuntime{feedResults: [][3]string{{"committed", "en", "committed draft"}}, finishResult: [2]string{"committed final", "en"}}}
	s := NewStreamSession(rt)
	_, _ = s.Start(3, "", "")
	r, err := s.Feed(3, pcm(0.1), 0)
	wantResp(t, r, err, Response{SessionID: 3, Text: "committed", TentativeText: " draft", Language: "en", SampleCount: 1})
	r, err = s.Finish(3)
	wantResp(t, r, err, Response{SessionID: 3, Text: "committed final", Language: "en", SampleCount: 1, Final: true})

	rt = &previewRuntime{streamRuntime{feedResults: [][3]string{{"committed", "en", "committed draft"}}}}
	s = NewStreamSession(rt)
	_, _ = s.Start(4, "", "")
	_, _ = s.Feed(4, pcm(0.1), 0)
	r, err = s.Cancel(4)
	wantResp(t, r, err, Response{SessionID: 4, Language: "en", SampleCount: 1, Final: true})
}

func TestConflictingPreviewResetsAndInvalidatesSession(t *testing.T) {
	rt := &previewRuntime{streamRuntime{feedResults: [][3]string{{"hello", "en", "hello draft"}, {"hello world", "en", "hello stale"}}}}
	s := NewStreamSession(rt)
	_, _ = s.Start(5, "", "")
	_, _ = s.Feed(5, pcm(0.1), 0)
	r, err := s.Feed(5, pcm(0.1), 1)
	wantRuntimeErr(t, r, err, "preview")
	if rt.resetCalls != 1 {
		t.Fatal(rt.resetCalls)
	}
	r, err = s.Feed(5, pcm(0.1), 0)
	wantInvalid(t, r, err)
}
