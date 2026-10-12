// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Go 引擎（lw-engine）接入层，PLAN 步骤 1 / 5 的 Rust 一半。
//!
//! 开关：环境变量 `LW_ENGINE_BACKEND=go|python`，**默认 `go`**（安装包纯 Go，
//! 不再捆绑 `engine.tar.xz` / PyInstaller）。开发者可用 `python` 回退到源码树
//! 里的 Python 编排（不进默认安装包）。
//!
//! `go` 时：
//! - 启动时拉起 lw-engine（`--backend` 取 `LW_ENGINE_GO_BACKEND`，缺省时若原生库齐全则 `native` 否则 `mock`；
//!   真实推理需 `-tags lwnative` 构建并带原生 DLL，见 `engine/README.md`）；
//! - 设置读写走 `/v1/config`；`/v1/events` 转发为 Tauri 事件；
//! - 识别走 `go_asr`（WS 流、HTTP 转录、`/v1/jobs`）。
//!
//! `python` 时本模块不拉起进程；funasr_service 仍可解压/启动历史 Python 引擎
//!（仅本地调试，默认包不再提供归档）。

use std::path::PathBuf;
use std::sync::OnceLock;
use std::time::Duration;

use lw_engine_client::{
    locate_engine_binary, ClientError, ConfigSnapshot, PatchResult, SpawnOptions, Supervisor,
    SupervisorEvent, SupervisorOptions, SupervisorState,
};
use serde_json::{json, Map, Value};
use tauri::{Emitter, Manager};

use crate::utils::error::AppError;
use crate::utils::paths;

/// 进程状态（启动/运行/重启/失败/停止），载荷为 `SupervisorStatus`。
pub const GO_ENGINE_PROCESS_EVENT: &str = "go-engine-process";
/// 引擎 `engine_status` 事件，载荷为 `{seq,type,time,data}`。
pub const GO_ENGINE_STATUS_EVENT: &str = "go-engine-status";
/// 引擎 `config_changed` 事件，载荷为 `{seq,type,time,data}`。
pub const GO_ENGINE_CONFIG_EVENT: &str = "go-engine-config-changed";
/// 引擎 `job_progress` 事件，载荷为 `{seq,type,time,data}`（data = Job，无 text/segments）。
pub const GO_ENGINE_JOB_EVENT: &str = "go-engine-job-progress";

/// Native DLL load + antivirus scan on first withcore start can exceed 10s.
const READY_TIMEOUT: Duration = Duration::from_secs(60);
const CONFLICT_RETRIES: u32 = 3;

#[derive(Debug, Clone, Copy, PartialEq, Eq, serde::Serialize)]
#[serde(rename_all = "lowercase")]
pub enum EngineBackend {
    Python,
    Go,
}

fn parse_backend(value: Option<&str>) -> EngineBackend {
    match value.map(|v| v.trim().to_ascii_lowercase()) {
        Some(v) if v == "python" => EngineBackend::Python,
        Some(v) if v.is_empty() || v == "go" => EngineBackend::Go,
        Some(other) => {
            log::warn!("LW_ENGINE_BACKEND={other} 无法识别，使用 go");
            EngineBackend::Go
        }
        None => EngineBackend::Go,
    }
}

/// 进程启动时读取一次 `LW_ENGINE_BACKEND`。
pub fn backend() -> EngineBackend {
    static BACKEND: OnceLock<EngineBackend> = OnceLock::new();
    *BACKEND.get_or_init(|| parse_backend(std::env::var("LW_ENGINE_BACKEND").ok().as_deref()))
}

pub fn is_go() -> bool {
    backend() == EngineBackend::Go
}

static SUPERVISOR: OnceLock<Supervisor> = OnceLock::new();

