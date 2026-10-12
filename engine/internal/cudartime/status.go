// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package cudartime

// Source identifies where the active / candidate runtime comes from.
type Source string

const (
	SourceNone       Source = "none"
	SourceSystem     Source = "system"
	SourceDownloaded Source = "downloaded"
)

// Status is GET /v1/runtimes/cuda (PLAN §5.5).
type Status struct {
	ProjectCUDAVersion string `json:"project_cuda_version"`
	RequiredMajor      int    `json:"required_major"`

	NVIDIADetected bool   `json:"nvidia_detected"`
	DriverVersion  string `json:"driver_version,omitempty"`
	DriverOK       bool   `json:"driver_ok"`
	DriverMessage  string `json:"driver_message,omitempty"`

	SystemAvailable bool   `json:"system_available"`
	SystemVersion   string `json:"system_version,omitempty"`
	SystemPath      string `json:"system_path,omitempty"`
	SystemMatches   bool   `json:"system_matches_project"` // exact 12.9.x family
	SystemMajorOK   bool   `json:"system_major_ok"`        // major == 12
	VersionMismatch bool   `json:"version_mismatch"`       // system present but != project default
	VersionWarning  string `json:"version_warning,omitempty"`

	DownloadedAvailable bool   `json:"downloaded_available"`
	DownloadedVersion   string `json:"downloaded_version,omitempty"`
	DownloadedPath      string `json:"downloaded_path,omitempty"`
	DownloadedVerified  bool   `json:"downloaded_verified"`

	ActiveSource Source `json:"active_source"`
	ActivePath   string `json:"active_path,omitempty"`
	ActiveVersion string `json:"active_version,omitempty"`

	DevicePreference string `json:"device_preference"` // from config
	UsingCUDABackend bool   `json:"using_cuda_backend"`

	Download *DownloadState `json:"download,omitempty"`
	CUDALibsPresent bool    `json:"cuda_r2t2_dll_present"` // audiocpp cuda sibling present
}

// DownloadState is embedded in Status and download_progress events.
type DownloadState struct {
	Active    bool    `json:"active"`
	Target    string  `json:"target"` // "cuda_runtime"
	Progress  float64 `json:"progress"` // 0..1
	BytesDone int64   `json:"bytes_done"`
	BytesTotal int64  `json:"bytes_total,omitempty"`
	Message   string  `json:"message,omitempty"`
	Error     string  `json:"error,omitempty"`
}
