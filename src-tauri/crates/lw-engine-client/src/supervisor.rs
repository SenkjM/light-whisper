// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Keeps one engine process alive and reports its status.
//!
//! Restart policy (PLAN §6 #3 "指数退避重启"), bounded so a binary that
//! crashes on start does not loop forever:
//! - backoff `initial_backoff × 2^(n-1)`, capped at `max_backoff`
//!   (defaults: 1 s, 2 s, 4 s, 8 s, 16 s, 30 s …);
//! - after `max_consecutive_failures` failures in a row the supervisor gives
//!   up and reports [`SupervisorState::Failed`];
//! - a process that stayed up for `healthy_reset_after` resets the counter;
//! - an incompatible `api_version` is fatal immediately (restarting the same
//!   binary cannot fix it).

use serde::Serialize;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};
use tokio::sync::watch;
use tokio::task::JoinHandle;

use crate::client::EngineClient;
use crate::events::{EngineEvent, EventStream};
use crate::handshake::HandshakeError;
use crate::process::{spawn_engine, SpawnError, SpawnOptions};
use crate::SUPPORTED_API_VERSION;

#[derive(Debug, Clone)]
pub struct RestartPolicy {
    pub initial_backoff: Duration,
    pub max_backoff: Duration,
    pub max_consecutive_failures: u32,
    pub healthy_reset_after: Duration,
}

impl Default for RestartPolicy {
    fn default() -> Self {
        Self {
            initial_backoff: Duration::from_secs(1),
            max_backoff: Duration::from_secs(30),
            max_consecutive_failures: 5,
            healthy_reset_after: Duration::from_secs(60),
        }
    }
}

impl RestartPolicy {
    /// Backoff before restart attempt number `failures` (1-based).
    pub fn backoff(&self, failures: u32) -> Duration {
        let exp = failures.saturating_sub(1).min(16);
        self.initial_backoff
            .saturating_mul(1u32 << exp)
            .min(self.max_backoff)
    }
}

#[derive(Debug, Clone)]
pub struct SupervisorOptions {
    pub spawn: SpawnOptions,
    pub policy: RestartPolicy,
}

#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum SupervisorState {
    Starting,
    Running,
    Restarting,
    Failed,
    Stopped,
}

/// Snapshot reported to the UI (`go-engine-status` in the app).
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct SupervisorStatus {
    pub state: SupervisorState,
    pub pid: Option<u32>,
    pub port: Option<u16>,
    pub version: Option<String>,
    pub api_version: Option<u32>,
    pub backend: Option<String>,
    /// Restarts performed since start.
    pub restarts: u32,
    pub consecutive_failures: u32,
    /// Delay before the next restart attempt (state `restarting`).
    pub next_restart_ms: Option<u64>,
    pub last_error: Option<String>,
    pub binary: String,
}

#[derive(Debug, Clone)]
pub enum SupervisorEvent {
    Status(SupervisorStatus),
    Engine(EngineEvent),
}

type Callback = Arc<dyn Fn(SupervisorEvent) + Send + Sync + 'static>;

struct Shared {
    status: Mutex<SupervisorStatus>,
    client_tx: watch::Sender<Option<EngineClient>>,
    callback: Callback,
}

impl Shared {
    fn update(&self, f: impl FnOnce(&mut SupervisorStatus)) {
        let snap = {
            let mut s = self.status.lock().unwrap_or_else(|e| e.into_inner());
            f(&mut s);
            s.clone()
        };
        (self.callback)(SupervisorEvent::Status(snap));
    }
}

/// Handle to a running supervisor. Clones share the same engine.
#[derive(Clone)]
pub struct Supervisor {
    shared: Arc<Shared>,
    shutdown_tx: watch::Sender<bool>,
    task: Arc<Mutex<Option<JoinHandle<()>>>>,
}