fn dev_engine_dir() -> Option<PathBuf> {
    // 开发构建：engine/ 下 `go build -o lw-engine[.exe] ./cmd/lw-engine` 的产物。
    #[cfg(debug_assertions)]
    {
        Some(PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../engine"))
    }
    #[cfg(not(debug_assertions))]
    {
        None
    }
}

/// 在 `setup` 中调用；python 模式下直接返回。
pub fn start(app: &tauri::AppHandle) {
    if !is_go() {
        return;
    }
    let override_path = std::env::var_os("LW_ENGINE_PATH")
        .map(PathBuf::from)
        .or_else(|| {
            crate::state::app_preferences::snapshot()
                .management_engine_path
                .filter(|p| !p.trim().is_empty())
                .map(PathBuf::from)
        });
    let resource_dir = app.path().resource_dir().ok();
    let Some(binary) = locate_engine_binary(
        override_path.as_deref(),
        resource_dir.as_deref(),
        dev_engine_dir().as_deref(),
    ) else {
        let msg = format!(
            "找不到 Go 引擎 {}（可用 LW_ENGINE_PATH 指定，或先运行 scripts/build_lw_engine_for_bundle.sh）",
            lw_engine_client::binary_file_name()
        );
        log::error!("{msg}");
        let _ = app.emit(
            GO_ENGINE_PROCESS_EVENT,
            serde_json::json!({ "state": "failed", "last_error": msg }),
        );
        return;
    };

    let data_dir = paths::get_data_dir().clone();
    let mut spawn = SpawnOptions::new(&binary);
    // Native DLLs / VAD assets are packaged *next to* lw-engine
    // (`<resource_dir>/resources/…`), not at Tauri's resource_dir itself.
    // Prefer the binary directory so withcore installs resolve to native.
    let resource_root = pick_resource_root(&binary, resource_dir.as_deref());
    spawn.backend = resolve_go_backend(&resource_root);
    spawn.env = native_env_for_resources(&resource_root);
    spawn.stderr_log = Some(data_dir.join("go_engine_stderr.log"));
    spawn.data_dir = Some(data_dir);
    if spawn.backend == "mock" {
        let missing = missing_native_libs(&resource_root);
        if missing.is_empty() {
            log::info!(
                "Go 引擎后端已启用: {} (--backend mock; resource_root={})",
                binary.display(),
                resource_root.display()
            );
        } else {
            log::warn!(
                "Go 引擎后端已启用: {} (--backend mock; resource_root={}; missing native: {})",
                binary.display(),
                resource_root.display(),
                missing.join(", ")
            );
        }
    } else {
        log::info!(
            "Go 引擎后端已启用: {} (--backend {}; resource_root={})",
            binary.display(),
            spawn.backend,
            resource_root.display()
        );
    }

    let handle = app.clone();
    let callback = move |event: SupervisorEvent| match event {
        SupervisorEvent::Status(status) => {
            if status.state == SupervisorState::Failed {
                log::error!("Go 引擎不可用: {:?}", status.last_error);
            }
            let _ = handle.emit(GO_ENGINE_PROCESS_EVENT, &status);
        }
        SupervisorEvent::Engine(ev) => match ev.kind.as_str() {
            "engine_status" => {
                let _ = handle.emit(GO_ENGINE_STATUS_EVENT, &ev);
                // Keep AppState.funasr_ready in sync so recording start checks pass.
                sync_ready_from_status(&handle, &ev.data);
            }
            "config_changed" => {
                let _ = handle.emit(GO_ENGINE_CONFIG_EVENT, &ev);
            }
            "job_progress" => {
                let _ = handle.emit(GO_ENGINE_JOB_EVENT, &ev);
            }
            "download_progress" => {
                let _ = handle.emit("go-engine-download-progress", &ev);
            }
            other => log::debug!("Go 引擎事件 {other} 未转发"),
        },
    };
    let opts = SupervisorOptions {
        spawn,
        policy: Default::default(),
    };
    // Supervisor::start 需要 Tokio 上下文；tauri 的 async runtime 就是 Tokio。
    let sup = tauri::async_runtime::block_on(async move { Supervisor::start(opts, callback) });
    if SUPERVISOR.set(sup).is_err() {
        log::warn!("Go 引擎 supervisor 已存在，忽略重复启动");
    }
}

/// 退出时调用：结束 lw-engine 子进程（stdin 关闭也会让它自行退出）。
pub fn stop_on_exit() {
    if let Some(sup) = SUPERVISOR.get() {
        log::info!("正在停止 Go 引擎...");
        tauri::async_runtime::block_on(sup.shutdown(Duration::from_secs(4)));
    }
}

fn map_err(e: ClientError) -> AppError {
    match e {
        ClientError::Conflict => AppError::Other("引擎设置被并发修改，请重试".to_string()),
        ClientError::Invalid { message, fields } if !fields.is_empty() => {
            let detail: Vec<String> = fields.iter().map(|(k, v)| format!("{k}: {v}")).collect();
            AppError::Other(format!("引擎设置无效: {message}（{}）", detail.join("；")))
        }
        other => AppError::Other(format!("Go 引擎: {other}")),
    }
}

enum Availability {
    Ready(lw_engine_client::EngineClient),
    /// 未启动或已放弃重启：此时没有进程持有 engine.json，可以直接读写文件。
    Down,
}

async fn availability() -> Result<Availability, AppError> {
    let Some(sup) = SUPERVISOR.get() else {
        return Ok(Availability::Down);
    };
    if let Some(client) = sup.wait_ready(READY_TIMEOUT).await {
        return Ok(Availability::Ready(client));
    }
    match sup.status().state {
        SupervisorState::Failed | SupervisorState::Stopped => Ok(Availability::Down),
        _ => Err(AppError::Other(
            "Go 引擎正在启动或重启，请稍后重试".to_string(),
        )),
    }
}

/// `GET /v1/config`；引擎不可用时返回 `None`（调用方回退读 engine.json）。
pub async fn read_config() -> Option<ConfigSnapshot> {
    match availability().await {
        Ok(Availability::Ready(client)) => match client.get_config().await {
            Ok(snap) => Some(snap),
            Err(e) => {
                log::warn!("读取 Go 引擎设置失败，回退 engine.json: {e}");
                None
            }
        },
        _ => None,
    }
}

/// 读取一个引擎字段；`None` 表示应回退到 engine.json。
pub async fn read_value(key: &str) -> Option<Value> {
    read_config()
        .await
        .and_then(|snap| snap.values.get(key).cloned())
}

/// 通过 Go API 写入引擎字段（JSON merge patch，`null` = 恢复默认）。
///
/// - 412 冲突：重读 revision 后重试，最多 3 次；
/// - 需要 reload 的字段：随后调用 `/v1/engine/reload`；实时会话中返回 409 时
///   保留 pending_reload，由引擎状态继续报告；
/// - 引擎未运行（未启动/已放弃）：返回 `Ok(None)`，调用方回退写 engine.json。
pub async fn write_values(patch: Map<String, Value>) -> Result<Option<PatchResult>, AppError> {
    let client = match availability().await? {
        Availability::Ready(client) => client,
        Availability::Down => {
            log::warn!("Go 引擎未运行，设置直接写入 engine.json");
            return Ok(None);
        }
    };
    let result = client
        .update_config(&patch, CONFLICT_RETRIES)
        .await
        .map_err(map_err)?;
    if !result.pending_reload.is_empty() {
        match client.reload().await {
            Ok(_) => {}
            Err(e) if e.is_busy() => {
                log::info!(
                    "Go 引擎会话进行中，{:?} 将在会话结束后 reload",
                    result.pending_reload
                )
            }
            Err(e) => log::warn!("Go 引擎 reload 失败: {e}"),
        }
    }
    Ok(Some(result))
}

/// `get_go_engine_status` 命令的载荷。
pub async fn snapshot() -> Value {
    let Some(sup) = SUPERVISOR.get() else {
        return serde_json::json!({ "backend": backend(), "process": Value::Null });
    };
    let (engine_status, config) = match sup.client() {
        Some(client) => (client.status().await.ok(), client.get_config().await.ok()),
        None => (None, None),
    };
    serde_json::json!({
        "backend": backend(),
        "process": sup.status(),
        "engine_status": engine_status,
        "config": config,
    })
}

fn sync_ready_from_status(app: &tauri::AppHandle, data: &Value) {
    let state = app.state::<crate::state::AppState>();
    let ready = data
        .get("model_loaded")
        .and_then(|v| v.as_bool())
        .unwrap_or(false)
        && data
            .get("local_engine")
            .and_then(|v| v.as_bool())
            .unwrap_or(true);
    let was = state.is_funasr_ready();
    if ready != was {
        state.set_funasr_ready(ready);
        let status = if ready { "ready" } else { "loading" };
        let message = if ready {
            "Go 引擎就绪"
        } else {
            "Go 引擎加载中…"
        };
        let _ = app.emit(
            "funasr-status",
            serde_json::json!({
                "status": status,
                "message": message,
                "device": data.get("device").cloned().unwrap_or(Value::Null),
                "gpu_name": data.get("gpu_name").cloned().unwrap_or(Value::Null),
                "models_present": true,
                "missing_models": data.get("missing_models").cloned().unwrap_or(json!([])),
                "engine": data.get("configured_engine").cloned()
                    .or_else(|| data.get("engine").cloned())
                    .unwrap_or(Value::Null),
            }),
        );
    }
}

/// Wait until the supervisor has a client (engine handshake done), up to `READY_TIMEOUT`.
pub async fn client_ready() -> Option<lw_engine_client::EngineClient> {
    let sup = SUPERVISOR.get()?;
    sup.wait_ready(READY_TIMEOUT).await
}

/// Map Go engine API errors onto the existing UI error paths (`AppError::Asr` /
/// `AppError::Other`). 409 busy / no_local_engine and invalid_priority stay
/// recognizable via the message text the overlay already shows.
pub fn map_recognition_error(e: ClientError) -> AppError {
    match &e {
        ClientError::Api { code, message, .. } if code == "realtime_session_active" => {
            AppError::Asr(format!("实时会话进行中，请稍后再试（{message}）"))
        }
        ClientError::Api { code, message, .. } if code == "no_local_engine" => {
            AppError::Asr(format!("当前为云端引擎，本地识别不可用（{message}）"))
        }
        ClientError::Api { code, message, .. } if code == "invalid_priority" => {
            AppError::Asr(format!("无效的优先级（{message}）"))
        }
        ClientError::Api { code, message, .. }
            if code == "pre_start_buffer_overflow" || code == "start_timeout" =>
        {
            AppError::Asr(format!("实时会话启动失败：{message}"))
        }
        ClientError::Unauthorized => AppError::Asr("Go 引擎拒绝了访问令牌".into()),
        ClientError::Transport(msg) => AppError::Asr(format!("Go 引擎不可用：{msg}")),
        other => AppError::Asr(format!("Go 引擎识别错误：{other}")),
    }
}

/// Mark the shell "ASR ready" from the current Go engine status (startup path).
pub async fn bootstrap_ready(app: &tauri::AppHandle) {
    // First wait (READY_TIMEOUT). If still starting, soft-load + one more wait
    // so a slow native DLL load is not reported as a hard failure.
    if let Some(client) = client_ready().await {
        match client.status().await {
            Ok(data) => sync_ready_from_status(app, &data),
            Err(e) => {
                log::warn!("读取 Go 引擎状态失败: {e}");
                let state = app.state::<crate::state::AppState>();
                state.set_funasr_ready(false);
            }
        }
        return;
    }

    let still_starting = SUPERVISOR.get().is_some_and(|sup| {
        matches!(
            sup.status().state,
            SupervisorState::Starting | SupervisorState::Restarting
        )
    });

    let state = app.state::<crate::state::AppState>();
    state.set_funasr_ready(false);

    if still_starting {
        let _ = app.emit(
            "funasr-status",
            serde_json::json!({
                "status": "loading",
                "message": "Go 引擎仍在启动，请稍候…",
            }),
        );
        let handle = app.clone();
        tauri::async_runtime::spawn(async move {
            if let Some(client) = client_ready().await {
                match client.status().await {
                    Ok(data) => sync_ready_from_status(&handle, &data),
                    Err(e) => log::warn!("读取 Go 引擎状态失败: {e}"),
                }
                return;
            }
            let state = handle.state::<crate::state::AppState>();
            state.set_funasr_ready(false);
            let detail = SUPERVISOR
                .get()
                .and_then(|s| s.status().last_error)
                .unwrap_or_else(|| "handshake timed out".into());
            let _ = handle.emit(
                "funasr-status",
                serde_json::json!({
                    "status": "error",
                    "message": format!(
                        "Go 引擎启动超时（可稍后在连接页刷新）。若持续失败请查看 go_engine_stderr.log。详情：{detail}"
                    ),
                }),
            );
        });
        return;
    }

    let detail = SUPERVISOR
        .get()
        .and_then(|s| s.status().last_error)
        .unwrap_or_else(|| "not ready".into());
    let _ = app.emit(
        "funasr-status",
        serde_json::json!({
            "status": "error",
            "message": format!(
                "Go 引擎启动超时（可稍后在连接页刷新）。若持续失败请查看 go_engine_stderr.log。详情：{detail}"
            ),
        }),
    );
}


/// Handshake / `--backend` value actually used by the running lw-engine (`mock` | `native`).
/// Falls back to the resolved preference when the supervisor is not up yet.
pub fn inference_backend() -> String {
    if let Some(sup) = SUPERVISOR.get() {
        if let Some(b) = sup.status().backend {
            if !b.is_empty() {
                return b;
            }
        }
    }
    if let Ok(v) = std::env::var("LW_ENGINE_GO_BACKEND") {
        let t = v.trim();
        if !t.is_empty() {
            return t.to_string();
        }
    }
    if native_libs_available() {
        "native".into()
    } else {
        "mock".into()
    }
}

/// True when at least one resource root has the full CPU native ASR stack.
pub fn native_libs_available() -> bool {
    resource_roots().iter().any(|root| native_libs_present(root))
}

/// Prefer `native` when CPU R2T2 + transcribe + onnxruntime (+ VAD) are present;
/// otherwise `mock`. `LW_ENGINE_GO_BACKEND` always wins when set.
fn resolve_go_backend(resource_root: &std::path::Path) -> String {
    if let Ok(v) = std::env::var("LW_ENGINE_GO_BACKEND") {
        let t = v.trim();
        if !t.is_empty() {
            return t.to_string();
        }
    }
    if native_libs_present(resource_root) {
        "native".into()
    } else {
        "mock".into()
    }
}

/// Whether CUDA R2T2 audiocpp.dll is staged (runtime still on-demand).
pub fn cuda_r2t2_present() -> bool {
    resource_roots().iter().any(|root| {
        root.join("r2t2-native")
            .join("cuda")
            .join("audiocpp.dll")
            .is_file()
    })
}

/// Whether CUDA Qwen3 transcribe is staged (VARIANT=cuda and/or ggml-cuda.dll).
pub fn cuda_transcribe_present() -> bool {
    for root in resource_roots() {
        let dir = root.join("transcribe-native");
        let marker = dir.join("VARIANT.txt");
        if let Ok(text) = std::fs::read_to_string(&marker) {
            if text.trim().eq_ignore_ascii_case("cuda") {
                return true;
            }
        }
        if dir.join("ggml-cuda.dll").is_file() {
            return true;
        }
    }
    false
}

/// Candidates for the withcore layout: next to lw-engine, then
/// `<tauri resource_dir>/resources`, then `resource_dir` itself.
fn candidate_resource_roots(
    binary_dir: Option<&std::path::Path>,
    resource_dir: Option<&std::path::Path>,
) -> Vec<PathBuf> {
    let mut roots = Vec::new();
    let mut push = |p: PathBuf| {
        if !roots.iter().any(|r| r == &p) {
            roots.push(p);
        }
    };
    if let Some(d) = binary_dir {
        push(d.to_path_buf());
    }
    if let Some(d) = resource_dir {
        push(d.join("resources"));
        push(d.to_path_buf());
    }
    roots
}

/// Pick the directory that holds lw-engine + native DLLs.
///
/// Packaged withcore layout (Tauri destination `resources/…`):
/// `<resource_dir>/resources/{lw-engine.exe, onnxruntime.dll, r2t2-native/, …}`.
/// Using bare `resource_dir` made `native_libs_present` fail and forced `--backend mock`.
fn pick_resource_root(binary: &std::path::Path, resource_dir: Option<&std::path::Path>) -> PathBuf {
    let candidates = candidate_resource_roots(binary.parent(), resource_dir);
    for c in &candidates {
        if native_libs_present(c) {
            return c.clone();
        }
    }
    binary
        .parent()
        .map(|p| p.to_path_buf())
        .or_else(|| resource_dir.map(|d| d.join("resources")))
        .or_else(|| resource_dir.map(|d| d.to_path_buf()))
        .unwrap_or_else(|| PathBuf::from("."))
}

fn resource_roots() -> Vec<std::path::PathBuf> {
    let mut roots = Vec::new();
    let mut push = |p: PathBuf| {
        if !roots.iter().any(|r| r == &p) {
            roots.push(p);
        }
    };
    if let Ok(p) = std::env::var("LW_RESOURCES_DIR") {
        push(PathBuf::from(p));
    }
    if let Ok(exe) = std::env::current_exe() {
        if let Some(parent) = exe.parent() {
            // App exe dir and nested resources/ (NSIS withcore layout).
            push(parent.join("resources"));
            push(parent.to_path_buf());
        }
    }
    // Dev tree / staged resources next to the crate.
    let manifest_res = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("resources");
    if manifest_res.is_dir() {
        push(manifest_res);
    }
    let cwd_res = PathBuf::from("src-tauri/resources");
    if cwd_res.is_dir() {
        push(cwd_res);
    }
    roots
}

fn native_libs_present(root: &std::path::Path) -> bool {
    missing_native_libs(root).is_empty()
}

/// Relative paths required for CPU withcore native inference (Windows names).
fn missing_native_libs(root: &std::path::Path) -> Vec<&'static str> {
    let mut missing = Vec::new();
    let onnx = root.join("onnxruntime.dll").is_file()
        || root.join("onnxruntime").join("onnxruntime.dll").is_file();
    if !onnx {
        missing.push("onnxruntime.dll");
    }
    if !root.join("fireredvad_vad.onnx").is_file() {
        missing.push("fireredvad_vad.onnx");
    }
    if !root.join("fireredvad_cmvn.json").is_file() {
        missing.push("fireredvad_cmvn.json");
    }
    if !root
        .join("r2t2-native")
        .join("cpu")
        .join("audiocpp.dll")
        .is_file()
    {
        missing.push("r2t2-native/cpu/audiocpp.dll");
    }
    if !root
        .join("transcribe-native")
        .join("transcribe.dll")
        .is_file()
    {
        missing.push("transcribe-native/transcribe.dll");
    }
    missing
}

