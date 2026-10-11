#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
"""Fetch Windows native deps into src-tauri/resources/ (models NOT included).

Downloads:
  - onnxruntime 1.24.1 win-x64 -> resources/onnxruntime/ + resources/onnxruntime.dll
  - transcribe.cpp 0.1.3 windows (cpu-vulkan | cuda) -> resources/transcribe-native/

R2T2 audiocpp is built separately via scripts/build_r2t2_runtime.py.

Usage:
  python scripts/fetch_native_deps_windows.py
  python scripts/fetch_native_deps_windows.py --transcribe cpu
  python scripts/fetch_native_deps_windows.py --transcribe cuda
"""
from __future__ import annotations

import argparse
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
TRANSCRIBE_URLS = {
    "cpu": (
        "https://github.com/handy-computer/transcribe.cpp/releases/download/v0.1.3/"
        "transcribe-native-0.1.3-windows-x86_64-cpu-vulkan.tar.gz"
    ),
    "cuda": (
        "https://github.com/handy-computer/transcribe.cpp/releases/download/v0.1.3/"
        "transcribe-native-0.1.3-windows-x86_64-cuda.tar.gz"
    ),
}


def download(url: str, dest: Path) -> None:
    dest.parent.mkdir(parents=True, exist_ok=True)
    print(f"Downloading {url}")
    urllib.request.urlretrieve(url, dest)


def stage_onnxruntime(staging: Path) -> None:
    zpath = staging / "onnxruntime.zip"
    download(ONNX_URL, zpath)
    with zipfile.ZipFile(zpath) as zf:
        zf.extractall(staging / "onnx")
    libs = list((staging / "onnx").rglob("onnxruntime.dll"))
    if not libs:
        raise RuntimeError("onnxruntime.dll missing from archive")
    lib_dir = libs[0].parent
    out = RESOURCES / "onnxruntime"
    if out.exists():
        shutil.rmtree(out)
    out.mkdir(parents=True)
    for item in lib_dir.iterdir():
        if item.suffix.lower() == ".dll" or item.name.endswith(".dll"):
            shutil.copy2(item, out / item.name)
    shutil.copy2(out / "onnxruntime.dll", RESOURCES / "onnxruntime.dll")
    print(f"Staged onnxruntime -> {out} and resources/onnxruntime.dll")


def stage_transcribe(staging: Path, variant: str) -> None:
    import tarfile

    url = TRANSCRIBE_URLS[variant]
    tpath = staging / f"transcribe-{variant}.tgz"
    download(url, tpath)
    extract = staging / f"transcribe-{variant}"
    extract.mkdir()
    with tarfile.open(tpath, "r:gz") as tf:
        tf.extractall(extract)
    dlls = list(extract.rglob("transcribe.dll"))
    if not dlls:
        raise RuntimeError("transcribe.dll missing from archive")
    src = dlls[0].parent
    out = RESOURCES / "transcribe-native"
    if out.exists():
        shutil.rmtree(out)
    shutil.copytree(src, out)
    # Marker for which variant we staged
    (out / "VARIANT.txt").write_text(variant + "\n", encoding="utf-8")
    print(f"Staged transcribe-native ({variant}) -> {out}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--transcribe",
        choices=("cpu", "cuda"),
        default="cpu",
        help="transcribe.cpp Windows native variant (default: cpu)",
    )
    parser.add_argument(
        "--skip-onnx",
        action="store_true",
        help="skip onnxruntime download (reuse existing)",
    )
    args = parser.parse_args()
    if sys.platform != "win32":
        print(
            f"Note: staging Windows binaries on {sys.platform} is OK for asset prep.",
            file=sys.stderr,
        )
    with tempfile.TemporaryDirectory(prefix="lw-native-deps-") as tmp:
        staging = Path(tmp)
        if not args.skip_onnx:
            stage_onnxruntime(staging)
        stage_transcribe(staging, args.transcribe)
    print("Models are NOT bundled -- run download_models / in-app download separately.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
