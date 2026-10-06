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

## Rust 客户端（步骤 1）

实现：`src-tauri/crates/lw-engine-client`（无 Tauri 依赖，AGPL-3.0-only）+ `src-tauri/src/services/go_engine.rs`（Tauri 接入）。

| 环境变量 | 作用 |
|----------|------|
| `LW_ENGINE_BACKEND` | `python`（默认，行为不变）或 `go` |
| `LW_ENGINE_PATH` | 指定 `lw-engine[.exe]` 路径（优先级最高） |
| `LW_ENGINE_GO_BACKEND` | 传给 `--backend`，默认 `mock` |

- **查找顺序**：`LW_ENGINE_PATH` → `<resource_dir>/resources/lw-engine[.exe]`（安装包）→ 开发构建下的 `engine/lw-engine[.exe]`。
- **打包**：`tauri.conf.json` 暂未加入 `lw-engine.exe`（文件缺失会让打包失败）。需要时手动执行 `cd engine && go build -o ../src-tauri/resources/lw-engine.exe ./cmd/lw-engine`，再把 `resources/lw-engine.exe` 加进 `bundle.resources`。步骤 5 会改成正式构建步骤。
- **拉起**：`--listen 127.0.0.1:0 --backend <mock> --exit-on-stdin-close --data-dir <应用数据目录>`；stdin 保持打开（父进程消失 → 引擎退出）；stderr 写 `<数据目录>/go_engine_stderr.log`；Windows 用 `CREATE_NO_WINDOW`。握手超时 15 s，`api_version` ≠ 1 视为不兼容；随后再用 `GET /health` 核对一次 token 与版本。
- **崩溃重启**（PLAN §6 #3）：退避 1 s、2 s、4 s … 上限 30 s；连续失败 5 次后停止并报告 `failed`；稳定运行 60 s 后计数清零；版本不兼容直接 `failed`，不重启。应用退出（托盘「退出」）时结束子进程。
- **设置**：`go` 模式下 `get/set_engine`、`get/set_models_dir`、`get/set_gpu_idle_seconds` 经 `GET/PATCH /v1/config`。412 时重读 revision 并重试（最多 3 次，补丁是绝对值，可安全重放）；写入 reload 键后调用 `POST /v1/engine/reload`，实时会话中返回 409 时保留 `pending_reload`。引擎处于 `failed` / 未启动时回退为直接写 engine.json（此时没有进程持有该文件）。识别仍由 Python 引擎完成。
- **Tauri 事件**：`go-engine-process`（进程状态：`starting` / `running` / `restarting` / `failed` / `stopped`，含 pid、端口、重启次数、`last_error`）；`go-engine-status`、`go-engine-config-changed`（`/v1/events` 原样转发 `{seq,type,time,data}`）。断线后重连，首条 `engine_status` 快照用于重新同步。
- **命令**：`get_go_engine_status` → `{backend, process, engine_status, config}`，供前端在监听注册前补拉一次状态。
- **测试**：`cd src-tauri && cargo test -p lw-engine-client`；集成测试会 `go build` 本目录的引擎并以 `--backend mock` 运行，没有 Go 时打印 `SKIP` 并通过。

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
| `/v1/asr/transcribe` 的引擎 | **已定（用户决定）**：Qwen3 与 R2T2 都可用，走当前本地引擎的 `Backend.Transcribe`；R2T2 把整段音频按块送入新建分段会话再结束（同 Python `transcribe_audio`）。Qwen3 仍是非实时与小工具的推荐 / 回退选择；云端引擎返回 409 `no_local_engine` |
| R2T2 批处理与实时会话 | 两者共用唯一推理线程和同一个已加载的 R2T2 模型。现有调度为两级（实时优先、运行中的任务不抢占）：整段 R2T2 识别是一个调度任务，执行期间开始的实时会话等它结束；实时会话进行中提交 R2T2 批处理返回 409 `realtime_session_active`（同 Python `stream_busy`，在排队时与推理线程上各检查一次）。Qwen3 批处理照常排队。**409 规则保持现状**（用户决定），三级优先级实现后只适用于这条未分段的整段路径，步骤 4 再评估 |
| GPU 空闲卸载 | 与 Python `gpu_idle_should_unload` 一致：只在设备不是 CPU、无实时会话、调度队列为空、超时后卸载；下一次请求自动重新加载 |
| 启动时加载 | 默认在启动后立即加载当前本地引擎（与 Python 一致），`--no-autoload` 关闭 |
| WS 鉴权 | 只接受 `Authorization` 头。浏览器无法给 WebSocket 设置该头，开发模式前端直连 WS 的方案留待步骤 6 |

## 已定设计、尚未实现（PLAN §1、§4.4–§4.6，步骤 4）

| 主题 | 决定 | 契约中的体现 |
|------|------|--------------|
| 请求模型 | 前端 / Rust 只发**语音转录**（`WS /v1/asr/stream` 实时、`POST /v1/asr/transcribe` 整段）和**文件转录**（`POST /v1/jobs`）两类请求，每个带优先级；引擎由后端按设置决定，请求不指定引擎 | 各端点都没有 engine 参数 |
| 三级优先级 | 字段名 `priority`，取值 `realtime` / `high` / `normal` = **实时 / 优先 / 普通**（沿用 `live` / `reload` 这类小写英文枚举，中文名用于文档与 UI）。实时 = 实时听写，可抢占；优先 = 普通按键听写的缺省；普通 = 文件转录的缺省；缺省值后续开放为用户设置 | `components/schemas/Priority`、`/v1/asr/transcribe` 的 `priority` 参数（均 `x-status: planned`）；实时会话固定 `realtime` |
| 抢占与重排 | 实时请求到来时打断并丢弃正在运行的任务、立即执行；被打断的任务从中断的段重新排队（保持原优先级、排同级队首），不判失败；有 VAD 分段时损失至多一段。单段 C 调用能否中途取消待核实，不能时在段结束后让出 | 现有调度器为两级、run-to-completion（`SchedulerStats` 说明） |
| 大文件转录 | Go 先 VAD 分段，每段作为一个任务按 job 优先级入队；两种引擎都按段让出（解决此前 R2T2 长文件 job 的待定项） | `/v1/jobs`（planned） |
| Qwen3 整句实时 | Qwen3 无原生流式，实时听写时 VAD 切句、逐句识别，整句字幕（`committed` 按句追加、`tentative` 为空）；R2T2 仍是原生流式实时引擎 | `ws-protocol.md`「计划中」；现有实现对 Qwen3 返回 409 `wrong_engine` |
