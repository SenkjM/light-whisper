// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Management-backend connection surface for the 命令方块 shell (PLAN §11).

use serde::Serialize;

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
    })
}

#[tauri::command]
pub async fn set_management_settings(
    management_url: Option<String>,
    management_engine_path: Option<String>,
    management_autofill_token: Option<bool>,
    package_flavor: Option<String>,
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
    })
    .map_err(AppError::Other)
}
