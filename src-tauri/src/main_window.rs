//! Main window close-to-tray: destroy WebView on hide, recreate on show.
//!
//! Hiding only keeps the WebView2 renderer (JS heap / DOM / caches) resident.
//! Destroying releases that memory; tray reopen rebuilds from tauri.conf.json.
//! Also recovers when a stale `main` handle remains after destroy, or when the
//! WebView was killed externally while the host process stayed alive.

use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;

use tauri::{AppHandle, Manager, WebviewWindow, WebviewWindowBuilder};

pub const MAIN_WINDOW_LABEL: &str = "main";

static MAIN_WINDOW_DESTROYED_TO_TRAY: AtomicBool = AtomicBool::new(false);
static APP_EXIT_REQUESTED: AtomicBool = AtomicBool::new(false);

fn must_recreate_main_window(window_present: bool, destroyed_to_tray: bool, looks_alive: bool) -> bool {
    destroyed_to_tray || !window_present || !looks_alive
}

fn window_looks_alive(window: &WebviewWindow) -> bool {
    // A dead / externally-killed WebView often fails basic probes.
    window.is_visible().is_ok()
}

fn clone_main_window_config(
    app: &AppHandle,
) -> Result<tauri::utils::config::WindowConfig, String> {
    let mut config = app
        .config()
        .app
        .windows
        .iter()
        .find(|item| item.label == MAIN_WINDOW_LABEL)
        .cloned()
        .ok_or_else(|| "tauri.conf.json 中未找到 main 窗口配置".to_string())?;
    // Recreate on demand; caller shows/focuses explicitly.
    config.create = false;
    config.visible = false;
    Ok(config)
}

fn wait_until_main_window_gone(app: &AppHandle, attempts: u32) {
    for _ in 0..attempts {
        if app.get_webview_window(MAIN_WINDOW_LABEL).is_none() {
            return;
        }
        std::thread::sleep(Duration::from_millis(20));
    }
}

fn ensure_main_window(app: &AppHandle) -> Result<(WebviewWindow, bool), String> {
    let destroyed_to_tray = MAIN_WINDOW_DESTROYED_TO_TRAY.load(Ordering::SeqCst);

    if let Some(window) = app.get_webview_window(MAIN_WINDOW_LABEL) {
        let looks_alive = window_looks_alive(&window);
        if !must_recreate_main_window(true, destroyed_to_tray, looks_alive) {
            return Ok((window, false));
        }

        log::info!(
            "主窗口需重建 (destroyed_to_tray={}, looks_alive={})，清理残留句柄",
            destroyed_to_tray,
            looks_alive
        );
        if let Err(err) = window.destroy() {
            log::warn!("清理残留主窗口失败（仍将尝试重建）: {err}");
        }
        wait_until_main_window_gone(app, 50);
    }

    log::info!(
        "开始重建主窗口 WebView (destroyed_to_tray={})",
        destroyed_to_tray
    );
    let window_config = clone_main_window_config(app)?;
    let window = WebviewWindowBuilder::from_config(app, &window_config)
        .map_err(|err| err.to_string())?
        .build()
        .map_err(|err| {
            format!("重建主窗口失败（destroyed_to_tray={destroyed_to_tray}）: {err}")
        })?;

    log::info!("主窗口 WebView 已重新创建");
    Ok((window, true))
}

fn show_main_window_internal(app: &AppHandle) -> Result<(), String> {
    let (window, created) = ensure_main_window(app)?;

    window.show().map_err(|err| err.to_string())?;
    window.unminimize().map_err(|err| err.to_string())?;
    if let Err(err) = window.set_focus() {
        log::warn!("主窗口聚焦失败: {err}");
    }

    if created {
        MAIN_WINDOW_DESTROYED_TO_TRAY.store(false, Ordering::SeqCst);
        log::info!("主窗口已恢复（前端将重新加载）");
    }

    Ok(())
}

/// Destroy main WebView for close-to-tray. Host process + tray stay alive.
pub fn destroy_main_window_to_tray(app: &AppHandle) -> Result<(), String> {
    let Some(window) = app.get_webview_window(MAIN_WINDOW_LABEL) else {
        MAIN_WINDOW_DESTROYED_TO_TRAY.store(true, Ordering::SeqCst);
        return Ok(());
    };

    MAIN_WINDOW_DESTROYED_TO_TRAY.store(true, Ordering::SeqCst);
    if let Err(err) = window.destroy() {
        MAIN_WINDOW_DESTROYED_TO_TRAY.store(false, Ordering::SeqCst);
        return Err(err.to_string());
    }
    log::info!("主窗口 WebView 已销毁（托盘进程保留）");
    Ok(())
}

/// Show main window, recreating the WebView when missing / stale / dead.
pub fn show_main_window(app: &AppHandle) -> Result<(), String> {
    show_main_window_internal(app)
}

/// Schedule recreate+show on the UI thread (avoids WebView2 deadlock from sync tray handlers).
pub async fn show_main_window_async(app: AppHandle) -> Result<(), String> {
    let (tx, rx) = tokio::sync::oneshot::channel();
    let app_for_task = app.clone();
    app.run_on_main_thread(move || {
        let result = show_main_window_internal(&app_for_task);
        let _ = tx.send(result);
    })
    .map_err(|err| format!("调度主线程显示主窗口失败: {err}"))?;
    rx.await
        .map_err(|_| "主线程显示主窗口任务已取消".to_string())?
}

pub fn request_app_exit() {
    APP_EXIT_REQUESTED.store(true, Ordering::SeqCst);
}

pub fn should_keep_alive_after_main_window_destroyed() -> bool {
    MAIN_WINDOW_DESTROYED_TO_TRAY.load(Ordering::SeqCst)
        && !APP_EXIT_REQUESTED.load(Ordering::SeqCst)
}

pub fn is_destroyed_to_tray() -> bool {
    MAIN_WINDOW_DESTROYED_TO_TRAY.load(Ordering::SeqCst)
}

#[cfg(test)]
mod tests {
    use super::must_recreate_main_window;

    #[test]
    fn recreates_when_window_missing() {
        assert!(must_recreate_main_window(false, false, false));
        assert!(must_recreate_main_window(false, true, false));
    }

    #[test]
    fn reuses_live_window_when_not_destroyed_to_tray() {
        assert!(!must_recreate_main_window(true, false, true));
    }

    #[test]
    fn force_recreates_stale_handle_after_tray_destroy() {
        assert!(must_recreate_main_window(true, true, true));
    }

    #[test]
    fn force_recreates_when_webview_looks_dead() {
        assert!(must_recreate_main_window(true, false, false));
    }
}
