#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
"""Generate the synthetic dictation WAVs for the Go/Python R2T2 parity tests.

Needs espeak-ng and numpy. The WAVs are committed; rerun only to change them.
Speech comes from espeak-ng (synthetic voice, no recorded people). The long
clip has several sentences separated by pauses so the outer segmentation
(≥ 8 s segment + ≥ 300 ms trailing silence) ends segments mid-recording.
"""

from __future__ import annotations

import subprocess
import tempfile
import wave
from pathlib import Path

import numpy as np

HERE = Path(__file__).resolve().parent
SR = 16_000
RNG = np.random.default_rng(20261006)


def espeak(text: str, voice: str, speed: int = 165) -> np.ndarray:
    with tempfile.TemporaryDirectory() as tmp:
        path = Path(tmp) / "out.wav"
        subprocess.run(["espeak-ng", "-v", voice, "-s", str(speed), "-w", str(path), text], check=True)
        with wave.open(str(path), "rb") as w:
            rate = w.getframerate()
            data = np.frombuffer(w.readframes(w.getnframes()), dtype="<i2").astype(np.float64)
    n_out = int(round(len(data) * SR / rate))
    spec = np.fft.rfft(data)
    keep = n_out // 2 + 1
    out_spec = np.zeros(keep, dtype=complex)
    m = min(keep, len(spec))
    out_spec[:m] = spec[:m]
    out = np.fft.irfft(out_spec, n_out) * (n_out / len(data))
    return out / 32768.0


def silence(seconds: float) -> np.ndarray:
    return RNG.normal(0.0, 0.0005, int(seconds * SR))


def write(name: str, audio: np.ndarray) -> None:
    audio = audio + RNG.normal(0.0, 0.0003, audio.size)
    pcm = np.clip(np.round(audio * 32767.0), -32768, 32767).astype("<i2")
    with wave.open(str(HERE / name), "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(SR)
        w.writeframes(pcm.tobytes())
    print(f"{name}: {len(pcm) / SR:.2f} s")


def main() -> None:
    long = np.concatenate(
        [
            silence(0.5),
            espeak("我们明天早上九点在公司门口集合，然后一起去机场。", "cmn") * 0.8,
            silence(0.7),
            espeak("Please send the quarterly report to the finance team before Friday.", "en-us") * 0.7,
            silence(1.0),
            espeak("这个周末的天气预报说会下雨，记得带伞。", "cmn") * 0.8,
            silence(0.6),
            espeak("Thank you very much.", "en-us") * 0.7,
            silence(0.8),
        ]
    )
    write("dictation_long.wav", long)
    short = np.concatenate(
        [silence(0.4), espeak("Hello world, this is a short test.", "en-us") * 0.7, silence(0.3)]
    )
    write("dictation_short.wav", short)


if __name__ == "__main__":
    main()
