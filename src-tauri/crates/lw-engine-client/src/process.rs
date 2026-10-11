// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Spawning `lw-engine` and reading its handshake.

use std::collections::HashMap;
use std::path::PathBuf;
use std::process::Stdio;
use std::time::Duration;
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::process::{Child, ChildStdin, Command};

use crate::handshake::{read_handshake, Handshake, HandshakeError};

/// How to start the engine.
#[derive(Debug, Clone)]
pub struct SpawnOptions {
    pub binary: PathBuf,
    /// App data directory (`--data-dir`), same as the Tauri app's.
    pub data_dir: Option<PathBuf>,
    /// `--backend` (`mock` until the native backend exists).
    pub backend: String,
    /// Extra CLI arguments (tests, `--no-autoload`, …).
    pub extra_args: Vec<String>,
    /// Where to redirect the engine's stderr log; `None` discards it.
    pub stderr_log: Option<PathBuf>,
    /// Extra environment variables for the child (native lib paths, etc.).
    pub env: HashMap<String, String>,
    pub handshake_timeout: Duration,
}

impl SpawnOptions {
    pub fn new(binary: impl Into<PathBuf>) -> Self {
        Self {
            binary: binary.into(),
            data_dir: None,
            backend: "mock".to_string(),
            extra_args: Vec::new(),
            stderr_log: None,
            env: HashMap::new(),
            handshake_timeout: Duration::from_secs(15),
        }
    }
}

#[derive(Debug, thiserror::Error)]
pub enum SpawnError {
    #[error("failed to start {path}: {source}")]
    Spawn {
        path: String,
        source: std::io::Error,
    },
    #[error(transparent)]
    Handshake(#[from] HandshakeError),
}

/// Windows Job Object that kills assigned children when the last handle closes.
/// Held for the lifetime of [`EngineProcess`] so a crashed / killed shell
/// tears down `lw-engine` even if stdin inheritance or `kill_on_drop` fail.
#[cfg(windows)]
struct KillOnCloseJob(windows_sys::Win32::Foundation::HANDLE);

#[cfg(windows)]
impl KillOnCloseJob {
    fn create() -> Option<Self> {
        use windows_sys::Win32::Foundation::CloseHandle;
        use windows_sys::Win32::System::JobObjects::{
            CreateJobObjectW, JobObjectExtendedLimitInformation, SetInformationJobObject,
            JOBOBJECT_EXTENDED_LIMIT_INFORMATION, JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
        };
        // SAFETY: CreateJobObjectW with null name/attrs returns an owned handle or null.
        let handle = unsafe { CreateJobObjectW(std::ptr::null(), std::ptr::null()) };
        if handle.is_null() || handle == windows_sys::Win32::Foundation::INVALID_HANDLE_VALUE {
            log::warn!("lw-engine: CreateJobObjectW failed; falling back to stdin/kill_on_drop");
            return None;
        }
        let mut info = unsafe { std::mem::zeroed::<JOBOBJECT_EXTENDED_LIMIT_INFORMATION>() };
        info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
        // SAFETY: info is fully initialized for the fields SetInformationJobObject reads.
        let ok = unsafe {
            SetInformationJobObject(
                handle,
                JobObjectExtendedLimitInformation,
                &info as *const _ as *const _,
                std::mem::size_of_val(&info) as u32,
            )
        };
        if ok == 0 {
            log::warn!("lw-engine: SetInformationJobObject(KILL_ON_JOB_CLOSE) failed; closing job");
            unsafe { CloseHandle(handle) };
            return None;
        }
        Some(Self(handle))
    }

    fn assign(&self, process: windows_sys::Win32::Foundation::HANDLE) -> bool {
        use windows_sys::Win32::System::JobObjects::AssignProcessToJobObject;
        // SAFETY: both handles are valid and owned by us / the Child.
        unsafe { AssignProcessToJobObject(self.0, process) != 0 }
    }
}

#[cfg(windows)]
impl Drop for KillOnCloseJob {
    fn drop(&mut self) {
        // Closing the job with KILL_ON_JOB_CLOSE terminates remaining children.
        unsafe { windows_sys::Win32::Foundation::CloseHandle(self.0) };
    }
}

#[cfg(windows)]
impl std::fmt::Debug for KillOnCloseJob {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_tuple("KillOnCloseJob").field(&self.0).finish()
    }
}

