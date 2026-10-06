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
