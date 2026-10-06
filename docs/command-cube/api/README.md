<!-- SPDX-FileCopyrightText: 2026 SenkjM -->
<!-- SPDX-License-Identifier: AGPL-3.0-only -->

# Go 引擎 API 契约 v1（迁移步骤 0）

本目录冻结 [PLAN.md](../PLAN.md) §4 的本地 API 契约，供 Go 引擎（`engine/`）与 Rust 壳双方对照实现。

| 文件 | 内容 |
|------|------|
| [openapi.yaml](openapi.yaml) | HTTP 接口（`x-status: implemented / planned`） |
| [ws-protocol.md](ws-protocol.md) | `WS /v1/events`、`WS /v1/asr/stream` 消息格式 |
| [`engine/internal/config/schema_v1.json`](../../../engine/internal/config/schema_v1.json) | 引擎设置 JSON Schema v1（`GET /v1/config/schema` 原样返回） |

`api_version = 1`。只在不兼容变更时递增；新增字段 / 端点不递增。

## 启动握手

Rust 壳拉起 `lw-engine`（默认 `--listen 127.0.0.1:0`），读取 **stdout 第一行** JSON：

```json
{"event":"ready","port":53817,"token":"<64 hex>","api_version":1,"version":"0.0.0-dev","pid":1234,"backend":"mock"}
```

- 使用 `--token-file <path>` 时 token 写入该文件（权限 0600），stdout 中不含 `token`。
- 日志只写 stderr。
- `--exit-on-stdin-close`：stdin 关闭（父进程退出）时引擎自动退出，作为 Job Object 之外的兜底。
- `--data-dir` 缺省与 Tauri 应用相同：`LIGHT_WHISPER_DATA_DIR`，否则 `<系统数据目录>/com.light-whisper.app`（Windows 为 `%APPDATA%\com.light-whisper.app`）。

## 安全

- 只监听回环 IP；`--listen` 不是回环 IP 时拒绝启动。
- 所有请求（含 `/health`、WebSocket 握手）必须带 `Authorization: Bearer <token>`，常量时间比较。
- `Host` 必须是回环地址或 `localhost`（防 DNS rebinding）。
- 带 `Origin` 头的请求一律拒绝，除非用 `--allow-origin` 显式放行（开发模式前端直连调试用）；从不输出 CORS 头。

## 现有 stdio JSON → 新 API 对照

现协议见 `src-tauri/src/services/funasr_service/protocol.rs`（`ServerCommand` / `ServerResponse`）。

| 现 `ServerCommand` | 新 API | 备注 |
|--------------------|--------|------|
| `status` | `GET /v1/engine/status` | `model_loaded`、`device`、`gpu_name`、`gpu_memory_total`、`engine` 字段同名保留；`models{asr,vad,punc}` 由 `model_loaded` + `missing_models` 取代 |
| `stream_start {session_id, context, hot_words?, language?}` | `WS /v1/asr/stream` → `start` | 字段同名 |
| `stream_feed {session_id, offset, audio_base64, audio_format, sample_rate}` | 二进制帧：8 字节 offset + PCM | 不再 base64；格式固定 s16le / 16 kHz / 单声道；`session_id` 由连接上下文确定 |
| `stream_finish` | `finish` → `result` | |
| `stream_cancel` | `cancel` → `cancelled` | |
| 流式响应 `tentative_text` / `text` / `final` | `partial {committed, tentative}` / `result {text}` | committed 与 tentative 分开下发，不再由壳端相减 |
| `transcribe {audio_base64 \| audio_path, audio_format, sample_rate, hot_words?, options{language, context}}` | `POST /v1/asr/transcribe?language=&context=&hot_word=` | body 为原始 PCM 或 WAV；`audio_path` 不再支持（文件走后续 `/v1/jobs`） |
| `set_gpu_idle {seconds}` | `PATCH /v1/config {"gpu_idle_seconds": n}` | live 键，立即生效 |
| `exit` | 进程信号 / stdin 关闭 | Rust 壳负责进程生命周期 |
| `request_id`（防迟到响应） | 不需要 | HTTP 请求-响应天然配对；WS 以 `session_id` 区分 |
| Rust `set_engine` / `set_models_dir`（写 engine.json + 重启进程） | `PATCH /v1/config` + `POST /v1/engine/reload` | reload 键 |

## 步骤 1 中的取舍（PLAN 未写明处）

| 问题 | 选择 |
|------|------|
| Go 模块目录 | `engine/`（PLAN §10.4 建议名）；可执行文件 `lw-engine` |
| `revision` 的形式 | 持久化值（引擎持有的键 + schema_version）的 SHA-256 前 16 位十六进制。文件被外部修改也会改变 revision，重启后保持稳定 |
| engine.json 中壳持有的键 | `glm_endpoint`、`alibaba_region`、`alibaba_model` 等原样保留，不暴露在 `/v1/config`，PATCH 时视为未知键（400）。过渡期内现有 Rust 代码仍可读写它们 |
| `engine` 键的取值 | 与现有应用一致，含云端引擎 `glm-asr` / `alibaba-asr`：此时引擎不加载本地模型（`local_engine: false`），流式 / 识别返回 409 `no_local_engine`。旧值 `qwen3-asr-1.7b` 按 `confucius4-r2t2` 读取，但不改写文件 |
| 新增设置键 | `device`（auto/cpu/cuda/vulkan，reload）、`default_language`、`log_level`（live），对应 PLAN §3.2；缺省时与现状行为一致 |
| PATCH 语义 | JSON merge patch：`null` 恢复默认并删除该键；`models_dir: ""` 同样删除（与 `write_models_dir(None)` 一致）；缺 `If-Match` 返回 428 |
| `/v1/asr/transcribe` 的引擎 | 只在当前引擎为 Qwen3 时可用（PLAN §4.5 标题为「批处理（Qwen3）」），R2T2 时返回 409 `wrong_engine`。若需要 R2T2 也支持整段识别（现 Python R2T2 服务支持 `transcribe`），后续再放开 |
| GPU 空闲卸载 | 与 Python `gpu_idle_should_unload` 一致：只在设备不是 CPU、无实时会话、调度队列为空、超时后卸载；下一次请求自动重新加载 |
| 启动时加载 | 默认在启动后立即加载当前本地引擎（与 Python 一致），`--no-autoload` 关闭 |
| WS 鉴权 | 只接受 `Authorization` 头。浏览器无法给 WebSocket 设置该头，开发模式前端直连 WS 的方案留待步骤 6 |
