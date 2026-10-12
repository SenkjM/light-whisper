// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Shell-local NVIDIA GPU / driver detection (PLAN §5).
//!
//! Does **not** require the CUDA Toolkit or bundled R2T2 CUDA DLLs. Used so the
//! thin-shell (`without`) package — and withcore when the engine is briefly
//! unavailable — can still show GPU UI and warn about missing libs separately.

use std::path::PathBuf;
use std::process::Command;

/// Hide console window on Windows (nvidia-smi / powershell flash black consoles).
fn with_no_window(cmd: &mut Command) -> &mut Command {
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        cmd.creation_flags(CREATE_NO_WINDOW);
    }
    cmd
}

/// Whether an NVIDIA GPU / display driver is present on this machine.
pub fn nvidia_present() -> bool {
    #[cfg(windows)]
    {
        windows_nvidia_present()
    }
    #[cfg(not(windows))]
    {
        unix_nvidia_present()
    }
}

/// Best-effort driver version string (may be empty).
pub fn driver_version() -> String {
    if let Some(v) = nvidia_smi_driver_version() {
        return v;
    }
    #[cfg(windows)]
    {
        return windows_registry_driver_version();
    }
    #[cfg(not(windows))]
    {
        String::new()
    }
}

fn nvidia_smi_driver_version() -> Option<String> {
    let out = with_no_window(&mut Command::new("nvidia-smi")).output().ok()?;
    if !out.status.success() {
        return None;
    }
    let text = String::from_utf8_lossy(&out.stdout);
    // "Driver Version: 576.02"
    for line in text.lines() {
        let lower = line.to_ascii_lowercase();
        if let Some(idx) = lower.find("driver version") {
            let rest = &line[idx..];
            if let Some(colon) = rest.find(':') {
                let ver = rest[colon + 1..].trim();
                if !ver.is_empty() {
                    return Some(ver.split_whitespace().next().unwrap_or(ver).to_string());
                }
            }
        }
    }
    // nvidia-smi alone succeeding is still a strong signal elsewhere.
    None
}

#[cfg(not(windows))]
fn unix_nvidia_present() -> bool {
    if PathBuf::from("/dev/nvidia0").exists() || PathBuf::from("/dev/nvidiactl").exists() {
        return true;
    }
    if with_no_window(&mut Command::new("nvidia-smi"))
        .output()
        .ok()
        .is_some_and(|o| o.status.success())
    {
        return true;
    }
    false
}

#[cfg(windows)]
fn windows_nvidia_present() -> bool {
    if windows_has_nvapi() || windows_has_nvcuda() {
        return true;
    }
    if with_no_window(&mut Command::new("nvidia-smi"))
        .output()
        .ok()
        .is_some_and(|o| o.status.success())
    {
        return true;
    }
    windows_display_adapter_nvidia()
}

#[cfg(windows)]
fn windows_has_nvapi() -> bool {
    use windows_sys::Win32::Foundation::FreeLibrary;
    use windows_sys::Win32::System::LibraryLoader::LoadLibraryW;

    for name in ["nvapi64.dll", "nvapi.dll"] {
        let wide: Vec<u16> = name.encode_utf16().chain(std::iter::once(0)).collect();
        let handle = unsafe { LoadLibraryW(wide.as_ptr()) };
        if !handle.is_null() {
            unsafe { FreeLibrary(handle) };
            return true;
        }
    }
    let sys = std::env::var("SystemRoot").unwrap_or_else(|_| r"C:\Windows".into());
    for name in ["nvapi64.dll", "nvldumdx.dll", "nvldumd.dll"] {
        let p = PathBuf::from(&sys).join("System32").join(name);
        if p.is_file() {
            return true;
        }
    }
    false
}

#[cfg(windows)]
fn windows_has_nvcuda() -> bool {
    use windows_sys::Win32::Foundation::FreeLibrary;
    use windows_sys::Win32::System::LibraryLoader::LoadLibraryW;

    let wide: Vec<u16> = "nvcuda.dll\0".encode_utf16().collect();
    let handle = unsafe { LoadLibraryW(wide.as_ptr()) };
    if handle.is_null() {
        return false;
    }
    unsafe { FreeLibrary(handle) };
    true
}

#[cfg(windows)]
fn windows_display_adapter_nvidia() -> bool {
    // Fallback: PowerShell Win32_VideoController (no Toolkit required).
    let out = with_no_window(&mut Command::new("powershell"))
        .args([
            "-NoProfile",
            "-Command",
            "Get-CimInstance Win32_VideoController | Select-Object -ExpandProperty Name",
        ])
        .output();
    match out {
        Ok(o) if o.status.success() => {
            String::from_utf8_lossy(&o.stdout).to_ascii_uppercase().contains("NVIDIA")
        }
        _ => false,
    }
}

#[cfg(windows)]
fn windows_registry_driver_version() -> String {
    // Best-effort via PowerShell — avoids adding a winreg dependency.
    let out = with_no_window(&mut Command::new("powershell"))
        .args([
            "-NoProfile",
            "-Command",
            r#"Get-CimInstance Win32_VideoController | Where-Object { $_.Name -match 'NVIDIA' } | Select-Object -First 1 -ExpandProperty DriverVersion"#,
        ])
        .output();
    match out {
        Ok(o) if o.status.success() => String::from_utf8_lossy(&o.stdout).trim().to_string(),
        _ => String::new(),
    }
}
