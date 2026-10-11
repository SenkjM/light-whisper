// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! `WS /v1/asr/stream` client (PLAN 步骤 5).
//!
//! ## Pre-`started` audio buffer
//!
//! Capture may produce PCM before the engine answers `started` (model still
//! loading, realtime hold waiting for a preempted job, …). This client
//! **buffers** those samples and, once `started` arrives, flushes them in
//! order as binary frames with the correct sample offsets — nothing is
//! dropped.
//!
//! | Limit | Value | On exceed / timeout |
//! |-------|-------|---------------------|
//! | Buffer cap | [`PRE_START_BUFFER_MAX_SAMPLES`] (30 s @ 16 kHz ≈ 960 KiB) | [`ClientError::Api`] `code = "pre_start_buffer_overflow"` — session fails; UI should show a start error |
//! | Wait for `started` | [`START_TIMEOUT`] (15 s) | [`ClientError::Api`] `code = "start_timeout"` — session fails |
//!
//! After `started`, frames are sent in [`STREAM_CHUNK_SAMPLES`] (160 ms)
//! chunks. `started.mode` is exposed as [`StreamMode`].

use std::time::{Duration, Instant};

use futures_util::{SinkExt, StreamExt};
use serde::Deserialize;
use serde_json::{json, Value};
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::tungstenite::http::HeaderValue;
use tokio_tungstenite::tungstenite::Message;

use crate::client::{ClientError, EngineClient};

/// 160 ms of 16 kHz mono PCM — matches the native capture cadence.
pub const STREAM_CHUNK_SAMPLES: usize = 2560;

/// Max PCM retained before `started` (30 s @ 16 kHz). Beyond this the
/// session fails instead of dropping audio.
pub const PRE_START_BUFFER_MAX_SAMPLES: usize = 30 * 16_000;

/// How long to wait for the engine's `started` after sending `start`.
pub const START_TIMEOUT: Duration = Duration::from_secs(15);

/// Realtime recognition mode from `started.mode`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StreamMode {
    /// R2T2 native streaming — per-frame `committed` / `tentative`.
    Streaming,
    /// Qwen3 whole-sentence captions — `partial` only on new sentences.
    Sentence,
}

impl StreamMode {
    pub fn as_str(self) -> &'static str {
        match self {
            StreamMode::Streaming => "streaming",
            StreamMode::Sentence => "sentence",
        }
    }

    fn parse(s: &str) -> Option<Self> {
        match s {
            "streaming" => Some(StreamMode::Streaming),
            "sentence" => Some(StreamMode::Sentence),
            _ => None,
        }
    }
}

/// One caption update (`partial` from the engine).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct StreamPartial {
    pub committed: String,
    pub tentative: String,
}

/// Final `result` payload.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct StreamResult {
    pub text: String,
    pub language: String,
    pub sample_count: u64,
}

#[derive(Debug, Deserialize)]
struct WireMsg {
    #[serde(rename = "type")]
    kind: String,
    #[serde(default)]
    session_id: u64,
    #[serde(default)]
    mode: String,
    #[serde(default)]
    committed: String,
    #[serde(default)]
    tentative: String,
    #[serde(default)]
    text: String,
    #[serde(default)]
    language: String,
    #[serde(default)]
    sample_count: u64,
    #[serde(default)]
    code: String,
    #[serde(default)]
    message: String,
    #[serde(default)]
    expected_offset: Option<u64>,
}

type Ws =
    tokio_tungstenite::WebSocketStream<tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>>;

/// Live ASR WebSocket session.
pub struct AsrStream {
    ws: Ws,
    session_id: u64,
    mode: Option<StreamMode>,
    started: bool,
    /// Deadline for receiving `started` after `start` was sent.
    start_deadline: Option<Instant>,
    /// Samples accepted (buffered or sent); next frame's offset.
    sample_offset: u64,
    /// PCM waiting for `started`.
    pre_start: Vec<i16>,
    /// Last committed+tentative for callers that want a stable snapshot.
    last: StreamPartial,
    closed: bool,
}

