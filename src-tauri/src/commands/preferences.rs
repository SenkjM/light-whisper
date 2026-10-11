// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Shell app preferences (PLAN 步骤 5): get / migrate from localStorage.

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
    app_preferences::update(|p| p.hotkey = hotkey.filter(|s| !s.is_empty()))
        .map_err(AppError::Other)
}
