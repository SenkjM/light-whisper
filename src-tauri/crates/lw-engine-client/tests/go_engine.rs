// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Integration tests against the real Go engine (`engine/`, `--backend mock`).
//!
//! The engine is built with `go build` into a temp directory. Tests that need
//! it print a SKIP note and pass when no Go toolchain is available (set
//! `LW_ENGINE_TEST_GO` to point at a specific `go` binary).

use lw_engine_client::*;
use serde_json::{json, Map, Value};
use std::path::{Path, PathBuf};
use std::sync::{Mutex, OnceLock};
use std::time::Duration;
use tokio::sync::mpsc;

fn engine_src() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../engine")
}

fn temp_dir(tag: &str) -> PathBuf {
    let d = std::env::temp_dir().join(format!("lw-engine-it-{tag}-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&d);
    std::fs::create_dir_all(&d).unwrap();
    d
}

/// Builds lw-engine once per test binary; `None` = skip.
fn engine_binary() -> Option<PathBuf> {
    static BIN: OnceLock<Option<PathBuf>> = OnceLock::new();
    BIN.get_or_init(|| {
        let go = std::env::var_os("LW_ENGINE_TEST_GO").unwrap_or_else(|| "go".into());
        let ok = std::process::Command::new(&go)
            .arg("version")
            .output()
            .map(|o| o.status.success());
        if !matches!(ok, Ok(true)) {
            eprintln!("SKIP: Go toolchain not found; lw-engine integration tests skipped");
            return None;
        }
        if !engine_src().join("go.mod").is_file() {
            eprintln!(
                "SKIP: engine sources not found at {}",
                engine_src().display()
            );
            return None;
        }
        let out = temp_dir("bin").join(binary_file_name());
        let status = std::process::Command::new(&go)
            .current_dir(engine_src())
            .args(["build", "-o"])
            .arg(&out)
            .arg("./cmd/lw-engine")
            .status()
            .expect("run go build");
        assert!(status.success(), "go build ./cmd/lw-engine failed");
        Some(out)
    })
    .clone()
}

fn obj(v: Value) -> Map<String, Value> {
    v.as_object().unwrap().clone()
}

async fn next_event(rx: &mut mpsc::UnboundedReceiver<EngineEvent>, kind: &str) -> EngineEvent {
    tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            let ev = rx.recv().await.expect("event channel closed");
            if ev.kind == kind {
                return ev;
            }
        }
    })
    .await
    .unwrap_or_else(|_| panic!("timed out waiting for {kind} event"))
}

