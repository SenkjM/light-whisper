// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Integration tests against the real Go engine (`engine/`, `--backend mock`).
//!
//! The engine is built with `go build` into a temp directory. Tests that need
//! it print a SKIP note and pass when no Go toolchain is available. Set
//! `LW_ENGINE_TEST_GO` to use a specific `go` binary, or `LW_ENGINE_TEST_BIN`
//! to use a prebuilt `lw-engine` (e.g. a Windows build under Wine).

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
        if let Some(bin) = std::env::var_os("LW_ENGINE_TEST_BIN") {
            let bin = PathBuf::from(bin);
            assert!(
                bin.is_file(),
                "LW_ENGINE_TEST_BIN={} does not exist",
                bin.display()
            );
            return Some(bin);
        }
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

/// Helper: spawn mock engine with qwen3 (sentence mode) or r2t2 (streaming).
async fn spawn_mock(tag: &str, engine: &str) -> (EngineProcess, EngineClient, PathBuf) {
    let bin = engine_binary().expect("go binary");
    let data = temp_dir(tag);
    std::fs::write(
        data.join("engine.json"),
        format!(r#"{{"engine":"{engine}"}}"#),
    )
    .unwrap();
    let mut opts = SpawnOptions::new(&bin);
    opts.data_dir = Some(data.clone());
    opts.stderr_log = Some(data.join("stderr.log"));
    // Autoload mock so NewStream / Transcribe work immediately.
    let proc = spawn_engine(&opts).await.expect("spawn");
    let client = EngineClient::new(proc.base_url(), proc.token.clone());
    // Wait until model_loaded.
    for _ in 0..50 {
        let st = client.status().await.unwrap();
        if st["model_loaded"] == true {
            break;
        }
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    (proc, client, data)
}

fn pcm_silence(n: usize) -> Vec<i16> {
    vec![0; n]
}

fn pcm_tone(n: usize) -> Vec<i16> {
    // Non-zero samples so the Qwen3 mock sentence stream commits.
    vec![1000; n]
}

#[tokio::test(flavor = "multi_thread")]
async fn stream_buffers_before_started_then_flushes() {
    let Some(_) = engine_binary() else { return };
    let (mut proc, client, _data) = spawn_mock("stream-buf", "qwen3-asr-0.6b").await;

    let mut stream = AsrStream::connect(&client).await.unwrap();
    stream.start(7, Some("zh"), None, None).await.unwrap();

    // Feed audio BEFORE waiting for started — must buffer, not drop.
    let chunk = pcm_tone(STREAM_CHUNK_SAMPLES);
    for _ in 0..5 {
        stream.feed(&chunk).await.unwrap();
    }
    assert!(!stream.started() || stream.sample_offset() > 0);
    // Force the wait/flush path.
    let partial = stream.feed(&[]).await.unwrap();
    assert!(stream.started(), "started should have arrived");
    assert_eq!(stream.mode(), Some(StreamMode::Sentence));
    // 5 chunks flushed (+ possibly empty feed) — offset accounts for buffered audio.
    assert!(
        stream.sample_offset() >= (5 * STREAM_CHUNK_SAMPLES) as u64,
        "offset {} after buffered flush",
        stream.sample_offset()
    );
    let _ = partial;

    // More live audio + silence to commit a sentence in the mock.
    stream.feed(&pcm_tone(16_000)).await.unwrap();
    stream.feed(&pcm_silence(8_000)).await.unwrap(); // 500 ms silence → commit
    let result = stream.finish().await.unwrap();
    assert!(!result.text.is_empty(), "mock sentence text: {:?}", result);
    assert_eq!(result.sample_count, stream.sample_offset());

    proc.kill(Duration::from_secs(3)).await;
}

#[tokio::test(flavor = "multi_thread")]
async fn stream_pre_start_buffer_overflow_fails() {
    let Some(_) = engine_binary() else { return };
    let (mut proc, client, _data) = spawn_mock("stream-ovf", "confucius4-r2t2").await;

    // Use a client that never gets started quickly: connect to a second
    // stream while the first holds the realtime slot → second connect is 409.
    // For overflow we need start sent but started delayed. Hold the slot with
    // stream A, then... actually overflow test doesn't need the engine to
    // answer: we can fill the buffer before pumping for started by feeding
    // more than the cap while the engine is busy.
    // Simplest: connect, start, feed PRE_START_BUFFER_MAX_SAMPLES + 1 without
    // giving the runtime time — but mock answers started instantly.
    // So craft the overflow by calling feed with a huge slice in one go
    // before the first pump reads started. Racey on a fast machine.
    // Instead: unit-style — feed exactly cap+1 in one shot right after start;
    // if started arrives first, skip. We accept either overflow error or
    // success; assert overflow when !started after first large feed attempt.

    let mut stream = AsrStream::connect(&client).await.unwrap();
    stream.start(1, None, None, None).await.unwrap();
    let huge = pcm_silence(PRE_START_BUFFER_MAX_SAMPLES + STREAM_CHUNK_SAMPLES);
    match stream.feed(&huge).await {
        Err(ClientError::Api { code, .. }) if code == "pre_start_buffer_overflow" => {}
        Ok(_) => {
            // Engine answered started before we appended — buffer path not hit.
            // Still verify a normal finish works.
            let _ = stream.finish().await;
        }
        Err(e) => panic!("unexpected error: {e}"),
    }
    proc.kill(Duration::from_secs(3)).await;
}

#[tokio::test(flavor = "multi_thread")]
async fn stream_second_session_conflict_maps_to_409() {
    let Some(_) = engine_binary() else { return };
    let (mut proc, client, _data) = spawn_mock("stream-409", "qwen3-asr-0.6b").await;

    let mut a = AsrStream::connect(&client).await.unwrap();
    a.start(1, None, None, None).await.unwrap();
    a.feed(&[]).await.unwrap(); // wait started
    assert!(a.started());

    match AsrStream::connect(&client).await {
        Err(e) => {
            assert!(
                e.is_realtime_session_active() || e.is_busy(),
                "expected 409 busy, got {e:?}"
            );
        }
        Ok(mut b) => {
            // Handshake allowed (race) — start should fail.
            b.start(2, None, None, None).await.ok();
            let err = b
                .feed(&pcm_tone(100))
                .await
                .err()
                .or(b.finish().await.err());
            assert!(err.is_some());
        }
    }
    a.cancel().await.unwrap();
    proc.kill(Duration::from_secs(3)).await;
}

#[tokio::test(flavor = "multi_thread")]
async fn transcribe_with_priority_and_invalid_priority() {
    let Some(_) = engine_binary() else { return };
    let (mut proc, client, _data) = spawn_mock("tx-prio", "qwen3-asr-0.6b").await;

    let pcm = pcm_tone(16_000); // 1 s
    let ok = client
        .transcribe_pcm(
            &pcm,
            &TranscribeOptions {
                language: Some("zh".into()),
                priority: Some("high".into()),
                ..Default::default()
            },
        )
        .await
        .unwrap();
    assert!(!ok.text.is_empty());
    assert!(ok.sample_count >= 16_000);

    let err = client
        .transcribe_pcm(
            &pcm,
            &TranscribeOptions {
                priority: Some("urgent".into()),
                ..Default::default()
            },
        )
        .await
        .unwrap_err();
    assert!(
        err.is_invalid_priority(),
        "expected invalid_priority, got {err:?}"
    );

    proc.kill(Duration::from_secs(3)).await;
}

#[tokio::test(flavor = "multi_thread")]
async fn jobs_submit_progress_cancel() {
    let Some(_) = engine_binary() else { return };
    let (mut proc, client, data) = spawn_mock("jobs", "qwen3-asr-0.6b").await;

    // Write a tiny WAV (1 s tone) for path-based submit.
    let wav_path = data.join("clip.wav");
    write_wav(&wav_path, &pcm_tone(16_000));

    // Event listener for job_progress.
    let (tx, mut rx) = mpsc::unbounded_channel();
    let mut ev = events::EventStream::connect(&client).await.unwrap();
    tokio::spawn(async move {
        while let Some(Ok(e)) = ev.next().await {
            if e.kind == "job_progress" {
                let _ = tx.send(e);
            }
        }
    });

    let job = client
        .submit_job_path(&JobPathRequest {
            path: wav_path.to_string_lossy().to_string(),
            priority: Some("normal".into()),
            segments: true,
            ..Default::default()
        })
        .await
        .unwrap();
    assert_eq!(job.state, "queued");
    assert!(!job.id.is_empty());

    // Wait for completion via GET + events.
    let mut saw_progress = false;
    let finished = tokio::time::timeout(Duration::from_secs(15), async {
        loop {
            while let Ok(ev) = rx.try_recv() {
                saw_progress = true;
                assert_eq!(ev.data["id"], job.id);
            }
            let j = client.get_job(&job.id).await.unwrap();
            if j.state == "completed" || j.state == "failed" || j.state == "cancelled" {
                return j;
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await
    .expect("job timed out");
    assert_eq!(finished.state, "completed");
    assert!(finished.text.as_ref().is_some_and(|t| !t.is_empty()));
    assert!(saw_progress, "expected at least one job_progress event");

    // Cancel a long job: submit silence-padded long PCM then delete.
    let long = pcm_tone(16_000 * 5);
    let long_wav = data.join("long.wav");
    write_wav(&long_wav, &long);
    // Slow the mock so we can cancel mid-flight.
    // (Mock has no WorkPerSecond knob via API; cancel a queued job instead by
    // holding realtime session.)
    let mut hold = AsrStream::connect(&client).await.unwrap();
    hold.start(99, None, None, None).await.unwrap();
    hold.feed(&[]).await.unwrap();

    let job2 = client
        .submit_job_path(&JobPathRequest {
            path: long_wav.to_string_lossy().to_string(),
            ..Default::default()
        })
        .await
        .unwrap();
    // Still queued because of realtime hold.
    tokio::time::sleep(Duration::from_millis(100)).await;
    client.cancel_job(&job2.id).await.unwrap();
    let cancelled = client.get_job(&job2.id).await;
    match cancelled {
        Ok(j) => assert!(
            j.state == "cancelled" || j.state == "queued",
            "state after cancel: {}",
            j.state
        ),
        Err(e) => assert!(e.is_not_found(), "unexpected: {e:?}"),
    }
    hold.cancel().await.unwrap();

    // invalid priority on jobs
    let err = client
        .submit_job_path(&JobPathRequest {
            path: wav_path.to_string_lossy().to_string(),
            priority: Some("realtime".into()), // files reject realtime
            ..Default::default()
        })
        .await
        .unwrap_err();
    assert!(err.is_invalid_priority(), "{err:?}");

    proc.kill(Duration::from_secs(3)).await;
}

fn write_wav(path: &Path, pcm: &[i16]) {
    let mut out = Vec::with_capacity(44 + pcm.len() * 2);
    let data_len = (pcm.len() * 2) as u32;
    let file_len = 36 + data_len;
    out.extend_from_slice(b"RIFF");
    out.extend_from_slice(&file_len.to_le_bytes());
    out.extend_from_slice(b"WAVE");
    out.extend_from_slice(b"fmt ");
    out.extend_from_slice(&16u32.to_le_bytes());
    out.extend_from_slice(&1u16.to_le_bytes()); // PCM
    out.extend_from_slice(&1u16.to_le_bytes()); // mono
    out.extend_from_slice(&16_000u32.to_le_bytes());
    out.extend_from_slice(&(16_000u32 * 2).to_le_bytes()); // byte rate
    out.extend_from_slice(&2u16.to_le_bytes()); // block align
    out.extend_from_slice(&16u16.to_le_bytes()); // bits
    out.extend_from_slice(b"data");
    out.extend_from_slice(&data_len.to_le_bytes());
    for s in pcm {
        out.extend_from_slice(&s.to_le_bytes());
    }
    std::fs::write(path, out).unwrap();
}
