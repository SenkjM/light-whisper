// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Go 引擎（PLAN 步骤 1）的只读状态查询。
//!
//! 事件（go-engine-process / go-engine-status / go-engine-config-changed）在前端
//! 注册监听之前发出会丢失，这个命令用于随时拉取当前快照。

use crate::services::go_engine;

/// 返回 `{backend, process, engine_status, config}`；python 模式下 `process` 为 null。
#[tauri::command]
pub async fn get_go_engine_status() -> Result<serde_json::Value, crate::utils::error::AppError> {
    Ok(go_engine::snapshot().await)
}
