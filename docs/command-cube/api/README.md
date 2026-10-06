<!-- SPDX-FileCopyrightText: 2026 SenkjM -->
<!-- SPDX-License-Identifier: AGPL-3.0-only -->

# Go 引擎 API 契约 v1（迁移步骤 0 冻结，后续步骤只做新增）

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
| `transcribe {audio_base64 \| audio_path, audio_format, sample_rate, hot_words?, options{language, context}}` | `POST /v1/asr/transcribe?language=&context=&hot_word=&priority=` | body 为原始 PCM 或 WAV；`audio_path` 改走 `POST /v1/jobs {"path": ...}` |
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
| R2T2 批处理与实时会话 | 两者共用唯一推理线程和同一个已加载的 R2T2 模型。（步骤 1 时调度为两级、运行中的任务不抢占，步骤 4 起为三级 + 抢占，见下节）整段 R2T2 识别是一个调度任务，执行期间开始的实时会话等它结束；实时会话进行中提交 R2T2 批处理返回 409 `realtime_session_active`（同 Python `stream_busy`，在排队时与推理线程上各检查一次）。Qwen3 批处理照常排队。**409 规则保持现状**（用户决定）；步骤 4 评估后保留，只适用于这条未分段的整段路径（见下节） |
| GPU 空闲卸载 | 与 Python `gpu_idle_should_unload` 一致：只在设备不是 CPU、无实时会话、调度队列为空、超时后卸载；下一次请求自动重新加载 |
| 启动时加载 | 默认在启动后立即加载当前本地引擎（与 Python 一致），`--no-autoload` 关闭 |
| WS 鉴权 | 只接受 `Authorization` 头。浏览器无法给 WebSocket 设置该头，开发模式前端直连 WS 的方案留待步骤 6 |

## 步骤 4：三级优先级、抢占、文件 job、Qwen3 整句实时（已实现）

| 主题 | 实现 | 契约中的体现 |
|------|------|--------------|
| 请求模型 | 前端 / Rust 只发**语音转录**（`WS /v1/asr/stream` 实时、`POST /v1/asr/transcribe` 整段）和**文件转录**（`POST /v1/jobs`）两类请求，每个带优先级；引擎由后端按设置决定，请求不指定引擎 | 各端点都没有 engine 参数 |
| 三级优先级 | 字段名 `priority`，取值 `realtime` / `high` / `normal` = **实时 / 优先 / 普通**。实时会话固定 `realtime`；`/v1/asr/transcribe` 缺省取设置 `default_priority_voice`（缺省 `high`，可设为三者之一），`/v1/jobs` 缺省取 `default_priority_file`（缺省 `normal`，可设 `high` / `normal`）；两个键都是 live。非法值 400 `invalid_priority` | `components/schemas/Priority`、`/v1/asr/transcribe` 与 `/v1/jobs` 的 `priority`；`SchedulerStats` 三个队列计数 + `realtime_hold` / `preemptions` / `yields`；`EngineStatus.jobs` |
| 串行调度 | 仍是单推理线程。同级 FIFO；`high` 与 `normal` 之间不打断正在运行的任务，但文件 job 在每个识别单元之前检查，有更高优先级任务在等就让出（等同于每段一个任务） | `/v1/jobs` 说明 |
| 实时抢占 | 实时会话开始（或 realtime 任务入队）时：正在运行的可抢占任务被打断，结果丢弃，回到同级队首，不判失败；会话期间只执行 realtime 任务（`realtime_hold`），会话结束后被打断的任务从中断的单元继续。可抢占 = Qwen3 整段识别、两种引擎的文件 job；不可抢占 = R2T2 整段识别（未分段路径） | `Priority`、`/v1/jobs` 说明 |
| 原生调用中途能否停 | R2T2（audio.cpp）无中止接口：在下一次分块推送前停下（≤ 一个 320 ms / 160 ms 块的推理）。Qwen3（transcribe.cpp 0.1.3）：已注册 abort 回调，但 Qwen3 识别中途实测不轮询（Python `Session.cancel` 同样无效），一次调用会算完，结果保留，随后让出——实时任务最多等待一个单元（文件 job ≤ 20 s 音频；整段识别 = 整个片段）。VAD、模型加载先完成 | 同上 |
| R2T2 409 规则 | 评估后**保留**给未分段的 `/v1/asr/transcribe`（实时会话中 409 `realtime_session_active`）；R2T2 文件 job 按单元让出，实时会话中提交照常排队，不返回 409 | `/v1/asr/transcribe`、`/v1/jobs` |
| 文件 job | `POST /v1/jobs`（JSON `{path}` 或直接上传 PCM / WAV）→ 202；VAD 分段 → ≤ 20 s 识别单元 → 逐单元识别；进度经 `job_progress`；`DELETE` 取消；`GET /v1/jobs`、`GET /v1/jobs/{id}` 查询；`segments=true` 返回单元时间与文本 | `/v1/jobs*`、`Job`、`job_progress` |
| Qwen3 整句实时 | 同一 WS 端点，`started.mode = "sentence"`：VAD 切句、逐句识别，`partial` 只在新句识别完成时发送，`committed` 按句追加、`tentative` 为空 | `ws-protocol.md` |

协议新增（均为新增字段 / 端点 / 错误码，`api_version` 仍为 1）：`started.mode`；`SchedulerStats` 的 `high_queued` / `normal_queued` / `realtime_hold` / `preemptions` / `yields`（移除了两级时代的 `batch_queued`，`running_kind` 改为 Priority 取值）；`EngineStatus.jobs`；`/v1/jobs` 全部；错误码 `invalid_priority`（400）、`not_found`（404）；设置键 `default_priority_voice` / `default_priority_file`。
