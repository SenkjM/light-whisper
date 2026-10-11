#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
"""Patch tauri.conf.json bundle.resources for withcore (native DLLs) or without.

withcore: include lw-engine + VAD + r2t2-native/cpu + transcribe-native + onnxruntime
without: shell-only (no lw-engine / native libs) — connect via management URL

Usage:
  python scripts/stage_withcore_tauri_resources.py withcore
  python scripts/stage_withcore_tauri_resources.py without
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CONF = ROOT / "src-tauri" / "tauri.conf.json"
RES = ROOT / "src-tauri" / "resources"

BASE = {
    "resources/FireRedVAD-LICENSE.txt": "resources/FireRedVAD-LICENSE.txt",
    "resources/R2T2-CODE-LICENSE.txt": "resources/R2T2-CODE-LICENSE.txt",
    "resources/R2T2-MODEL-LICENSE.txt": "resources/R2T2-MODEL-LICENSE.txt",
    "../LICENSE": "LICENSE.txt",
    "../NOTICE": "NOTICE.txt",
    "../THIRD_PARTY_NOTICES.md": "THIRD_PARTY_NOTICES.md",
}

WITHCORE_CORE = {
    "resources/lw-engine.exe": "resources/lw-engine.exe",
    "resources/fireredvad_vad.onnx": "resources/fireredvad_vad.onnx",
    "resources/fireredvad_cmvn.json": "resources/fireredvad_cmvn.json",
}


def add_tree(mapping: dict[str, str], src_rel: str, dest_rel: str) -> None:
    src = ROOT / "src-tauri" / src_rel
    if not src.is_dir():
        print(f"skip missing dir {src_rel}", file=sys.stderr)
        return
    for path in src.rglob("*"):
        if not path.is_file():
            continue
        rel = path.relative_to(ROOT / "src-tauri").as_posix()
        dest = Path(dest_rel) / path.relative_to(src)
        mapping[rel] = dest.as_posix()


def main() -> int:
    flavor = (sys.argv[1] if len(sys.argv) > 1 else "withcore").strip().lower()
    conf = json.loads(CONF.read_text(encoding="utf-8"))
    resources: dict[str, str] = dict(BASE)
    if flavor == "withcore":
        resources.update(WITHCORE_CORE)
        if (RES / "onnxruntime.dll").is_file():
            resources["resources/onnxruntime.dll"] = "resources/onnxruntime.dll"
        add_tree(resources, "resources/onnxruntime", "resources/onnxruntime")
        add_tree(resources, "resources/transcribe-native", "resources/transcribe-native")
        add_tree(resources, "resources/r2t2-native/cpu", "resources/r2t2-native/cpu")
        # cuda optional
        if (RES / "r2t2-native" / "cuda").is_dir():
            add_tree(resources, "resources/r2t2-native/cuda", "resources/r2t2-native/cuda")
    elif flavor == "without":
        # VAD assets not needed without engine; keep licenses + notices only
        pass
    else:
        print("usage: withcore|without", file=sys.stderr)
        return 2
    conf["bundle"]["resources"] = resources
    CONF.write_text(json.dumps(conf, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"tauri.conf.json resources -> {flavor} ({len(resources)} entries)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
