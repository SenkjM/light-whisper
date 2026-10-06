// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: GPL-3.0-only
//
// Port of server_common._has_nvidia_gpu (inherited light-whisper code).

package qwen3

import "syscall"

// HasNVIDIAGPU reports whether the NVIDIA CUDA driver (nvcuda.dll) loads.
func HasNVIDIAGPU() bool {
	d, err := syscall.LoadDLL("nvcuda.dll")
	if err != nil {
		return false
	}
	_ = d.Release()
	return true
}
