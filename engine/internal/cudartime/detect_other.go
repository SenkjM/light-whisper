// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !windows

package cudartime

import (
	"os"
	"path/filepath"
	"strings"
)

func driverLevelNVIDIA() bool {
	for _, p := range []string{"/dev/nvidia0", "/dev/nvidiactl"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	entries, err := os.ReadDir("/sys/bus/pci/devices")
	if err != nil {
		return false
	}
	for _, e := range entries {
		vendor, err := os.ReadFile(filepath.Join("/sys/bus/pci/devices", e.Name(), "vendor"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(vendor)) == "0x10de" {
			return true
		}
	}
	return false
}

func windowsHasNVCUDA() bool { return false }

func readDriverVersionFallback() string { return "" }
