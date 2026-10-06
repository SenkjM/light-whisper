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
	if _, err := m.Transcribe(nil, TranscribeOptions{}); !errors.Is(err, ErrWrongKind) {
		t.Fatal("R2T2 mock must not do batch transcription")
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
	if _, err := m.NewStream(StreamOptions{}); !errors.Is(err, ErrWrongKind) {
		t.Fatal("Qwen3 mock must not stream")
	}
	if m.Loads.Load() != 2 || m.Unloads.Load() != 1 {
		t.Fatalf("loads %d unloads %d", m.Loads.Load(), m.Unloads.Load())
	}
	if _, err := NewNative(); !errors.Is(err, ErrNativeUnavailable) {
		t.Fatal("native backend should be unavailable without the lwnative tag")
	}
}