impl Supervisor {
    /// Starts supervising. Must be called inside a Tokio runtime.
    pub fn start(
        opts: SupervisorOptions,
        callback: impl Fn(SupervisorEvent) + Send + Sync + 'static,
    ) -> Self {
        let (client_tx, _) = watch::channel(None);
        let shared = Arc::new(Shared {
            status: Mutex::new(SupervisorStatus {
                state: SupervisorState::Starting,
                pid: None,
                port: None,
                version: None,
                api_version: None,
                backend: None,
                restarts: 0,
                consecutive_failures: 0,
                next_restart_ms: None,
                last_error: None,
                binary: opts.spawn.binary.display().to_string(),
            }),
            client_tx,
            callback: Arc::new(callback),
        });
        let (shutdown_tx, shutdown_rx) = watch::channel(false);
        let task = tokio::spawn(run(opts, shared.clone(), shutdown_rx));
        Self {
            shared,
            shutdown_tx,
            task: Arc::new(Mutex::new(Some(task))),
        }
    }

    pub fn status(&self) -> SupervisorStatus {
        self.shared
            .status
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .clone()
    }

    /// Client for the currently running engine, if any.
    pub fn client(&self) -> Option<EngineClient> {
        self.shared.client_tx.borrow().clone()
    }

    /// Waits until an engine is running (or `timeout` elapses / the supervisor gives up).
    pub async fn wait_ready(&self, timeout: Duration) -> Option<EngineClient> {
        let mut rx = self.shared.client_tx.subscribe();
        let fut = async {
            loop {
                if let Some(c) = rx.borrow_and_update().clone() {
                    return Some(c);
                }
                if matches!(
                    self.status().state,
                    SupervisorState::Failed | SupervisorState::Stopped
                ) {
                    return None;
                }
                // Wake on client change, or poll state every 200 ms.
                let _ = tokio::time::timeout(Duration::from_millis(200), rx.changed()).await;
            }
        };
        tokio::time::timeout(timeout, fut).await.ok().flatten()
    }

    /// Stops the engine (kills the child) and the supervisor loop.
    pub async fn shutdown(&self, timeout: Duration) {
        let _ = self.shutdown_tx.send(true);
        let task = self.task.lock().unwrap_or_else(|e| e.into_inner()).take();
        if let Some(task) = task {
            if tokio::time::timeout(timeout, task).await.is_err() {
                log::warn!("lw-engine supervisor did not stop within {timeout:?}");
            }
        }
    }
}

async fn wait_shutdown(rx: &mut watch::Receiver<bool>) {
    while !*rx.borrow_and_update() {
        if rx.changed().await.is_err() {
            return; // all handles dropped: treat as shutdown
        }
    }
}

enum Outcome {
    Shutdown,
    Fatal(String),
    Failed(String),
    Exited { error: String, uptime: Duration },
}

async fn run(opts: SupervisorOptions, shared: Arc<Shared>, mut shutdown: watch::Receiver<bool>) {
    let policy = opts.policy.clone();
    let mut failures: u32 = 0;
    loop {
        shared.update(|s| {
            s.state = SupervisorState::Starting;
            s.next_restart_ms = None;
        });
        let outcome = run_once(&opts.spawn, &shared, &mut shutdown).await;
        shared.client_tx.send_replace(None);
        let error = match outcome {
            Outcome::Shutdown => {
                shared.update(|s| {
                    s.state = SupervisorState::Stopped;
                    s.pid = None;
                    s.port = None;
                });
                return;
            }
            Outcome::Fatal(e) => {
                log::error!("lw-engine: {e}; not restarting");
                shared.update(|s| {
                    s.state = SupervisorState::Failed;
                    s.pid = None;
                    s.port = None;
                    s.last_error = Some(e);
                });
                return;
            }
            Outcome::Failed(e) => e,
            Outcome::Exited { error, uptime } => {
                if uptime >= policy.healthy_reset_after {
                    failures = 0;
                }
                error
            }
        };
        failures += 1;
        log::warn!("lw-engine: {error} (consecutive failures: {failures})");
        if failures >= policy.max_consecutive_failures.max(1) {
            shared.update(|s| {
                s.state = SupervisorState::Failed;
                s.pid = None;
                s.port = None;
                s.consecutive_failures = failures;
                s.last_error = Some(format!(
                    "{error}; gave up after {failures} consecutive failures"
                ));
            });
            return;
        }
        let delay = policy.backoff(failures);
        shared.update(|s| {
            s.state = SupervisorState::Restarting;
            s.pid = None;
            s.port = None;
            s.consecutive_failures = failures;
            s.next_restart_ms = Some(delay.as_millis() as u64);
            s.last_error = Some(error);
        });
        tokio::select! {
            _ = tokio::time::sleep(delay) => {}
            _ = wait_shutdown(&mut shutdown) => {
                shared.update(|s| { s.state = SupervisorState::Stopped; s.next_restart_ms = None; });
                return;
            }
        }
        shared.update(|s| s.restarts += 1);
    }
}

