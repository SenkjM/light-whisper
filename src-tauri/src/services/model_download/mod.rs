//! Native HuggingFace model download (no Python). Used when LW_ENGINE_BACKEND=go.

use crate::state::AppState;
use crate::utils::{paths, AppError};
use sha2::{Digest, Sha256};
use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use tauri::Emitter;
use tokio::io::AsyncWriteExt;
use tokio::sync::oneshot;

static NEXT_TASK_ID: AtomicU64 = AtomicU64::new(1);

#[derive(Clone, Copy)]
struct ModelPin {
    id: &'static str,
    repo_id: &'static str,
    filename: &'static str,
    revision: &'static str,
    size: u64,
    sha256: &'static str,
}

const MODELS: &[ModelPin] = &[
    ModelPin {
        id: "qwen3-asr-0.6b",
        repo_id: "handy-computer/Qwen3-ASR-0.6B-gguf",
        filename: "Qwen3-ASR-0.6B-Q8_0.gguf",
        revision: "e4e16599b900eb0cb36e524514756bb92eb092b7",
        size: 850_423_456,
        sha256: "f081b2d5e23bd669d92cc331d722a8a0681943b8e6f34b48996fd5c319b5acd8",
    },
    ModelPin {
        id: "confucius4-r2t2",
        repo_id: "davidxifeng/Confucius4-R2T2-gguf",
        filename: "r2t2-q8_0.gguf",
        revision: "a8e6b385d7df7eae9519363e07034a209004797a",
        size: 2_477_512_064,
        sha256: "19f5ccd624484bcb5d44301437de41560b0ecc40c430e8850dfeefefbe82ccf5",
    },
];

fn hf_endpoints() -> Vec<String> {
    let primary = std::env::var("HF_ENDPOINT")
        .unwrap_or_else(|_| "https://huggingface.co".into())
        .trim_end_matches('/')
        .to_string();
    let mut out = vec![primary.clone()];
    if std::env::var("HF_ENDPOINT").is_err() {
        let mirror = std::env::var("HF_FALLBACK_ENDPOINT")
            .unwrap_or_else(|_| "https://hf-mirror.com".into())
            .trim_end_matches('/')
            .to_string();
        if mirror != primary {
            out.push(mirror);
        }
    }
    out
}

fn cache_root() -> PathBuf {
    if let Ok(v) = std::env::var("HF_HUB_CACHE") {
        return PathBuf::from(v);
    }
    if let Ok(v) = std::env::var("HF_HOME") {
        return PathBuf::from(v).join("hub");
    }
    paths::get_effective_models_dir()
}

fn repo_dir(root: &Path, repo_id: &str) -> PathBuf {
    root.join(format!("models--{}", repo_id.replace('/', "--")))
}

fn snapshot_file(root: &Path, pin: &ModelPin) -> PathBuf {
    repo_dir(root, pin.repo_id)
        .join("snapshots")
        .join(pin.revision)
        .join(pin.filename)
}

fn emit(app: &tauri::AppHandle, stage: &str, model: &str, progress: f64, overall: f64, message: Option<&str>, error: Option<&str>) {
    let mut payload = serde_json::json!({
        "stage": stage,
        "model": model,
        "progress": progress,
        "overall_progress": overall,
    });
    if let Some(m) = message {
        payload["message"] = serde_json::Value::String(m.into());
    }
    if let Some(e) = error {
        payload["error"] = serde_json::Value::String(e.into());
    }
    let _ = app.emit("model-download-status", payload);
}

fn model_ready(root: &Path, pin: &ModelPin) -> bool {
    let path = snapshot_file(root, pin);
    let Ok(meta) = std::fs::metadata(&path) else {
        return false;
    };
    meta.len() == pin.size
}

