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

// DetectNVIDIA reports whether an NVIDIA GPU / driver is present without
// requiring the CUDA Toolkit. Order (PLAN §5):
//  1. Driver-level signals (nvapi / display adapter / device node) — no toolkit
//  2. nvidia-smi for driver version when on PATH
//  3. nvcuda.dll only as a secondary hint (CUDA driver user-mode, still not Toolkit)
func DetectNVIDIA() (present bool, driver string) {
	present = driverLevelNVIDIA()
	if out, err := exec.Command("nvidia-smi").CombinedOutput(); err == nil {
		text := string(out)
		present = true
		if m := driverLine.FindStringSubmatch(text); len(m) == 2 {
			driver = m[1]
		}
	}
	if !present && runtime.GOOS == "windows" {
		present = windowsHasNVCUDA()
	}
	if present && driver == "" {
		driver = readDriverVersionFallback()
	}
	return present, driver
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
