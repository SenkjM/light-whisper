// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !lwnative || !cgo

package qwen3

import "github.com/SenkjM/light-whisper/engine/internal/asr"

// NativeAvailable reports whether this build can load transcribe.cpp.
const NativeAvailable = false

// OpenLibrary is unavailable without the lwnative build tag and cgo.
func OpenLibrary(path string, dllDirs []string) (CLib, error) { return nil, asr.ErrNativeUnavailable }
