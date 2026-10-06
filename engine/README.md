<!-- SPDX-FileCopyrightText: 2026 SenkjM -->
<!-- SPDX-License-Identifier: AGPL-3.0-only -->

# lw-engine — light-whisper Go 引擎

独立的本地推理后端（[PLAN.md](../docs/command-cube/PLAN.md) §3–§4）：持有模型加载、串行调度、引擎设置与模型管理，通过 `127.0.0.1` 上的 HTTP + WebSocket API 提供服务。Rust（Tauri）壳只做音频采集、系统集成与云服务，经 API 使用本引擎。

API 契约：[`docs/command-cube/api/`](../docs/command-cube/api/README.md)。

## 当前进度（迁移步骤 1–2）

| 部分 | 状态 |
|------|------|
| 进程启动、端口与 token 握手、`/health`、`/v1/engine/status` | ✅ |
| `GET/PATCH /v1/config`（revision + If-Match、live / reload）、`/v1/config/schema`、`/v1/engine/reload` | ✅ |
| `WS /v1/events` | ✅ |
| 串行调度器（单 OS 线程、实时优先） | ✅ |
| `WS /v1/asr/stream`、`POST /v1/asr/transcribe`（Qwen3 或 R2T2） | ✅ 协议已实现，推理为 **mock** |
| GPU 空闲卸载 | ✅（mock 后端） |
| FireRedVAD（步骤 2）：`internal/vad`，纯 Go fbank + CMVN + 区间后处理，onnxruntime 走 `lwnative` | ✅ 与 Python 逐帧对齐（见下）；尚未接入会话 |
| R2T2 cgo（步骤 3）、Qwen3 cgo（步骤 4） | ⏳ 接入点：`internal/asr` 的 `Backend` 接口 |
| 模型下载、job、CUDA 运行时下载 | ⏳ 契约占位 |
| Rust 客户端：拉起、握手、版本校验、崩溃重启、config 读写、事件转发（`src-tauri/crates/lw-engine-client`，开关 `LW_ENGINE_BACKEND=go`） | ✅ |
| Rust 壳全面切换到 API、移除 Python（步骤 5） | ⏳ |

## 目录

```text
cmd/lw-engine/       可执行入口：参数、握手、优雅退出
internal/config/     engine.json 存储、revision、JSON Schema v1
internal/events/     /v1/events 广播
internal/scheduler/  串行推理调度器
internal/asr/        推理后端接口（cgo 接入点）+ mock
internal/manager/    加载 / 卸载 / reload、实时会话槽、GPU 空闲卸载、状态
internal/vad/        FireRedVAD：fbank、CMVN、ONNX 模型（lwnative）、平滑与区间；testdata/ 为与 Python 对齐的夹具
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

`--backend native` 需要以 `-tags lwnative` 构建，原生 ASR 后端尚未实现（步骤 3–4，Windows + CGO + MSVC/CUDA 构建的 DLL，经 C ABI 动态加载）。

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

## 许可证

本目录的新写代码采用 **AGPL-3.0-only**（见 [LICENSE](LICENSE) 与 PLAN §10）。每个文件带 SPDX 头；无法加注释的文件（`go.sum`、JSON、测试夹具）用同名 `.license` 旁注文件。例外：

- `internal/vad/postprocess.go` 由继承的 `src-tauri/resources/firered_vad.py` 移植，按 PLAN §10.4 标 **GPL-3.0-only**，并保留 FireRedVAD 的 Apache-2.0 声明。
- `internal/vad/kissfft.go` 是 KISS FFT 的移植，**BSD-3-Clause**，文件内保留原声明。
- `internal/vad/fbank.go` 只重写 kaldi-native-fbank（Apache-2.0）的算法，不含其代码。

依赖：`github.com/coder/websocket`（ISC）；`github.com/yalue/onnxruntime_go`（MIT，仅 `lwnative` 构建）。见根目录 `THIRD_PARTY_NOTICES.md`。
