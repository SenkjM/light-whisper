<!-- SPDX-FileCopyrightText: 2026 SenkjM -->
<!-- SPDX-License-Identifier: AGPL-3.0-only -->

# lw-engine — light-whisper Go 引擎

独立的本地推理后端（[PLAN.md](../docs/command-cube/PLAN.md) §3–§4）：持有模型加载、串行调度、引擎设置与模型管理，通过 `127.0.0.1` 上的 HTTP + WebSocket API 提供服务。Rust（Tauri）壳只做音频采集、系统集成与云服务，经 API 使用本引擎。

API 契约：[`docs/command-cube/api/`](../docs/command-cube/api/README.md)。

## 当前进度（迁移步骤 1–4）

| 部分 | 状态 |
|------|------|
| 进程启动、端口与 token 握手、`/health`、`/v1/engine/status` | ✅ |
| `GET/PATCH /v1/config`（revision + If-Match、live / reload）、`/v1/config/schema`、`/v1/engine/reload` | ✅ |
| `WS /v1/events` | ✅ |
| 串行调度器（单 OS 线程）：三级优先级 `realtime` / `high` / `normal`、实时抢占与从中断单元重排（步骤 4） | ✅ |
| `WS /v1/asr/stream`（R2T2 原生流式 / Qwen3 VAD 整句）、`POST /v1/asr/transcribe`（Qwen3 或 R2T2） | ✅ 默认构建推理为 **mock**，`lwnative` 构建走真实推理 |
| GPU 空闲卸载 | ✅（Qwen3 卸载时保留 VAD，与 Python 相同） |
| FireRedVAD（步骤 2）：`internal/vad`，纯 Go fbank + CMVN + 区间后处理，onnxruntime 走 `lwnative` | ✅ 与 Python 逐帧对齐（见下）；已接入 R2T2 分段 |
| R2T2 会话与分段（步骤 3）：`internal/r2t2`，audio.cpp C ABI 走 cgo（`lwnative`） | ✅ 与 Python 逐调用、逐字一致（Linux CPU，见下）；Windows / CUDA 未实测 |
| Qwen3（步骤 4）：`internal/qwen3`，transcribe.cpp C ABI 走 cgo（`lwnative`）；整段识别 + VAD 整句实时 | ✅ 与 Python 逐调用一致（Linux CPU，见下）；Windows / CUDA / Vulkan 未实测 |
| 文件转录 job（步骤 4）：`/v1/jobs`，VAD 分段、按单元让出 / 被抢占后续做、`job_progress` 事件、取消 | ✅ |
| 模型下载、CUDA 运行时下载 | ⏳ 契约占位 |
| Rust 客户端：拉起、握手、版本校验、崩溃重启、config 读写、事件转发（`src-tauri/crates/lw-engine-client`，开关 `LW_ENGINE_BACKEND=go`） | ✅ |
| Rust 壳全面切换到 API、移除 Python（步骤 5） | ⏳ |

## 目录

```text
cmd/lw-engine/       可执行入口：参数、握手、优雅退出
internal/config/     engine.json 存储、revision、JSON Schema v1
internal/events/     /v1/events 广播
internal/scheduler/  串行推理调度器：三级优先级、实时抢占、段边界让出
internal/asr/        推理后端接口（cgo 接入点）+ mock
internal/manager/    加载 / 卸载 / reload、实时会话槽、GPU 空闲卸载、状态、文件 job（jobs.go）
internal/vad/        FireRedVAD：fbank、CMVN、ONNX 模型（lwnative）、平滑与区间；testdata/ 为与 Python 对齐的夹具
internal/r2t2/       R2T2：audio.cpp C ABI（capi*.go，lwnative）、NativeRuntime / SegmentedR2T2 / R2T2StreamSession 移植、asr.Backend 实现；testdata/ 为 Python 参考轨迹
internal/qwen3/      Qwen3：transcribe.cpp C ABI（capi*.go，lwnative）、qwen3_asr_server.py 移植（backend.go）、VAD 整句实时（sentence.go）；testdata/ 为 Python 参考轨迹
internal/native/     组装原生后端（R2T2 + Qwen3 + FireRedVAD，按引擎切换），读取 LW_* 环境变量
internal/server/     HTTP / WebSocket 处理与安全检查
```

