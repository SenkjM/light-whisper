// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package native

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
)

func TestOptionsFromEnv(t *testing.T) {
	t.Setenv("LW_R2T2_RUNTIME_DIR", "")
	t.Setenv("LW_RESOURCES_DIR", "")
	t.Setenv("LW_R2T2_MODEL", "")
	t.Setenv("LW_TRANSCRIBE_LIBRARY", "")
	t.Setenv("LW_QWEN3_MODEL", "")
	o := OptionsFromEnv(filepath.FromSlash("/app/res"))
	if o.ResourcesDir != filepath.FromSlash("/app/res") || o.RuntimeDir != filepath.FromSlash("/app/res/r2t2-native") ||
		filepath.Dir(o.TranscribeLibrary) != filepath.FromSlash("/app/res/transcribe-native") {
		t.Fatalf("%+v", o)
	}
	t.Setenv("LW_TRANSCRIBE_LIBRARY", "/t/libtranscribe.so")
	t.Setenv("LW_QWEN3_MODEL", "/q.gguf")
	if o := OptionsFromEnv("/x"); o.TranscribeLibrary != "/t/libtranscribe.so" || o.Qwen3Model != "/q.gguf" {
		t.Fatalf("%+v", o)
	}
	t.Setenv("LW_R2T2_RUNTIME_DIR", "/rt")
	t.Setenv("LW_RESOURCES_DIR", "/res")
	t.Setenv("LW_R2T2_MODEL", "/m.gguf")
	if o := OptionsFromEnv("/x"); o.RuntimeDir != "/rt" || o.ResourcesDir != "/res" || o.R2T2Model != "/m.gguf" {
		t.Fatalf("%+v", o)
	}
}

func TestNewWithoutNative(t *testing.T) {
	if Available {
		t.Skip("native build")
	}
	if _, err := New(Options{RuntimeDir: "a", ResourcesDir: "b"}); !errors.Is(err, asr.ErrNativeUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestMultiLoadsOneKindAtATime(t *testing.T) {
	r, q := asr.NewMock(), asr.NewMock()
	m := NewMulti(map[asr.Kind]asr.Backend{asr.KindR2T2: r, asr.KindQwen3: q})
	if _, err := m.Transcribe(nil, asr.TranscribeOptions{}); !errors.Is(err, asr.ErrNotLoaded) {
		t.Fatal(err)
	}
	if err := m.Load(asr.LoadSpec{Engine: "confucius4-r2t2", Kind: asr.KindR2T2}); err != nil || m.Info().Kind != asr.KindR2T2 {
		t.Fatal(err, m.Info())
	}
	if err := m.Load(asr.LoadSpec{Engine: "qwen3-asr-0.6b", Kind: asr.KindQwen3}); err != nil || m.Info().Kind != asr.KindQwen3 {
		t.Fatal(err, m.Info())
	}
	if r.Unloads.Load() != 1 || r.Info().ModelLoaded {
		t.Fatal("R2T2 not unloaded when Qwen3 loaded")
	}
	if res, err := m.Transcribe(make([]int16, asr.SampleRate), asr.TranscribeOptions{}); err != nil || res.Text != "字" {
		t.Fatal(res, err)
	}
	if st, err := m.NewStream(asr.StreamOptions{}); err != nil || st == nil {
		t.Fatal(err)
	}
	if err := m.Load(asr.LoadSpec{Kind: "other"}); !errors.Is(err, asr.ErrWrongKind) {
		t.Fatal(err)
	}
	_ = m.Unload()
	if m.Info().ModelLoaded {
		t.Fatal("still loaded")
	}
}
