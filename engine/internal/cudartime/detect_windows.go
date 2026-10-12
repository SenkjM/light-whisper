// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package cudartime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

func driverLevelNVIDIA() bool {
	if windowsHasNVAPI() || windowsHasDisplayAdapter() {
		return true
	}
	return windowsHasNVCUDA()
}

func windowsHasNVAPI() bool {
	// NVIDIA display driver user-mode API — present with Game Ready / Studio
	// drivers without any CUDA Toolkit install.
	for _, name := range []string{"nvapi64.dll", "nvapi.dll"} {
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
	for _, name := range []string{"nvapi64.dll", "nvldumdx.dll", "nvldumd.dll"} {
		if st, err := os.Stat(filepath.Join(sys, "System32", name)); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

func windowsHasNVCUDA() bool {
	d, err := syscall.LoadDLL("nvcuda.dll")
	if err != nil {
		return false
	}
	_ = d.Release()
	return true
}

func windowsHasDisplayAdapter() bool {
	// Display class GUID {4d36e968-e325-11ce-bfc1-08002be10318}
	k, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`,
		registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE,
	)
	if err != nil {
		return wmiHasNVIDIA()
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(256)
	if err != nil {
		return wmiHasNVIDIA()
	}
	for _, name := range names {
		if name == "Properties" || !isDigits(name) {
			continue
		}
		sub, err := registry.OpenKey(k, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		provider, _, _ := sub.GetStringValue("ProviderName")
		desc, _, _ := sub.GetStringValue("DriverDesc")
		sub.Close()
		blob := strings.ToUpper(provider + " " + desc)
		if strings.Contains(blob, "NVIDIA") {
			return true
		}
	}
	return wmiHasNVIDIA()
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func wmiHasNVIDIA() bool {
	// Optional fallback; ignore failures (no PowerShell / locked down).
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		`Get-CimInstance Win32_VideoController | Select-Object -ExpandProperty Name`).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToUpper(string(out)), "NVIDIA")
}

func readDriverVersionFallback() string {
	k, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`,
		registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE,
	)
	if err != nil {
		return ""
	}
	defer k.Close()
	names, _ := k.ReadSubKeyNames(256)
	for _, name := range names {
		if !isDigits(name) {
			continue
		}
		sub, err := registry.OpenKey(k, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		provider, _, _ := sub.GetStringValue("ProviderName")
		ver, _, _ := sub.GetStringValue("DriverVersion")
		sub.Close()
		if strings.Contains(strings.ToUpper(provider), "NVIDIA") && ver != "" {
			return ver
		}
	}
	return ""
}