fn native_env_for_resources(root: &std::path::Path) -> std::collections::HashMap<String, String> {
    let mut env = std::collections::HashMap::new();
    env.insert(
        "LW_RESOURCES_DIR".into(),
        root.to_string_lossy().into_owned(),
    );
    let onnx = root.join("onnxruntime.dll");
    let onnx_nested = root.join("onnxruntime").join("onnxruntime.dll");
    if onnx.is_file() {
        env.insert(
            "LW_ONNXRUNTIME_LIB".into(),
            onnx.to_string_lossy().into_owned(),
        );
    } else if onnx_nested.is_file() {
        env.insert(
            "LW_ONNXRUNTIME_LIB".into(),
            onnx_nested.to_string_lossy().into_owned(),
        );
    }
    let r2t2 = root.join("r2t2-native");
    if r2t2.is_dir() {
        env.insert(
            "LW_R2T2_RUNTIME_DIR".into(),
            r2t2.to_string_lossy().into_owned(),
        );
    }
    let transcribe = root.join("transcribe-native").join("transcribe.dll");
    if transcribe.is_file() {
        env.insert(
            "LW_TRANSCRIBE_LIBRARY".into(),
            transcribe.to_string_lossy().into_owned(),
        );
    }
    env
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;

    #[test]
    fn backend_defaults_to_go() {
        assert_eq!(parse_backend(None), EngineBackend::Go);
        assert_eq!(parse_backend(Some("")), EngineBackend::Go);
        assert_eq!(parse_backend(Some("go")), EngineBackend::Go);
        assert_eq!(parse_backend(Some("rust")), EngineBackend::Go);
        assert_eq!(parse_backend(Some(" Go ")), EngineBackend::Go);
        assert_eq!(parse_backend(Some("python")), EngineBackend::Python);
    }

    fn write_withcore_layout(root: &std::path::Path) {
        fs::create_dir_all(root.join("r2t2-native/cpu")).unwrap();
        fs::create_dir_all(root.join("transcribe-native")).unwrap();
        fs::create_dir_all(root.join("onnxruntime")).unwrap();
        for (rel, body) in [
            ("onnxruntime.dll", b"x" as &[u8]),
            ("fireredvad_vad.onnx", b"x"),
            ("fireredvad_cmvn.json", b"{}"),
            ("r2t2-native/cpu/audiocpp.dll", b"x"),
            ("transcribe-native/transcribe.dll", b"x"),
        ] {
            fs::write(root.join(rel), body).unwrap();
        }
    }

    #[test]
    fn pick_resource_root_prefers_nested_resources_next_to_engine() {
        let base = std::env::temp_dir().join(format!(
            "lw-go-engine-layout-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let _ = fs::remove_dir_all(&base);
        let nested = base.join("resources");
        write_withcore_layout(&nested);
        let binary = nested.join("lw-engine.exe");
        fs::write(&binary, b"x").unwrap();

        // Tauri resource_dir is the *parent* of the nested resources/ folder.
        let picked = pick_resource_root(&binary, Some(&base));
        assert_eq!(picked, nested);
        assert!(native_libs_present(&picked));
        // Bare resource_dir (bug before this fix) must not look complete.
        assert!(!native_libs_present(&base));
        assert_eq!(
            candidate_resource_roots(binary.parent(), Some(&base)),
            vec![nested.clone(), base.clone()]
        );

        let env = native_env_for_resources(&picked);
        assert_eq!(env.get("LW_RESOURCES_DIR").map(String::as_str), Some(nested.to_str().unwrap()));
        assert!(env.get("LW_ONNXRUNTIME_LIB").unwrap().ends_with("onnxruntime.dll"));
        assert!(env.get("LW_R2T2_RUNTIME_DIR").unwrap().ends_with("r2t2-native"));
        assert!(env
            .get("LW_TRANSCRIBE_LIBRARY")
            .unwrap()
            .ends_with("transcribe.dll"));

        let _ = fs::remove_dir_all(&base);
    }

    #[test]
    fn missing_native_libs_lists_gaps() {
        let base = std::env::temp_dir().join(format!(
            "lw-go-engine-missing-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let _ = fs::remove_dir_all(&base);
        fs::create_dir_all(&base).unwrap();
        let missing = missing_native_libs(&base);
        assert!(missing.contains(&"onnxruntime.dll"));
        assert!(missing.contains(&"r2t2-native/cpu/audiocpp.dll"));
        write_withcore_layout(&base);
        assert!(missing_native_libs(&base).is_empty());
        let _ = fs::remove_dir_all(&base);
    }
}
