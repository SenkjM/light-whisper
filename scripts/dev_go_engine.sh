#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
#
# Start lw-engine for browser debugging without Tauri (PLAN 步骤 6).
# Then: pnpm dev → http://localhost:1422/?view=dev-engine
#
# Env:
#   LW_ENGINE_GO_BACKEND  mock|native (default mock)
#   LW_ENGINE_LISTEN      default 127.0.0.1:8741
#   LW_ENGINE_DATA_DIR    optional data dir

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
LISTEN="${LW_ENGINE_LISTEN:-127.0.0.1:8741}"
BACKEND="${LW_ENGINE_GO_BACKEND:-mock}"
TOKEN_FILE="${LW_ENGINE_TOKEN_FILE:-$ROOT/.lw-engine-dev.token}"
BIN="${LW_ENGINE_PATH:-}"
if [[ -z "$BIN" ]]; then
  if [[ -x "$ROOT/engine/lw-engine" ]]; then
    BIN="$ROOT/engine/lw-engine"
  elif [[ -x "$ROOT/src-tauri/resources/lw-engine" ]]; then
    BIN="$ROOT/src-tauri/resources/lw-engine"
  else
    echo "Building lw-engine…"
    (cd "$ROOT/engine" && go build -o lw-engine ./cmd/lw-engine)
    BIN="$ROOT/engine/lw-engine"
  fi
fi

DATA_ARGS=()
if [[ -n "${LW_ENGINE_DATA_DIR:-}" ]]; then
  DATA_ARGS+=(--data-dir "$LW_ENGINE_DATA_DIR")
fi

echo "Listening on $LISTEN (backend=$BACKEND)"
echo "Token file: $TOKEN_FILE"
echo "Then run:  pnpm dev"
echo "Open:      http://localhost:1422/?view=dev-engine"
echo "Paste the token from:  cat $TOKEN_FILE"
exec "$BIN" \
  --listen "$LISTEN" \
  --backend "$BACKEND" \
  --token-file "$TOKEN_FILE" \
  --allow-origin "http://localhost:1422" \
  --allow-origin "http://127.0.0.1:1422" \
  "${DATA_ARGS[@]}"
