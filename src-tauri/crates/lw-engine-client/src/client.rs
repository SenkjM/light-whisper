// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Authenticated HTTP client for the engine API.

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use std::collections::BTreeMap;
use std::time::Duration;

/// `GET /v1/config` payload.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct ConfigSnapshot {
    pub schema_version: u32,
    pub revision: String,
    /// Engine-owned settings (see engine/internal/config/schema_v1.json).
    pub values: Map<String, Value>,
    pub apply: BTreeMap<String, String>,
    pub pending_reload: Vec<String>,
}

/// `PATCH /v1/config` payload.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct PatchResult {
    pub revision: String,
    pub applied: Vec<String>,
    pub pending_reload: Vec<String>,
}

#[derive(Debug, Deserialize)]
struct ErrorBody {
    error: ErrorDetail,
}

#[derive(Debug, Deserialize)]
struct ErrorDetail {
    code: String,
    message: String,
    #[serde(default)]
    fields: Option<BTreeMap<String, String>>,
}

#[derive(Debug, thiserror::Error, Clone, PartialEq)]
pub enum ClientError {
    /// 412: the config changed since the given revision. Re-read and retry.
    #[error("engine config changed concurrently (revision mismatch); re-read and retry")]
    Conflict,
    /// 428: If-Match missing.
    #[error("engine config update requires a revision")]
    RevisionRequired,
    /// 400 invalid_config.
    #[error("invalid engine config: {message}")]
    Invalid {
        message: String,
        fields: BTreeMap<String, String>,
    },
    /// 401.
    #[error("engine rejected the token")]
    Unauthorized,
    /// Any other API error (`code` from the error body).
    #[error("engine API error {status} {code}: {message}")]
    Api {
        status: u16,
        code: String,
        message: String,
    },
    #[error("engine request failed: {0}")]
    Transport(String),
    #[error("unexpected engine response: {0}")]
    Decode(String),
}

impl ClientError {
    /// 409 realtime_session_active and similar "busy" conflicts.
    pub fn is_busy(&self) -> bool {
        matches!(self, ClientError::Api { status: 409, .. })
    }

    /// Engine error `code` when this is an [`ClientError::Api`], else `None`.
    pub fn code(&self) -> Option<&str> {
        match self {
            ClientError::Api { code, .. } => Some(code.as_str()),
            _ => None,
        }
    }

    pub fn is_realtime_session_active(&self) -> bool {
        self.code() == Some("realtime_session_active")
    }

    pub fn is_no_local_engine(&self) -> bool {
        self.code() == Some("no_local_engine")
    }

    pub fn is_invalid_priority(&self) -> bool {
        self.code() == Some("invalid_priority")
    }

    pub fn is_not_found(&self) -> bool {
        matches!(self, ClientError::Api { status: 404, code, .. } if code == "not_found")
    }
}

/// Health payload.
#[derive(Debug, Clone, Deserialize, PartialEq, Eq)]
pub struct Health {
    pub status: String,
    pub version: String,
    pub api_version: u32,
}

/// Cheap to clone; all clones share one connection pool.
#[derive(Debug, Clone)]
pub struct EngineClient {
    base: String,
    token: String,
    http: reqwest::Client,
}

impl EngineClient {
    /// `base` is e.g. `http://127.0.0.1:53817`.
    pub fn new(base: impl Into<String>, token: impl Into<String>) -> Self {
        let http = reqwest::Client::builder()
            .no_proxy() // never send loopback traffic (and the token) through a proxy
            .timeout(Duration::from_secs(30))
            .build()
            .expect("reqwest client");
        Self {
            base: base.into().trim_end_matches('/').to_string(),
            token: token.into(),
            http,
        }
    }

    pub fn base_url(&self) -> &str {
        &self.base
    }

    pub fn token(&self) -> &str {
        &self.token
    }

    /// `ws://127.0.0.1:<port>` for WebSocket endpoints.
    pub fn ws_base_url(&self) -> String {
        self.base.replacen("http://", "ws://", 1)
    }

    fn req(&self, method: reqwest::Method, path: &str) -> reqwest::RequestBuilder {
        self.http
            .request(method, format!("{}{}", self.base, path))
            .bearer_auth(&self.token)
    }

    async fn send<T: for<'de> Deserialize<'de>>(
        rb: reqwest::RequestBuilder,
    ) -> Result<T, ClientError> {
        let resp = rb
            .send()
            .await
            .map_err(|e| ClientError::Transport(e.to_string()))?;
        let status = resp.status().as_u16();
        let bytes = resp
            .bytes()
            .await
            .map_err(|e| ClientError::Transport(e.to_string()))?;
        if (200..300).contains(&status) {
            return serde_json::from_slice(&bytes).map_err(|e| ClientError::Decode(e.to_string()));
        }
        let body: Option<ErrorBody> = serde_json::from_slice(&bytes).ok();
        let (code, message, fields) = match body {
            Some(b) => (
                b.error.code,
                b.error.message,
                b.error.fields.unwrap_or_default(),
            ),
            None => (
                String::new(),
                String::from_utf8_lossy(&bytes).into_owned(),
                BTreeMap::new(),
            ),
        };
        Err(match status {
            401 => ClientError::Unauthorized,
            412 => ClientError::Conflict,
            428 => ClientError::RevisionRequired,
            400 if code == "invalid_config" => ClientError::Invalid { message, fields },
            _ => ClientError::Api {
                status,
                code,
                message,
            },
        })
    }

