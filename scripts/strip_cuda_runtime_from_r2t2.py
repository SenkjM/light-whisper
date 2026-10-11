#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
"""Remove NVIDIA CUDA runtime DLLs from r2t2-native/cuda (PLAN §5 on-demand).

Keeps audiocpp.dll, CRT redistributables, licenses, and updates
runtime-manifest.json file list. CUDA runtime (cudart/cublas/cublasLt) must be
obtained on the user machine (system install or future Go on-demand download).
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CUDA_DIR = ROOT / "src-tauri" / "resources" / "r2t2-native" / "cuda"

# Patterns written by build_r2t2_runtime._assemble_runtime for cuda backend
RUNTIME_PREFIXES = ("cudart64_", "cublas64_", "cublasLt64_")
RUNTIME_NAMES = {"CUDA-LICENSE.txt"}


def main() -> int:
    if not CUDA_DIR.is_dir():
        print(f"missing {CUDA_DIR}", file=sys.stderr)
        return 1
    removed = []
    for path in list(CUDA_DIR.iterdir()):
        if not path.is_file():
            continue
        name = path.name
        if name in RUNTIME_NAMES or any(name.startswith(p) for p in RUNTIME_PREFIXES):
            path.unlink()
            removed.append(name)
    manifest_path = CUDA_DIR / "runtime-manifest.json"
    if manifest_path.is_file():
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        files = manifest.get("files", {})
        for name in removed:
            files.pop(name, None)
        manifest["files"] = files
        manifest["cuda_runtime"] = "on_demand"  # PLAN §5
        manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print(f"Stripped CUDA runtime from package: {', '.join(removed) or '(none)'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
