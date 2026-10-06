// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

module github.com/SenkjM/light-whisper/engine

go 1.24

require (
	github.com/coder/websocket v1.8.15
	// v1.27.x requests ORT C API 24, so it loads onnxruntime >= 1.24 (the app
	// bundles 1.24.1); newer onnxruntime_go releases need a newer runtime.
	github.com/yalue/onnxruntime_go v1.27.0
)
