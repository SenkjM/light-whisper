// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Spawning `lw-engine` and reading its handshake.

use std::path::PathBuf;
use std::process::Stdio;
use std::time::Duration;
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::process::{Child, ChildStdin, Command};

use crate::handshake::{read_handshake, Handshake, HandshakeError};

/// How to start the engine.
#[derive(Debug, Clone)]
pub struct SpawnOptions {
    pub binary: PathBuf,
    /// App data directory (`--data-dir`), same as the Tauri app's.
    pub data_dir: Option<PathBuf>,
    /// `--backend` (`mock` until the native backend exists).
    pub backend: String,
    /// Extra CLI arguments (tests, `--no-autoload`, …).
    pub extra_args: Vec<String>,
    /// Where to redirect the engine's stderr log; `None` discards it.
    pub stderr_log: Option<PathBuf>,
    pub handshake_timeout: Duration,
}

impl SpawnOptions {
    pub fn new(binary: impl Into<PathBuf>) -> Self {
        Self {
            binary: binary.into(),
            data_dir: None,
            backend: "mock".to_string(),
            extra_args: Vec::new(),
            stderr_log: None,
            handshake_timeout: Duration::from_secs(15),
        }
    }
}

#[derive(Debug, thiserror::Error)]
pub enum SpawnError {
    #[error("failed to start {path}: {source}")]
    Spawn {
        path: String,
        source: std::io::Error,
    },
    #[error(transparent)]
    Handshake(#[from] HandshakeError),
}

/// A running engine process. Dropping it kills the process.
///
/// stdin stays open for the process lifetime: the engine runs with
/// `--exit-on-stdin-close`, so it also exits if this (parent) process dies
/// without cleaning up.
#[derive(Debug)]
pub struct EngineProcess {
    child: Child,
    _stdin: Option<ChildStdin>,
    pub handshake: Handshake,
    pub token: String,
}

impl EngineProcess {
    pub fn pid(&self) -> Option<u32> {
        self.child.id()
    }

    pub fn base_url(&self) -> String {
        format!("http://127.0.0.1:{}", self.handshake.port)
    }

    /// Waits for the process to exit.
    pub async fn wait(&mut self) -> std::io::Result<std::process::ExitStatus> {
        self.child.wait().await
    }

    /// Kills the process and waits up to `timeout` for it to exit.
    pub async fn kill(&mut self, timeout: Duration) {
        let _ = self.child.start_kill();
        let _ = tokio::time::timeout(timeout, self.child.wait()).await;
    }
}

/// Starts the engine and reads its handshake.
pub async fn spawn_engine(opts: &SpawnOptions) -> Result<EngineProcess, SpawnError> {
    let mut cmd = Command::new(&opts.binary);
    cmd.arg("--listen")
        .arg("127.0.0.1:0")
        .arg("--backend")
        .arg(&opts.backend)
        .arg("--exit-on-stdin-close");
    if let Some(dir) = &opts.data_dir {
        cmd.arg("--data-dir").arg(dir);
    }
    cmd.args(&opts.extra_args)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(match &opts.stderr_log {
            Some(path) => match std::fs::File::create(path) {
                Ok(f) => Stdio::from(f),
                Err(e) => {
                    log::warn!(
                        "lw-engine: cannot create stderr log {}: {e}",
                        path.display()
                    );
                    Stdio::null()
                }
            },
            None => Stdio::null(),
        })
        .kill_on_drop(true);

    // Hide the console window on Windows, same as the Python engine spawn.
    #[cfg(target_os = "windows")]
    {
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        cmd.creation_flags(CREATE_NO_WINDOW);
    }

    let mut child = cmd.spawn().map_err(|source| SpawnError::Spawn {
        path: opts.binary.display().to_string(),
        source,
    })?;
    let stdin = child.stdin.take();
    let stdout = child.stdout.take().expect("stdout is piped");
    let mut reader = BufReader::new(stdout);

    let handshake = match read_handshake(&mut reader, opts.handshake_timeout).await {
        Ok(h) => h,
        Err(e) => {
            let _ = child.start_kill();
            let _ = tokio::time::timeout(Duration::from_secs(2), child.wait()).await;
            return Err(e.into());
        }
    };
    let Some(token) = handshake.token.clone() else {
        let _ = child.start_kill();
        return Err(HandshakeError::MissingToken.into());
    };

    // Keep draining stdout so the pipe can never fill up.
    tokio::spawn(async move {
        let mut line = String::new();
        while matches!(reader.read_line(&mut line).await, Ok(n) if n > 0) {
            line.clear();
        }
    });

    Ok(EngineProcess {
        child,
        _stdin: stdin,
        handshake,
        token,
    })
}
