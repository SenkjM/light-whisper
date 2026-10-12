// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Release packaging checks for `build.rs`.
//!
//! Default (pure Go) packages require `resources/lw-engine.exe` (Windows NSIS)
//! or `resources/lw-engine` (Unix). `LW_ENGINE_BACKEND=python` at build time
//! still requires a non-empty `resources/engine.tar.xz` for local debug /
//! legacy packaging.
//!
//! Thin-shell (`without`) packages intentionally omit the engine binary; set
//! `VERIFY_WITHOUT=1` or `LW_PACKAGE_FLAVOR=without` so release builds may
//! fingerprint `resources/lw-engine.placeholder` instead.

use std::env;
use std::fs;
use std::path::{Path, PathBuf};

pub const ENGINE_ARCHIVE_CANDIDATES: &[&str] = &["resources/engine.tar.xz", "resources/engine.zip"];

pub const GO_ENGINE_CANDIDATES: &[&str] = &["resources/lw-engine.exe", "resources/lw-engine"];

fn is_non_empty_file(path: &Path) -> bool {
    fs::metadata(path)
        .map(|metadata| metadata.is_file() && metadata.len() > 0)
        .unwrap_or(false)
}

/// True when the build explicitly opts into the Python/PyInstaller archive path.
pub fn wants_python_archive() -> bool {
    env::var("LW_ENGINE_BACKEND")
        .map(|v| v.trim().eq_ignore_ascii_case("python"))
        .unwrap_or(false)
}

/// True when packaging the thin shell that omits lw-engine from the bundle
/// (PLAN §11.5 / `command-cube-without.yml`).
pub fn wants_without_package() -> bool {
    if env::var("VERIFY_WITHOUT")
        .map(|v| v.trim() == "1")
        .unwrap_or(false)
    {
        return true;
    }
    env::var("LW_PACKAGE_FLAVOR")
        .map(|v| v.trim().eq_ignore_ascii_case("without"))
        .unwrap_or(false)
}

pub fn select_engine_archive(
    manifest_dir: &Path,
    allow_placeholder: bool,
) -> Result<PathBuf, String> {
    let candidates: Vec<PathBuf> = ENGINE_ARCHIVE_CANDIDATES
        .iter()
        .map(|relative| manifest_dir.join(relative))
        .collect();

    if allow_placeholder {
        if let Some(archive) = candidates
            .iter()
            .find(|candidate| is_non_empty_file(candidate))
        {
            return Ok(archive.clone());
        }

        let placeholder = &candidates[0];
        if !placeholder.exists() {
            fs::File::create(placeholder).map_err(|error| {
                format!(
                    "无法创建开发用引擎占位文件 {}: {error}",
                    placeholder.display()
                )
            })?;
        }
        return Ok(placeholder.clone());
    }

    let release_archive = &candidates[0];
    if is_non_empty_file(release_archive) {
        return Ok(release_archive.clone());
    }

    if release_archive.exists() {
        return Err(format!(
            "正式构建需要非空的 {}。请先运行 `uv run --locked python scripts/build_engine.py`，或复用已经验证过的归档。",
            release_archive.display()
        ));
    }

    Err(format!(
        "正式构建需要非空的 {}。请先运行 `uv run --locked python scripts/build_engine.py`，或复用已经验证过的归档。",
        release_archive.display()
    ))
}

pub fn select_go_engine_binary(
    manifest_dir: &Path,
    allow_placeholder: bool,
) -> Result<PathBuf, String> {
    let candidates: Vec<PathBuf> = GO_ENGINE_CANDIDATES
        .iter()
        .map(|relative| manifest_dir.join(relative))
        .collect();

    if let Some(bin) = candidates
        .iter()
        .find(|candidate| is_non_empty_file(candidate))
    {
        return Ok(bin.clone());
    }

    if allow_placeholder {
        // Debug builds and without (thin-shell) release packages may omit a
        // prebuilt binary; fingerprint an empty placeholder so rustc-env is
        // always set (same idea as the old Python archive placeholder).
        let placeholder = manifest_dir.join("resources/lw-engine.placeholder");
        if !placeholder.exists() {
            if let Some(parent) = placeholder.parent() {
                fs::create_dir_all(parent).map_err(|error| {
                    format!("无法创建 resources 目录 {}: {error}", parent.display())
                })?;
            }
            fs::File::create(&placeholder).map_err(|error| {
                format!(
                    "无法创建开发用 Go 引擎占位文件 {}: {error}",
                    placeholder.display()
                )
            })?;
        }
        return Ok(placeholder);
    }

    Err(
        "正式构建需要非空的 resources/lw-engine.exe（Windows）或 resources/lw-engine（Unix）。请先运行 `./scripts/build_lw_engine_for_bundle.sh windows`（或 host）。薄壳 without 包请设置 VERIFY_WITHOUT=1（或 LW_PACKAGE_FLAVOR=without）。若仍要打 Python 包，构建时设置 LW_ENGINE_BACKEND=python 并准备 engine.tar.xz。"
            .into(),
    )
}

/// File whose content is hashed into `LIGHT_WHISPER_ENGINE_ARCHIVE_FINGERPRINT`.
/// Default packaging fingerprints `lw-engine[.exe]`; Python opt-in fingerprints
/// `engine.tar.xz`.
pub fn select_packaging_fingerprint_source(
    manifest_dir: &Path,
    allow_placeholder: bool,
) -> Result<PathBuf, String> {
    if wants_python_archive() {
        select_engine_archive(manifest_dir, allow_placeholder)
    } else {
        select_go_engine_binary(manifest_dir, allow_placeholder)
    }
}
