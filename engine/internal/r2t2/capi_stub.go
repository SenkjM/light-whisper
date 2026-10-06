// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !lwnative || !cgo

package r2t2

// NativeAvailable reports whether this build can load audio.cpp.
const NativeAvailable = false

// OpenLibrary is unavailable without the "lwnative" tag and cgo.
func OpenLibrary(path string, dllDirs []string) (CLib, error) { return nil, ErrNativeUnavailable }