#[tokio::test(flavor = "multi_thread")]
async fn handshake_config_conflict_and_events() {
    let Some(bin) = engine_binary() else { return };
    let data = temp_dir("cfg");
    // A shell-owned key that the engine must preserve.
    std::fs::write(
        data.join("engine.json"),
        r#"{"engine":"qwen3-asr-0.6b","glm_endpoint":"intl"}"#,
    )
    .unwrap();

    let mut opts = SpawnOptions::new(&bin);
    opts.data_dir = Some(data.clone());
    opts.extra_args = vec!["--no-autoload".into()];
    opts.stderr_log = Some(data.join("stderr.log"));
    let mut proc = spawn_engine(&opts).await.expect("spawn + handshake");
    assert_eq!(proc.handshake.api_version, SUPPORTED_API_VERSION);
    assert_eq!(proc.handshake.backend, "mock");
    assert_eq!(proc.token.len(), 64);

    let client = EngineClient::new(proc.base_url(), proc.token.clone());
    let health = client.health().await.unwrap();
    assert_eq!(health.api_version, 1);
    let status = client.status().await.unwrap();
    assert_eq!(status["backend"], "mock");

    // A wrong token is rejected.
    let bad = EngineClient::new(proc.base_url(), "0".repeat(64));
    assert_eq!(bad.health().await.unwrap_err(), ClientError::Unauthorized);

    // Event subscription: seq 0 is an engine_status snapshot.
    let (tx, mut rx) = mpsc::unbounded_channel();
    let mut stream = events::EventStream::connect(&client).await.unwrap();
    tokio::spawn(async move {
        while let Some(Ok(ev)) = stream.next().await {
            if tx.send(ev).is_err() {
                break;
            }
        }
    });
    let first = next_event(&mut rx, "engine_status").await;
    assert_eq!(first.seq, 0);

    let snap = client.get_config().await.unwrap();
    assert_eq!(snap.schema_version, 1);
    assert_eq!(snap.values["engine"], "qwen3-asr-0.6b");
    assert_eq!(snap.apply["gpu_idle_seconds"], "live");

    // PATCH with the current revision.
    let res = client
        .patch_config(&snap.revision, &obj(json!({"gpu_idle_seconds": 120})))
        .await
        .unwrap();
    assert_ne!(res.revision, snap.revision);
    assert_eq!(res.applied, vec!["gpu_idle_seconds".to_string()]);
    let ev = next_event(&mut rx, "config_changed").await;
    assert_eq!(ev.data["revision"], Value::String(res.revision.clone()));
    assert!(ev.seq > 0);

    // The old revision is now stale → 412 surfaced as Conflict.
    let err = client
        .patch_config(&snap.revision, &obj(json!({"gpu_idle_seconds": 5})))
        .await
        .unwrap_err();
    assert_eq!(err, ClientError::Conflict);

    // update_config re-reads the revision and succeeds.
    let res2 = client
        .update_config(&obj(json!({"engine": "confucius4-r2t2"})), 3)
        .await
        .unwrap();
    assert_eq!(res2.pending_reload, vec!["engine".to_string()]);

    // Validation errors carry per-field messages.
    match client
        .update_config(&obj(json!({"gpu_idle_seconds": "abc"})), 3)
        .await
        .unwrap_err()
    {
        ClientError::Invalid { fields, .. } => assert!(fields.contains_key("gpu_idle_seconds")),
        other => panic!("expected Invalid, got {other:?}"),
    }

    // Persisted to engine.json; the shell-owned key survived.
    let on_disk: Value =
        serde_json::from_slice(&std::fs::read(data.join("engine.json")).unwrap()).unwrap();
    assert_eq!(on_disk["gpu_idle_seconds"], 120);
    assert_eq!(on_disk["engine"], "confucius4-r2t2");
    assert_eq!(on_disk["glm_endpoint"], "intl");

    // Reload applies the pending engine change (mock backend, nothing loaded yet).
    let reload = client.reload().await.unwrap();
    assert!(reload["applied"].is_array(), "reload response: {reload}");

    proc.kill(Duration::from_secs(5)).await;
}

#[cfg(unix)]
fn kill_pid(pid: u32) {
    let _ = std::process::Command::new("kill")
        .arg("-9")
        .arg(pid.to_string())
        .status();
}

#[cfg(windows)]
fn kill_pid(pid: u32) {
    let _ = std::process::Command::new("taskkill")
        .args(["/F", "/PID", &pid.to_string()])
        .status();
}