    pub async fn health(&self) -> Result<Health, ClientError> {
        Self::send(self.req(reqwest::Method::GET, "/health")).await
    }

    /// `GET /v1/engine/status` (raw JSON; the shape is in the OpenAPI contract).
    pub async fn status(&self) -> Result<Value, ClientError> {
        Self::send(self.req(reqwest::Method::GET, "/v1/engine/status")).await
    }

    pub async fn get_config(&self) -> Result<ConfigSnapshot, ClientError> {
        Self::send(self.req(reqwest::Method::GET, "/v1/config")).await
    }

    /// `PATCH /v1/config` with `If-Match: <revision>`. A stale revision yields
    /// [`ClientError::Conflict`]; `null` values reset keys to their defaults.
    pub async fn patch_config(
        &self,
        revision: &str,
        patch: &Map<String, Value>,
    ) -> Result<PatchResult, ClientError> {
        Self::send(
            self.req(reqwest::Method::PATCH, "/v1/config")
                .header("If-Match", format!("\"{revision}\""))
                .json(patch),
        )
        .await
    }

    /// Read-modify-write helper: GET the current revision, PATCH, and retry on
    /// [`ClientError::Conflict`] up to `max_attempts` times. Safe because the
    /// patch holds absolute values, not deltas.
    pub async fn update_config(
        &self,
        patch: &Map<String, Value>,
        max_attempts: u32,
    ) -> Result<PatchResult, ClientError> {
        let mut attempt = 0;
        loop {
            attempt += 1;
            let snap = self.get_config().await?;
            match self.patch_config(&snap.revision, patch).await {
                Err(ClientError::Conflict) if attempt < max_attempts.max(1) => continue,
                other => return other,
            }
        }
    }

    /// `POST /v1/engine/reload` → `{applied, status}`.
    pub async fn reload(&self) -> Result<Value, ClientError> {
        Self::send(self.req(reqwest::Method::POST, "/v1/engine/reload")).await
    }
}

/// Options for `POST /v1/asr/transcribe`.
#[derive(Debug, Clone, Default)]
pub struct TranscribeOptions {
    pub language: Option<String>,
    pub context: Option<String>,
    pub hot_words: Vec<String>,
    /// `realtime` / `high` / `normal`; `None` → engine default_priority_voice.
    pub priority: Option<String>,
}

/// Result of `POST /v1/asr/transcribe`.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct TranscribeResult {
    pub text: String,
    #[serde(default)]
    pub language: String,
    pub sample_count: u64,
    pub duration: f64,
}

/// One VAD unit in a completed job (`segments=true`).
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct JobSegment {
    pub start: f64,
    pub end: f64,
    pub text: String,
}

/// File-transcription job (`GET /v1/jobs/{id}`, `job_progress` events omit text/segments).
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Job {
    pub id: String,
    pub state: String,
    pub priority: String,
    #[serde(default)]
    pub engine: String,
    #[serde(default)]
    pub created_at: String,
    #[serde(default)]
    pub started_at: Option<String>,
    #[serde(default)]
    pub finished_at: Option<String>,
    #[serde(default)]
    pub duration: f64,
    #[serde(default)]
    pub segments_total: u32,
    #[serde(default)]
    pub segments_done: u32,
    #[serde(default)]
    pub progress: f64,
    #[serde(default)]
    pub preemptions: u32,
    #[serde(default)]
    pub text: Option<String>,
    #[serde(default)]
    pub language: String,
    #[serde(default)]
    pub segments: Option<Vec<JobSegment>>,
    #[serde(default)]
    pub error: String,
}

/// Body for `POST /v1/jobs` with a file path (absolute WAV).
#[derive(Debug, Clone, Serialize, Default)]
pub struct JobPathRequest {
    pub path: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub language: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub context: Option<String>,
    #[serde(skip_serializing_if = "Vec::is_empty", default)]
    pub hot_words: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub priority: Option<String>,
    #[serde(skip_serializing_if = "std::ops::Not::not", default)]
    pub segments: bool,
}

impl EngineClient {
    /// `POST /v1/asr/transcribe` with raw PCM s16le 16 kHz mono.
    pub async fn transcribe_pcm(
        &self,
        pcm: &[i16],
        opts: &TranscribeOptions,
    ) -> Result<TranscribeResult, ClientError> {
        let mut bytes = Vec::with_capacity(pcm.len() * 2);
        for s in pcm {
            bytes.extend_from_slice(&s.to_le_bytes());
        }
        self.transcribe_bytes(&bytes, "application/octet-stream", opts)
            .await
    }

