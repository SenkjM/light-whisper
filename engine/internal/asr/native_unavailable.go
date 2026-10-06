// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !lwnative

package asr

// NewNative returns the native (cgo) backend. In builds without the
// "lwnative" tag it always fails; the cgo bindings to audio.cpp /
// transcribe.cpp / onnxruntime will be added in a file guarded by
// //go:build lwnative (Windows + CGO_ENABLED=1, migration steps 2–4).
func NewNative() (Backend, error) { return nil, ErrNativeUnavailable }