impl AsrStream {
    /// Connects to `WS /v1/asr/stream`. A 409 from the handshake (busy /
    /// no local engine) becomes [`ClientError::Api`].
    pub async fn connect(client: &EngineClient) -> Result<Self, ClientError> {
        let url = format!("{}/v1/asr/stream", client.ws_base_url());
        let mut req = url
            .into_client_request()
            .map_err(|e| ClientError::Transport(e.to_string()))?;
        let auth = HeaderValue::from_str(&format!("Bearer {}", client.token()))
            .map_err(|e| ClientError::Transport(e.to_string()))?;
        req.headers_mut().insert("Authorization", auth);
        let (ws, _resp) = tokio_tungstenite::connect_async(req)
            .await
            .map_err(|e| match e {
                tokio_tungstenite::tungstenite::Error::Http(resp) => {
                    let status = resp.status().as_u16();
                    let body = resp
                        .body()
                        .as_ref()
                        .map(|b| String::from_utf8_lossy(b).into_owned())
                        .unwrap_or_default();
                    let (code, message) = parse_http_error_body(&body);
                    if status == 401 {
                        ClientError::Unauthorized
                    } else {
                        ClientError::Api {
                            status,
                            code,
                            message,
                        }
                    }
                }
                other => ClientError::Transport(other.to_string()),
            })?;
        Ok(Self {
            ws,
            session_id: 0,
            mode: None,
            started: false,
            start_deadline: None,
            sample_offset: 0,
            pre_start: Vec::new(),
            last: StreamPartial {
                committed: String::new(),
                tentative: String::new(),
            },
            closed: false,
        })
    }

    pub fn mode(&self) -> Option<StreamMode> {
        self.mode
    }

    pub fn sample_offset(&self) -> u64 {
        self.sample_offset
    }

    pub fn started(&self) -> bool {
        self.started
    }

    /// Sends `start`. Does not wait — call [`feed`](Self::feed) / [`finish`](Self::finish)
    /// which wait for `started` (with buffering) as needed.
    pub async fn start(
        &mut self,
        session_id: u64,
        language: Option<&str>,
        context: Option<&str>,
        hot_words: Option<&[String]>,
    ) -> Result<(), ClientError> {
        self.ensure_open()?;
        if self.start_deadline.is_some() || self.started {
            return Err(api_err(
                400,
                "session_already_started",
                "stream start already sent",
            ));
        }
        self.session_id = session_id;
        let mut body = json!({
            "type": "start",
            "session_id": session_id,
        });
        let obj = body.as_object_mut().unwrap();
        if let Some(lang) = language.filter(|s| !s.is_empty()) {
            obj.insert("language".into(), Value::String(lang.to_string()));
        }
        if let Some(ctx) = context.filter(|s| !s.is_empty()) {
            obj.insert("context".into(), Value::String(ctx.to_string()));
        }
        if let Some(words) = hot_words.filter(|w| !w.is_empty()) {
            obj.insert(
                "hot_words".into(),
                Value::Array(words.iter().cloned().map(Value::String).collect()),
            );
        }
        self.send_text(&body.to_string()).await?;
        self.start_deadline = Some(Instant::now() + START_TIMEOUT);
        Ok(())
    }

    /// Appends PCM. Before `started`, samples go into the pre-start buffer
    /// (capped); after `started`, they are sent as 160 ms binary frames.
    /// Returns the latest caption when the engine emitted a `partial` while
    /// servicing this call (otherwise the previous snapshot).
    pub async fn feed(&mut self, pcm: &[i16]) -> Result<StreamPartial, ClientError> {
        self.ensure_open()?;
        if pcm.is_empty() {
            self.pump(false).await?;
            return Ok(self.last.clone());
        }
        if !self.started {
            if self.pre_start.len() + pcm.len() > PRE_START_BUFFER_MAX_SAMPLES {
                self.closed = true;
                let _ = self.send_text(&json!({"type": "cancel"}).to_string()).await;
                return Err(api_err(
                    400,
                    "pre_start_buffer_overflow",
                    &format!(
                        "audio buffered before engine started exceeded {} samples (~{} s); session aborted",
                        PRE_START_BUFFER_MAX_SAMPLES,
                        PRE_START_BUFFER_MAX_SAMPLES / 16_000
                    ),
                ));
            }
            self.pre_start.extend_from_slice(pcm);
            self.pump(true).await?;
            return Ok(self.last.clone());
        }
        self.send_pcm(pcm).await?;
        self.pump(false).await?;
        Ok(self.last.clone())
    }