## 构建与测试

需要 Go ≥ 1.24。

```bash
cd engine
go vet ./...
go test -race ./...
go build -o lw-engine ./cmd/lw-engine          # Windows: GOOS=windows go build -o lw-engine.exe ./cmd/lw-engine
./lw-engine --data-dir /tmp/lw-demo            # stdout 第一行为握手 JSON
```

在应用里试用：在本目录 `go build -o lw-engine ./cmd/lw-engine`（Windows 为 `lw-engine.exe`），然后以 `LW_ENGINE_BACKEND=go` 启动开发版应用（`npm run tauri dev`）。详见 [API README 的「Rust 客户端」](../docs/command-cube/api/README.md#rust-客户端步骤-1)。

常用参数：`--listen 127.0.0.1:0`、`--data-dir`、`--config`、`--backend mock|native`、`--token-file`、`--exit-on-stdin-close`、`--no-autoload`、`--allow-origin`（可重复）。

`--backend native` 需要以 `-tags lwnative` 且 `CGO_ENABLED=1` 构建（见下节），R2T2 与 Qwen3 按 `engine` 设置加载（同一时间只加载一种）。

### FireRedVAD 与 `lwnative`

默认构建不含 cgo：`internal/vad` 的 fbank、CMVN、平滑与区间都是纯 Go，模型通过 `vad.Model` 接口注入（测试里用 mock）。真实模型要 `-tags lwnative` 且 `CGO_ENABLED=1`，经 [`onnxruntime_go`](https://github.com/yalue/onnxruntime_go) v1.27 在运行时加载 onnxruntime 动态库（ORT C API 24，需 onnxruntime ≥ 1.24）。库路径取 `vad.ONNXOptions.LibraryPath`，否则取环境变量 `LW_ONNXRUNTIME_LIB`，否则交给系统加载器。

```bash
# 与 Python 参考输出对比（含 ONNX 逐帧概率）；模型与 CMVN 取自 src-tauri/resources/
CGO_ENABLED=1 LW_ONNXRUNTIME_LIB=/path/to/libonnxruntime.so.1.24.1 \
  go test -race -tags lwnative -v ./internal/vad
```

没有 `LW_ONNXRUNTIME_LIB` 时 ONNX 测试跳过。重新生成夹具 / 参考输出（需要 numpy、onnxruntime、`kaldi-native-fbank==1.22.3`，生成音频还需要 espeak-ng）：

```bash
python internal/vad/testdata/gen_fixtures.py    # 合成 WAV 夹具
python internal/vad/testdata/gen_reference.py   # 调用 src-tauri/resources/firered_vad.py 写参考输出
```

`testdata/kissfft_ref.bin` 由 `testdata/gen_kissfft_ref.c` 用 KISS FFT 的 C 源码生成（命令见文件头）。

当前对齐结果（Linux，onnxruntime 1.24.1 与 1.30.0 相同）：

| 比较项 | 结果 |
|--------|------|
| fbank（log-mel，CMVN 前） | 最大差 1.6e-3，平均 1.1e-6；kaldi-native-fbank 的 Linux 构建对 kissfft 用了 `-ffast-math`，无法逐位复现 |
| CMVN 后特征 | 最大差 3.6e-4 |
| 逐帧概率（全流程） | 最大差 1.3e-6（语音夹具）/ 1.2e-5（合成信号夹具），标准为 1e-4；同一组特征送入模型时差为 0 |
| 平滑、区间 | 与 Python 逐位 / 完全一致（2 个夹具 + 71 组后处理用例） |

### R2T2（步骤 3）与 `lwnative`

`internal/r2t2` 是 Python R2T2 服务的移植：`runtime.go` ← `r2t2_native.py`（160 ms CUDA / 320 ms CPU 分块推送、committed delta 拼接、preview 前缀检查、ABI 0.4 在 CUDA 上首段一次推送、结束前补 320 ms 静音），`segmented.go` ← `r2t2_segmented.py`（先 VAD 再推理；最短 8 s 且尾部静音 ≥ 300 ms 才结束段，无上限；ASCII 感知的段间空格），`session.go` ← `r2t2_stream.py`（session id / offset 校验、committed 只增、tentative = preview − committed、错误后 reset 并作废会话），`backend.go` ← `r2t2_asr_server.py` 与 `hf_cache_utils.py`（固定模型的大小 + SHA-256 校验、CUDA → CPU 回退与 CUDA 预热、`上下文\nHotwords: a, b`、整段识别 = 新建分段会话按块送入再结束、实时会话进行中整段识别返回 busy）。实时会话期间的 R2T2 整段识别仍由 manager 先返回 409。

audio.cpp 只经 C ABI 0.3/0.4（`scripts/patches/audio-cpp-r2t2-windows-streaming.patch` 打过补丁的构建）在**运行时**加载：Linux `dlopen`，Windows `LoadLibraryExW` + `AddDllDirectory`（库所在目录和资源目录，与 Python 的 `os.add_dll_directory` 相同）。不链接导入库、不跨 ABI 传 C++ 对象，内存都由库自己分配和释放，所以 cgo 的 MinGW gcc 与 MSVC（CUDA 12.9）构建的 DLL 只在 C 调用约定上相遇。默认构建（无 cgo）里 `OpenLibrary` 返回 `ErrNativeUnavailable`，会话与分段逻辑照常编译、用假的 C 层测试。

`--backend native` 读取的环境变量（均可省略）：

| 变量 | 默认 | 含义 |
|------|------|------|
| `LW_RESOURCES_DIR` | 可执行文件所在目录 | `fireredvad_vad.onnx`、`fireredvad_cmvn.json` 所在目录 |
| `LW_R2T2_RUNTIME_DIR` | `<资源目录>/r2t2-native` | 其下 `cuda/`、`cpu/` 各放一份 audio.cpp 构建（`audiocpp.dll` / `libaudiocpp.so`） |
| `LW_ONNXRUNTIME_LIB` | 系统加载器 | onnxruntime 动态库 |
| `LW_R2T2_MODEL` | Hugging Face 缓存中的固定模型（`models_dir` 或 `HF_HUB_CACHE` / `HF_HOME/hub` / `~/.cache/huggingface/hub`） | 仅开发用：直接使用该 GGUF，跳过大小与哈希校验 |
| `LW_TRANSCRIBE_LIBRARY` | `<资源目录>/transcribe-native/transcribe.dll`（Linux `libtranscribe.so`，macOS `libtranscribe.dylib`） | transcribe.cpp 0.1.x 动态库（Qwen3） |
| `LW_QWEN3_MODEL` | Hugging Face 缓存中的固定模型 | 仅开发用：直接使用该 Qwen3-ASR GGUF |

`device` 为 `cpu` 时不尝试 CUDA（Python 总是先试 CUDA）；其余取值先 CUDA 后 CPU。

测试：

```bash
go test -race ./internal/r2t2      # 默认构建：移植自 Python 的单测 + 用 testdata/*.trace.json 回放
# 真实 audio.cpp + 模型 + FireRedVAD，与 Python 轨迹逐调用比较（CPU 4 线程，比实时慢约 6–10 倍）
CGO_ENABLED=1 LW_R2T2_LIBRARY=/path/libaudiocpp.so LW_R2T2_MODEL=/path/r2t2-q8_0.gguf \
  LW_ONNXRUNTIME_LIB=/path/libonnxruntime.so.1.24.1 \
  go test -tags lwnative -run Live -v -timeout 60m ./internal/r2t2
```

`testdata/gen_reference.py` 用真实的 Python 代码（`handle_stream_command` 以 160 ms PCM16 块推流、`transcribe_audio`）和 audio.cpp 生成轨迹：每次原生调用（start 的上下文 / 语言、每次 push 的 offset、长度、float32 样本 SHA-256、事件的 delta / preview）、每次 VAD 调用（窗口长度、哈希、区间）、每个流式响应和最终文本。回放测试要求 Go 发出完全相同的调用序列并得到相同的响应；Live 测试再把每次调用的真实输出与 Python 比对。`LW_R2T2_TRACE_DIRS` 可追加别处的轨迹目录（如不便入库的真人录音）。

当前结果（Linux x86-64，audio.cpp 按 `scripts/build_r2t2_runtime.py` 的固定修订、补丁与 CMake 选项用 GCC 构建 CPU 版，仅 `ENGINE_ENABLE_NATIVE_CPU=ON`；Q8 模型，4 线程）：6 段音频（合成中英文 20.9 s / 3.2 s、2 s 静音、0.8 s 截断，加上公开的英文 11 s 与中文 17 s 真人录音），实时会话与整段识别两条路径上，原生调用序列、每个 delta / preview、VAD 区间、每个流式响应（committed + tentative）以及最终文本与 Python **完全一致**。用 `--backend native` 启动的 `lw-engine` 端到端（`POST /v1/asr/transcribe` 与 `WS /v1/asr/stream` 的 20 个 `partial`）也与 Python 一致。

### Qwen3（步骤 4）与 `lwnative`

`internal/qwen3/backend.go` 移植 `qwen3_asr_server.py` 与 `server_common` 的相关部分：固定模型 `Qwen3-ASR-0.6B-Q8_0.gguf` 的 HF 缓存查找（先 `refs/main`、≥ 1 MB、按清单大小校验，同 Python 不校验哈希）；后端顺序 = 有 NVIDIA 显卡时 cuda → vulkan → cpu，否则 auto → cpu（`device` 设置为 `cpu` / `vulkan` / `cuda` 时收窄为 [cpu] / [vulkan, cpu] / [cuda, vulkan, cpu]）；KV f16、`n_ctx` 32768；加载后用 1 s 弱噪声预热；不足 0.5 s 的片段直接返回空（不带语言）；VAD 无语音返回空、语言 `unknown`；否则识别从第一个语音区间开始到最后一个结束的音频（中间停顿保留），文本去首尾空白、无语言时为 `unknown`；语言与热词参数和 Python 一样被忽略；空闲卸载保留 VAD。`status.device` 报告 transcribe.cpp 实际使用的后端（小写，如 `cpu`），Python 报告的是请求的后端名（如 `auto`）。

transcribe.cpp 只经 C ABI 在**运行时**加载（Linux `dlopen`，Windows `LoadLibraryExW` + `AddDllDirectory`），先校验库版本（0.1.x）与三个参数结构体的大小，再调用 `transcribe_init_backends(库目录)`；不链接导入库，内存由库分配释放。默认构建里 `OpenLibrary` 返回 `ErrNativeUnavailable`，逻辑用假的 C 层测试。

实时会话用 `sentence.go` 的 VAD 整句模式（1 s 窗口、句尾静音 ≥ 500 ms 且句长 ≥ 1 s 结句、30 s 强制切句、1 s 句首预留，每句走整段识别路径），见 [ws-protocol.md](../docs/command-cube/api/ws-protocol.md)。

抢占：每次 transcribe.cpp 调用都挂了 abort 回调，调度器打断时置位；但 transcribe.cpp 0.1.3 的 Qwen3 识别中途不轮询它（CPU 实测，Python 的 `Session.cancel` 同样无效），所以一次调用总会算完、结果保留，打断在调用返回后生效。

测试：

```bash
go test -race ./internal/qwen3     # 默认构建：移植自 test_qwen3_asr_server.py 的单测 + testdata/*.trace.json 回放 + 整句模式 / 打断单测
# 真实 transcribe.cpp + 模型 + FireRedVAD，与 Python 轨迹逐调用比较；另测打断与整句模式
CGO_ENABLED=1 LW_TRANSCRIBE_LIBRARY=/path/libtranscribe.so LW_QWEN3_MODEL=/path/Qwen3-ASR-0.6B-Q8_0.gguf \
  LW_ONNXRUNTIME_LIB=/path/libonnxruntime.so.1.24.1 \
  go test -tags lwnative -run Live -v ./internal/qwen3
```

`testdata/gen_reference.py` 驱动真实的 `Qwen3ASRServer`（`transcribe_cpp` 0.1.3 + Go 同一个 `libtranscribe.so`，CPU），记录每次 VAD 调用（样本数、float32 SHA-256、区间）、每次 `session.run`（样本数、SHA-256、文本、语言）与最终结果。回放测试要求 Go 发出同样的调用、送入逐位相同的裁剪音频并得到同样的结果；Live 测试把真实输出逐项比对。`LW_QWEN3_TRACE_DIRS` 可追加别处的轨迹目录。

当前结果（Linux x86-64，transcribe.cpp 0.1.3 的 manylinux 预编译库，即 Python 包自带的同一个库；Q8_0 模型；CPU）：12 条轨迹（合成中英文 20.9 s / 3.2 s / 6.5 s、合成信号 2 s、2 s 静音、0.4 s 截断、首尾各补 1.5 s 静音，以及不入库的公开英文 11 s 与中文 17 s 真人录音及其派生片段），VAD 区间、送入 transcribe.cpp 的裁剪音频（逐位）、识别文本与语言、最终结果均与 Python **完全一致**。`lw-engine --backend native`（Qwen3）端到端：`POST /v1/asr/transcribe` 结果与 Python 一致（3.2 s / 11 s / 17 s / 20.9 s 音频耗时 0.65 / 1.5 / 2.4 / 2.6 s）。整句实时（20.9 s 合成口述，按 160 ms 实时推流）：4 句字幕分别在结句帧后约 0.6 s 内到达，最终文本与整段识别只差一处用词（单句识别「说会下雨，记得带伞」，整段为「会下雨，记得带上」）。压测：92 s 文件 job（5 个单元，单独运行 16.1 s）执行到第 2 个单元中途时开始实时会话：`started` 在 1.98 s 后返回（等正在进行的 transcribe.cpp 调用算完），此后首字幕相对音频起点 8.54 s（空载 8.30 s），之后每句与空载相差 < 0.3 s；会话期间 job 停住，会话结束后继续，最终文本与未被打断时**逐字相同**；`job_progress` 事件按单元推送。

## 许可证

本目录的新写代码采用 **AGPL-3.0-only**（见 [LICENSE](LICENSE) 与 PLAN §10）。每个文件带 SPDX 头；无法加注释的文件（`go.sum`、JSON、测试夹具）用同名 `.license` 旁注文件。例外：

- `internal/vad/postprocess.go` 由继承的 `src-tauri/resources/firered_vad.py` 移植，按 PLAN §10.4 标 **GPL-3.0-only**，并保留 FireRedVAD 的 Apache-2.0 声明。
- `internal/vad/kissfft.go` 是 KISS FFT 的移植，**BSD-3-Clause**，文件内保留原声明。
- `internal/vad/fbank.go` 只重写 kaldi-native-fbank（Apache-2.0）的算法，不含其代码。
- `internal/r2t2/` 的 `runtime.go`、`segmented.go`、`session.go`、`backend.go` 及对应的 `*_test.go`（`runtime_test.go`、`segmented_test.go`、`session_test.go`、`backend_test.go`）由继承的 Python 代码与测试移植，标 **GPL-3.0-only**；C ABI 绑定、轨迹回放与 Live 测试为新写的 AGPL-3.0-only。
- `internal/qwen3/` 的 `backend.go`、`nvidia_*.go` 与 `backend_test.go` 由 `qwen3_asr_server.py` 及其测试移植，标 **GPL-3.0-only**；C ABI 绑定、整句模式（`sentence.go`）、回放 / Live / 打断测试为新写的 AGPL-3.0-only。

依赖：`github.com/coder/websocket`（ISC）；`github.com/yalue/onnxruntime_go`（MIT，仅 `lwnative` 构建）。见根目录 `THIRD_PARTY_NOTICES.md`。
