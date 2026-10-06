#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
"""Generate the synthetic WAV fixtures used by the Go/Python VAD parity tests.

Needs espeak-ng and numpy. The WAVs are committed; rerun only to change them.
Speech comes from espeak-ng (synthetic voice, no recorded people).
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


def espeak(text: str, voice: str) -> np.ndarray:
    with tempfile.TemporaryDirectory() as tmp:
        path = Path(tmp) / "out.wav"
        subprocess.run(["espeak-ng", "-v", voice, "-s", "190", "-w", str(path), text], check=True)
        with wave.open(str(path), "rb") as w:
            rate = w.getframerate()
            data = np.frombuffer(w.readframes(w.getnframes()), dtype="<i2").astype(np.float64)
    # Band-limited resample to 16 kHz via FFT (enough for fixtures).
    n_out = int(round(len(data) * SR / rate))
    spec = np.fft.rfft(data)
    keep = n_out // 2 + 1
    out_spec = np.zeros(keep, dtype=complex)
    m = min(keep, len(spec))
    out_spec[:m] = spec[:m]
    out = np.fft.irfft(out_spec, n_out) * (n_out / len(data))
    return out / 32768.0


def silence(seconds: float, noise: float = 0.0005) -> np.ndarray:
    return RNG.normal(0.0, noise, int(seconds * SR))


def write(name: str, audio: np.ndarray) -> None:
    pcm = np.clip(np.round(audio * 32767.0), -32768, 32767).astype("<i2")
    with wave.open(str(HERE / name), "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(SR)
        w.writeframes(pcm.tobytes())
    print(f"{name}: {len(pcm) / SR:.2f} s")


def main() -> None:
    zh = espeak("今天天气很好", "cmn")
    en = espeak("voice activity test", "en-us")
    short = espeak("ok", "en-us")
    noise = RNG.normal(0.0, 0.0005, 1)  # advance RNG deterministically
    del noise
    mixed = np.concatenate(
        [
            silence(0.6),
            zh * 0.8,
            silence(0.8),
            en * 0.6,
            silence(0.22),  # shorter than min silence (300 ms) -> merged
            short * 0.7,
            silence(0.9),
        ]
    )
    mixed += RNG.normal(0.0, 0.0003, mixed.size)
    write("speech_mixed.wav", mixed)

    t = np.arange(int(2.0 * SR)) / SR
    tones = 0.3 * np.sin(2 * np.pi * 440 * t) + 0.2 * np.sin(2 * np.pi * 3100 * t)
    tones[: SR // 2] = 0.0  # exact digital silence
    tones[SR : SR + 1600] = 1.5 * np.sign(np.sin(2 * np.pi * 200 * t[:1600]))  # clipped
    tones += RNG.normal(0.0, 0.02, tones.size) * (t > 1.5)
    write("tones_clip.wav", tones[: int(2.0 * SR) - 77])  # odd length: partial last frame


if __name__ == "__main__":
    main()
