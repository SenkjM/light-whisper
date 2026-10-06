#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 SenkjM
# SPDX-License-Identifier: AGPL-3.0-only
"""Record reference traces of the Python R2T2 stack for the Go parity tests.

Drives the real Python code (src-tauri/resources: r2t2_asr_server.py's
stream commands and transcribe_audio, SegmentedR2T2, R2T2StreamSession,
NativeRuntime, FireRedVad) with an audio.cpp build and the pinned model, and
writes <name>.trace.json next to each WAV (or into --out-dir):

- "stream": a live session fed like the Rust shell (160 ms PCM16 blocks):
  every response (committed text, tentative text, sample count) and the
  final result;
- "transcribe": whole-clip transcription of the same audio;
- for both, "log": every native call (stream start with context/language,
  each push with offset, length and the SHA-256 of its float32 samples plus
  the event's delta / preview and statuses, finish, reset) and every VAD
  call (window length, SHA-256, regions), in call order.

The Go tests replay these logs through fake native and VAD layers (default
build) and compare a live Go run against them (lwnative build).

Usage:
  python gen_reference.py --model r2t2-q8_0.gguf --library libaudiocpp.so [WAV ...]
Default WAVs: the committed dictation_*.wav. Two derived clips are added:
"silence" (2 s of zeros) and "<first>-short" (its first 12800 samples).
Needs numpy, onnxruntime and kaldi-native-fbank==1.22.3.
"""

from __future__ import annotations

import argparse
import base64
import contextlib
import ctypes as ct
import hashlib
import json
import logging
import sys
import wave
from pathlib import Path
from unittest import mock

import numpy as np

HERE = Path(__file__).resolve().parent
RESOURCES = HERE.parents[3] / "src-tauri" / "resources"
sys.path.insert(0, str(RESOURCES))

import r2t2_asr_server  # noqa: E402
from firered_vad import FireRedVad  # noqa: E402
from r2t2_native import NativeRuntime  # noqa: E402
from r2t2_segmented import SegmentedR2T2  # noqa: E402
from r2t2_stream import R2T2StreamSession  # noqa: E402

FEED_SAMPLES = 2560  # the shell sends complete 160 ms blocks
CONTEXT = "Topic: travel and office"
HOT_WORDS = ["机场", "quarterly report"]


def sha(samples: np.ndarray) -> str:
    return hashlib.sha256(np.ascontiguousarray(samples, dtype="<f4").tobytes()).hexdigest()


def text(value) -> str | None:
    return None if value is None else value.decode("utf-8")


def instrument(native: NativeRuntime, vad: FireRedVad, log: list) -> None:
    f = native.functions
    orig = dict(f)

    def request_set_text(request, context, language):
        log.append({"op": "start", "context": text(context) or "", "language": text(language)})
        return orig["request_set_text"](request, context, language)

    def stream_push(session, ptr, n, rate, channels, offset, event):
        samples = np.ctypeslib.as_array(ptr, shape=(n,)).copy()
        status = orig["stream_push"](session, ptr, n, rate, channels, offset, event)
        log.append({"op": "push", "offset": int(offset), "n": int(n), "sha": sha(samples),
                    "status": int(status), "event": bool(event._obj.value)})
        return status

    def result_text(result, out_text, out_language):
        status = orig["result_text"](result, out_text, out_language)
        entry = log[-1]
        entry["text_status"] = int(status)
        if status == 0:
            entry["text"] = text(out_text._obj.value) or ""
            entry["language"] = text(out_language._obj.value) or ""
        return status

    def result_preview_text(result, out_text):
        status = orig["result_preview_text"](result, out_text)
        entry = log[-1]
        entry["preview_status"] = int(status)
        if status == 0:
            entry["preview"] = text(out_text._obj.value) or ""
        return status

    def stream_finish(session, result):
        status = orig["stream_finish"](session, result)
        log.append({"op": "finish", "status": int(status)})
        return status

    def stream_reset(session):
        log.append({"op": "reset"})
        return orig["stream_reset"](session)

    f.update(request_set_text=request_set_text, stream_push=stream_push, result_text=result_text,
             result_preview_text=result_preview_text, stream_finish=stream_finish, stream_reset=stream_reset)

    detect = vad.speech_timestamps

    def speech_timestamps(audio):
        regions = detect(audio)
        log.append({"op": "vad", "n": int(len(audio)), "sha": sha(audio),
                    "regions": [{"start": int(r["start"]), "end": int(r["end"])} for r in regions]})
        return regions

    vad.speech_timestamps = speech_timestamps


