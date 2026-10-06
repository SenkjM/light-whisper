<!-- SPDX-FileCopyrightText: 2026 SenkjM -->
<!-- SPDX-License-Identifier: AGPL-3.0-only -->

# lw-engine — light-whisper Go 引擎

独立的本地推理后端（[PLAN.md](../docs/command-cube/PLAN.md) §3–§4）：持有模型加载、串行调度、引擎设置与模型管理，通过 `127.0.0.1` 上的 HTTP + WebSocket API 提供服务。Rust（Tauri）壳只做音频采集、系统集成与云服务，经 API 使用本引擎。

API 契约：[`docs/command-cube/api/`](../docs/command-cube/api/README.md)。

## 当前进度（迁移步骤 1）

| 部分 | 状态 |
|------|------|
| 进程启动、端口与 token 握手、`/health`、`/v1/engine/status` | ✅ |
| `GET/PATCH /v1/config`（revision + If-Match、live / reload）、`/v1/config/schema`、`/v1/engine/reload` | ✅ |
| `WS /v1/events` | ✅ |
| 串行调度器（单 OS 线程、实时优先） | ✅ |
| `WS /v1/asr/stream`、`POST /v1/asr/transcribe`（Qwen3 或 R2T2） | ✅ 协议已实现，推理为 **mock** |
| GPU 空闲卸载 | ✅（mock 后端） |
| FireRedVAD（步骤 2）、R2T2 cgo（步骤 3）、Qwen3 cgo（步骤 4） | ⏳ 接入点：`internal/asr` 的 `Backend` 接口 |
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

`--backend native` 需要以 `-tags lwnative` 构建，原生后端尚未实现（步骤 2–4，Windows + CGO + MSVC/CUDA 构建的 DLL，经 C ABI 动态加载）。

## 许可证

本目录全部为新写代码，采用 **AGPL-3.0-only**（见 [LICENSE](LICENSE) 与 PLAN §10）。每个文件带 SPDX 头；无法加注释的文件（`go.sum`、JSON）用同名 `.license` 旁注文件。依赖：`github.com/coder/websocket`（ISC，兼容）。
