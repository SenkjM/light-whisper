// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !lwnative || !cgo

package vad

// ONNXOptions configures the onnxruntime-backed model (lwnative builds).
type ONNXOptions struct {
	// LibraryPath is the onnxruntime shared library (onnxruntime.dll,
	// libonnxruntime.so.*, libonnxruntime.*.dylib).
	LibraryPath string
}

// NativeAvailable reports whether this build can run the ONNX model.
const NativeAvailable = false

// OpenONNX is unavailable in the default (pure Go) build.
func OpenONNX(modelPath string, opts ONNXOptions) (Model, error) {
	return nil, ErrNativeUnavailable
}