    /// Waits for `started` if needed, flushes the buffer, sends `finish`,
    /// returns the engine `result`.
    pub async fn finish(&mut self) -> Result<StreamResult, ClientError> {
        self.ensure_open()?;
        self.pump(true).await?;
        if !self.started {
            return Err(api_err(
                400,
                "no_session",
                "finish without a started session",
            ));
        }
        self.send_text(&json!({"type": "finish"}).to_string())
            .await?;
        loop {
            let msg = self.recv_msg().await?;
            match msg.kind.as_str() {
                "partial" => self.apply_partial(&msg),
                "result" => {
                    self.closed = true;
                    return Ok(StreamResult {
                        text: msg.text,
                        language: msg.language,
                        sample_count: msg.sample_count,
                    });
                }
                "error" => return Err(wire_error(&msg)),
                "cancelled" => {
                    self.closed = true;
                    return Err(api_err(409, "cancelled", "session was cancelled"));
                }
                other => {
                    return Err(ClientError::Decode(format!(
                        "unexpected message while finishing: {other}"
                    )))
                }
            }
        }
    }

    /// Cancels the session (best-effort if already closed).
    pub async fn cancel(&mut self) -> Result<(), ClientError> {
        if self.closed {
            return Ok(());
        }
        let _ = self.send_text(&json!({"type": "cancel"}).to_string()).await;
        // Drain until cancelled / close; ignore transport errors on the way out.
        let deadline = Instant::now() + Duration::from_secs(2);
        while Instant::now() < deadline {
            match tokio::time::timeout(Duration::from_millis(200), self.recv_msg()).await {
                Ok(Ok(msg)) if msg.kind == "cancelled" || msg.kind == "error" => break,
                Ok(Ok(_)) => continue,
                _ => break,
            }
        }
        self.closed = true;
        self.pre_start.clear();
        Ok(())
    }

    fn ensure_open(&self) -> Result<(), ClientError> {
        if self.closed {
            Err(api_err(400, "no_session", "stream is closed"))
        } else {
            Ok(())
        }
    }

    async fn send_text(&mut self, text: &str) -> Result<(), ClientError> {
        self.ws
            .send(Message::Text(text.into()))
            .await
            .map_err(|e| ClientError::Transport(e.to_string()))
    }

    async fn send_frame(&mut self, offset: u64, pcm: &[i16]) -> Result<(), ClientError> {
        let mut buf = vec![0u8; 8 + pcm.len() * 2];
        buf[..8].copy_from_slice(&offset.to_le_bytes());
        for (i, s) in pcm.iter().enumerate() {
            buf[8 + 2 * i..8 + 2 * i + 2].copy_from_slice(&s.to_le_bytes());
        }
        self.ws
            .send(Message::Binary(buf.into()))
            .await
            .map_err(|e| ClientError::Transport(e.to_string()))
    }

    /// Sends PCM as 160 ms frames. Does not read replies (avoids async
    /// recursion with [`pump`](Self::pump)); callers drain with `pump` after.
    async fn send_pcm(&mut self, pcm: &[i16]) -> Result<(), ClientError> {
        let mut rest = pcm;
        while !rest.is_empty() {
            let n = rest.len().min(STREAM_CHUNK_SAMPLES);
            let chunk = &rest[..n];
            let offset = self.sample_offset;
            self.send_frame(offset, chunk).await?;
            self.sample_offset += n as u64;
            rest = &rest[n..];
        }
        Ok(())
    }

    async fn flush_pre_start(&mut self) -> Result<(), ClientError> {
        if self.pre_start.is_empty() {
            return Ok(());
        }
        let buf = std::mem::take(&mut self.pre_start);
        self.send_pcm(&buf).await
    }

    fn apply_partial(&mut self, msg: &WireMsg) {
        self.last = StreamPartial {
            committed: msg.committed.clone(),
            tentative: msg.tentative.clone(),
        };
    }

