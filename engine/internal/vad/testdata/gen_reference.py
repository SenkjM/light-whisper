#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
"""Reference outputs of the Python FireRedVAD (src-tauri/resources/firered_vad.py).

Writes, for every *.wav fixture in this directory:
  <name>.fbank.f32   raw kaldi-native-fbank features (frames x 80, float32 LE, before CMVN)
  <name>.probs.f32   per-frame model probabilities (float32 LE)
  <name>.ref.json    sample/frame counts and FireRedVad.speech_timestamps() segments
and postprocess_cases.json: probability arrays + options -> expected segments from
FireRedVad._timestamps_from_probabilities (no model needed).

Requires: numpy, kaldi-native-fbank==1.22.3 (as pinned in pyproject.toml), onnxruntime.
Run: python gen_reference.py
"""

from __future__ import annotations

import json
import sys
import wave
from pathlib import Path

import numpy as np

HERE = Path(__file__).resolve().parent
RESOURCES = HERE.parents[3] / "src-tauri" / "resources"
sys.path.insert(0, str(RESOURCES))

from firered_vad import FireRedVad, FireRedVadOptions  # noqa: E402


def load_wav(path: Path) -> np.ndarray:
    with wave.open(str(path), "rb") as w:
        assert w.getframerate() == 16_000 and w.getnchannels() == 1 and w.getsampwidth() == 2
        pcm = np.frombuffer(w.readframes(w.getnframes()), dtype="<i2")
    # Same conversion as the Python servers: int16 / 32768 -> float32.
    return pcm.astype(np.float32) / 32768.0


def raw_fbank(vad: FireRedVad, audio: np.ndarray) -> np.ndarray:
    pcm = np.clip(np.asarray(audio, dtype=np.float32) * 32768.0, -32768.0, 32767.0)
    fbank = vad._knf.OnlineFbank(vad._fbank_options)
    fbank.accept_waveform(16_000, pcm.tolist())
    n = fbank.num_frames_ready
    return np.asarray([fbank.get_frame(i) for i in range(n)], dtype=np.float32).reshape(n, 80)


def fixtures(vad: FireRedVad) -> None:
    for wav in sorted(HERE.glob("*.wav")):
        audio = load_wav(wav)
        feats = raw_fbank(vad, audio)
        normalized = vad._extract_features(audio)
        assert np.array_equal((feats - vad._mean) * vad._inverse_std, normalized)
        probs = vad.probabilities(audio)
        segments = vad.speech_timestamps(audio)
        stem = wav.with_suffix("")
        feats.astype("<f4").tofile(f"{stem}.fbank.f32")
        probs.astype("<f4").tofile(f"{stem}.probs.f32")
        Path(f"{stem}.ref.json").write_text(
            json.dumps(
                {
                    "samples": int(audio.size),
                    "frames": int(feats.shape[0]),
                    "segments": segments,
                },
                indent=1,
            )
            + "\n"
        )
        print(f"{wav.name}: {feats.shape[0]} frames, segments {segments}")


