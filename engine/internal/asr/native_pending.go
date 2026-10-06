// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build lwnative

package asr

import "fmt"

// errNativePending is returned by lwnative builds until the cgo ASR bindings
// exist. Since migration step 2 the tag already compiles the onnxruntime
// FireRedVAD runtime (internal/vad); the ASR engines follow in steps 3–4 and
// will replace this file.
var errNativePending = fmt.Errorf("native ASR backend not implemented yet (the lwnative build so far only adds the FireRedVAD runtime; ASR follows in migration steps 3–4): %w", ErrNativeUnavailable)

// NewNative returns the native (cgo) backend; not implemented yet.
func NewNative() (Backend, error) { return nil, errNativePending }
