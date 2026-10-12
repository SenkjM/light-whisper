// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package cudartime

import (
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var driverLine = regexp.MustCompile(`(?i)Driver Version\s*:\s*([0-9.]+)`)

// DetectNVIDIA reports whether an NVIDIA GPU appears to be present and the
// driver version from nvidia-smi when available.
func DetectNVIDIA() (present bool, driver string) {
	out, err := exec.Command("nvidia-smi").CombinedOutput()
	if err == nil {
		text := string(out)
		if m := driverLine.FindStringSubmatch(text); len(m) == 2 {
			return true, m[1]
		}
		// nvidia-smi ran; treat as present even if version parse failed.
		return true, ""
	}
	if runtime.GOOS == "windows" {
		return windowsHasNVCUDA(), ""
	}
	return false, ""
}

// DriverMeetsMinimum compares dotted versions (e.g. 576.02 vs 575.51.03).
func DriverMeetsMinimum(driver, minimum string) bool {
	if driver == "" || minimum == "" {
		return false
	}
	return compareDotted(driver, minimum) >= 0
}

func compareDotted(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		if i < len(as) {
			ai, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bi, _ = strconv.Atoi(bs[i])
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

func minDriverForOS() string {
	if runtime.GOOS == "windows" {
		return MinDriverWindows
	}
	return MinDriverLinux
}
