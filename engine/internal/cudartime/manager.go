// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Package cudartime implements PLAN §5 CUDA runtime on-demand install.
package cudartime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/SenkjM/light-whisper/engine/internal/events"
)

// ErrVersionMismatch is returned when the caller must acknowledge a system
// runtime that is not the project default 12.9 before using it.
var ErrVersionMismatch = errors.New("cuda_runtime_version_mismatch")

// ErrDriverTooOld is returned when the NVIDIA driver is below the CUDA 12.9 floor.
var ErrDriverTooOld = errors.New("cuda_driver_too_old")

// ErrNoNVIDIA is returned when no NVIDIA GPU is detected.
var ErrNoNVIDIA = errors.New("nvidia_not_detected")

// ErrDownloadInProgress is returned when a download is already running.
var ErrDownloadInProgress = errors.New("cuda_download_in_progress")

// Manager owns detection, download, and the active CUDA runtime directory.
type Manager struct {
	dataDir string
	hub     *events.Hub
	client  *http.Client
	goos    string

	mu       sync.Mutex
	download *DownloadState
	cancel   context.CancelFunc
	active   activeFile
}

type activeFile struct {
	Source  Source `json:"source"`
	Path    string `json:"path"`
	Version string `json:"version"`
}

// Options configure Manager.
type Options struct {
	DataDir    string
	Hub        *events.Hub
	HTTPClient *http.Client
	GOOS       string // override for tests; "" = runtime.GOOS
}

// NewManager creates a CUDA runtime manager under dataDir/runtimes/cuda.
func NewManager(opts Options) *Manager {
	c := opts.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: 0} // large downloads; per-request ctx cancels
	}
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	m := &Manager{dataDir: opts.DataDir, hub: opts.Hub, client: c, goos: goos}
	m.active = m.loadActive()
	return m
}

func (m *Manager) root() string {
	return filepath.Join(m.dataDir, "runtimes", "cuda")
}

func (m *Manager) downloadedDir() string {
	return filepath.Join(m.root(), RedistribLabel)
}

func (m *Manager) activePath() string {
	return filepath.Join(m.root(), "active.json")
}

func (m *Manager) loadActive() activeFile {
	b, err := os.ReadFile(m.activePath())
	if err != nil {
		return activeFile{Source: SourceNone}
	}
	var a activeFile
	if json.Unmarshal(b, &a) != nil || a.Path == "" {
		return activeFile{Source: SourceNone}
	}
	if !dirHasRequired(a.Path, m.goos) {
		return activeFile{Source: SourceNone}
	}
	return a
}

func (m *Manager) saveActive(a activeFile) error {
	if err := os.MkdirAll(m.root(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.activePath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.activePath())
}

// ActiveDir returns the directory to put on the DLL/so search path, or "".
func (m *Manager) ActiveDir() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active.Source == SourceNone || m.active.Path == "" {
		return ""
	}
	return m.active.Path
}

// Status builds the PLAN §5.5 status payload.
func (m *Manager) Status(devicePref string, usingCUDA bool, cudaDLLPresent bool) Status {
	nvidia, driver := DetectNVIDIA()
	minDrv := minDriverForOS()
	driverOK := false
	driverMsg := ""
	if !nvidia {
		driverMsg = "no NVIDIA GPU detected"
	} else if driver == "" {
		driverMsg = "NVIDIA GPU present but driver version unknown; install/update nvidia-smi"
		driverOK = true // don't hard-block when smi missing but nvcuda loads
	} else if DriverMeetsMinimum(driver, minDrv) {
		driverOK = true
	} else {
		driverMsg = fmt.Sprintf("driver %s is below CUDA %s minimum %s; update the NVIDIA driver", driver, ProjectDefault, minDrv)
	}

	sys := ProbeSystem()
	st := Status{
		ProjectCUDAVersion: ProjectDefault,
		RequiredMajor:      ProjectMajor,
		NVIDIADetected:     nvidia,
		DriverVersion:      driver,
		DriverOK:           driverOK,
		DriverMessage:      driverMsg,
		DevicePreference:   devicePref,
		UsingCUDABackend:   usingCUDA,
		CUDALibsPresent:    cudaDLLPresent,
		ActiveSource:       SourceNone,
	}

	if sys != nil {
		st.SystemAvailable = true
		st.SystemVersion = sys.Version
		st.SystemPath = sys.Path
		st.SystemMajorOK = sys.Major == ProjectMajor
		st.SystemMatches = MatchesProject(sys.Version)
		st.VersionMismatch = st.SystemAvailable && !st.SystemMatches
		if st.VersionMismatch {
			st.VersionWarning = fmt.Sprintf(
				"System CUDA runtime is %s; this build targets %s. It may still work (major %d) but is unverified — continue only if you accept the risk.",
				sys.Version, ProjectDefault, ProjectMajor,
			)
		}
	}

	dlDir := m.downloadedDir()
	if dirHasRequired(dlDir, m.goos) {
		st.DownloadedAvailable = true
		st.DownloadedPath = dlDir
		st.DownloadedVersion = RedistribLabel
		st.DownloadedVerified = true
	}

	m.mu.Lock()
	st.ActiveSource = m.active.Source
	st.ActivePath = m.active.Path
	st.ActiveVersion = m.active.Version
	if m.download != nil {
		cp := *m.download
		st.Download = &cp
	}
	m.mu.Unlock()
	return st
}

