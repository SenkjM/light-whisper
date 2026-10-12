// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Management-backend connection surface for the 命令方块 shell (PLAN §11).

use serde::Serialize;
use serde_json::Value;

use crate::services::go_engine;
use crate::state::app_preferences::{self, AppPreferences};
use crate::utils::AppError;

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ManagementConnection {
    pub backend: String,
    pub base_url: Option<String>,
    /// Root token plaintext — only when autofill is enabled (shell window).
    pub root_token: Option<String>,
    pub autofill_token: bool,
    pub engine_path: Option<String>,
    pub package_flavor: String,
    pub ready: bool,
    /// True when resources/r2t2-native/cuda/audiocpp.dll is next to the engine
    /// (CUDA runtime still on-demand per PLAN §5).
    pub cuda_r2t2_present: bool,
    /// True when transcribe-native was staged as the CUDA variant (VARIANT.txt).
    pub cuda_transcribe_present: bool,
}

fn map_client(e: lw_engine_client::ClientError) -> AppError {
    AppError::Other(e.to_string())
}

async fn require_client() -> Result<lw_engine_client::EngineClient, AppError> {
    go_engine::client_ready()
        .await
        .ok_or_else(|| AppError::Other("管理后端尚未就绪".into()))
}

#[tauri::command]
pub async fn get_management_connection() -> Result<ManagementConnection, AppError> {
    let prefs = app_preferences::snapshot();
    let autofill = prefs.management_autofill_token;
    let package_flavor = if prefs.package_flavor.is_empty() {
        "withcore".into()
    } else {
        prefs.package_flavor.clone()
    };

    if !go_engine::is_go() {
        return Ok(ManagementConnection {
            backend: "python".into(),
            base_url: prefs.management_url.clone(),
            root_token: None,
            autofill_token: autofill,
            engine_path: prefs.management_engine_path.clone(),
            package_flavor,
            ready: false,
            cuda_r2t2_present: go_engine::cuda_r2t2_present(),
            cuda_transcribe_present: go_engine::cuda_transcribe_present(),
        });
    }

    let (base_url, root_token, ready) = match go_engine::client_ready().await {
        Some(client) => {
            let token = if autofill {
                Some(client.token().to_string())
            } else {
                None
            };
            (Some(client.base_url().to_string()), token, true)
        }
        None => (prefs.management_url.clone(), None, false),
    };

    Ok(ManagementConnection {
        backend: "go".into(),
        base_url,
        root_token,
        autofill_token: autofill,
        engine_path: prefs.management_engine_path.clone(),
        package_flavor,
        ready,
        cuda_r2t2_present: go_engine::cuda_r2t2_present(),
        cuda_transcribe_present: go_engine::cuda_transcribe_present(),
    })
}

#[tauri::command]
pub async fn set_management_settings(
    management_url: Option<String>,
    management_engine_path: Option<String>,
    management_autofill_token: Option<bool>,
    package_flavor: Option<String>,
    cloud_polish_endpoint: Option<String>,
    cloud_assistant_endpoint: Option<String>,
    cloud_decision_endpoint: Option<String>,
    cloud_asr_endpoint: Option<String>,
) -> Result<AppPreferences, AppError> {
    app_preferences::update(|p| {
        if let Some(url) = management_url {
            p.management_url = if url.trim().is_empty() {
                None
            } else {
                Some(url)
            };
        }
        if let Some(path) = management_engine_path {
            p.management_engine_path = if path.trim().is_empty() {
                None
            } else {
                Some(path)
            };
        }
        if let Some(v) = management_autofill_token {
            p.management_autofill_token = v;
        }
        if let Some(flavor) = package_flavor {
            let f = flavor.trim().to_ascii_lowercase();
            if f == "withcore" || f == "without" {
                p.package_flavor = f;
            }
        }
        if let Some(v) = cloud_polish_endpoint {
            p.cloud_polish_endpoint = empty_to_none(v);
        }
        if let Some(v) = cloud_assistant_endpoint {
            p.cloud_assistant_endpoint = empty_to_none(v);
        }
        if let Some(v) = cloud_decision_endpoint {
            p.cloud_decision_endpoint = empty_to_none(v);
        }
        if let Some(v) = cloud_asr_endpoint {
            p.cloud_asr_endpoint = empty_to_none(v);
        }
    })
    .map_err(AppError::Other)
}

fn empty_to_none(v: String) -> Option<String> {
    let t = v.trim().to_string();
    if t.is_empty() {
        None
    } else {
        Some(t)
    }
}

#[tauri::command]
pub async fn get_management_auth_me() -> Result<Value, AppError> {
    require_client().await?.auth_me().await.map_err(map_client)
}

#[tauri::command]
pub async fn get_management_stats() -> Result<Value, AppError> {
    require_client().await?.stats().await.map_err(map_client)
}

#[tauri::command]
pub async fn list_management_tokens() -> Result<Value, AppError> {
    require_client().await?.list_tokens().await.map_err(map_client)
}

#[tauri::command]
pub async fn create_management_token(role: String, label: String) -> Result<Value, AppError> {
    require_client()
        .await?
        .create_token(&role, &label)
        .await
        .map_err(map_client)
}

#[tauri::command]
pub async fn delete_management_token(id: String) -> Result<(), AppError> {
    require_client()
        .await?
        .delete_token(&id)
        .await
        .map_err(map_client)
}


#[tauri::command]
pub async fn get_cuda_runtime_status() -> Result<Value, AppError> {
    let c = require_client().await?;
    c.cuda_runtime_status().await.map_err(map_client)
}

#[tauri::command]
pub async fn start_cuda_runtime_download() -> Result<Value, AppError> {
    let c = require_client().await?;
    c.cuda_runtime_download().await.map_err(map_client)
}

#[tauri::command]
pub async fn cancel_cuda_runtime_download() -> Result<(), AppError> {
    let c = require_client().await?;
    c.cuda_runtime_cancel_download().await.map_err(map_client)
}

#[tauri::command]
pub async fn enable_cuda_runtime(
    prefer_system: bool,
    acknowledge_version_mismatch: bool,
    reload: Option<bool>,
) -> Result<Value, AppError> {
    let c = require_client().await?;
    c.cuda_runtime_enable(
        prefer_system,
        acknowledge_version_mismatch,
        reload.unwrap_or(true),
    )
    .await
    .map_err(map_client)
}

#[tauri::command]
pub async fn disable_cuda_runtime() -> Result<Value, AppError> {
    let c = require_client().await?;
    c.cuda_runtime_disable().await.map_err(map_client)
}
