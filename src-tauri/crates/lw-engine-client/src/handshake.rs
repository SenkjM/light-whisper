// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Startup handshake: the first JSON line the engine prints on stdout.
//!
//! ```json
//! {"event":"ready","port":53817,"token":"…","api_version":1,"version":"…","pid":1234,"backend":"mock"}
//! ```

use serde::Deserialize;
use std::time::Duration;
use tokio::io::{AsyncBufRead, AsyncBufReadExt};

use crate::SUPPORTED_API_VERSION;

/// Parsed handshake line.
#[derive(Debug, Clone, Deserialize, PartialEq, Eq)]
pub struct Handshake {
    pub event: String,
    pub port: u16,
    /// Absent when the engine was started with `--token-file`.
    #[serde(default)]
    pub token: Option<String>,
    pub api_version: u32,
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub pid: u32,
    #[serde(default)]
    pub backend: String,
}

#[derive(Debug, thiserror::Error, PartialEq, Eq)]
pub enum HandshakeError {
    #[error("engine handshake timed out after {0:?}")]
    Timeout(Duration),
    #[error("engine stdout closed before the handshake")]
    Closed,
    #[error("reading engine stdout failed: {0}")]
    Io(String),
    #[error("engine API version {got} is not supported (expected {expected})")]
    UnsupportedApiVersion { got: u32, expected: u32 },
    #[error("engine handshake has no token and no token file was configured")]
    MissingToken,
}

/// Parses one stdout line; `None` for non-handshake lines (noise).
pub fn parse_handshake_line(line: &str) -> Option<Handshake> {
    let trimmed = line.trim();
    if !trimmed.starts_with('{') {
        return None;
    }
    let hs: Handshake = serde_json::from_str(trimmed).ok()?;
    (hs.event == "ready" && hs.port != 0).then_some(hs)
}

/// Checks the contract major version.
pub fn check_api_version(got: u32) -> Result<(), HandshakeError> {
    if got == SUPPORTED_API_VERSION {
        Ok(())
    } else {
        Err(HandshakeError::UnsupportedApiVersion {
            got,
            expected: SUPPORTED_API_VERSION,
        })
    }
}

/// Reads lines until a valid `ready` handshake, skipping noise, within `timeout`.
/// The API version is checked before returning.
pub async fn read_handshake<R: AsyncBufRead + Unpin>(
    reader: &mut R,
    timeout: Duration,
) -> Result<Handshake, HandshakeError> {
    let fut = async {
        let mut line = String::new();
        loop {
            line.clear();
            let n = reader
                .read_line(&mut line)
                .await
                .map_err(|e| HandshakeError::Io(e.to_string()))?;
            if n == 0 {
                return Err(HandshakeError::Closed);
            }
            if let Some(hs) = parse_handshake_line(&line) {
                check_api_version(hs.api_version)?;
                return Ok(hs);
            }
            log::debug!("lw-engine: skipping non-handshake stdout line");
        }
    };
    tokio::time::timeout(timeout, fut)
        .await
        .map_err(|_| HandshakeError::Timeout(timeout))?
}

#[cfg(test)]
mod tests {
    use super::*;
    use tokio::io::BufReader;

    #[tokio::test]
    async fn parses_ready_line_after_noise() {
        let input = b"warming up\n{\"other\":1}\n{\"event\":\"ready\",\"port\":4242,\"token\":\"abc\",\"api_version\":1,\"version\":\"0.0.0-dev\",\"pid\":7,\"backend\":\"mock\"}\n";
        let mut r = BufReader::new(&input[..]);
        let hs = read_handshake(&mut r, Duration::from_secs(1))
            .await
            .unwrap();
        assert_eq!(hs.port, 4242);
        assert_eq!(hs.token.as_deref(), Some("abc"));
        assert_eq!(hs.backend, "mock");
    }

    #[tokio::test]
    async fn rejects_unsupported_api_version() {
        let input = b"{\"event\":\"ready\",\"port\":1,\"token\":\"t\",\"api_version\":2}\n";
        let mut r = BufReader::new(&input[..]);
        assert_eq!(
            read_handshake(&mut r, Duration::from_secs(1)).await,
            Err(HandshakeError::UnsupportedApiVersion {
                got: 2,
                expected: 1
            })
        );
    }

    #[tokio::test]
    async fn closed_and_timeout() {
        let mut r = BufReader::new(&b"noise only\n"[..]);
        assert_eq!(
            read_handshake(&mut r, Duration::from_secs(1)).await,
            Err(HandshakeError::Closed)
        );

        let (_w, rd) = tokio::io::duplex(64); // writer kept open, nothing written
        let mut r = BufReader::new(rd);
        assert!(matches!(
            read_handshake(&mut r, Duration::from_millis(50)).await,
            Err(HandshakeError::Timeout(_))
        ));
    }

    #[test]
    fn ignores_non_ready_or_zero_port() {
        assert!(parse_handshake_line("{\"event\":\"log\",\"port\":1,\"api_version\":1}").is_none());
        assert!(
            parse_handshake_line("{\"event\":\"ready\",\"port\":0,\"api_version\":1}").is_none()
        );
        assert!(parse_handshake_line("not json").is_none());
    }
}