    /// Reads available messages. When `wait_started` is set, blocks until
    /// `started` or the start deadline.
    async fn pump(&mut self, wait_started: bool) -> Result<(), ClientError> {
        if self.started || self.start_deadline.is_none() {
            // Non-blocking drain of anything already queued.
            loop {
                match tokio::time::timeout(Duration::from_millis(0), self.recv_msg()).await {
                    Ok(Ok(msg)) => self.handle_msg(msg)?,
                    Ok(Err(e)) => return Err(e),
                    Err(_) => break,
                }
            }
            return Ok(());
        }
        if !wait_started {
            // Opportunistic: take whatever is ready, else return.
            match tokio::time::timeout(Duration::from_millis(0), self.recv_msg()).await {
                Ok(Ok(msg)) => {
                    self.handle_msg(msg)?;
                    if self.started {
                        self.flush_pre_start().await?;
                    }
                }
                Ok(Err(e)) => return Err(e),
                Err(_) => {}
            }
            return Ok(());
        }
        // Block until started or deadline.
        let deadline = self.start_deadline.unwrap();
        while !self.started {
            let left = deadline.saturating_duration_since(Instant::now());
            if left.is_zero() {
                self.closed = true;
                return Err(api_err(
                    504,
                    "start_timeout",
                    &format!(
                        "engine did not send started within {}s",
                        START_TIMEOUT.as_secs()
                    ),
                ));
            }
            match tokio::time::timeout(left, self.recv_msg()).await {
                Ok(Ok(msg)) => {
                    self.handle_msg(msg)?;
                }
                Ok(Err(e)) => return Err(e),
                Err(_) => {
                    self.closed = true;
                    return Err(api_err(
                        504,
                        "start_timeout",
                        &format!(
                            "engine did not send started within {}s",
                            START_TIMEOUT.as_secs()
                        ),
                    ));
                }
            }
        }
        self.flush_pre_start().await?;
        // Drain partials produced by the flush without waiting again.
        loop {
            match tokio::time::timeout(Duration::from_millis(0), self.recv_msg()).await {
                Ok(Ok(msg)) => self.handle_msg(msg)?,
                Ok(Err(e)) => return Err(e),
                Err(_) => break,
            }
        }
        Ok(())
    }

    fn handle_msg(&mut self, msg: WireMsg) -> Result<(), ClientError> {
        match msg.kind.as_str() {
            "started" => {
                if msg.session_id != 0 && msg.session_id != self.session_id {
                    return Err(ClientError::Decode(format!(
                        "started session_id {} != {}",
                        msg.session_id, self.session_id
                    )));
                }
                self.mode = StreamMode::parse(&msg.mode).or(Some(StreamMode::Streaming));
                self.started = true;
                self.start_deadline = None;
                Ok(())
            }
            "partial" => {
                self.apply_partial(&msg);
                Ok(())
            }
            "error" => Err(wire_error(&msg)),
            "result" | "cancelled" => Err(ClientError::Decode(format!(
                "unexpected {} before finish/cancel",
                msg.kind
            ))),
            other => Err(ClientError::Decode(format!(
                "unknown stream message type {other}"
            ))),
        }
    }

    async fn recv_msg(&mut self) -> Result<WireMsg, ClientError> {
        loop {
            match self.ws.next().await {
                Some(Ok(Message::Text(t))) => {
                    return serde_json::from_str(t.as_str())
                        .map_err(|e| ClientError::Decode(e.to_string()));
                }
                Some(Ok(Message::Ping(p))) => {
                    self.ws
                        .send(Message::Pong(p))
                        .await
                        .map_err(|e| ClientError::Transport(e.to_string()))?;
                }
                Some(Ok(Message::Pong(_))) | Some(Ok(Message::Frame(_))) => continue,
                Some(Ok(Message::Binary(_))) => {
                    return Err(ClientError::Decode(
                        "unexpected binary frame from engine".into(),
                    ));
                }
                Some(Ok(Message::Close(_))) | None => {
                    self.closed = true;
                    return Err(ClientError::Transport(
                        "engine closed the ASR stream".into(),
                    ));
                }
                Some(Err(e)) => {
                    self.closed = true;
                    return Err(ClientError::Transport(e.to_string()));
                }
            }
        }
    }
}

fn api_err(status: u16, code: &str, message: &str) -> ClientError {
    ClientError::Api {
        status,
        code: code.to_string(),
        message: message.to_string(),
    }
}

fn wire_error(msg: &WireMsg) -> ClientError {
    let status = match msg.code.as_str() {
        "realtime_session_active" | "no_local_engine" | "wrong_engine" => 409,
        "invalid_priority" => 400,
        _ => 500,
    };
    let mut message = msg.message.clone();
    if let Some(exp) = msg.expected_offset {
        if !message.is_empty() {
            message.push(' ');
        }
        message.push_str(&format!("(expected_offset={exp})"));
    }
    api_err(status, &msg.code, &message)
}

fn parse_http_error_body(body: &str) -> (String, String) {
    #[derive(Deserialize)]
    struct Envelope {
        error: Detail,
    }
    #[derive(Deserialize)]
    struct Detail {
        code: String,
        message: String,
    }
    serde_json::from_str::<Envelope>(body)
        .map(|e| (e.error.code, e.error.message))
        .unwrap_or_else(|_| (String::new(), body.to_string()))
}
