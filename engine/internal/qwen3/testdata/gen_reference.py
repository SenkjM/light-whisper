#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
"""Record reference traces of the Python Qwen3-ASR stack for the Go parity tests.

Drives the real src-tauri/resources/qwen3_asr_server.py (Qwen3ASRServer:
initialize, transcribe_audio with inline PCM, _filter_speech) with the real
transcribe_cpp binding, a transcribe.cpp library and the pinned
Qwen3-ASR-0.6B Q8 GGUF, plus the real FireRedVad, and writes
<name>.trace.json for each clip:

- "result": the transcribe_audio text / language;
- "log": every VAD call (input length, SHA-256 of the float32 samples,
  regions) and every transcribe.cpp run after warmup (input length, SHA-256,
  raw text, detected language), in call order.

The Go tests replay the logs through fake VAD / transcribe.cpp layers
(default build) and compare a live Go run with them (lwnative build).

Usage (no pip needed; the wheels can simply be unzipped):
  TRANSCRIBE_LIBRARY=.../libtranscribe.so \\
  python gen_reference.py --site <unzipped transcribe_cpp wheels> \\
      --model Qwen3-ASR-0.6B-Q8_0.gguf [--out-dir DIR] [WAV ...]

Default WAVs: the committed R2T2 and VAD fixtures. Derived clips are added:
"silence" (2 s of zeros), "<first>-tiny" (its first 0.4 s, below the
0.5 s floor) and "<first>-padded" (1.5 s of silence on both sides).
The device is forced to "cpu", so the Python server tries transcribe.cpp's
"auto" backend, then "cpu" — the same order the Go backend uses for device
"auto" without an NVIDIA driver.
"""

from __future__ import annotations

import argparse
import hashlib
import base64
import json
import sys
import wave
from pathlib import Path
from unittest import mock

import numpy as np

HERE = Path(__file__).resolve().parent
ENGINE = HERE.parents[2]
RESOURCES = ENGINE.parent / "src-tauri" / "resources"


def sha(a: np.ndarray) -> str:
    return hashlib.sha256(np.ascontiguousarray(a, dtype="<f4").tobytes()).hexdigest()


def read_wav(path: Path) -> np.ndarray:
    with wave.open(str(path), "rb") as w:
        assert w.getnchannels() == 1 and w.getsampwidth() == 2 and w.getframerate() == 16000, path
        return np.frombuffer(w.readframes(w.getnframes()), dtype="<i2").copy()


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--site", required=True, help="directory containing transcribe_cpp")
    ap.add_argument("--model", required=True)
    ap.add_argument("--out-dir", default=str(HERE))
    ap.add_argument("wavs", nargs="*")
    args = ap.parse_args()
    sys.path.insert(0, args.site)
    sys.path.insert(0, str(RESOURCES))
    import qwen3_asr_server  # noqa: E402

    wavs = [Path(p) for p in args.wavs] or [
        ENGINE / "internal/r2t2/testdata/dictation_short.wav",
        ENGINE / "internal/r2t2/testdata/dictation_long.wav",
        ENGINE / "internal/vad/testdata/speech_mixed.wav",
        ENGINE / "internal/vad/testdata/tones_clip.wav",
    ]
    clips = [(p.stem, read_wav(p)) for p in wavs]
    first_name, first = clips[0]
    clips.append(("silence", np.zeros(32000, dtype="<i2")))
    clips.append((first_name + "-tiny", first[:6400].copy()))
    pad = np.zeros(24000, dtype="<i2")
    clips.append((first_name + "-padded", np.concatenate([pad, first, pad])))

    with (
        mock.patch.object(qwen3_asr_server.Qwen3ASRServer, "_detect_device", return_value="cpu"),
        mock.patch.object(qwen3_asr_server.Qwen3ASRServer, "_resolve_model_path", return_value=args.model),
    ):
        server = qwen3_asr_server.Qwen3ASRServer(engine="qwen3-asr-0.6b")
        init = server.initialize()
        assert init["success"], init
    log: list[dict] = []
    vad, session = server.vad_model, server.session
    orig_vad, orig_run = vad.speech_timestamps, session.run

    def vad_wrap(audio):
        regions = orig_vad(audio)
        log.append({"op": "vad", "n": int(len(audio)), "sha256": sha(audio),
                    "regions": [{"start": int(r["start"]), "end": int(r["end"])} for r in regions]})
        return regions

    def run_wrap(audio, **kw):
        assert kw == {"timestamps": "none"}, kw
        res = orig_run(audio, **kw)
        log.append({"op": "run", "n": int(len(audio)), "sha256": sha(audio),
                    "text": res.text, "language": res.language or ""})
        return res

    vad.speech_timestamps = vad_wrap
    session.run = run_wrap
    out = Path(args.out_dir)
    out.mkdir(parents=True, exist_ok=True)
    for name, pcm in clips:
        log.clear()
        res = server.transcribe_audio(None, audio_base64=base64.b64encode(pcm.tobytes()).decode(),
                                      audio_format="pcm_s16le", sample_rate=16000)
        assert res["success"], res
        trace = {
            "name": name,
            "samples": int(len(pcm)),
            "pcm_sha256": hashlib.sha256(pcm.tobytes()).hexdigest(),
            "backend": server.backend,
            "result": {"text": res["text"], "language": res.get("language", "")},
            "log": list(log),
        }
        if name in ("silence",) or name.endswith(("-tiny", "-padded")):
            trace["derived_from"] = first_name
        (out / f"{name}.trace.json").write_text(json.dumps(trace, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
        print(f"{name}: {res['text']!r} ({res.get('language', '')}) vad={sum(e['op'] == 'vad' for e in log)} runs={sum(e['op'] == 'run' for e in log)}")


if __name__ == "__main__":
    main()