def postprocess_cases() -> None:
    rng = np.random.default_rng(7)
    cases = []
    reprs = []
    smoothed_reprs = {}

    def smoothed_like_python(probs, window):
        # Same lines as FireRedVad._timestamps_from_probabilities (kept for bitwise checks).
        if window > 1:
            kernel = np.ones(window, dtype=np.float32) / window
            smoothed = np.convolve(probs, kernel, mode="full")[: probs.size]
            for index in range(min(window - 1, probs.size)):
                smoothed[index] = probs[: index + 1].mean()
            return smoothed
        return probs

    def add(name, probs, audio_len=None, with_smoothed=False, **opts):
        probs = np.asarray(probs, dtype=np.float32)
        options = FireRedVadOptions(**opts)
        vad = object.__new__(FireRedVad)
        vad.options = options
        if audio_len is None:
            audio_len = int(probs.size) * 160 + 240
        fmt = lambda arr: "[" + ",".join(np.format_float_positional(v, unique=True, trim="-") for v in arr) + "]"
        reprs.append(fmt(probs))
        if with_smoothed:
            smoothed_reprs[len(cases)] = fmt(smoothed_like_python(probs, max(1, options.smooth_window_frames)))
        cases.append(
            {
                "name": name,
                "options": {
                    "threshold": options.threshold,
                    "smooth_window_frames": options.smooth_window_frames,
                    "min_speech_duration_ms": options.min_speech_duration_ms,
                    "min_silence_duration_ms": options.min_silence_duration_ms,
                    "speech_pad_ms": options.speech_pad_ms,
                },
                "audio_length": int(audio_len),
                # shortest float32 repr; parse back with 32-bit precision
                "probabilities": "@@" + str(len(cases)) + "@@",
                "expected": vad._timestamps_from_probabilities(probs, audio_len),
            }
        )
        if with_smoothed:
            cases[-1]["smoothed"] = "@@s" + str(len(cases) - 1) + "@@"

    def blocks(*spec):
        return np.concatenate([np.full(n, v, dtype=np.float32) for n, v in spec])

    add("empty", [])
    add("shorter_than_window", [0.9, 0.9, 0.1])
    add("all_speech", np.ones(80))
    add("all_silence", np.zeros(80))
    add("short_spike_rejected", blocks((10, 0), (9, 1), (30, 0)), smooth_window_frames=1,
        min_speech_duration_ms=100, min_silence_duration_ms=200, speech_pad_ms=0)
    add("merge_padded", blocks((20, 0), (15, 1), (20, 0), (15, 1), (30, 0)), audio_len=16_000,
        smooth_window_frames=1, min_speech_duration_ms=100, min_silence_duration_ms=200)
    add("speech_until_end", blocks((40, 0), (60, 1)))
    add("gap_one_short_of_min_silence", blocks((30, 0), (40, 1), (29, 0), (40, 1), (40, 0)),
        smooth_window_frames=1)
    add("gap_exactly_min_silence", blocks((30, 0), (40, 1), (30, 0), (40, 1), (40, 0)),
        smooth_window_frames=1)
    add("speech_exactly_min_speech", blocks((30, 0), (15, 1), (40, 0)), smooth_window_frames=1)
    add("speech_one_short_of_min_speech", blocks((30, 0), (14, 1), (40, 0)), smooth_window_frames=1)
    add("pad_clipped_to_audio_length", blocks((2, 0), (50, 1), (5, 0)), audio_len=9_000)
    add("audio_shorter_than_frames", blocks((10, 1), (40, 1)), audio_len=3_000)
    add("exact_threshold_values", np.full(100, 0.5))
    add("threshold_neighbours", np.tile(np.array([0.5, 0.49999997, 0.50000006, 0.5, 0.5], np.float32), 40),
        with_smoothed=True)
    add("smoothing_edge_pattern", blocks((1, 1), (3, 0), (1, 1), (60, 0.6), (35, 0.2)), with_smoothed=True)
    for window in (1, 2, 3, 5, 9):
        for i in range(6):
            base = np.repeat(rng.random(12) > 0.5, rng.integers(5, 60, 12)).astype(np.float32)
            noisy = np.clip(base * 0.8 + rng.normal(0.1, 0.25, base.size), 0, 1).astype(np.float32)
            add(f"random_w{window}_{i}", noisy, smooth_window_frames=window,
                min_speech_duration_ms=int(rng.choice([10, 50, 150, 300])),
                min_silence_duration_ms=int(rng.choice([10, 100, 300, 500])),
                speech_pad_ms=int(rng.choice([0, 30, 120, 400])),
                threshold=float(np.float32(rng.choice([0.3, 0.5, 0.7]))))
    for i in range(25):
        # values clustered near the threshold to exercise float32 smoothing order
        noisy = (0.5 + rng.normal(0, 0.02, 200)).astype(np.float32)
        add(f"near_threshold_{i}", noisy, with_smoothed=True,
            smooth_window_frames=int(rng.choice([2, 3, 5, 7])))

    text = json.dumps({"cases": cases}, separators=(",", ":"))
    for i, r in enumerate(reprs):
        text = text.replace(f'"@@{i}@@"', r, 1)
    for i, r in smoothed_reprs.items():
        text = text.replace(f'"@@s{i}@@"', r, 1)
    (HERE / "postprocess_cases.json").write_text(text + "\n")
    print(f"postprocess_cases.json: {len(cases)} cases")


def main() -> None:
    vad = FireRedVad()
    fixtures(vad)
    postprocess_cases()


if __name__ == "__main__":
    main()