def server_shell(native: NativeRuntime, vad: FireRedVad):
    # Same wiring as R2T2ASRServer.initialize() after a successful load.
    server = object.__new__(r2t2_asr_server.R2T2ASRServer)
    server.engine = "confucius4-r2t2"
    server.backend = native.backend
    server.device = native.backend
    server.initialized = True
    server.native = native
    server.vad_model = vad
    server.segmented = SegmentedR2T2(native, vad, chunk_samples=native.chunk_samples, max_segment_samples=None)
    server.stream = R2T2StreamSession(server.segmented)
    server.logger = logging.getLogger("gen_reference")
    server.stdout_suppressor = mock.Mock()
    server.stdout_suppressor.suppress.side_effect = contextlib.nullcontext
    server.transcription_count = 0
    server.total_audio_duration = 0.0
    server._total_inference_ms = 0.0
    return server


def b64(pcm16: np.ndarray) -> str:
    return base64.b64encode(pcm16.astype("<i2").tobytes()).decode()


def run_stream(server, pcm16: np.ndarray, session_id: int) -> dict:
    responses = []
    started = server.handle_stream_command({"action": "stream_start", "session_id": session_id,
                                            "context": CONTEXT, "hot_words": HOT_WORDS})
    assert started["success"], started
    offset = 0
    for start in range(0, len(pcm16), FEED_SAMPLES):
        block = pcm16[start:start + FEED_SAMPLES]
        r = server.handle_stream_command({"action": "stream_feed", "session_id": session_id, "offset": offset,
                                          "sample_rate": 16000, "audio_format": "pcm_s16le",
                                          "audio_base64": b64(block)})
        assert r["success"], r
        offset += len(block)
        responses.append({"samples": r["sample_count"], "text": r["text"], "tentative": r["tentative_text"]})
    r = server.handle_stream_command({"action": "stream_finish", "session_id": session_id})
    assert r["success"], r
    return {"responses": responses,
            "final": {"text": r["text"], "language": r["language"] or "", "sample_count": r["sample_count"]}}


def run_transcribe(server, pcm16: np.ndarray) -> dict:
    r = server.transcribe_audio(None, options={"context": CONTEXT, "hot_words": HOT_WORDS},
                                audio_base64=b64(pcm16), audio_format="pcm_s16le", sample_rate=16000)
    assert r["success"], r
    return {"text": r["text"], "language": r["language"] or ""}


def load_wav(path: Path) -> np.ndarray:
    with wave.open(str(path), "rb") as w:
        assert (w.getframerate(), w.getnchannels(), w.getsampwidth()) == (16000, 1, 2), path
        return np.frombuffer(w.readframes(w.getnframes()), dtype="<i2").copy()


def main() -> None:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--model", type=Path, required=True)
    p.add_argument("--library", type=Path, required=True)
    p.add_argument("--backend", default="cpu", choices=("cpu", "cuda"))
    p.add_argument("--threads", type=int, default=4)
    p.add_argument("--out-dir", type=Path)
    p.add_argument("--no-derived", action="store_true", help="skip the silence / -short clips")
    p.add_argument("wav", type=Path, nargs="*")
    args = p.parse_args()
    wavs = args.wav or sorted(HERE.glob("dictation_*.wav"))

    clips = [(w.stem, {"wav": w.name}, load_wav(w), w.parent) for w in wavs]
    if not args.no_derived:
        first = clips[0]
        clips.append(("silence", {"zeros": 32000}, np.zeros(32000, dtype="<i2"), first[3]))
        clips.append((first[0] + "-short", {"wav": first[1]["wav"], "samples": 12800}, first[2][:12800], first[3]))

    chunk_ms = 160 if args.backend == "cuda" else 320
    native = NativeRuntime(args.model, args.library, backend=args.backend, chunk_ms=chunk_ms,
                           threads=args.threads, rolling=True)
    vad = FireRedVad()
    log: list = []
    instrument(native, vad, log)
    server = server_shell(native, vad)
    try:
        for session_id, (name, source, pcm16, directory) in enumerate(clips, start=1):
            log.clear()
            stream = run_stream(server, pcm16, session_id)
            stream["log"] = list(log)
            log.clear()
            transcribe = run_transcribe(server, pcm16)
            transcribe["log"] = list(log)
            trace = {
                "source": source, "samples": int(len(pcm16)), "backend": args.backend,
                "chunk_samples": native.chunk_samples, "feed_samples": FEED_SAMPLES, "threads": args.threads,
                "context": CONTEXT, "hot_words": HOT_WORDS, "stream": stream, "transcribe": transcribe,
            }
            out = (args.out_dir or directory) / f"{name}.trace.json"
            out.write_text(json.dumps(trace, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
            print(f"{name}: stream={stream['final']['text']!r} transcribe={transcribe['text']!r}", flush=True)
    finally:
        native.close()


if __name__ == "__main__":
    main()
