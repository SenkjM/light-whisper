#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
"""Verify Windows withcore native layout under src-tauri/resources (or DEST).

Closes the 'Windows native path' gap for packaging: expected DLL names and
optional CUDA drop-in. Does not load DLLs (no Windows required).
"""
from __future__ import annotations

import argparse
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument(
        "resources",
        nargs="?",
        default=str(ROOT / "src-tauri" / "resources"),
        help="resources directory (default: src-tauri/resources)",
    )
    ap.add_argument("--require-cuda", action="store_true")
    args = ap.parse_args()
    res = Path(args.resources)
    required = [
        "lw-engine.exe",
        "onnxruntime.dll",
        "fireredvad_vad.onnx",
        "fireredvad_cmvn.json",
        "transcribe-native/transcribe.dll",
        "r2t2-native/cpu/audiocpp.dll",
    ]
    missing = [p for p in required if not (res / p).is_file()]
    if missing:
        print("MISSING:", *missing, sep="\n  ", file=sys.stderr)
        return 1
    cuda = res / "r2t2-native" / "cuda" / "audiocpp.dll"
    if args.require_cuda and not cuda.is_file():
        print("MISSING CUDA:", cuda, file=sys.stderr)
        return 1
    print("OK Windows native layout at", res)
    print("  CPU R2T2:", res / "r2t2-native" / "cpu" / "audiocpp.dll")
    print("  CUDA R2T2:", "present" if cuda.is_file() else "absent (install via install_cuda_native_libs)")
    print("  transcribe:", res / "transcribe-native" / "transcribe.dll")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
