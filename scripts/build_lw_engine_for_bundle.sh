#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
#
# Build lw-engine into src-tauri/resources/ for a Go-backend install package.
# Default python packaging still uses engine.tar.xz and is unchanged.
#
# Usage:
#   ./scripts/build_lw_engine_for_bundle.sh           # host OS binary
#   ./scripts/build_lw_engine_for_bundle.sh windows   # cross to Windows .exe (needs zig/mingw)
#
# After building, add to src-tauri/tauri.conf.json bundle.resources:
#   "resources/lw-engine.exe": "resources/lw-engine.exe"   # Windows
#   or "resources/lw-engine": "resources/lw-engine"       # Unix
# Do NOT remove engine.tar.xz from the default python package.

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="$ROOT/src-tauri/resources"
mkdir -p "$OUT_DIR"
TARGET="${1:-host}"
cd "$ROOT/engine"
case "$TARGET" in
  host)
    name="lw-engine"
    if [[ "$(uname -s)" == MINGW* || "$(uname -s)" == MSYS* || "$(uname -s)" == CYGWIN* ]]; then
      name="lw-engine.exe"
    fi
    go build -o "$OUT_DIR/$name" ./cmd/lw-engine
    echo "Built $OUT_DIR/$name"
    ;;
  windows)
    GOOS=windows GOARCH=amd64 go build -o "$OUT_DIR/lw-engine.exe" ./cmd/lw-engine
    echo "Built $OUT_DIR/lw-engine.exe"
    ;;
  *)
    echo "unknown target: $TARGET (host|windows)" >&2
    exit 1
    ;;
esac
echo "Reminder: default LW_ENGINE_BACKEND=python packages still need engine.tar.xz."
echo "For Go-only installs set LW_ENGINE_BACKEND=go and include the binary above in bundle.resources."
