// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package qwen3

import (
	"errors"
	"fmt"
	"testing"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
)

const sr = asr.SampleRate

// tone returns n voiced samples (value v) — the energy fake VAD sees them as speech.
func tone(n int, v int16) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = v
	}
	return out
}

type recCall struct{ n int }

func newSentenceHarness() (*SentenceStream, *[]recCall) {
	var calls []recCall
	det := &fakeVAD{energy: true}
	rec := func(audio []float32) (string, string, error) {
		calls = append(calls, recCall{len(audio)})
		voiced := 0
		for _, x := range audio {
			if x != 0 {
				voiced++
			}
		}
		if voiced == 0 {
			return "", "unknown", nil
		}
		return fmt.Sprintf("句%d", len(calls)), "zh", nil
	}
	return NewSentenceStream(det, rec, DefaultSentenceOptions()), &calls
}

func pushAll(t *testing.T, s *SentenceStream, pcm []int16, block int) []asr.Partial {
	t.Helper()
	var out []asr.Partial
	for i := 0; i < len(pcm); i += block {
		p, err := s.Push(pcm[i:min(i+block, len(pcm))])
		if err != nil {
			t.Fatal(err)
		}
		if p.Tentative != "" {
			t.Fatalf("tentative %q in sentence mode", p.Tentative)
		}
		out = append(out, p)
	}
	return out
}

func TestSentenceCutOnPause(t *testing.T) {
	s, calls := newSentenceHarness()
	var audio []int16
	audio = append(audio, make([]int16, 2*sr)...) // leading silence: dropped down to the preroll
	audio = append(audio, tone(3*sr, 100)...)
	audio = append(audio, make([]int16, sr)...) // pause ends sentence 1
	audio = append(audio, tone(sr*3/2, 100)...)
	audio = append(audio, make([]int16, 2560)...) // too short a pause
	audio = append(audio, tone(sr, 100)...)
	ps := pushAll(t, s, audio, 2560)
	var commits []string
	for _, p := range ps {
		if len(commits) == 0 || commits[len(commits)-1] != p.Committed {
			commits = append(commits, p.Committed)
		}
	}
	if len(commits) != 2 || commits[1] != "句1" || len(*calls) != 1 {
		t.Fatalf("commits %q calls %v", commits, *calls)
	}
	// Sentence 1 = 1 s preroll (window of silence kept) + speech + the pause
	// that ended it (cut once 500 ms of trailing silence were seen).
	if n := (*calls)[0].n; n < 4*sr || n > 4*sr+sr/2+2*2560 {
		t.Fatalf("sentence 1 length %d", n)
	}
	res, err := s.Finish()
	if err != nil || res.Text != "句1句2" || res.Language != "zh" || res.SampleCount != len(audio) || len(*calls) != 2 {
		t.Fatalf("%+v %v %v", res, err, *calls)
	}
	if _, err := s.Push(make([]int16, 10)); err == nil {
		t.Fatal("push after finish accepted")
	}
}

func TestSentenceSilenceOnlyNeverRecognizesAndKeepsPrerollBounded(t *testing.T) {
	s, calls := newSentenceHarness()
	pushAll(t, s, make([]int16, 10*sr), 2560)
	if len(s.pending) > s.opts.PrerollSamples {
		t.Fatalf("pending grew to %d", len(s.pending))
	}
	res, err := s.Finish()
	if err != nil || res.Text != "" || res.Language != "unknown" {
		t.Fatal(res, err)
	}
	if len(*calls) != 1 { // finish recognizes the (silent) preroll once; the recognizer filters it
		t.Fatal(*calls)
	}
}

func TestSentenceMaxLengthForcesCut(t *testing.T) {
	s, calls := newSentenceHarness()
	s.opts.MaxSentenceSamples = 5 * sr
	pushAll(t, s, tone(12*sr, 7), 2560)
	if len(*calls) != 2 {
		t.Fatalf("calls %v", *calls)
	}
	for _, c := range *calls {
		if c.n < 5*sr || c.n >= 5*sr+2560 {
			t.Fatalf("forced cut length %d", c.n)
		}
	}
	res, _ := s.Finish()
	if res.Text != "句1句2句3" {
		t.Fatal(res.Text)
	}
}

func TestSentenceJoinAndErrors(t *testing.T) {
	det := &fakeVAD{energy: true}
	texts := []string{"Hello", "world.", "你好"}
	i := 0
	s := NewSentenceStream(det, func([]float32) (string, string, error) {
		t := texts[i]
		i++
		return t, "en", nil
	}, DefaultSentenceOptions())
	for range texts {
		pushAll(t, s, tone(2*sr, 1), 2560)
		pushAll(t, s, make([]int16, sr), 2560)
	}
	if got := s.Committed(); got != "Hello world.你好" {
		t.Fatalf("%q", got)
	}
	boom := errors.New("boom")
	s2 := NewSentenceStream(det, func([]float32) (string, string, error) { return "", "", boom }, DefaultSentenceOptions())
	pushAll(t, s2, tone(2*sr, 1), 2560)
	if _, err := s2.Push(make([]int16, sr)); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	s2.Close()
	if _, err := s2.Finish(); err == nil {
		t.Fatal("finish after close")
	}
}

func TestBackendStreamUsesWholeClipPath(t *testing.T) {
	h := newHarness(t, nil)
	h.vad.energy = true
	_ = h.b.Load(spec)
	st, err := h.b.NewStream(asr.StreamOptions{SessionID: 1})
	if err != nil {
		t.Fatal(err)
	}
	ss := st.(*SentenceStream)
	pushAll(t, ss, tone(2*sr, 50), 2560)
	pushAll(t, ss, make([]int16, sr), 2560)
	if ss.Committed() != "测试文本" {
		t.Fatalf("%q", ss.Committed())
	}
	// The sentence went through FilterSpeech: only the voiced part reached the model.
	in := h.lib.sessions[0].inputs
	if got := len(in[len(in)-1]); got != 2*sr {
		t.Fatalf("model got %d samples", got)
	}
	res, err := st.Finish()
	if err != nil || res.Text != "测试文本" || res.Language != "zh" {
		t.Fatal(res, err)
	}
}
