// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Shell-owned UI preferences (PLAN 步骤 5): formerly localStorage-only keys
//! that the frontend pushed into Rust memory at startup.
//!
//! Persisted as `<data_dir>/app_preferences.json`. Loaded on shell start;
//! each `set_*` command updates memory and rewrites the file. Theme / UI
//! language / onboarding stay in frontend localStorage (PLAN §3.2).

use std::path::PathBuf;
use std::sync::RwLock;

use serde::{Deserialize, Serialize};

use crate::utils::paths;

pub const PREFERENCES_FILE: &str = "app_preferences.json";
pub const SCHEMA_VERSION: u32 = 1;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Default)]
#[serde(rename_all = "camelCase")]
pub struct AppPreferences {
    #[serde(default = "default_schema")]
    pub schema_version: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub hotkey: Option<String>,
    /// `true` = toggle mode; `false` / absent = hold-to-talk.
    #[serde(default)]
    pub recording_toggle: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub input_device: Option<String>,
    /// `"clipboard"` or `"paste"` (default paste / injection).
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub input_method: Option<String>,
    #[serde(default = "default_true")]
    pub sound_enabled: bool,
    #[serde(default)]
    pub mic_level_monitor_enabled: bool,
    #[serde(default)]
    pub ai_polish_enabled: bool,
    /// Set after a one-shot localStorage → shell migration.
    #[serde(default)]
    pub migrated_from_local_storage: bool,
}

fn default_schema() -> u32 {
    SCHEMA_VERSION
}

fn default_true() -> bool {
    true
}

impl AppPreferences {
    pub fn path() -> PathBuf {
        paths::get_data_dir().join(PREFERENCES_FILE)
    }

    pub fn load() -> Self {
        let path = Self::path();
        match std::fs::read(&path) {
            Ok(bytes) => match serde_json::from_slice::<AppPreferences>(&bytes) {
                Ok(mut prefs) => {
                    if prefs.schema_version == 0 {
                        prefs.schema_version = SCHEMA_VERSION;
                    }
                    prefs
                }
                Err(e) => {
                    log::warn!("读取 {} 失败，使用默认偏好: {e}", path.display());
                    Self::default_prefs()
                }
            },
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Self::default_prefs(),
            Err(e) => {
                log::warn!("无法打开 {}: {e}", path.display());
                Self::default_prefs()
            }
        }
    }

    fn default_prefs() -> Self {
        Self {
            schema_version: SCHEMA_VERSION,
            sound_enabled: true,
            ..Self::default()
        }
    }

    pub fn save(&self) -> Result<(), String> {
        let path = Self::path();
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent).map_err(|e| e.to_string())?;
        }
        let mut out = self.clone();
        out.schema_version = SCHEMA_VERSION;
        let json = serde_json::to_vec_pretty(&out).map_err(|e| e.to_string())?;
        let tmp = path.with_extension("json.tmp");
        std::fs::write(&tmp, &json).map_err(|e| e.to_string())?;
        std::fs::rename(&tmp, &path).map_err(|e| e.to_string())?;
        Ok(())
    }
}

/// Process-wide preferences (loaded once, mutated under the lock).
static PREFS: RwLock<Option<AppPreferences>> = RwLock::new(None);

pub fn init_from_disk() -> AppPreferences {
    let prefs = AppPreferences::load();
    *PREFS.write().expect("prefs lock") = Some(prefs.clone());
    prefs
}

pub fn snapshot() -> AppPreferences {
    PREFS
        .read()
        .expect("prefs lock")
        .clone()
        .unwrap_or_else(AppPreferences::default_prefs)
}

pub fn update<F>(f: F) -> Result<AppPreferences, String>
where
    F: FnOnce(&mut AppPreferences),
{
    let mut guard = PREFS.write().expect("prefs lock");
    let mut prefs = guard.clone().unwrap_or_else(AppPreferences::default_prefs);
    f(&mut prefs);
    prefs.save()?;
    *guard = Some(prefs.clone());
    Ok(prefs)
}

/// Merge fields from a frontend one-shot migration. Does not clear
/// `migrated_from_local_storage` if already true (idempotent).
pub fn migrate_from_local_storage(incoming: AppPreferences) -> Result<AppPreferences, String> {
    update(|prefs| {
        if prefs.migrated_from_local_storage {
            return;
        }
        if incoming.hotkey.is_some() {
            prefs.hotkey = incoming.hotkey;
        }
        prefs.recording_toggle = incoming.recording_toggle;
        if incoming.input_device.is_some() {
            prefs.input_device = incoming.input_device;
        }
        if incoming.input_method.is_some() {
            prefs.input_method = incoming.input_method;
        }
        prefs.sound_enabled = incoming.sound_enabled;
        prefs.mic_level_monitor_enabled = incoming.mic_level_monitor_enabled;
        prefs.ai_polish_enabled = incoming.ai_polish_enabled;
        prefs.migrated_from_local_storage = true;
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn round_trip_defaults() {
        let p = AppPreferences::default_prefs();
        let v = serde_json::to_value(&p).unwrap();
        assert_eq!(v["schemaVersion"], 1);
        assert_eq!(v["soundEnabled"], true);
        let back: AppPreferences = serde_json::from_value(v).unwrap();
        assert!(back.sound_enabled);
        assert!(!back.recording_toggle);
    }
}
