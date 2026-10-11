#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
#
# PLAN §11.5 — package flavor switch (withcore | without).
#
# withcore: stage native resources into tauri.conf (expects DLLs already built).
# without: thin shell — no lw-engine / native libs in the bundle.
#
# Usage:
#   ./scripts/package_flavor.sh withcore
#   ./scripts/package_flavor.sh without

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FLAVOR="${1:-withcore}"
case "$FLAVOR" in
  withcore|without)
    python3 "$ROOT/scripts/stage_withcore_tauri_resources.py" "$FLAVOR"
    ;;
  *)
    echo "usage: $0 withcore|without" >&2
    exit 1
    ;;
esac
