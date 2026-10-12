// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package cudartime

import "syscall"

func windowsHasNVCUDA() bool {
	d, err := syscall.LoadDLL("nvcuda.dll")
	if err != nil {
		return false
	}
	_ = d.Release()
	return true
}
