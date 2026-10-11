// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Recognition through the Go engine (PLAN 步骤 5, `LW_ENGINE_BACKEND=go`).
//!
//! Live dictation uses `WS /v1/asr/stream` with the client's pre-`started`
//! audio buffer. Push-to-talk / finalize / history re-ASR use
//! `POST /v1/asr/transcribe`. File jobs use `/v1/jobs`.

use std::sync::{
    atomic::{AtomicU64, Ordering},
    Arc,
};

use lw_engine_client::{
    AsrStream, ClientError, Job, JobPathRequest, StreamMode, StreamPartial, TranscribeOptions,
};
use tauri::{Emitter, Manager};
use tokio::sync::oneshot;

use super::audio_service::{run_capture, NativeCaptureControl, NativeCaptureSink};
use super::funasr_service::{native_stream::NativeStreamUpdate, TranscriptionResult};
use super::go_engine;
use crate::state::AppState;
use crate::utils::AppError;

static NEXT_GO_SESSION: AtomicU64 = AtomicU64::new(1);

fn map_client_err(e: ClientError) -> AppError {
    go_engine::map_recognition_error(e)
}

/// Whole-clip transcription via `POST /v1/asr/transcribe`.
pub async fn transcribe_pcm16(
    samples: &[i16],
    language: Option<String>,
    context: Option<String>,
    hot_words: Option<Vec<String>>,
    priority: Option<String>,
) -> Result<TranscriptionResult, AppError> {
    let client = go_engine::client_ready()
        .await
        .ok_or_else(|| AppError::Asr("Go 引擎尚未就绪".into()))?;
    let opts = TranscribeOptions {
        language,
        context,
        hot_words: hot_words.unwrap_or_default(),
        priority,
    };
    match client.transcribe_pcm(samples, &opts).await {
        Ok(r) => Ok(TranscriptionResult {
            text: r.text,
            duration: Some(r.duration),
            success: true,
            error: None,
            language: if r.language.is_empty() {
                None
            } else {
                Some(r.language)
            },
        }),
        Err(e) => Err(map_client_err(e)),
    }
}

pub async fn transcribe_wav_bytes(
    wav: &[u8],
    language: Option<String>,
    context: Option<String>,
    hot_words: Option<Vec<String>>,
    priority: Option<String>,
) -> Result<TranscriptionResult, AppError> {
    let client = go_engine::client_ready()
        .await
        .ok_or_else(|| AppError::Asr("Go 引擎尚未就绪".into()))?;
    let opts = TranscribeOptions {
        language,
        context,
        hot_words: hot_words.unwrap_or_default(),
        priority,
    };
    match client.transcribe_wav(wav, &opts).await {
        Ok(r) => Ok(TranscriptionResult {
            text: r.text,
            duration: Some(r.duration),
            success: true,
            error: None,
            language: if r.language.is_empty() {
                None
            } else {
                Some(r.language)
            },
        }),
        Err(e) => Err(map_client_err(e)),
    }
}

pub async fn submit_job_path(req: JobPathRequest) -> Result<Job, AppError> {
    let client = go_engine::client_ready()
        .await
        .ok_or_else(|| AppError::Asr("Go 引擎尚未就绪".into()))?;
    client.submit_job_path(&req).await.map_err(map_client_err)
}

pub async fn get_job(id: &str) -> Result<Job, AppError> {
    let client = go_engine::client_ready()
        .await
        .ok_or_else(|| AppError::Asr("Go 引擎尚未就绪".into()))?;
    client.get_job(id).await.map_err(map_client_err)
}

pub async fn list_jobs() -> Result<Vec<Job>, AppError> {
    let client = go_engine::client_ready()
        .await
        .ok_or_else(|| AppError::Asr("Go 引擎尚未就绪".into()))?;
    client.list_jobs().await.map_err(map_client_err)
}

pub async fn cancel_job(id: &str) -> Result<(), AppError> {
    let client = go_engine::client_ready()
        .await
        .ok_or_else(|| AppError::Asr("Go 引擎尚未就绪".into()))?;
    client.cancel_job(id).await.map_err(map_client_err)
}

fn partial_to_update(p: &StreamPartial, samples: usize, final_: bool) -> NativeStreamUpdate {
    NativeStreamUpdate {
        tentative_text: p.tentative.clone(),
        text: p.committed.clone(),
        language: None,
        sample_count: samples,
        is_final: final_,
    }
}

struct GoSink {
    stream: AsrStream,
    mode_slot: std::sync::Arc<parking_lot::Mutex<Option<StreamMode>>>,
}

impl NativeCaptureSink for GoSink {
    async fn feed(&mut self, samples: &[i16]) -> Result<NativeStreamUpdate, AppError> {
        let partial = self.stream.feed(samples).await.map_err(map_client_err)?;
        if let Some(m) = self.stream.mode() {
            *self.mode_slot.lock() = Some(m);
        }
        Ok(partial_to_update(
            &partial,
            self.stream.sample_offset() as usize,
            false,
        ))
    }

