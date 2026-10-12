#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
"""Verify Windows withcore native layout under src-tauri/resources (or DEST).

Closes the 'Windows native path' gap for packaging: expected DLL names and
CUDA R2T2 + CUDA transcribe (required with --require-cuda / withcore CI).
Does not load DLLs.
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
    ggml_cuda = res / "transcribe-native" / "ggml-cuda.dll"
    variant_path = res / "transcribe-native" / "VARIANT.txt"
    if args.require_cuda and not cuda.is_file():
        print("MISSING CUDA R2T2:", cuda, file=sys.stderr)
        return 1
    if args.require_cuda:
        if not ggml_cuda.is_file():
            print("MISSING CUDA transcribe:", ggml_cuda, file=sys.stderr)
            return 1
        variant = variant_path.read_text(encoding="utf-8").strip() if variant_path.is_file() else ""
        if variant.lower() != "cuda":
            print(f"MISSING CUDA transcribe VARIANT (want cuda, got {variant!r}): {variant_path}", file=sys.stderr)
            return 1
    print("OK Windows native layout at", res)
    print("  CPU R2T2:", res / "r2t2-native" / "cpu" / "audiocpp.dll")
    print("  CUDA R2T2:", "present" if cuda.is_file() else "absent (withcore CI requires it; optional merge via install_cuda_native_libs)")
    tr_variant = variant_path.read_text(encoding="utf-8").strip() if variant_path.is_file() else "(no VARIANT.txt)"
    print("  transcribe:", res / "transcribe-native" / "transcribe.dll", f"(VARIANT={tr_variant})")
    print("  CUDA transcribe (ggml-cuda):", "present" if ggml_cuda.is_file() else "absent (withcore CI requires it)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
