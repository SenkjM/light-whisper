// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Management-backend connection surface for the 命令方块 shell (PLAN §11).

use serde::Serialize;
use serde_json::Value;

use crate::services::go_engine;
use crate::services::nvidia_detect;
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
    /// True when CUDA Qwen3 transcribe is staged (VARIANT=cuda and/or ggml-cuda.dll).
    pub cuda_transcribe_present: bool,
    /// lw-engine `--backend` actually in use: `mock` (placeholder ASR) or `native`.
    pub inference_backend: String,
    /// Whether the CPU native ASR libraries are staged next to the engine.
    pub native_libs_present: bool,
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
            inference_backend: go_engine::inference_backend(),
            native_libs_present: go_engine::native_libs_available(),
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
        inference_backend: go_engine::inference_backend(),
        native_libs_present: go_engine::native_libs_available(),
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


/// Go engine emits snake_case; the cube UI expects camelCase IPC.
fn cuda_status_to_camel(raw: Value) -> Value {
    let Some(obj) = raw.as_object() else {
        return raw;
    };
    let mut out = serde_json::Map::new();
    let map = |k: &str| -> String {
        match k {
            "project_cuda_version" => "projectCudaVersion".into(),
            "required_major" => "requiredMajor".into(),
            "nvidia_detected" => "nvidiaDetected".into(),
            "driver_version" => "driverVersion".into(),
            "driver_ok" => "driverOk".into(),
            "driver_message" => "driverMessage".into(),
            "system_available" => "systemAvailable".into(),
            "system_version" => "systemVersion".into(),
            "system_path" => "systemPath".into(),
            "system_matches_project" => "systemMatchesProject".into(),
            "system_major_ok" => "systemMajorOk".into(),
            "version_mismatch" => "versionMismatch".into(),
            "version_warning" => "versionWarning".into(),
            "downloaded_available" => "downloadedAvailable".into(),
            "downloaded_version" => "downloadedVersion".into(),
            "downloaded_path" => "downloadedPath".into(),
            "downloaded_verified" => "downloadedVerified".into(),
            "active_source" => "activeSource".into(),
            "active_path" => "activePath".into(),
            "active_version" => "activeVersion".into(),
            "device_preference" => "devicePreference".into(),
            "using_cuda_backend" => "usingCudaBackend".into(),
            "cuda_r2t2_dll_present" => "cudaR2t2DllPresent".into(),
            "bytes_done" => "bytesDone".into(),
            "bytes_total" => "bytesTotal".into(),
            other => other.to_string(),
        }
    };
    for (k, v) in obj {
        let key = map(k);
        let val = if k == "download" {
            match v {
                Value::Object(d) => {
                    let mut dd = serde_json::Map::new();
                    for (dk, dv) in d {
                        dd.insert(map(dk), dv.clone());
                    }
                    Value::Object(dd)
                }
                other => other.clone(),
            }
        } else {
            v.clone()
        };
        out.insert(key, val);
    }
    Value::Object(out)
}

fn shell_cuda_status_fallback() -> Value {
    let nvidia = nvidia_detect::nvidia_present();
    let driver = nvidia_detect::driver_version();
    let dll = go_engine::cuda_r2t2_present();
    let (driver_ok, driver_message) = if !nvidia {
        (
            false,
            "no NVIDIA GPU/driver detected (checked display adapter / nvapi / device node; CUDA Toolkit not required)",
        )
    } else if driver.is_empty() {
        (
            true,
            "NVIDIA GPU/driver detected; install nvidia-smi (driver extras) for an exact version check",
        )
    } else {
        (true, "")
    };
    serde_json::json!({
        "projectCudaVersion": "12.9",
        "requiredMajor": 12,
        "nvidiaDetected": nvidia,
        "driverVersion": driver,
        "driverOk": driver_ok,
        "driverMessage": driver_message,
        "systemAvailable": false,
        "systemMatchesProject": false,
        "systemMajorOk": false,
        "versionMismatch": false,
        "downloadedAvailable": false,
        "downloadedVerified": false,
        "activeSource": "none",
        "devicePreference": "",
        "usingCudaBackend": false,
        "cudaR2t2DllPresent": dll,
    })
}

/// Ensure shell-local NVIDIA detection wins over a false negative from the
/// engine (or missing field). Missing CUDA libs stay a separate warning.
fn overlay_shell_nvidia(mut status: Value) -> Value {
    let shell_nvidia = nvidia_detect::nvidia_present();
    if !shell_nvidia {
        return status;
    }
    let Some(obj) = status.as_object_mut() else {
        return status;
    };
    let engine_says_no = obj
        .get("nvidiaDetected")
        .and_then(|v| v.as_bool())
        .unwrap_or(false)
        == false;
    if engine_says_no {
        obj.insert("nvidiaDetected".into(), Value::Bool(true));
        if obj
            .get("driverVersion")
            .and_then(|v| v.as_str())
            .unwrap_or("")
            .is_empty()
        {
            let ver = nvidia_detect::driver_version();
            if !ver.is_empty() {
                obj.insert("driverVersion".into(), Value::String(ver));
            }
        }
        // Do not hard-block GPU UI when shell sees a GPU; libs are separate.
        if obj.get("driverOk").and_then(|v| v.as_bool()) != Some(true) {
            obj.insert("driverOk".into(), Value::Bool(true));
        }
        let msg = obj
            .get("driverMessage")
            .and_then(|v| v.as_str())
            .unwrap_or("")
            .to_string();
        if msg.is_empty()
            || msg.contains("not detected")
            || msg.contains("no NVIDIA")
        {
            obj.insert(
                "driverMessage".into(),
                Value::String(
                    "NVIDIA GPU/driver detected by shell (CUDA Toolkit not required)".into(),
                ),
            );
        }
    }
    // Keep dll presence honest from local resources when engine omitted it.
    if !obj.contains_key("cudaR2t2DllPresent") {
        obj.insert(
            "cudaR2t2DllPresent".into(),
            Value::Bool(go_engine::cuda_r2t2_present()),
        );
    }
    status
}

#[tauri::command]
pub async fn get_cuda_runtime_status() -> Result<Value, AppError> {
    match require_client().await {
        Ok(c) => {
            let raw = c.cuda_runtime_status().await.map_err(map_client)?;
            Ok(overlay_shell_nvidia(cuda_status_to_camel(raw)))
        }
        Err(_) => {
            // without / not-yet-ready: still expose shell GPU detect so Workbench
            // can show download/enable UI and warn about missing native libs.
            Ok(overlay_shell_nvidia(shell_cuda_status_fallback()))
        }
    }
}

#[tauri::command]
pub async fn start_cuda_runtime_download() -> Result<Value, AppError> {
    let c = require_client().await?;
    let raw = c.cuda_runtime_download().await.map_err(map_client)?;
    Ok(overlay_shell_nvidia(cuda_status_to_camel(raw)))
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
    let raw = c
        .cuda_runtime_enable(
            prefer_system,
            acknowledge_version_mismatch,
            reload.unwrap_or(true),
        )
        .await
        .map_err(map_client)?;
    Ok(overlay_shell_nvidia(cuda_status_to_camel(raw)))
}

#[tauri::command]
pub async fn disable_cuda_runtime() -> Result<Value, AppError> {
    let c = require_client().await?;
    let raw = c.cuda_runtime_disable().await.map_err(map_client)?;
    Ok(overlay_shell_nvidia(cuda_status_to_camel(raw)))
}
