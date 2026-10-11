// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Shell app preferences (PLAN 步骤 5–6): get / migrate / patch.
//! Source of truth for business UI prefs (hotkey, mode, mic, …). Theme /
//! language / onboarding stay in frontend localStorage.

use crate::state::app_preferences::{self, AppPreferences};
use crate::utils::AppError;

#[tauri::command]
pub async fn get_app_preferences() -> Result<AppPreferences, AppError> {
    Ok(app_preferences::snapshot())
}

/// One-shot migration: frontend sends values read from localStorage.
/// Idempotent once `migrated_from_local_storage` is set.
#[tauri::command]
pub async fn migrate_app_preferences(
    preferences: AppPreferences,
) -> Result<AppPreferences, AppError> {
    app_preferences::migrate_from_local_storage(preferences).map_err(AppError::Other)
}

#[tauri::command]
pub async fn set_app_preference_hotkey(hotkey: Option<String>) -> Result<AppPreferences, AppError> {
    app_preferences::update(|p| p.hotkey = hotkey.filter(|s| !s.is_empty())).map_err(AppError::Other)
}

/// Partial update of shell preferences (PLAN 步骤 6). Only fields present in
/// the patch object are applied; `schema_version` / `migrated_from_local_storage`
/// are ignored from the client (migration uses migrate_app_preferences).
#[tauri::command]
pub async fn patch_app_preferences(
    patch: serde_json::Value,
) -> Result<AppPreferences, AppError> {
    let obj = patch
        .as_object()
        .ok_or_else(|| AppError::Other("patch must be a JSON object".into()))?;
    app_preferences::update(|p| {
        if let Some(v) = obj.get("hotkey") {
            p.hotkey = v.as_str().filter(|s| !s.is_empty()).map(str::to_string);
        }
        if let Some(v) = obj.get("recordingToggle").and_then(|x| x.as_bool()) {
            p.recording_toggle = v;
        }
        if let Some(v) = obj.get("inputDevice") {
            p.input_device = if v.is_null() {
                None
            } else {
                v.as_str().map(str::to_string)
            };
        }
        if let Some(v) = obj.get("inputMethod") {
            p.input_method = v.as_str().map(str::to_string);
        }
        if let Some(v) = obj.get("soundEnabled").and_then(|x| x.as_bool()) {
            p.sound_enabled = v;
        }
        if let Some(v) = obj.get("micLevelMonitorEnabled").and_then(|x| x.as_bool()) {
            p.mic_level_monitor_enabled = v;
        }
        if let Some(v) = obj.get("aiPolishEnabled").and_then(|x| x.as_bool()) {
            p.ai_polish_enabled = v;
        }
        if let Some(v) = obj.get("managementUrl") {
            p.management_url = v.as_str().filter(|s| !s.is_empty()).map(str::to_string);
        }
        if let Some(v) = obj.get("managementEnginePath") {
            p.management_engine_path = v.as_str().filter(|s| !s.is_empty()).map(str::to_string);
        }
        if let Some(v) = obj.get("managementAutofillToken").and_then(|x| x.as_bool()) {
            p.management_autofill_token = v;
        }
        if let Some(v) = obj.get("packageFlavor").and_then(|x| x.as_str()) {
            let f = v.trim().to_ascii_lowercase();
            if f == "withcore" || f == "without" {
                p.package_flavor = f;
            }
        }
        if let Some(v) = obj.get("cloudPolishEndpoint") {
            p.cloud_polish_endpoint = v.as_str().filter(|s| !s.is_empty()).map(str::to_string);
        }
        if let Some(v) = obj.get("cloudAssistantEndpoint") {
            p.cloud_assistant_endpoint = v.as_str().filter(|s| !s.is_empty()).map(str::to_string);
        }
        if let Some(v) = obj.get("cloudDecisionEndpoint") {
            p.cloud_decision_endpoint = v.as_str().filter(|s| !s.is_empty()).map(str::to_string);
        }
        if let Some(v) = obj.get("cloudAsrEndpoint") {
            p.cloud_asr_endpoint = v.as_str().filter(|s| !s.is_empty()).map(str::to_string);
        }
    })
    .map_err(AppError::Other)
}