func dirHasRequired(dir, goos string) bool {
	if dir == "" {
		return false
	}
	if goos == "windows" {
		return anyPrefixDLL(dir, "cudart64_") &&
			anyPrefixDLL(dir, "cublas64_") &&
			anyPrefixDLL(dir, "cublaslt64_")
	}
	return fileExists(filepath.Join(dir, "libcudart.so.12")) &&
		fileExists(filepath.Join(dir, "libcublas.so.12")) &&
		fileExists(filepath.Join(dir, "libcublasLt.so.12"))
}

func anyPrefixDLL(dir, prefix string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	prefix = strings.ToLower(prefix)
	for _, e := range ents {
		n := strings.ToLower(e.Name())
		if strings.HasPrefix(n, prefix) && strings.HasSuffix(n, ".dll") {
			return true
		}
	}
	return false
}

// StartDownload begins (or resumes) downloading redistrib packages into dataDir.
func (m *Manager) StartDownload(parent context.Context) error {
	m.mu.Lock()
	if m.download != nil && m.download.Active {
		m.mu.Unlock()
		return ErrDownloadInProgress
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.download = &DownloadState{Active: true, Target: "cuda_runtime", Message: "starting"}
	m.mu.Unlock()
	m.publishProgress()

	go func() {
		err := m.runDownload(ctx)
		m.mu.Lock()
		if m.download != nil {
			m.download.Active = false
			if err != nil && !errors.Is(err, context.Canceled) {
				m.download.Error = err.Error()
				m.download.Message = "failed"
			} else if err == nil {
				m.download.Progress = 1
				m.download.Message = "complete"
				m.download.Error = ""
			} else {
				m.download.Message = "cancelled"
			}
		}
		m.cancel = nil
		m.mu.Unlock()
		m.publishProgress()
	}()
	return nil
}

// CancelDownload aborts an in-flight download.
func (m *Manager) CancelDownload() {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *Manager) runDownload(ctx context.Context) error {
	pkgs, err := fetchRedistrib(ctx, m.client, RedistribLabel)
	if err != nil {
		return err
	}
	staging := filepath.Join(m.root(), "staging", RedistribLabel)
	dest := m.downloadedDir()
	_ = os.MkdirAll(staging, 0o755)

	type job struct {
		id  string
		art *redistribArtifact
	}
	var jobs []job
	var totalBytes int64
	for _, id := range RedistribPackages {
		pkg := pkgs[id]
		art, err := artifactFor(pkg, m.goos)
		if err != nil {
			return err
		}
		jobs = append(jobs, job{id: id, art: art})
		var sz int64
		fmt.Sscan(art.Size, &sz)
		totalBytes += sz
	}

	m.mu.Lock()
	if m.download != nil {
		m.download.BytesTotal = totalBytes
		m.download.Message = "downloading"
	}
	m.mu.Unlock()

	var doneBytes int64
	extracted := map[string]string{}
	for _, j := range jobs {
		url := redistribBase + "/" + j.art.RelativePath
		zipPath := filepath.Join(staging, filepath.Base(j.art.RelativePath))
		baseDone := doneBytes
		err := downloadFile(ctx, m.client, url, j.art.SHA256, zipPath, func(d, _ int64) {
			m.mu.Lock()
			if m.download != nil {
				m.download.BytesDone = baseDone + d
				if totalBytes > 0 {
					m.download.Progress = float64(m.download.BytesDone) / float64(totalBytes)
				}
				m.download.Message = "downloading " + j.id
			}
			m.mu.Unlock()
			m.publishProgress()
		})
		if err != nil {
			return err
		}
		var sz int64
		fmt.Sscan(j.art.Size, &sz)
		doneBytes += sz

		want := dllNamesForPackage(j.id, m.goos)
		tmpOut := filepath.Join(staging, "out", j.id)
		_ = os.RemoveAll(tmpOut)
		if err := extractNeededDLLs(zipPath, tmpOut, want); err != nil {
			return err
		}
		ents, _ := os.ReadDir(tmpOut)
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			extracted[e.Name()] = filepath.Join(tmpOut, e.Name())
		}
	}

	_ = os.RemoveAll(dest)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	finalFiles := map[string]string{}
	for name, src := range extracted {
		dst := filepath.Join(dest, name)
		if err := copyFile(src, dst); err != nil {
			return err
		}
		finalFiles[name] = dst
	}
	if err := writeVerifiedManifest(dest, RedistribLabel, finalFiles); err != nil {
		return err
	}
	if !dirHasRequired(dest, m.goos) {
		return fmt.Errorf("downloaded runtime incomplete in %s", dest)
	}
	return nil
}

