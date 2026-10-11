#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
"""Fetch Windows CPU native deps into src-tauri/resources/ (models NOT included).

Downloads:
  - onnxruntime 1.24.1 win-x64 → resources/onnxruntime/onnxruntime.dll (+ companions)
  - transcribe.cpp 0.1.3 windows cpu-vulkan → resources/transcribe-native/

R2T2 audiocpp is built separately via scripts/build_r2t2_runtime.py --backend cpu
(on a Windows host with VS C++ tools).

Usage (Windows CI / local Windows):
  python scripts/fetch_native_deps_windows.py
"""
from __future__ import annotations

import shutil
import sys
import tempfile
import urllib.request
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
RESOURCES = ROOT / "src-tauri" / "resources"

ONNX_URL = (
    "https://github.com/microsoft/onnxruntime/releases/download/v1.24.1/"
    "onnxruntime-win-x64-1.24.1.zip"
)
TRANSCRIBE_URL = (
    "https://github.com/handy-computer/transcribe.cpp/releases/download/v0.1.3/"
    "transcribe-native-0.1.3-windows-x86_64-cpu-vulkan.tar.gz"
)


def download(url: str, dest: Path) -> None:
    dest.parent.mkdir(parents=True, exist_ok=True)
    print(f"Downloading {url}")
    urllib.request.urlretrieve(url, dest)


def stage_onnxruntime(staging: Path) -> None:
    zpath = staging / "onnxruntime.zip"
    download(ONNX_URL, zpath)
    with zipfile.ZipFile(zpath) as zf:
        zf.extractall(staging / "onnx")
    # layout: onnxruntime-win-x64-1.24.1/lib/onnxruntime.dll
    libs = list((staging / "onnx").rglob("onnxruntime.dll"))
    if not libs:
        raise RuntimeError("onnxruntime.dll missing from archive")
    lib_dir = libs[0].parent
    out = RESOURCES / "onnxruntime"
    if out.exists():
        shutil.rmtree(out)
    out.mkdir(parents=True)
    for item in lib_dir.iterdir():
        if item.suffix.lower() in {".dll", ".pdb"} or item.name.endswith(".dll"):
            shutil.copy2(item, out / item.name)
    # Also place onnxruntime.dll next to resources root for simple LW_ONNXRUNTIME_LIB
    shutil.copy2(out / "onnxruntime.dll", RESOURCES / "onnxruntime.dll")
    print(f"Staged onnxruntime → {out} and resources/onnxruntime.dll")


def stage_transcribe(staging: Path) -> None:
    import tarfile

    tpath = staging / "transcribe.tgz"
    download(TRANSCRIBE_URL, tpath)
    extract = staging / "transcribe"
    extract.mkdir()
    with tarfile.open(tpath, "r:gz") as tf:
        tf.extractall(extract)
    # find directory containing transcribe.dll
    dlls = list(extract.rglob("transcribe.dll"))
    if not dlls:
        raise RuntimeError("transcribe.dll missing from archive")
    src = dlls[0].parent
    out = RESOURCES / "transcribe-native"
    if out.exists():
        shutil.rmtree(out)
    shutil.copytree(src, out)
    print(f"Staged transcribe-native → {out}")


def main() -> int:
    if sys.platform != "win32":
        print(
            "Note: this script stages Windows binaries; running on "
            f"{sys.platform} is OK for CI asset prep if you only need the files.",
            file=sys.stderr,
        )
    with tempfile.TemporaryDirectory(prefix="lw-native-deps-") as tmp:
        staging = Path(tmp)
        stage_onnxruntime(staging)
        stage_transcribe(staging)
    print("Models are NOT bundled — run download_models / in-app download separately.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
