// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Go 引擎（lw-engine）接入层，PLAN 步骤 1 的 Rust 一半。
//!
//! 开关：环境变量 `LW_ENGINE_BACKEND=go|python`，默认 `python`（行为与以前完全
//! 一致，本模块什么都不做）。`go` 时：
//! - 启动时拉起 lw-engine（`--backend` 取 `LW_ENGINE_GO_BACKEND`，默认 `mock`），
//!   读握手、校验 api_version，崩溃后有界指数退避重启，退出时结束子进程；
//! - 引擎字段设置（engine / models_dir / gpu_idle_seconds）的读写走 Go 的
//!   `/v1/config`（412 冲突自动重读重试；需要 reload 的字段写入后调用
//!   `/v1/engine/reload`，会话中 409 时保持 pending_reload）；
//! - `/v1/events` 的状态与配置事件转发为 Tauri 事件。
//!
//! 推理仍走现有 Python 引擎：Go 后端在步骤 1 只是 mock，二者共享同一个
//! engine.json，所以 Python 下次启动会读到 Go 写入的设置。

use std::path::PathBuf;
use std::sync::OnceLock;
use std::time::Duration;

use lw_engine_client::{
    locate_engine_binary, ClientError, ConfigSnapshot, PatchResult, SpawnOptions, Supervisor,
    SupervisorEvent, SupervisorOptions, SupervisorState,
};
use serde_json::{Map, Value};
use tauri::{Emitter, Manager};

use crate::utils::error::AppError;
use crate::utils::paths;

/// 进程状态（启动/运行/重启/失败/停止），载荷为 `SupervisorStatus`。
pub const GO_ENGINE_PROCESS_EVENT: &str = "go-engine-process";
/// 引擎 `engine_status` 事件，载荷为 `{seq,type,time,data}`。
pub const GO_ENGINE_STATUS_EVENT: &str = "go-engine-status";
/// 引擎 `config_changed` 事件，载荷为 `{seq,type,time,data}`。
pub const GO_ENGINE_CONFIG_EVENT: &str = "go-engine-config-changed";

const READY_TIMEOUT: Duration = Duration::from_secs(10);
const CONFLICT_RETRIES: u32 = 3;

#[derive(Debug, Clone, Copy, PartialEq, Eq, serde::Serialize)]
#[serde(rename_all = "lowercase")]
pub enum EngineBackend {
    Python,
    Go,
}

fn parse_backend(value: Option<&str>) -> EngineBackend {
    match value.map(|v| v.trim().to_ascii_lowercase()) {
        Some(v) if v == "go" => EngineBackend::Go,
        Some(v) if v.is_empty() || v == "python" => EngineBackend::Python,
        Some(other) => {
            log::warn!("LW_ENGINE_BACKEND={other} 无法识别，使用 python");
            EngineBackend::Python
        }
        None => EngineBackend::Python,
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
    let override_path = std::env::var_os("LW_ENGINE_PATH").map(PathBuf::from);
    let resource_dir = app.path().resource_dir().ok();
    let Some(binary) = locate_engine_binary(
        override_path.as_deref(),
        resource_dir.as_deref(),
        dev_engine_dir().as_deref(),
    ) else {
        let msg = format!(
            "LW_ENGINE_BACKEND=go 但找不到 {}（可用 LW_ENGINE_PATH 指定，或放到 resources/ 下）",
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
    spawn.backend = std::env::var("LW_ENGINE_GO_BACKEND").unwrap_or_else(|_| "mock".to_string());
    spawn.stderr_log = Some(data_dir.join("go_engine_stderr.log"));
    spawn.data_dir = Some(data_dir);
    log::info!(
        "Go 引擎后端已启用: {} (--backend {})",
        binary.display(),
        spawn.backend
    );

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
            }
            "config_changed" => {
                let _ = handle.emit(GO_ENGINE_CONFIG_EVENT, &ev);
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn backend_defaults_to_python() {
        assert_eq!(parse_backend(None), EngineBackend::Python);
        assert_eq!(parse_backend(Some("")), EngineBackend::Python);
        assert_eq!(parse_backend(Some("python")), EngineBackend::Python);
        assert_eq!(parse_backend(Some("rust")), EngineBackend::Python);
        assert_eq!(parse_backend(Some(" Go ")), EngineBackend::Go);
    }
}