    /// `POST /v1/asr/transcribe` with a WAV body (PCM 16-bit mono 16 kHz).
    pub async fn transcribe_wav(
        &self,
        wav: &[u8],
        opts: &TranscribeOptions,
    ) -> Result<TranscribeResult, ClientError> {
        self.transcribe_bytes(wav, "audio/wav", opts).await
    }

    async fn transcribe_bytes(
        &self,
        body: &[u8],
        content_type: &str,
        opts: &TranscribeOptions,
    ) -> Result<TranscribeResult, ClientError> {
        let mut url = format!("{}/v1/asr/transcribe", self.base);
        let mut q: Vec<String> = Vec::new();
        if let Some(lang) = opts.language.as_deref().filter(|s| !s.is_empty()) {
            q.push(format!("language={}", enc(lang)));
        }
        if let Some(ctx) = opts.context.as_deref().filter(|s| !s.is_empty()) {
            q.push(format!("context={}", enc(ctx)));
        }
        for w in &opts.hot_words {
            q.push(format!("hot_word={}", enc(w)));
        }
        if let Some(p) = opts.priority.as_deref().filter(|s| !s.is_empty()) {
            q.push(format!("priority={}", enc(p)));
        }
        if !q.is_empty() {
            url.push('?');
            url.push_str(&q.join("&"));
        }
        Self::send(
            self.http
                .request(reqwest::Method::POST, url)
                .bearer_auth(&self.token)
                .header("Content-Type", content_type)
                .body(body.to_vec()),
        )
        .await
    }

    /// `POST /v1/jobs` with JSON `{path, ...}` → 202 Job.
    pub async fn submit_job_path(&self, req: &JobPathRequest) -> Result<Job, ClientError> {
        Self::send(self.req(reqwest::Method::POST, "/v1/jobs").json(req)).await
    }

    /// `POST /v1/jobs` with a WAV / PCM body (query options).
    pub async fn submit_job_wav(
        &self,
        wav: &[u8],
        opts: &TranscribeOptions,
        segments: bool,
    ) -> Result<Job, ClientError> {
        let mut url = format!("{}/v1/jobs", self.base);
        let mut q: Vec<String> = Vec::new();
        if let Some(lang) = opts.language.as_deref().filter(|s| !s.is_empty()) {
            q.push(format!("language={}", enc(lang)));
        }
        if let Some(ctx) = opts.context.as_deref().filter(|s| !s.is_empty()) {
            q.push(format!("context={}", enc(ctx)));
        }
        for w in &opts.hot_words {
            q.push(format!("hot_word={}", enc(w)));
        }
        if let Some(p) = opts.priority.as_deref().filter(|s| !s.is_empty()) {
            q.push(format!("priority={}", enc(p)));
        }
        if segments {
            q.push("segments=true".into());
        }
        if !q.is_empty() {
            url.push('?');
            url.push_str(&q.join("&"));
        }
        Self::send(
            self.http
                .request(reqwest::Method::POST, url)
                .bearer_auth(&self.token)
                .header("Content-Type", "audio/wav")
                .body(wav.to_vec()),
        )
        .await
    }

    pub async fn get_job(&self, id: &str) -> Result<Job, ClientError> {
        Self::send(self.req(reqwest::Method::GET, &format!("/v1/jobs/{id}"))).await
    }

    pub async fn list_jobs(&self) -> Result<Vec<Job>, ClientError> {
        #[derive(Deserialize)]
        struct List {
            jobs: Vec<Job>,
        }
        let list: List = Self::send(self.req(reqwest::Method::GET, "/v1/jobs")).await?;
        Ok(list.jobs)
    }

    /// `DELETE /v1/jobs/{id}` → 204.
    pub async fn cancel_job(&self, id: &str) -> Result<(), ClientError> {
        let resp = self
            .req(reqwest::Method::DELETE, &format!("/v1/jobs/{id}"))
            .send()
            .await
            .map_err(|e| ClientError::Transport(e.to_string()))?;
        let status = resp.status().as_u16();
        if status == 204 || (200..300).contains(&status) {
            return Ok(());
        }
        let bytes = resp
            .bytes()
            .await
            .map_err(|e| ClientError::Transport(e.to_string()))?;
        let body: Option<ErrorBody> = serde_json::from_slice(&bytes).ok();
        let (code, message) = match body {
            Some(b) => (b.error.code, b.error.message),
            None => (String::new(), String::from_utf8_lossy(&bytes).into_owned()),
        };
        Err(match status {
            401 => ClientError::Unauthorized,
            404 => ClientError::Api {
                status,
                code: if code.is_empty() {
                    "not_found".into()
                } else {
                    code
                },
                message,
            },
            _ => ClientError::Api {
                status,
                code,
                message,
            },
        })
    }
}

fn enc(s: &str) -> String {
    // Query-escape without pulling in the url crate (not in the tree).
    let mut out = String::with_capacity(s.len());
    for b in s.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char)
            }
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}
