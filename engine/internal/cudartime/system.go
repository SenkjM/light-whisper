// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package cudartime

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// probeGOOS overrides runtime.GOOS in tests ("" = real OS).
var probeGOOS string

func effectiveGOOS() string {
	if probeGOOS != "" {
		return probeGOOS
	}
	return runtime.GOOS
}

var cudaVerFromName = regexp.MustCompile(`(?i)(?:cudart|cublas|cublasLt|cufft)64[_-](\d+)`)

// SystemRuntime is a probed CUDA runtime directory on the machine.
type SystemRuntime struct {
	Path    string
	Version string // e.g. "12" from cudart64_12.dll, or "12.9" if CUDA_PATH hints
	Major   int
	Files   map[string]string // required name -> absolute path found
}

// ProbeSystem looks for cudart/cublas/cublasLt (and cufft) under CUDA_PATH and PATH.
func ProbeSystem() *SystemRuntime {
	if effectiveGOOS() != "windows" {
		return probeSystemUnix()
	}
	candidates := []string{}
	if p := os.Getenv("CUDA_PATH"); p != "" {
		candidates = append(candidates, filepath.Join(p, "bin"), filepath.Join(p, "bin", "x64"))
	}
	// CUDA_PATH_V12_9 etc.
	for _, e := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(e), "CUDA_PATH_V") {
			if i := strings.IndexByte(e, '='); i > 0 {
				p := e[i+1:]
				candidates = append(candidates, filepath.Join(p, "bin"), filepath.Join(p, "bin", "x64"))
			}
		}
	}
	for _, dir := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		if dir == "" {
			continue
		}
		base := strings.ToLower(filepath.Base(dir))
		if base == "bin" || strings.Contains(strings.ToLower(dir), "cuda") {
			candidates = append(candidates, dir)
		}
	}
	seen := map[string]bool{}
	for _, dir := range candidates {
		dir = filepath.Clean(dir)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if rt := probeWindowsDir(dir); rt != nil {
			return rt
		}
	}
	return nil
}

func probeWindowsDir(dir string) *SystemRuntime {
	files := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	lower := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		lower[strings.ToLower(e.Name())] = filepath.Join(dir, e.Name())
	}
	// Accept cudart64_12.dll or cudart64_129.dll style names.
	need := []struct {
		prefix string
		key    string
	}{
		{"cudart64_", "cudart"},
		{"cublas64_", "cublas"},
		{"cublaslt64_", "cublasLt"},
		{"cufft64_", "cufft"},
	}
	var major int
	var verHint string
	for _, n := range need {
		found := ""
		for name, path := range lower {
			if strings.HasPrefix(name, n.prefix) && strings.HasSuffix(name, ".dll") {
				found = path
				if m := cudaVerFromName.FindStringSubmatch(name); len(m) == 2 {
					if v, err := strconv.Atoi(m[1]); err == nil {
						if major == 0 || (v >= 10 && v < 100 && major == 0) {
							major = v
							if v > 100 { // e.g. 129 → 12.9 hint
								verHint = strconv.Itoa(v/10) + "." + strconv.Itoa(v%10)
								major = v / 10
							} else {
								verHint = strconv.Itoa(v)
							}
						}
					}
				}
				break
			}
		}
		if found == "" && n.key != "cufft" {
			return nil // cudart/cublas/cublasLt required
		}
		if found != "" {
			files[n.key] = found
		}
	}
	if files["cudart"] == "" || files["cublas"] == "" || files["cublasLt"] == "" {
		return nil
	}
	if major == 0 {
		major = ProjectMajor
	}
	version := verHint
	if version == "" {
		version = strconv.Itoa(major)
	}
	// Prefer CUDA_PATH folder version tag if present (v12.9).
	if p := os.Getenv("CUDA_PATH"); p != "" && strings.Contains(strings.ToLower(p), "v12.9") {
		version = "12.9"
	}
	return &SystemRuntime{Path: dir, Version: version, Major: major, Files: files}
}

func probeSystemUnix() *SystemRuntime {
	// Best-effort: look for libcudart.so.12 under CUDA_PATH /usr/local/cuda/lib64.
	candidates := []string{}
	if p := os.Getenv("CUDA_PATH"); p != "" {
		candidates = append(candidates, filepath.Join(p, "lib64"), filepath.Join(p, "lib"))
	}
	candidates = append(candidates, "/usr/local/cuda/lib64", "/usr/lib/x86_64-linux-gnu")
	for _, dir := range candidates {
		cudart := filepath.Join(dir, "libcudart.so.12")
		cublas := filepath.Join(dir, "libcublas.so.12")
		cublasLt := filepath.Join(dir, "libcublasLt.so.12")
		if fileExists(cudart) && fileExists(cublas) && fileExists(cublasLt) {
			ver := "12"
			if strings.Contains(dir, "12.9") || strings.Contains(os.Getenv("CUDA_PATH"), "12.9") {
				ver = "12.9"
			}
			return &SystemRuntime{
				Path: dir, Version: ver, Major: 12,
				Files: map[string]string{"cudart": cudart, "cublas": cublas, "cublasLt": cublasLt},
			}
		}
	}
	return nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// MatchesProject reports whether version is the project default 12.9 family.
func MatchesProject(version string) bool {
	v := strings.TrimSpace(version)
	if v == ProjectDefault || strings.HasPrefix(v, ProjectDefault+".") {
		return true
	}
	// cudart64_12.dll alone only proves major 12 — not a match for "12.9".
	return false
}
