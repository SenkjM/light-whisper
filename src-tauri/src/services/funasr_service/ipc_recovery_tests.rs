use super::{send_command_to_server, ServerCommand};
use crate::state::{AppState, FunasrProcess};
use std::io::{BufRead, Write};
use std::os::windows::io::AsRawHandle;
use std::process::Stdio;
use std::sync::Arc;
use std::time::Duration;
use tauri::Listener;
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::process::Command;

// Run the test binary itself as a tiny IPC server; no Python/GPU/model dependency.
#[test]
#[ignore = "subprocess fixture, invoked by the IPC recovery tests"]
fn ipc_test_server() {
    let Ok(mode) = std::env::var("LIGHT_WHISPER_TEST_IPC_MODE") else {
        return;
    };
    if mode == "closed_stdin" {
        unsafe {
            windows_sys::Win32::Foundation::CloseHandle(std::io::stdin().as_raw_handle());
        }
    }
    println!("{{\"test_ready\":true}}");
    std::io::stdout().flush().unwrap();
    if mode == "closed_stdout" {
        unsafe {
            windows_sys::Win32::Foundation::CloseHandle(std::io::stdout().as_raw_handle());
        }
    }
    if mode.starts_with("closed_") {
        loop {
            std::thread::park();
        }
    }
    for line in std::io::stdin().lock().lines() {
        let request: serde_json::Value = serde_json::from_str(&line.unwrap()).unwrap();
        println!(
            "{}",
            serde_json::json!({
                "request_id": request["request_id"],
                "success": mode != "application_error",
                "error": (mode == "application_error").then_some("invalid audio"),
            })
        );
        std::io::stdout().flush().unwrap();
    }
}

async fn test_process(mode: &str) -> FunasrProcess {
    let mut child = Command::new(std::env::current_exe().unwrap())
        .args([
            "--exact",
            "services::funasr_service::ipc_recovery_tests::ipc_test_server",
            "--ignored",
            "--nocapture",
        ])
        .env("LIGHT_WHISPER_TEST_IPC_MODE", mode)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .creation_flags(0x08000000)
        .kill_on_drop(true)
        .spawn()
        .unwrap();
    let stdin = child.stdin.take().unwrap();
    let mut stdout = BufReader::new(child.stdout.take().unwrap());
    tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            let mut line = String::new();
            assert_ne!(stdout.read_line(&mut line).await.unwrap(), 0);
            if line.contains("\"test_ready\":true") {
                break;
            }
        }
    })
    .await
    .expect("IPC fixture startup");
    assert!(
        child.try_wait().unwrap().is_none(),
        "fixture must still live"
    );
    FunasrProcess {
        child,
        stdin,
        stdout,
    }
}

#[tokio::test]
async fn broken_pipe_invalidates_live_process_and_allows_a_fresh_server() {
    let app = tauri::test::mock_app();

    for mode in ["closed_stdin", "closed_stdout"] {
        let state = AppState::default();
        *state.engine.funasr_process.lock().await = Some(test_process(mode).await);
        state.set_funasr_ready(true);
        state.set_inline_audio_transport(Some(true));
        let events = Arc::new(parking_lot::Mutex::new(Vec::new()));
        let received = events.clone();
        let listener = app.listen("funasr-status", move |event| {
            received.lock().push(
                serde_json::from_str::<serde_json::Value>(event.payload())
                    .expect("valid engine status payload"),
            );
        });

        let error = tokio::time::timeout(
            Duration::from_secs(5),
            send_command_to_server(&state, &ServerCommand::Status, Some(app.handle())),
        )
        .await
        .expect("broken IPC must return promptly")
        .expect_err("closed pipe must fail");
        assert!(
            error.to_string().contains("刷新 stdin 缓冲区失败")
                || error.to_string().contains("写入命令到 FunASR 失败")
                || error.to_string().contains("stdout 已关闭"),
            "unexpected failure: {error}"
        );
        assert!(!state.is_funasr_ready(), "broken IPC cannot remain ready");
        assert!(state.engine.funasr_process.lock().await.is_none());
        assert_eq!(state.inline_audio_transport(), None);
        assert_eq!(events.lock().len(), 1, "one crash notification per failure");
        assert_eq!(events.lock()[0]["status"], "crashed");
        app.unlisten(listener);

        *state.engine.funasr_process.lock().await = Some(test_process("healthy").await);
        state.set_funasr_ready(true);
        let response = send_command_to_server(&state, &ServerCommand::Status, Some(app.handle()))
            .await
            .expect("fresh server must accept commands");
        assert_eq!(response.success, Some(true));
        assert!(state.is_funasr_ready());
    }
}

#[tokio::test]
async fn application_error_keeps_the_healthy_ipc_server() {
    let app = tauri::test::mock_app();
    let state = AppState::default();
    *state.engine.funasr_process.lock().await = Some(test_process("application_error").await);
    state.set_funasr_ready(true);
    state.set_inline_audio_transport(Some(true));

    let response = send_command_to_server(&state, &ServerCommand::Status, Some(app.handle()))
        .await
        .expect("valid error response is not a transport failure");
    assert_eq!(response.success, Some(false));
    assert!(state.is_funasr_ready());
    assert!(state.engine.funasr_process.lock().await.is_some());
    assert_eq!(state.inline_audio_transport(), Some(true));
}
