#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
#
# Optional merge of command-cube-cuda-native into a resources tree.
# Current withcore already bundles CUDA R2T2 (r2t2-native/cuda/); this script
# is redundant for that path. Still useful to swap CUDA transcribe VARIANT or
# refresh libs on an older install (PLAN §5: cudart/cublas stay on-demand).
#
# Usage:
#   ./scripts/install_cuda_native_libs.sh /path/to/extracted-cuda-artifact [/path/to/resources]
# Default resources dir: src-tauri/resources
# Windows: prefer scripts/install_cuda_native_libs.ps1

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="${1:?usage: $0 <cuda-artifact-dir> [resources-dir]}"
DEST="${2:-$ROOT/src-tauri/resources}"

if [[ ! -f "$SRC/r2t2-native/cuda/audiocpp.dll" && ! -f "$SRC/audiocpp.dll" ]]; then
  echo "expected r2t2-native/cuda/audiocpp.dll (or audiocpp.dll) under $SRC" >&2
  ls -la "$SRC" >&2 || true
  exit 1
fi

mkdir -p "$DEST/r2t2-native"
if [[ -d "$SRC/r2t2-native/cuda" ]]; then
  rm -rf "$DEST/r2t2-native/cuda"
  cp -a "$SRC/r2t2-native/cuda" "$DEST/r2t2-native/cuda"
elif [[ -f "$SRC/audiocpp.dll" ]]; then
  mkdir -p "$DEST/r2t2-native/cuda"
  cp -a "$SRC"/. "$DEST/r2t2-native/cuda/"
fi

if [[ -f "$SRC/transcribe-native/transcribe.dll" ]]; then
  rm -rf "$DEST/transcribe-native"
  cp -a "$SRC/transcribe-native" "$DEST/transcribe-native"
  echo "Installed CUDA transcribe into $DEST/transcribe-native"
fi

echo "Installed CUDA R2T2 libs into $DEST/r2t2-native/cuda"
echo "Note: current withcore already bundles CUDA R2T2; merge is optional/redundant for that path."
echo "Engine prefers cuda then cpu when device is not forced to cpu."
echo "CUDA runtime (cudart/cublas) must come from the system or Workbench on-demand download (PLAN §5)."