// HANDLEs are process-wide opaque integers; we uniquely own this job object.
#[cfg(windows)]
unsafe impl Send for KillOnCloseJob {}
#[cfg(windows)]
unsafe impl Sync for KillOnCloseJob {}

/// A running engine process. Dropping it kills the process.
///
/// stdin stays open for the process lifetime: the engine runs with
/// `--exit-on-stdin-close`, so it also exits if this (parent) process dies
/// without cleaning up. On Windows an additional Job Object with
/// `KILL_ON_JOB_CLOSE` covers the case where the shell is terminated
/// without running Drop (task manager, crash).
#[derive(Debug)]
pub struct EngineProcess {
    child: Child,
    _stdin: Option<ChildStdin>,
    /// Kept alive so the job's KILL_ON_JOB_CLOSE fires when the shell exits.
    #[cfg(windows)]
    _job: Option<KillOnCloseJob>,
    pub handshake: Handshake,
    pub token: String,
}

impl EngineProcess {
    pub fn pid(&self) -> Option<u32> {
        self.child.id()
    }

    pub fn base_url(&self) -> String {
        format!("http://127.0.0.1:{}", self.handshake.port)
    }

    /// Waits for the process to exit.
    pub async fn wait(&mut self) -> std::io::Result<std::process::ExitStatus> {
        self.child.wait().await
    }

    /// Kills the process and waits up to `timeout` for it to exit.
    pub async fn kill(&mut self, timeout: Duration) {
        let _ = self.child.start_kill();
        let _ = tokio::time::timeout(timeout, self.child.wait()).await;
    }
}

/// Starts the engine and reads its handshake.
pub async fn spawn_engine(opts: &SpawnOptions) -> Result<EngineProcess, SpawnError> {
    let mut cmd = Command::new(&opts.binary);
    cmd.arg("--listen")
        .arg("127.0.0.1:0")
        .arg("--backend")
        .arg(&opts.backend)
        .arg("--exit-on-stdin-close");
    if let Some(dir) = &opts.data_dir {
        cmd.arg("--data-dir").arg(dir);
    }
    for (k, v) in &opts.env {
        cmd.env(k, v);
    }
    cmd.args(&opts.extra_args)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(match &opts.stderr_log {
            Some(path) => match std::fs::File::create(path) {
                Ok(f) => Stdio::from(f),
                Err(e) => {
                    log::warn!(
                        "lw-engine: cannot create stderr log {}: {e}",
                        path.display()
                    );
                    Stdio::null()
                }
            },
            None => Stdio::null(),
        })
        .kill_on_drop(true);

    // Hide the console window on Windows, same as the Python engine spawn.
    #[cfg(target_os = "windows")]
    {
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        cmd.creation_flags(CREATE_NO_WINDOW);
    }

    let mut child = cmd.spawn().map_err(|source| SpawnError::Spawn {
        path: opts.binary.display().to_string(),
        source,
    })?;
    let stdin = child.stdin.take();
    let stdout = child.stdout.take().expect("stdout is piped");
    let mut reader = BufReader::new(stdout);

    let handshake = match read_handshake(&mut reader, opts.handshake_timeout).await {
        Ok(h) => h,
        Err(e) => {
            let _ = child.start_kill();
            let _ = tokio::time::timeout(Duration::from_secs(2), child.wait()).await;
            return Err(e.into());
        }
    };
    let Some(token) = handshake.token.clone() else {
        let _ = child.start_kill();
        return Err(HandshakeError::MissingToken.into());
    };

    // Keep draining stdout so the pipe can never fill up.
    tokio::spawn(async move {
        let mut line = String::new();
        while matches!(reader.read_line(&mut line).await, Ok(n) if n > 0) {
            line.clear();
        }
    });

    #[cfg(windows)]
    let job = {
        match KillOnCloseJob::create() {
            Some(job) => match child.raw_handle() {
                Some(raw) => {
                    let handle = raw as windows_sys::Win32::Foundation::HANDLE;
                    if job.assign(handle) {
                        Some(job)
                    } else {
                        log::warn!(
                            "lw-engine: AssignProcessToJobObject failed; relying on stdin/kill_on_drop"
                        );
                        None
                    }
                }
                None => {
                    log::warn!("lw-engine: child has no raw handle; relying on stdin/kill_on_drop");
                    None
                }
            },
            None => None,
        }
    };

    Ok(EngineProcess {
        child,
        _stdin: stdin,
        #[cfg(windows)]
        _job: job,
        handshake,
        token,
    })
}
