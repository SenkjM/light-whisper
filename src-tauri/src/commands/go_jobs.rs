// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! File transcription jobs via the Go engine (`/v1/jobs`), PLAN 步骤 5.
//! Progress is also pushed as the Tauri event `go-engine-job-progress`.

use lw_engine_client::{Job, JobPathRequest};

use crate::services::go_asr;
use crate::services::go_engine;
use crate::utils::AppError;

fn require_go() -> Result<(), AppError> {
    if go_engine::is_go() {
        Ok(())
    } else {
        Err(AppError::Other(
            "文件转录 job 仅在 LW_ENGINE_BACKEND=go 时可用".into(),
        ))
    }
}

/// Submit a file job by absolute WAV path on this machine.
#[tauri::command]
pub async fn submit_go_transcription_job(
    path: String,
    language: Option<String>,
    context: Option<String>,
    hot_words: Option<Vec<String>>,
    priority: Option<String>,
    segments: Option<bool>,
) -> Result<Job, AppError> {
    require_go()?;
    go_asr::submit_job_path(JobPathRequest {
        path,
        language,
        context,
        hot_words: hot_words.unwrap_or_default(),
        priority,
        segments: segments.unwrap_or(false),
    })
    .await
}

#[tauri::command]
pub async fn get_go_transcription_job(id: String) -> Result<Job, AppError> {
    require_go()?;
    go_asr::get_job(&id).await
}

#[tauri::command]
pub async fn list_go_transcription_jobs() -> Result<Vec<Job>, AppError> {
    require_go()?;
    go_asr::list_jobs().await
}

#[tauri::command]
pub async fn cancel_go_transcription_job(id: String) -> Result<(), AppError> {
    require_go()?;
    go_asr::cancel_job(&id).await
}