async fn download_one(
    client: &reqwest::Client,
    app: &tauri::AppHandle,
    pin: &ModelPin,
    root: &Path,
    model_index: usize,
    model_total: usize,
    cancel: &mut tokio::sync::oneshot::Receiver<()>,
) -> Result<(), AppError> {
    if model_ready(root, pin) {
        let overall = ((model_index as f64) + 1.0) / model_total as f64 * 100.0;
        emit(app, "completed", pin.id, 100.0, overall, Some("already present"), None);
        return Ok(());
    }

    let dest = snapshot_file(root, pin);
    if let Some(parent) = dest.parent() {
        tokio::fs::create_dir_all(parent)
            .await
            .map_err(|e| AppError::Download(e.to_string()))?;
    }
    let partial = dest.with_extension("gguf.partial");

    let mut errors: Vec<String> = Vec::new();
    for endpoint in hf_endpoints() {
        let url = format!(
            "{}/{}/resolve/{}/{}",
            endpoint, pin.repo_id, pin.revision, pin.filename
        );
        emit(
            app,
            "downloading",
            pin.id,
            0.0,
            model_index as f64 / model_total as f64 * 100.0,
            Some(&format!("GET {endpoint}")),
            None,
        );
        match download_file(client, app, pin, &url, &partial, &dest, model_index, model_total, cancel).await {
            Ok(()) => {
                write_manifest(root, pin)?;
                let overall = ((model_index as f64) + 1.0) / model_total as f64 * 100.0;
                emit(app, "completed", pin.id, 100.0, overall, None, None);
                return Ok(());
            }
            Err(e) => {
                let msg = e.to_string();
                if msg.contains("cancelled") {
                    return Err(e);
                }
                errors.push(format!("{endpoint}: {msg}"));
            }
        }
    }
    Err(AppError::Download(format!(
        "{} failed on all HF endpoints: {}",
        pin.id,
        errors.join(" | ")
    )))
}

async fn download_file(
    client: &reqwest::Client,
    app: &tauri::AppHandle,
    pin: &ModelPin,
    url: &str,
    partial: &Path,
    dest: &Path,
    model_index: usize,
    model_total: usize,
    cancel: &mut tokio::sync::oneshot::Receiver<()>,
) -> Result<(), AppError> {
    let mut start = 0u64;
    let mut hasher = Sha256::new();
    if let Ok(meta) = tokio::fs::metadata(partial).await {
        start = meta.len();
        if start > 0 {
            let existing = tokio::fs::read(partial)
                .await
                .map_err(|e| AppError::Download(e.to_string()))?;
            hasher.update(&existing);
        }
    }

    let mut req = client.get(url);
    if start > 0 {
        req = req.header(reqwest::header::RANGE, format!("bytes={start}-"));
    }
    let resp = req
        .send()
        .await
        .map_err(|e| AppError::Download(e.to_string()))?;
    let status = resp.status();
    if !(status.is_success() || status.as_u16() == 206) {
        return Err(AppError::Download(format!("HTTP {status} for {url}")));
    }
    if status.as_u16() == 200 {
        start = 0;
        hasher = Sha256::new();
        let _ = tokio::fs::remove_file(partial).await;
    }

    let total = pin.size;
    let mut file = tokio::fs::OpenOptions::new()
        .create(true)
        .write(true)
        .append(start > 0 && status.as_u16() == 206)
        .truncate(start == 0)
        .open(partial)
        .await
        .map_err(|e| AppError::Download(e.to_string()))?;

    let mut stream = resp.bytes_stream();
    use futures_util::StreamExt;
    let mut done = start;
    while let Some(chunk) = stream.next().await {
        if cancel.try_recv().is_ok() {
            return Err(AppError::Download("cancelled".into()));
        }
        let chunk = chunk.map_err(|e| AppError::Download(e.to_string()))?;
        file.write_all(&chunk)
            .await
            .map_err(|e| AppError::Download(e.to_string()))?;
        hasher.update(&chunk);
        done += chunk.len() as u64;
        let model_pct = (done as f64 / total as f64) * 100.0;
        let overall = (model_index as f64 + done as f64 / total as f64) / model_total as f64 * 100.0;
        emit(
            app,
            "downloading",
            pin.id,
            model_pct,
            overall,
            Some(&format!("{:.0} / {:.0} MB", done as f64 / 1e6, total as f64 / 1e6)),
            None,
        );
    }
    file.flush()
        .await
        .map_err(|e| AppError::Download(e.to_string()))?;
    drop(file);

    let sum = format!("{:x}", hasher.finalize());
    if !sum.eq_ignore_ascii_case(pin.sha256) {
        let _ = tokio::fs::remove_file(partial).await;
        return Err(AppError::Download(format!(
            "sha256 mismatch for {}: got {sum}",
            pin.id
        )));
    }
    if done != pin.size {
        let _ = tokio::fs::remove_file(partial).await;
        return Err(AppError::Download(format!(
            "size mismatch for {}: got {done} want {}",
            pin.id, pin.size
        )));
    }
    let _ = tokio::fs::remove_file(dest).await;
    tokio::fs::rename(partial, dest)
        .await
        .map_err(|e| AppError::Download(e.to_string()))?;
    Ok(())
}

