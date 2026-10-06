// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Client for the light-whisper Go engine (`lw-engine`), PLAN 步骤 1.
//!
//! - [`locate`]: find the `lw-engine` binary.
//! - [`process`]: spawn it and read the one-line JSON startup handshake.
//! - [`client`]: authenticated HTTP calls (`/health`, `/v1/engine/status`,
//!   `/v1/config` with revision handling, `/v1/engine/reload`).
//! - [`events`]: `WS /v1/events` subscription.
//! - [`supervisor`]: keeps one engine process alive, restarts it with bounded
//!   exponential backoff after a crash (PLAN §6 #3) and reports status.
//!
//! The contract lives in `docs/command-cube/api/`. This crate has no Tauri
//! dependency so it can be tested on any platform.

pub mod client;
pub mod events;
pub mod handshake;
pub mod locate;
pub mod process;
pub mod supervisor;

pub use client::{ClientError, ConfigSnapshot, EngineClient, PatchResult};
pub use events::EngineEvent;
pub use handshake::{check_api_version, Handshake, HandshakeError};
pub use locate::{binary_file_name, locate_engine_binary};
pub use process::{spawn_engine, EngineProcess, SpawnError, SpawnOptions};
pub use supervisor::{
    RestartPolicy, Supervisor, SupervisorEvent, SupervisorOptions, SupervisorState,
    SupervisorStatus,
};

/// The contract major version this client implements (`api_version` in the
/// handshake and `GET /health`).
pub const SUPPORTED_API_VERSION: u32 = 1;