func dllNamesForPackage(id, goos string) []string {
	if goos != "windows" {
		switch id {
		case "cuda_cudart":
			return []string{"libcudart.so.12"}
		case "libcublas":
			return []string{"libcublas.so.12", "libcublasLt.so.12"}
		case "libcufft":
			return []string{"libcufft.so.11"}
		}
		return nil
	}
	switch id {
	case "cuda_cudart":
		return []string{"cudart64_12.dll"}
	case "libcublas":
		return []string{"cublas64_12.dll", "cublasLt64_12.dll"}
	case "libcufft":
		return []string{"cufft64_11.dll"}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// Enable selects a runtime source and records it as active.
// preferSystem tries system first; acknowledgeMismatch is required when
// system version != project default 12.9.
func (m *Manager) Enable(preferSystem, acknowledgeMismatch bool) (Status, error) {
	nvidia, driver := DetectNVIDIA()
	if !nvidia {
		return Status{}, ErrNoNVIDIA
	}
	minDrv := minDriverForOS()
	if driver != "" && !DriverMeetsMinimum(driver, minDrv) {
		return Status{}, fmt.Errorf("%w: %s < %s", ErrDriverTooOld, driver, minDrv)
	}

	sys := ProbeSystem()
	if preferSystem && sys != nil && sys.Major == ProjectMajor {
		mismatch := !MatchesProject(sys.Version)
		if mismatch && !acknowledgeMismatch {
			st := m.Status("", false, false)
			return st, ErrVersionMismatch
		}
		a := activeFile{Source: SourceSystem, Path: sys.Path, Version: sys.Version}
		if err := m.saveActive(a); err != nil {
			return Status{}, err
		}
		m.mu.Lock()
		m.active = a
		m.mu.Unlock()
		return m.Status("", false, false), nil
	}

	// Use downloaded 12.9 package.
	dest := m.downloadedDir()
	if !dirHasRequired(dest, m.goos) {
		return Status{}, fmt.Errorf("cuda_runtime_not_installed: download CUDA %s runtime first", ProjectDefault)
	}
	a := activeFile{Source: SourceDownloaded, Path: dest, Version: RedistribLabel}
	if err := m.saveActive(a); err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	m.active = a
	m.mu.Unlock()
	return m.Status("", false, false), nil
}

// Disable clears the active CUDA runtime selection (CPU fallback path).
func (m *Manager) Disable() error {
	_ = os.Remove(m.activePath())
	m.mu.Lock()
	m.active = activeFile{Source: SourceNone}
	m.mu.Unlock()
	return nil
}

func (m *Manager) publishProgress() {
	if m.hub == nil {
		return
	}
	m.mu.Lock()
	var data any
	if m.download != nil {
		cp := *m.download
		data = cp
	}
	m.mu.Unlock()
	m.hub.Publish(events.TypeDownloadProgress, data)
}
