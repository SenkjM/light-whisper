<!-- SPDX-FileCopyrightText: 2026 SenkjM -->
<!-- SPDX-License-Identifier: AGPL-3.0-only -->

# WebSocket 消息契约 v1

两个 WebSocket 端点都要求握手请求带 `Authorization: Bearer <token>`，并遵守与 HTTP 相同的 Host / Origin 检查（见 [README](README.md#安全)）。

## `WS /v1/events`

服务端单向推送，客户端不发送消息（发了也会被忽略）。每条消息是一个 JSON 文本帧：

```json
{ "seq": 12, "type": "config_changed", "time": "2026-10-06T09:00:00Z", "data": { ... } }
```

| 字段 | 说明 |
|------|------|
| `seq` | 进程内单调递增；**连接建立后的第一条消息 `seq = 0`**，是当前 `engine_status` 快照，供客户端同步初始状态 |
| `type` | `engine_status` / `config_changed` / `download_progress` / `job_progress` |
| `time` | UTC，RFC 3339 |
| `data` | 见下表 |

| type | data | 状态 |
|------|------|------|
| `engine_status` | 与 `GET /v1/engine/status` 相同 | 已实现 |
| `config_changed` | `{revision, changed[], applied[], pending_reload[]}` | 已实现 |
| `download_progress` | 待定（模型下载 / CUDA 运行时下载，`target` 区分） | 计划中 |
| `job_progress` | 与 `GET /v1/jobs/{id}` 相同的 job 结构，但不含 `text` / `segments`（见 openapi.yaml `Job`）。在提交、每次开始 / 继续执行、VAD 分段完成、每个单元完成、被抢占或让出（`state: queued`）、结束（`completed` / `failed` / `cancelled`）时各发一条；结束后用 `GET /v1/jobs/{id}` 取文本 | 已实现 |

**慢消费者**：每个连接有 256 条缓冲；缓冲满时服务端以关闭码 `1008`（policy violation）断开该连接，不会阻塞引擎。客户端应重新 `GET /v1/engine/status`、`GET /v1/config` 后重连。引擎退出时以 `1001` 关闭。

## `WS /v1/asr/stream`（语音转录 · 实时）

实时会话的优先级固定为 `realtime`（实时，PLAN §4.6），客户端不需要也不能指定；引擎由后端按设置决定，客户端不指定。

握手前检查：当前生效引擎为云端引擎时返回 HTTP 409（`no_local_engine`），**不静默更换引擎**；已有实时会话时同样返回 409（`realtime_session_active`）。

模式由当前引擎决定，`started` 消息的 `mode` 字段告诉客户端（步骤 4 新增字段）：

| mode | 引擎 | `partial` |
|------|------|-----------|
| `streaming` | R2T2（原生流式） | 每个音频帧一条；`committed` 逐字增长，`tentative` 为预览 |
| `sentence` | Qwen3（VAD 整句，PLAN §3.0.2） | **只在切出新的一句并识别完成时**发一条（该帧之后）；`committed` 按整句追加，`tentative` 恒为空串；其余帧没有回应 |

Qwen3 整句模式的切句（`engine/internal/qwen3/sentence.go`）：每帧对最新 1 s 音频跑 FireRedVAD；进入语音后，窗口尾部静音（含 VAD 两侧 120 ms 补边）≥ 500 ms 且本句已 ≥ 1 s 时结句，连续说话满 30 s 强制切一句；未进入语音时保留最近 1 s 作为句首预留。每句走 Qwen3 整段识别路径（短音频 / VAD 过滤 + 一次 transcribe.cpp），句子之间按 R2T2 分段的规则拼接（只在 ASCII 词之间加空格）。识别在结句的那一帧里同步完成，会话内仍然严格串行。`finish` 识别剩余音频并返回全文，`language` 为最后一个非 `unknown` 的句子语言。

会话期间调度器只执行实时任务：开始会话会打断正在运行的可抢占任务（Qwen3 整段识别、文件 job），排队中的非实时任务等会话结束（见 openapi.yaml `Priority`、`/v1/jobs`）。不可中途中止的原生调用（R2T2 整段识别整体、Qwen3 一次 transcribe.cpp 调用、一个 R2T2 分块推送）会先跑完，这段时间里会话的音频帧在后端排队，之后依次处理。

一条连接上可以先后进行多个会话，但同一时间全局只允许一个会话。

### 客户端 → 引擎

**控制消息（文本帧，JSON）**

| type | 字段 | 说明 |
|------|------|------|
| `start` | `session_id`（uint64）、`language?`、`context?`、`hot_words?` | 开始会话；`language` 缺省时用 `default_language` |
| `finish` | — | 结束会话，返回 `result` |
| `cancel` | — | 放弃会话，不返回结果 |

**音频（二进制帧）**

```
| offset: uint64 little-endian (8 字节) | PCM s16le, 16 kHz, 单声道 (偶数字节) |
```

`offset` 是本帧第一个采样在会话内的序号，必须等于已接收的采样总数（与 `R2T2StreamSession` 的 offset 校验一致）；不匹配时该帧被丢弃并返回 `offset_mismatch`（带 `expected_offset`），会话继续。建议每帧 160 ms（2560 个采样）。

服务端逐帧同步处理（先 VAD、再推理），处理期间不读取下一帧——背压由 TCP 自然形成，即 PLAN §4.4 中的「有界队列」。

### 引擎 → 客户端（文本帧，JSON）

| type | 字段 | 说明 |
|------|------|------|
| `started` | `session_id`、`mode` | `start` 成功；`mode` = `streaming` / `sentence`（见上） |
| `partial` | `session_id`、`committed`、`tentative` | `streaming`：每个音频帧一条；`sentence`：每切出一句一条，`tentative` 为空。`committed` 只增不减 |
| `result` | `session_id`、`text`、`language`、`sample_count` | `finish` 的结果；之后会话结束、全局会话槽释放 |
| `cancelled` | `session_id` | `cancel` 确认 |
| `error` | `session_id?`、`code`、`message`、`expected_offset?` | 见下表 |

| error code | 含义 |
|------------|------|
| `no_session` | 未 `start` 就发送音频或 `finish` |
| `session_already_started` | 会话进行中又发 `start` |
| `realtime_session_active` | 其他连接持有会话 |
| `no_local_engine` | 引擎在连接期间被切换为云端引擎（`wrong_engine` 自步骤 4 起不再出现：两种本地引擎都有实时模式；保留该码以兼容） |
| `bad_frame` | 二进制帧短于 8 字节或 PCM 为奇数字节 |
| `offset_mismatch` | 见上 |
| `bad_message` | 文本帧不是合法控制消息 |
| `engine_error` | 推理出错 |

连接断开时若会话仍在进行，引擎自动取消并释放会话槽。

> `started` / `cancelled` 两条确认消息是相对 PLAN §4.4 草案的补充，便于壳端状态机确认会话边界；`started.mode` 为步骤 4 新增（新增字段，`api_version` 不变）。
