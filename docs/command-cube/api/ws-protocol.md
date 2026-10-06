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
| `job_progress` | 待定（文件转录 job） | 计划中 |

**慢消费者**：每个连接有 256 条缓冲；缓冲满时服务端以关闭码 `1008`（policy violation）断开该连接，不会阻塞引擎。客户端应重新 `GET /v1/engine/status`、`GET /v1/config` 后重连。引擎退出时以 `1001` 关闭。

## `WS /v1/asr/stream`（仅 R2T2）

握手前检查：当前生效引擎不是 `confucius4-r2t2`（或为云端引擎）时返回 HTTP 409，**不降级到 Qwen3**；已有实时会话时同样返回 409（`realtime_session_active`）。

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

服务端逐帧同步处理（先 VAD、再推理；步骤 1 为 mock），处理期间不读取下一帧——背压由 TCP 自然形成，即 PLAN §4.4 中的「有界队列」。

### 引擎 → 客户端（文本帧，JSON）

| type | 字段 | 说明 |
|------|------|------|
| `started` | `session_id` | `start` 成功 |
| `partial` | `session_id`、`committed`、`tentative` | 每个音频帧一条；`committed` 只增不减 |
| `result` | `session_id`、`text`、`language`、`sample_count` | `finish` 的结果；之后会话结束、全局会话槽释放 |
| `cancelled` | `session_id` | `cancel` 确认 |
| `error` | `session_id?`、`code`、`message`、`expected_offset?` | 见下表 |

| error code | 含义 |
|------------|------|
| `no_session` | 未 `start` 就发送音频或 `finish` |
| `session_already_started` | 会话进行中又发 `start` |
| `realtime_session_active` | 其他连接持有会话 |
| `wrong_engine` / `no_local_engine` | 引擎在连接期间被切换 |
| `bad_frame` | 二进制帧短于 8 字节或 PCM 为奇数字节 |
| `offset_mismatch` | 见上 |
| `bad_message` | 文本帧不是合法控制消息 |
| `engine_error` | 推理出错 |

连接断开时若会话仍在进行，引擎自动取消并释放会话槽。

> `started` / `cancelled` 两条确认消息是相对 PLAN §4.4 草案的补充，便于壳端状态机确认会话边界。
