// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! `WS /v1/events` subscription.

use futures_util::StreamExt;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::tungstenite::http::HeaderValue;
use tokio_tungstenite::tungstenite::Message;

use crate::client::{ClientError, EngineClient};

/// One event from the engine (`seq` 0 is the initial status snapshot).
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct EngineEvent {
    pub seq: u64,
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(default)]
    pub time: String,
    #[serde(default)]
    pub data: Value,
}

/// An open event stream.
pub struct EventStream {
    inner: tokio_tungstenite::WebSocketStream<
        tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>,
    >,
}

impl EventStream {
    /// Connects to `WS /v1/events` with the bearer token.
    pub async fn connect(client: &EngineClient) -> Result<Self, ClientError> {
        let url = format!("{}/v1/events", client.ws_base_url());
        let mut req = url
            .into_client_request()
            .map_err(|e| ClientError::Transport(e.to_string()))?;
        let auth = HeaderValue::from_str(&format!("Bearer {}", client.token()))
            .map_err(|e| ClientError::Transport(e.to_string()))?;
        req.headers_mut().insert("Authorization", auth);
        let (inner, _resp) = tokio_tungstenite::connect_async(req)
            .await
            .map_err(|e| match e {
                tokio_tungstenite::tungstenite::Error::Http(resp) if resp.status() == 401 => {
                    ClientError::Unauthorized
                }
                other => ClientError::Transport(other.to_string()),
            })?;
        Ok(Self { inner })
    }

    /// Next event; `None` when the engine closed the stream (shutdown, or the
    /// client fell behind — resync via status/config and reconnect).
    pub async fn next(&mut self) -> Option<Result<EngineEvent, ClientError>> {
        loop {
            match self.inner.next().await? {
                Ok(Message::Text(t)) => {
                    return Some(
                        serde_json::from_str(t.as_str())
                            .map_err(|e| ClientError::Decode(e.to_string())),
                    )
                }
                Ok(Message::Close(_)) => return None,
                Ok(_) => continue, // ping/pong/binary
                Err(e) => return Some(Err(ClientError::Transport(e.to_string()))),
            }
        }
    }
}