async fn run_once(
    spawn: &SpawnOptions,
    shared: &Arc<Shared>,
    shutdown: &mut watch::Receiver<bool>,
) -> Outcome {
    let mut proc = tokio::select! {
        r = spawn_engine(spawn) => match r {
            Ok(p) => p,
            Err(SpawnError::Handshake(e @ HandshakeError::UnsupportedApiVersion { .. })) => return Outcome::Fatal(e.to_string()),
            Err(e) => return Outcome::Failed(e.to_string()),
        },
        _ = wait_shutdown(shutdown) => return Outcome::Shutdown,
    };
    let client = EngineClient::new(proc.base_url(), proc.token.clone());

    // Double-check through HTTP: the token works and /health agrees on the version.
    match client.health().await {
        Ok(h) if h.api_version == SUPPORTED_API_VERSION => {}
        Ok(h) => {
            proc.kill(Duration::from_secs(3)).await;
            return Outcome::Fatal(
                HandshakeError::UnsupportedApiVersion {
                    got: h.api_version,
                    expected: SUPPORTED_API_VERSION,
                }
                .to_string(),
            );
        }
        Err(e) => {
            proc.kill(Duration::from_secs(3)).await;
            return Outcome::Failed(format!("engine health check failed: {e}"));
        }
    }

    let hs = proc.handshake.clone();
    shared.update(|s| {
        s.state = SupervisorState::Running;
        s.pid = proc.pid();
        s.port = Some(hs.port);
        s.version = Some(hs.version.clone());
        s.api_version = Some(hs.api_version);
        s.backend = Some(hs.backend.clone());
        s.next_restart_ms = None;
        s.last_error = None;
    });
    shared.client_tx.send_replace(Some(client.clone()));
    let started = Instant::now();

    let forwarder = tokio::spawn(forward_events(client, shared.callback.clone()));
    let outcome = tokio::select! {
        status = proc.wait() => {
            let error = match status {
                Ok(st) => format!("engine exited unexpectedly ({st})"),
                Err(e) => format!("waiting for engine failed: {e}"),
            };
            Outcome::Exited { error, uptime: started.elapsed() }
        }
        _ = wait_shutdown(shutdown) => {
            proc.kill(Duration::from_secs(3)).await;
            Outcome::Shutdown
        }
    };
    forwarder.abort();
    outcome
}

/// Forwards `/v1/events` while the process lives; reconnects if the engine
/// closes the stream (e.g. slow-consumer close 1008). The first message after
/// each (re)connect is an `engine_status` snapshot, which resyncs the UI.
async fn forward_events(client: EngineClient, callback: Callback) {
    loop {
        match EventStream::connect(&client).await {
            Ok(mut stream) => {
                while let Some(item) = stream.next().await {
                    match item {
                        Ok(ev) => callback(SupervisorEvent::Engine(ev)),
                        Err(e) => {
                            log::debug!("lw-engine events: {e}");
                            break;
                        }
                    }
                }
            }
            Err(e) => log::debug!("lw-engine events connect failed: {e}"),
        }
        tokio::time::sleep(Duration::from_millis(500)).await;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn backoff_is_exponential_and_capped() {
        let p = RestartPolicy::default();
        let secs: Vec<u64> = (1..=8).map(|n| p.backoff(n).as_secs()).collect();
        assert_eq!(secs, vec![1, 2, 4, 8, 16, 30, 30, 30]);
        assert_eq!(p.backoff(1000), Duration::from_secs(30));
    }
}
