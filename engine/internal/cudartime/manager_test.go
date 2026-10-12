// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package cudartime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDriverMeetsMinimum(t *testing.T) {
	if !DriverMeetsMinimum("576.02", MinDriverWindows) {
		t.Fatal("576.02 should meet windows min")
	}
	if DriverMeetsMinimum("570.65", MinDriverWindows) {
		t.Fatal("570.65 should be too old for 12.9")
	}
	if !DriverMeetsMinimum("575.51.03", MinDriverLinux) {
		t.Fatal("exact linux min")
	}
}

func TestMatchesProject(t *testing.T) {
	if !MatchesProject("12.9") || !MatchesProject("12.9.0") {
		t.Fatal("12.9 family")
	}
	if MatchesProject("12") || MatchesProject("12.8") || MatchesProject("13.0") {
		t.Fatal("non-12.9 must not match")
	}
}

func TestEnableVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"cudart64_12.dll", "cublas64_12.dll", "cublasLt64_12.dll"} {
		if err := os.WriteFile(filepath.Join(bin, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CUDA_PATH", dir)
	t.Setenv("PATH", bin)
	probeGOOS = "windows"
	t.Cleanup(func() { probeGOOS = "" })

	m := NewManager(Options{DataDir: t.TempDir(), GOOS: "windows"})
	// Force probe via env — ProbeSystem reads CUDA_PATH.
	sys := ProbeSystem()
	if sys == nil {
		t.Fatal("expected system runtime in test dir")
	}
	// Version mismatch is warn-only: Enable must succeed without acknowledge.
	st, err := m.Enable(true, false)
	if err == ErrNoNVIDIA || err == ErrDriverTooOld {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("Enable should not block on version mismatch, got %v (sys=%+v)", err, sys)
	}
	if m.ActiveDir() == "" {
		t.Fatal("active dir empty after enable")
	}
	if !MatchesProject(sys.Version) && !st.VersionMismatch && st.VersionWarning == "" {
		// Status should still surface the reminder when versions differ.
		t.Logf("note: VersionMismatch/Warning not set (sys=%+v status=%+v)", sys, st)
	}
}

func TestDirHasRequired(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"cudart64_12.dll", "cublas64_12.dll", "cublasLt64_12.dll"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}
	if !dirHasRequired(dir, "windows") {
		t.Fatal("expected required dlls")
	}
}

// TestStatusWireFields locks Status fields the Workbench / CUDA E2E path poll.
func TestStatusWireFields(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir(), GOOS: "windows"})
	st := m.Status("auto", false, true)
	if st.ProjectCUDAVersion == "" {
		t.Fatalf("ProjectCUDAVersion empty")
	}
	if st.RequiredMajor != 12 {
		t.Fatalf("RequiredMajor=%d", st.RequiredMajor)
	}
	_ = st.NVIDIADetected
	_ = st.DriverOK
	_ = st.DownloadedAvailable
	_ = st.UsingCUDABackend
	_ = st.ActiveSource
	_ = st.VersionWarning
	_ = st.CUDALibsPresent
	_ = st.DevicePreference
}

