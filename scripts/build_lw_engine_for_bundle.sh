#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
#
# Build lw-engine into src-tauri/resources/ for the install package.
#
# Usage:
#   ./scripts/build_lw_engine_for_bundle.sh           # host OS binary (mock)
#   ./scripts/build_lw_engine_for_bundle.sh windows   # Windows .exe mock (cross)
#   ./scripts/build_lw_engine_for_bundle.sh all
#
# Native (lwnative + cgo) — set LW_ENGINE_NATIVE=1 (Windows CI / local with MinGW):
#   LW_ENGINE_NATIVE=1 ./scripts/build_lw_engine_for_bundle.sh windows
# Cross-compiling native Windows from Linux is not supported (needs Windows CGO).

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="$ROOT/src-tauri/resources"
mkdir -p "$OUT_DIR"
TARGET="${1:-host}"
cd "$ROOT/engine"

NATIVE="${LW_ENGINE_NATIVE:-0}"

build_flags=()
if [[ "$NATIVE" == "1" || "$NATIVE" == "true" ]]; then
  export CGO_ENABLED=1
  build_flags=(-tags lwnative)
  echo "Building with -tags lwnative (CGO_ENABLED=1)"
else
  export CGO_ENABLED=0
fi

build_host() {
  local name="lw-engine"
  if [[ "$(uname -s)" == MINGW* || "$(uname -s)" == MSYS* || "$(uname -s)" == CYGWIN* || "$(uname -s)" == Windows_NT ]]; then
    name="lw-engine.exe"
  fi
  go build "${build_flags[@]}" -o "$OUT_DIR/$name" ./cmd/lw-engine
  echo "Built $OUT_DIR/$name"
}

build_windows() {
  if [[ "$NATIVE" == "1" || "$NATIVE" == "true" ]]; then
    if [[ "$(uname -s)" != MINGW* && "$(uname -s)" != MSYS* && "$(uname -s)" != CYGWIN* && "${OS:-}" != "Windows_NT" ]]; then
      echo "ERROR: LW_ENGINE_NATIVE=1 Windows build must run on Windows (CGO)." >&2
      exit 1
    fi
    export GOOS=windows GOARCH=amd64
  else
    export GOOS=windows GOARCH=amd64
  fi
  go build "${build_flags[@]}" -o "$OUT_DIR/lw-engine.exe" ./cmd/lw-engine
  echo "Built $OUT_DIR/lw-engine.exe"
}

case "$TARGET" in
  host) build_host ;;
  windows) build_windows ;;
  all)
    build_host
    if [[ "$(uname -s)" != MINGW* && "$(uname -s)" != MSYS* && "$(uname -s)" != CYGWIN* ]]; then
      # Cross mock only from non-Windows
      NATIVE_SAVE="$NATIVE"
      LW_ENGINE_NATIVE=0 NATIVE=0
      GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o "$OUT_DIR/lw-engine.exe" ./cmd/lw-engine
      NATIVE="$NATIVE_SAVE"
      echo "Built $OUT_DIR/lw-engine.exe (cross mock)"
    fi
    ;;
  *)
    echo "unknown target: $TARGET (host|windows|all)" >&2
    exit 1
    ;;
esac

echo "Bundle binary ready. Models are not included — download separately."
