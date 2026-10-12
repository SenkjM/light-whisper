// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package cudartime

// ProjectDefault is the CUDA toolkit version used to build R2T2 CUDA DLLs (PLAN §5).
const ProjectDefault = "12.9"

// ProjectMajor is the CUDA major version required for the bundled CUDA audiocpp.
const ProjectMajor = 12

// RedistribLabel is the NVIDIA redistrib_*.json release we download by default.
const RedistribLabel = "12.9.0"

const redistribBase = "https://developer.download.nvidia.com/compute/cuda/redist"

// Minimum driver versions for CUDA 12.9 (from NVIDIA CUDA 12.9 release notes).
const (
	MinDriverWindows = "576.02"
	MinDriverLinux   = "575.51.03"
)

// RequiredWindowsDLLs are the runtime libraries PLAN §5 lists (plus cufft for
// audio.cpp ggml CUDA which links CUDA::cufft).
var RequiredWindowsDLLs = []string{
	"cudart64_12.dll",
	"cublas64_12.dll",
	"cublasLt64_12.dll",
	"cufft64_11.dll",
}

// RedistribPackages map local DLL needs to NVIDIA redistrib package ids.
var RedistribPackages = []string{"cuda_cudart", "libcublas", "libcufft"}