fn write_manifest(root: &Path, pin: &ModelPin) -> Result<(), AppError> {
    let snap = repo_dir(root, pin.repo_id)
        .join("snapshots")
        .join(pin.revision);
    let refs = repo_dir(root, pin.repo_id).join("refs");
    std::fs::create_dir_all(&refs).map_err(|e| AppError::Download(e.to_string()))?;
    std::fs::write(refs.join("main"), pin.revision).map_err(|e| AppError::Download(e.to_string()))?;
    let manifest = serde_json::json!({
        "files": [{ "path": pin.filename, "size": pin.size, "sha256": pin.sha256 }]
    });
    let mut f = std::fs::File::create(snap.join(".light_whisper_complete.json"))
        .map_err(|e| AppError::Download(e.to_string()))?;
    f.write_all(manifest.to_string().as_bytes())
        .map_err(|e| AppError::Download(e.to_string()))?;
    Ok(())
}

/// Download pinned local ASR models into the HuggingFace hub cache (no Python).
pub async fn run_native_download(
    app_handle: &tauri::AppHandle,
    state: &AppState,
) -> Result<String, AppError> {
    let (cancel_tx, mut cancel_rx) = oneshot::channel();
    let task_id = NEXT_TASK_ID.fetch_add(1, Ordering::Relaxed);
    {
        let _lifecycle_guard = state.engine.funasr_lifecycle_op.lock().await;
        let mut guard = state.engine.download_task.lock().await;
        if guard.is_some() {
            return Err(AppError::Download(
                "已有下载任务正在进行，请先取消或等待完成".into(),
            ));
        }
        *guard = Some(crate::state::DownloadTask {
            id: task_id,
            cancel: Some(cancel_tx),
        });
    }

    let root = cache_root();
    tokio::fs::create_dir_all(&root)
        .await
        .map_err(|e| AppError::Download(e.to_string()))?;

    // Prefer downloading the configured engine first, then the other local pin.
    let engine = paths::read_engine_config();
    let mut order: Vec<&ModelPin> = Vec::new();
    if let Some(p) = MODELS.iter().find(|m| m.id == engine) {
        order.push(p);
    }
    for p in MODELS {
        if !order.iter().any(|x| x.id == p.id) {
            order.push(p);
        }
    }

    let client = reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(60 * 60))
        .connect_timeout(std::time::Duration::from_secs(30))
        .build()
        .map_err(|e| AppError::Download(e.to_string()))?;

    let total = order.len();
    let mut failed = None;
    for (i, pin) in order.iter().enumerate() {
        emit(
            app_handle,
            "downloading",
            pin.id,
            0.0,
            i as f64 / total as f64 * 100.0,
            Some("starting"),
            None,
        );
        match download_one(&client, app_handle, pin, &root, i, total, &mut cancel_rx).await {
            Ok(()) => {}
            Err(e) if e.to_string().contains("cancelled") => {
                clear_task(state, task_id).await;
                emit(app_handle, "error", pin.id, 0.0, 0.0, None, Some("cancelled"));
                return Err(AppError::Download("下载已取消".into()));
            }
            Err(e) => {
                failed = Some(e.to_string());
                emit(
                    app_handle,
                    "error",
                    pin.id,
                    0.0,
                    i as f64 / total as f64 * 100.0,
                    None,
                    Some(&e.to_string()),
                );
                // Continue other models when one fails (network/mirror).
            }
        }
    }

    clear_task(state, task_id).await;
    if let Some(err) = failed {
        // If at least the configured engine is ready, treat as soft success.
        if let Some(pin) = MODELS.iter().find(|m| m.id == engine) {
            if model_ready(&root, pin) {
                emit(app_handle, "completed", "all", 100.0, 100.0, Some("partial"), None);
                return Ok("模型已下载（部分镜像失败已跳过）".into());
            }
        }
        return Err(AppError::Download(err));
    }
    emit(app_handle, "completed", "all", 100.0, 100.0, None, None);
    Ok("模型下载完成".into())
}

async fn clear_task(state: &AppState, task_id: u64) {
    let mut guard = state.engine.download_task.lock().await;
    if guard.as_ref().is_some_and(|t| t.id == task_id) {
        guard.take();
    }
}
