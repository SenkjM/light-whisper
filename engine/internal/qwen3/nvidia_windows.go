// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: GPL-3.0-only
//
// Port of server_common._has_nvidia_gpu (inherited light-whisper code).

package qwen3

import (
	"os"
	"path/filepath"
	"syscall"
)

// HasNVIDIAGPU reports whether an NVIDIA display driver is present.
// Prefers nvapi (no CUDA Toolkit required); falls back to nvcuda.dll.
func HasNVIDIAGPU() bool {
	for _, name := range []string{"nvapi64.dll", "nvapi.dll", "nvcuda.dll"} {
		d, err := syscall.LoadDLL(name)
		if err == nil {
			_ = d.Release()
			return true
		}
	}
	sys := os.Getenv("SystemRoot")
	if sys == "" {
		sys = `C:\Windows`
	}
	for _, name := range []string{"nvapi64.dll", "nvldumdx.dll"} {
		if st, err := os.Stat(filepath.Join(sys, "System32", name)); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}