    async fn finish(&mut self) -> Result<TranscriptionResult, AppError> {
        let result = self.stream.finish().await.map_err(map_client_err)?;
        Ok(TranscriptionResult {
            text: result.text,
            duration: Some(result.sample_count as f64 / 16_000.0),
            success: true,
            error: None,
            language: if result.language.is_empty() {
                None
            } else {
                Some(result.language)
            },
        })
    }

    async fn cancel(&mut self) -> Result<(), AppError> {
        self.stream.cancel().await.map_err(map_client_err)
    }
}

/// Same caption payload shape as `native_recording` so the subtitle overlay
/// stays unchanged for both `streaming` and `sentence` modes.
fn next_caption_update(
    previous: &mut Option<(String, String)>,
    update: &NativeStreamUpdate,
    recording_id: u64,
    mode: Option<StreamMode>,
) -> Option<serde_json::Value> {
    let next = (update.text.clone(), update.tentative_text.clone());
    if previous.as_ref() == Some(&next)
        || (previous.is_none() && next.0.is_empty() && next.1.is_empty())
    {
        return None;
    }
    *previous = Some(next);
    Some(serde_json::json!({
        "sessionId": recording_id,
        "text": format!("{}{}", update.text, update.tentative_text),
        "stableText": update.text,
        "tentativeText": update.tentative_text,
        "interim": !update.is_final,
        "language": update.language,
        "captionMode": mode.map(|m| m.as_str()),
    }))
}

fn finished_caption_update(
    result: &TranscriptionResult,
    recording_id: u64,
) -> Option<serde_json::Value> {
    if !result.success || result.text.trim().is_empty() {
        return None;
    }
    Some(serde_json::json!({
        "sessionId": recording_id,
        "text": result.text,
        "stableText": result.text,
        "tentativeText": "",
        "interim": true,
        "language": result.language,
    }))
}

/// Live recording over the Go engine (R2T2 streaming or Qwen3 sentence mode).
pub fn spawn_go_recording(
    app: tauri::AppHandle,
    recording_id: u64,
    samples: Arc<parking_lot::Mutex<Vec<i16>>>,
    sample_rate: u32,
) -> crate::services::audio_service::NativeRecording {
    use crate::services::audio_service::NativeRecording;

    let state = app.state::<AppState>();
    let owner = state.engine.native_asr_owner.clone();
    let config = state.with_profile(|profile| profile.r2t2.clone());
    let hot_words = state.with_profile(|p| p.get_hot_word_texts(100));
    let (sender, mut control) = oneshot::channel();
    let task = tokio::spawn(async move {
        let lease = owner.lock_owned();
        tokio::pin!(lease);
        let mut finish_waiting = false;
        let _guard = tokio::select! {
            biased;
            command = &mut control => match command {
                Ok(NativeCaptureControl::Finish) => {
                    finish_waiting = true;
                    lease.await
                }
                Ok(NativeCaptureControl::Cancel) | Err(_) => return Ok(None),
            },
            guard = &mut lease => guard,
        };
        let control = if finish_waiting {
            let (sender, receiver) = oneshot::channel();
            let _ = sender.send(NativeCaptureControl::Finish);
            receiver
        } else {
            control
        };

        let client = go_engine::client_ready()
            .await
            .ok_or_else(|| AppError::Asr("Go 引擎尚未就绪".into()))?;
        let mut stream = AsrStream::connect(&client).await.map_err(map_client_err)?;
        let session_id = NEXT_GO_SESSION.fetch_add(1, Ordering::Relaxed);
        let hot = if hot_words.is_empty() {
            None
        } else {
            Some(hot_words.as_slice())
        };
        stream
            .start(
                session_id,
                config.language.as_deref(),
                if config.context.is_empty() {
                    None
                } else {
                    Some(config.context.as_str())
                },
                hot,
            )
            .await
            .map_err(map_client_err)?;

        let mode_slot = std::sync::Arc::new(parking_lot::Mutex::new(None::<StreamMode>));
        let sink = GoSink {
            stream,
            mode_slot: mode_slot.clone(),
        };
        let previous_caption = parking_lot::Mutex::new(None);
        let caption_app = app.clone();
        let result = run_capture(sink, samples, sample_rate, control, move |update| {
            let mode = *mode_slot.lock();
            if let Some(payload) =
                next_caption_update(&mut previous_caption.lock(), &update, recording_id, mode)
            {
                let _ = caption_app.emit("transcription-result", payload);
            }
        })
        .await?;
        if let Some(payload) = result
            .as_ref()
            .and_then(|result| finished_caption_update(result, recording_id))
        {
            let _ = app.emit("transcription-result", payload);
        }
        Ok(result)
    });
    NativeRecording::from_parts(Some(sender), task)
}
