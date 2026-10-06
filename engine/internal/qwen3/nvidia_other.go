// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: GPL-3.0-only
//
// Port of server_common._has_nvidia_gpu (inherited light-whisper code).

//go:build !windows

package qwen3

import "path/filepath"

// HasNVIDIAGPU reports whether an NVIDIA device node exists (/dev/nvidia0…).
func HasNVIDIAGPU() bool {
	m, _ := filepath.Glob("/dev/nvidia[0-9]*")
	return len(m) > 0
}
