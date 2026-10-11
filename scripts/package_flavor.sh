#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
#
# PLAN §11.5 — package flavor switch (withcore | without).
# withcore: ensure lw-engine.exe is built into resources (default).
# without: remove lw-engine from tauri bundle resources for a thin shell
#          that only connects to an existing management backend URL.
#
# Usage:
#   ./scripts/package_flavor.sh withcore
#   ./scripts/package_flavor.sh without

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FLAVOR="${1:-withcore}"
CONF="$ROOT/src-tauri/tauri.conf.json"
case "$FLAVOR" in
  withcore)
    bash "$ROOT/scripts/build_lw_engine_for_bundle.sh" windows
    node -e '
      const fs=require("fs");
      const p="src-tauri/tauri.conf.json";
      const j=JSON.parse(fs.readFileSync(p,"utf8"));
      j.bundle.resources["resources/lw-engine.exe"]="resources/lw-engine.exe";
      fs.writeFileSync(p, JSON.stringify(j,null,2)+"\n");
      console.log("withcore: lw-engine.exe listed in bundle.resources");
    '
    ;;
  without)
    node -e '
      const fs=require("fs");
      const p="src-tauri/tauri.conf.json";
      const j=JSON.parse(fs.readFileSync(p,"utf8"));
      delete j.bundle.resources["resources/lw-engine.exe"];
      fs.writeFileSync(p, JSON.stringify(j,null,2)+"\n");
      console.log("without: lw-engine.exe removed from bundle.resources (connect via management URL)");
    '
    ;;
  *)
    echo "usage: $0 withcore|without" >&2
    exit 1
    ;;
esac
