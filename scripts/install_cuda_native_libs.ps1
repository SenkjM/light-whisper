# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
#
# Optional merge of command-cube-cuda-native into resources (PLAN §5: cudart on-demand).
# Current withcore already bundles CUDA R2T2 + CUDA transcribe; this is redundant
# for that path. Still useful for refreshing an older install.
#
# Usage:
#   .\scripts\install_cuda_native_libs.ps1 -Src C:\path\to\extracted-cuda-artifact [-Dest C:\path\to\resources]
# Default Dest: <repo>\src-tauri\resources  (dev) — for installed apps, point Dest at
#   <install>\resources  (next to lw-engine.exe).

param(
  [Parameter(Mandatory = $true)][string]$Src,
  [string]$Dest = ""
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
if (-not $Dest) { $Dest = Join-Path $Root "src-tauri\resources" }

$cudaDll = Join-Path $Src "r2t2-native\cuda\audiocpp.dll"
$flatDll = Join-Path $Src "audiocpp.dll"
if (-not (Test-Path $cudaDll) -and -not (Test-Path $flatDll)) {
  Write-Error "expected r2t2-native\cuda\audiocpp.dll (or audiocpp.dll) under $Src"
}

New-Item -ItemType Directory -Force -Path (Join-Path $Dest "r2t2-native") | Out-Null
$destCuda = Join-Path $Dest "r2t2-native\cuda"
if (Test-Path (Join-Path $Src "r2t2-native\cuda")) {
  if (Test-Path $destCuda) { Remove-Item -Recurse -Force $destCuda }
  Copy-Item -Recurse (Join-Path $Src "r2t2-native\cuda") $destCuda
} else {
  New-Item -ItemType Directory -Force -Path $destCuda | Out-Null
  Copy-Item -Recurse (Join-Path $Src "*") $destCuda
}

$trSrc = Join-Path $Src "transcribe-native\transcribe.dll"
if (Test-Path $trSrc) {
  $trDest = Join-Path $Dest "transcribe-native"
  if (Test-Path $trDest) { Remove-Item -Recurse -Force $trDest }
  Copy-Item -Recurse (Join-Path $Src "transcribe-native") $trDest
  Write-Host "Installed CUDA transcribe into $trDest"
}

Write-Host "Installed CUDA R2T2 libs into $destCuda"
Write-Host "Note: current withcore already bundles CUDA R2T2 + CUDA transcribe; merge is optional/redundant."
Write-Host "Engine prefers cuda then cpu when device is not forced to cpu."
Write-Host "CUDA runtime (cudart/cublas): Workbench one-click or system PATH (PLAN §5)."