#[tokio::test(flavor = "multi_thread")]
async fn supervisor_restarts_after_crash_and_stops() {
    let Some(bin) = engine_binary() else { return };
    let data = temp_dir("sup");
    let mut spawn = SpawnOptions::new(&bin);
    spawn.data_dir = Some(data.clone());
    spawn.extra_args = vec!["--no-autoload".into()];
    let policy = RestartPolicy {
        initial_backoff: Duration::from_millis(100),
        max_backoff: Duration::from_millis(400),
        max_consecutive_failures: 3,
        healthy_reset_after: Duration::from_secs(60),
    };
    let statuses: std::sync::Arc<Mutex<Vec<SupervisorStatus>>> = Default::default();
    let (ev_tx, mut ev_rx) = mpsc::unbounded_channel();
    let st = statuses.clone();
    let sup = Supervisor::start(SupervisorOptions { spawn, policy }, move |e| match e {
        SupervisorEvent::Status(s) => st.lock().unwrap().push(s),
        SupervisorEvent::Engine(ev) => {
            let _ = ev_tx.send(ev);
        }
    });

    let client = sup
        .wait_ready(Duration::from_secs(20))
        .await
        .expect("engine running");
    assert_eq!(sup.status().state, SupervisorState::Running);
    // Engine events are forwarded through the supervisor callback.
    next_event(&mut ev_rx, "engine_status").await;
    let pid1 = sup.status().pid.expect("pid");
    let rev1 = client.get_config().await.unwrap().revision;
    assert!(!rev1.is_empty());

    // Crash the engine: the supervisor restarts it with a new pid/port.
    kill_pid(pid1);
    let restarted = tokio::time::timeout(Duration::from_secs(20), async {
        loop {
            let s = sup.status();
            if s.state == SupervisorState::Running && s.pid.is_some() && s.pid != Some(pid1) {
                return s;
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await
    .expect("engine restarted");
    assert_eq!(restarted.restarts, 1);
    assert!(statuses
        .lock()
        .unwrap()
        .iter()
        .any(|s| s.state == SupervisorState::Restarting && s.last_error.is_some()));
    let client2 = sup.client().expect("new client");
    client2.health().await.expect("restarted engine healthy");

    sup.shutdown(Duration::from_secs(10)).await;
    assert_eq!(sup.status().state, SupervisorState::Stopped);
    assert!(sup.client().is_none());
    assert!(
        client2.health().await.is_err(),
        "engine still answering after shutdown"
    );
}

/// A fake engine announcing api_version 2 must not be restarted (no Go needed).
#[cfg(unix)]
#[tokio::test(flavor = "multi_thread")]
async fn supervisor_fails_fast_on_incompatible_api_version() {
    use std::os::unix::fs::PermissionsExt;
    let dir = temp_dir("fake");
    let script = dir.join("fake-engine.sh");
    std::fs::write(
        &script,
        "#!/bin/sh\necho '{\"event\":\"ready\",\"port\":1,\"token\":\"t\",\"api_version\":2}'\nsleep 30\n",
    )
    .unwrap();
    std::fs::set_permissions(&script, std::fs::Permissions::from_mode(0o755)).unwrap();

    let statuses: std::sync::Arc<Mutex<Vec<SupervisorStatus>>> = Default::default();
    let st = statuses.clone();
    let sup = Supervisor::start(
        SupervisorOptions {
            spawn: SpawnOptions::new(&script),
            policy: RestartPolicy::default(),
        },
        move |e| {
            if let SupervisorEvent::Status(s) = e {
                st.lock().unwrap().push(s);
            }
        },
    );
    assert!(sup.wait_ready(Duration::from_secs(10)).await.is_none());
    let s = sup.status();
    assert_eq!(s.state, SupervisorState::Failed);
    assert_eq!(s.restarts, 0);
    assert!(s.last_error.unwrap().contains("API version 2"));
    assert!(!statuses
        .lock()
        .unwrap()
        .iter()
        .any(|s| s.state == SupervisorState::Restarting));
}

/// A binary that exits immediately exhausts the bounded restart budget.
#[cfg(unix)]
#[tokio::test(flavor = "multi_thread")]
async fn supervisor_gives_up_after_consecutive_failures() {
    let policy = RestartPolicy {
        initial_backoff: Duration::from_millis(10),
        max_backoff: Duration::from_millis(20),
        max_consecutive_failures: 3,
        healthy_reset_after: Duration::from_secs(60),
    };
    let sup = Supervisor::start(
        SupervisorOptions {
            spawn: SpawnOptions::new("/bin/false"),
            policy,
        },
        |_| {},
    );
    assert!(sup.wait_ready(Duration::from_secs(10)).await.is_none());
    let s = sup.status();
    assert_eq!(s.state, SupervisorState::Failed);
    assert_eq!(s.restarts, 2);
    assert!(s.last_error.unwrap().contains("gave up after 3"));
}
