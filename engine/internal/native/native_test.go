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
	o := OptionsFromEnv(filepath.FromSlash("/app/res"))
	if o.ResourcesDir != filepath.FromSlash("/app/res") || o.RuntimeDir != filepath.FromSlash("/app/res/r2t2-native") {
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
