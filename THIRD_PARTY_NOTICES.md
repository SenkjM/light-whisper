# Third-party notices

Light-Whisper contains or interoperates with third-party software and assets.
The project license in [`LICENSE`](LICENSE) applies only to material for which
the Light-Whisper licensor can grant those rights. The items below remain under
their own terms.

## FireRedVAD model assets

The bundled upstream FireRedVAD ONNX model and CMVN data, together with portions
of the integration adapted from upstream, remain subject to the Apache License
2.0. The license text is available at
[`src-tauri/resources/FireRedVAD-LICENSE.txt`](src-tauri/resources/FireRedVAD-LICENSE.txt).
Light-Whisper's original integration and post-processing changes are licensed
under GPL-3.0-only.

## transcribe.cpp and downloaded models

[transcribe.cpp](https://github.com/handy-computer/transcribe.cpp) is distributed
under the MIT License. Qwen3-ASR model files are downloaded separately and
remain subject to the terms published by their respective upstream providers.

Package-manager dependencies may carry additional licenses. Their inclusion in
the source tree, Python engine, or application bundle does not change those
licenses.

## Confucius4-R2T2 native runtime and model

[audio.cpp](https://github.com/0xShug0/audio.cpp), copyright 2026 ShugoAI LLC,
is distributed under Apache-2.0. The build pins revision
`6c70f32d0d90a29a9863e556bd9712ce622f868b` and applies the reviewed patch in
`scripts/patches/audio-cpp-r2t2-windows-streaming.patch` for rolling streaming
and empty-prefix handling. Runtime bundles retain the audio.cpp, ggml and
SentencePiece license notices alongside their DLLs. The CUDA bundle also
includes NVIDIA's redistribution license; Microsoft runtime DLLs retain
their vendor terms.

The rolling inference adaptation also follows NetEase Youdao's R2T2 reference
implementation, whose Apache-2.0 license is retained in
[`R2T2-CODE-LICENSE.txt`](src-tauri/resources/R2T2-CODE-LICENSE.txt).

The separately downloaded [Q8_0 GGUF](https://huggingface.co/davidxifeng/Confucius4-R2T2-gguf)
is derived from [NetEase Youdao Confucius4-R2T2](https://github.com/netease-youdao/Confucius4-R2T2).
It is subject to the NetEase Youdao Model Use License Agreement, not this
project's GPL license. A copy is bundled as
[`R2T2-MODEL-LICENSE.txt`](src-tauri/resources/R2T2-MODEL-LICENSE.txt).

## Go engine (`engine/`)

- The FFT in `engine/internal/vad/kissfft.go` is a Go port of
  [KISS FFT](https://github.com/mborgerding/kissfft), Copyright (c) 2003-2010
  Mark Borgerding, distributed under the BSD-3-Clause license; the full notice
  is kept in that file.
- `engine/internal/vad/fbank.go` reimplements the filterbank algorithm of
  [kaldi-native-fbank](https://github.com/csukuangfj/kaldi-native-fbank),
  Copyright (c) 2022 Xiaomi Corporation, Apache-2.0. No upstream code is
  linked or vendored.
- `engine/internal/vad/postprocess.go` ports the FireRedVAD post-processing
  from `src-tauri/resources/firered_vad.py` and is GPL-3.0-only, with the
  FireRedVAD Apache-2.0 notice above.
- `engine/internal/r2t2` loads the audio.cpp runtime described above at run
  time through its C ABI (only in `lwnative` builds). The C declarations in
  `capi_lwnative.go` follow the patched `audiocpp.h`; no audio.cpp code is
  linked or vendored. `runtime.go`, `segmented.go`, `session.go` and
  `backend.go` port the inherited Python R2T2 server and are GPL-3.0-only.
- `engine/internal/qwen3` loads the transcribe.cpp shared library described
  above at run time through its C ABI (only in `lwnative` builds); the C
  declarations in `capi_lwnative.go` follow the transcribe.cpp 0.1.x public
  header, and no transcribe.cpp code is linked or vendored. `backend.go` and
  `nvidia_*.go` port the inherited Python Qwen3-ASR server and are
  GPL-3.0-only.
- Go modules: [github.com/coder/websocket](https://github.com/coder/websocket)
  (ISC, Copyright (c) 2025 Coder) and, only in builds with the `lwnative` tag,
  [github.com/yalue/onnxruntime_go](https://github.com/yalue/onnxruntime_go)
  (MIT, Copyright (c) 2023 Nathan Otterness).
