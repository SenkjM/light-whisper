// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package asr

import (
	"errors"
	"testing"
)

func TestMockKindsAndDeterminism(t *testing.T) {
	m := NewMock()
	if _, err := m.Transcribe(nil, TranscribeOptions{}); !errors.Is(err, ErrNotLoaded) {
		t.Fatal(err)
	}
	_ = m.Load(LoadSpec{Engine: "confucius4-r2t2", Kind: KindR2T2, Device: "auto"})
	if info := m.Info(); !info.ModelLoaded || info.Device != "cpu" || info.MissingModels == nil {
		t.Fatalf("%+v", info)
	}
	// R2T2 supports whole-clip transcription (segmented session, tail rounded up).
	if r, err := m.Transcribe(make([]int16, SampleRate*3/2), TranscribeOptions{Language: "en"}); err != nil ||
		r.Text != "字字" || r.SampleCount != SampleRate*3/2 || r.Language != "en" {
		t.Fatalf("R2T2 mock transcribe: %+v %v", r, err)
	}
	st, err := m.NewStream(StreamOptions{Language: "zh"})
	if err != nil {
		t.Fatal(err)
	}
	var committed string
	for i := 0; i < 25; i++ { // 25 x 160 ms = 4 s
		p, err := st.Push(make([]int16, SampleRate*160/1000))
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Committed) < len(committed) || p.Committed[:len(committed)] != committed {
			t.Fatalf("committed shrank or changed: %q -> %q", committed, p.Committed)
		}
		committed = p.Committed
	}
	r, _ := st.Finish()
	if r.Text != "字字字字" || r.SampleCount != 4*SampleRate || r.Language != "zh" {
		t.Fatalf("%+v", r)
	}
	if _, err := st.Push(make([]int16, 1)); err == nil {
		t.Fatal("push after finish accepted")
	}

	_ = m.Unload()
	_ = m.Load(LoadSpec{Engine: "qwen3-asr-0.6b", Kind: KindQwen3, Device: "cuda"})
	qs, err := m.NewStream(StreamOptions{Language: "zh"})
	if err != nil {
		t.Fatal(err)
	}
	voicedPCM := make([]int16, SampleRate*3/2)
	for i := range voicedPCM {
		voicedPCM[i] = 1
	}
	if p, _ := qs.Push(voicedPCM); p.Committed != "" || p.Tentative != "" {
		t.Fatalf("sentence committed before its pause: %+v", p)
	}
	if p, _ := qs.Push(make([]int16, SampleRate/2)); p.Committed != "字" || p.Tentative != "" {
		t.Fatalf("sentence not committed after 500 ms of silence: %+v", p)
	}
	if r, _ := qs.Finish(); r.Text != "字" || r.SampleCount != 2*SampleRate {
		t.Fatalf("%+v", r)
	}
	if MockSpeechSegments(append(append(make([]int16, 10), voicedPCM[:100]...), make([]int16, 100)...))[0] != (Span{Start: 10, End: 110}) {
		t.Fatal("mock VAD")
	}
	if r, err := m.Transcribe(make([]int16, SampleRate*3/2), TranscribeOptions{}); err != nil || r.Text != "字" {
		t.Fatalf("Qwen3 mock transcribe: %+v %v", r, err)
	}
	if m.Loads.Load() != 2 || m.Unloads.Load() != 1 {
		t.Fatalf("loads %d unloads %d", m.Loads.Load(), m.Unloads.Load())
	}
}
