#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
#
# Build lw-engine into src-tauri/resources/ for the default (pure Go) install
# package. Replaces the former PyInstaller engine.tar.xz path.
#
# Usage:
#   ./scripts/build_lw_engine_for_bundle.sh           # host OS binary
#   ./scripts/build_lw_engine_for_bundle.sh windows   # Windows .exe (GOOS=windows)
#   ./scripts/build_lw_engine_for_bundle.sh all       # host + windows .exe
#
# NSIS packaging (tauri.conf.json targets: nsis) requires resources/lw-engine.exe.
# Default `go build` is pure Go (mock inference). For real ASR use:
#   CGO_ENABLED=1 go build -tags lwnative …
# and ship native DLLs separately (see engine/README.md).

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="$ROOT/src-tauri/resources"
mkdir -p "$OUT_DIR"
TARGET="${1:-host}"
cd "$ROOT/engine"

build_host() {
  local name="lw-engine"
  if [[ "$(uname -s)" == MINGW* || "$(uname -s)" == MSYS* || "$(uname -s)" == CYGWIN* ]]; then
    name="lw-engine.exe"
  fi
  go build -o "$OUT_DIR/$name" ./cmd/lw-engine
  echo "Built $OUT_DIR/$name"
}

build_windows() {
  GOOS=windows GOARCH=amd64 go build -o "$OUT_DIR/lw-engine.exe" ./cmd/lw-engine
  echo "Built $OUT_DIR/lw-engine.exe"
}

case "$TARGET" in
  host) build_host ;;
  windows) build_windows ;;
  all)
    build_host
    # On Windows host, host already produced .exe; still ensure name.
    if [[ "$(uname -s)" != MINGW* && "$(uname -s)" != MSYS* && "$(uname -s)" != CYGWIN* ]]; then
      build_windows
    fi
    ;;
  *)
    echo "unknown target: $TARGET (host|windows|all)" >&2
    exit 1
    ;;
esac

echo "Default install package uses this binary (LW_ENGINE_BACKEND defaults to go)."
echo "Python / engine.tar.xz are not bundled; set LW_ENGINE_BACKEND=python only for local source-tree debugging."
