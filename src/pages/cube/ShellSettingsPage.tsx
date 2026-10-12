// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Monitor, Moon, Sun } from "lucide-react";
import { toast } from "sonner";
import { useTheme } from "@/hooks/useTheme";
import { useHotkeyCapture } from "@/hooks/useHotkeyCapture";
import { useMicrophoneSettings } from "@/hooks/useMicrophoneSettings";
import { useRecordingContext } from "@/contexts/RecordingContext";
import {
  getUserProfile,
  setAssistantHotkey,
  setRecordingMode,
  setSoundEnabled,
} from "@/api/tauri";
import { loadAppPreferences, updateAppPreferences } from "@/lib/appPreferences";
import { LANGUAGE_STORAGE_KEY } from "@/lib/constants";
import { writeLocalStorage } from "@/lib/storage";
import { formatHotkeyForDisplay } from "@/lib/hotkey";
import Kbd from "@/components/Kbd";

export default function ShellSettingsPage() {
  const { t, i18n } = useTranslation();
  const { theme, setTheme } = useTheme();
  const {
    setHotkey,
    hotkeyDisplay,
    hotkeyError,
    hotkeyDiagnostic,
    isRecording,
  } = useRecordingContext();
  const [recordingToggle, setRecordingToggle] = useState(false);
  const [soundEnabled, setSound] = useState(true);
  const [assistantHotkeyDisplay, setAssistantHotkeyDisplay] = useState("");
  const [modeBusy, setModeBusy] = useState(false);

  const mic = useMicrophoneSettings({
    active: true,
    closePicker: () => {},
    isRecording,
  });

  const capture = useHotkeyCapture({
    save: async (shortcut) => {
      await setHotkey(shortcut);
      await updateAppPreferences({ hotkey: shortcut });
    },
    label: t("settings.hotkeyLabel"),
  });

  const assistantCapture = useHotkeyCapture({
    save: async (shortcut) => {
      await setAssistantHotkey(shortcut);
      setAssistantHotkeyDisplay(formatHotkeyForDisplay(shortcut));
      toast.success(t("toast.hotkeySaved", { label: t("settings.assistantHotkeyLabel") }));
    },
    label: t("settings.assistantHotkeyLabel"),
  });

  useEffect(() => {
    void (async () => {
      const prefs = await loadAppPreferences();
      setRecordingToggle(!!prefs.recordingToggle);
      setSound(prefs.soundEnabled !== false);
      try {
        const profile = await getUserProfile();
        setAssistantHotkeyDisplay(
          profile.assistant_hotkey ? formatHotkeyForDisplay(profile.assistant_hotkey) : "",
        );
      } catch {
        /* ignore */
      }
    })();
  }, []);

  const hotkeyStatusError = hotkeyError || hotkeyDiagnostic?.lastError || null;
  const hotkeyRegistered = hotkeyDiagnostic?.registered;

  return (
    <div className="cube-page">
      <header className="cube-page-header">
        <h1>{t("cube.shellSettingsTitle")}</h1>
        <p className="cube-muted">{t("cube.shellSettingsHint")}</p>
      </header>

      <section className="cube-card">
        <h2>{t("settings.appearance")}</h2>
        <div className="cube-theme-row">
          {([
            { mode: "light" as const, Icon: Sun, key: "settings.themeLight" },
            { mode: "dark" as const, Icon: Moon, key: "settings.themeDark" },
            { mode: "system" as const, Icon: Monitor, key: "settings.themeSystem" },
          ]).map(({ mode, Icon, key }) => (
            <button
              key={mode}
              type="button"
              className={theme === mode ? "is-active" : ""}
              aria-pressed={theme === mode}
              onClick={() => setTheme(mode)}
            >
              <Icon size={18} /> {t(key)}
            </button>
          ))}
        </div>
        <label className="cube-field">
          {t("settings.language")}
          <select
            className="cube-select"
            value={i18n.language.startsWith("zh") ? "zh" : "en"}
            onChange={(e) => {
              void i18n.changeLanguage(e.target.value);
              writeLocalStorage(LANGUAGE_STORAGE_KEY, e.target.value);
            }}
          >
            <option value="zh">中文</option>
            <option value="en">English</option>
          </select>
        </label>
      </section>

      <section className="cube-card">
        <h2>{t("settings.hotkeySection")}</h2>
        <p className="cube-muted">
          {t("settings.hotkeyLabel")}:{" "}
          {hotkeyDisplay ? <Kbd combo={hotkeyDisplay} /> : <strong>—</strong>}
          {hotkeyRegistered === true ? (
            <span className="cube-model-ok" style={{ marginLeft: 8 }}>
              {t("cube.hotkeyRegistered")}
            </span>
          ) : hotkeyRegistered === false ? (
            <span className="cube-model-missing" style={{ marginLeft: 8 }}>
              {t("cube.hotkeyNotRegistered")}
            </span>
          ) : null}
        </p>
        {hotkeyDiagnostic?.backend ? (
          <p className="cube-muted" style={{ fontSize: 12 }}>
            {t("cube.hotkeyBackend")}: <span className="mono">{hotkeyDiagnostic.backend}</span>
            {hotkeyDiagnostic.lastEvent ? ` · ${hotkeyDiagnostic.lastEvent}` : ""}
          </p>
        ) : null}
        {hotkeyDiagnostic?.systemConflict ? (
          <p className="cube-error" role="alert">{hotkeyDiagnostic.systemConflict}</p>
        ) : null}
        {hotkeyDiagnostic?.warning ? (
          <p className="cube-muted">{hotkeyDiagnostic.warning}</p>
        ) : null}
        {hotkeyStatusError ? (
          <p className="cube-error" role="alert">{hotkeyStatusError}</p>
        ) : null}
        <div className="cube-engine-group" style={{ marginBottom: 12 }}>
          <button type="button" className="cube-engine-btn" onClick={() => capture.startCapture()} disabled={capture.saving}>
            {capture.capturing ? t("cube.pressKeys") : t("cube.changeHotkey")}
          </button>
          {capture.capturing && (
            <button type="button" className="cube-engine-btn" onClick={() => capture.cancelCapture()}>
              {t("common.cancel")}
            </button>
          )}
          <button
            type="button"
            className="cube-engine-btn"
            disabled={capture.saving || capture.capturing}
            onClick={() => {
              void (async () => {
                try {
                  const prefs = await loadAppPreferences();
                  const raw =
                    prefs.hotkey?.trim() ||
                    hotkeyDiagnostic?.shortcut?.trim() ||
                    "F2";
                  await setHotkey(raw);
                  toast.success(t("cube.hotkeyReregistered"));
                } catch (e) {
                  toast.error(e instanceof Error ? e.message : String(e));
                }
              })();
            }}
          >
            {t("cube.hotkeyReregister")}
          </button>
        </div>

        <p className="cube-muted">
          {t("settings.assistantHotkeyLabel")}:{" "}
          {assistantHotkeyDisplay ? <Kbd combo={assistantHotkeyDisplay} /> : <strong>—</strong>}
        </p>
        <div className="cube-engine-group">
          <button
            type="button"
            className="cube-engine-btn"
            onClick={() => assistantCapture.startCapture()}
            disabled={assistantCapture.saving}
          >
            {assistantCapture.capturing ? t("settings.pressAssistantHotkey") : t("cube.changeAssistantHotkey")}
          </button>
          {assistantCapture.capturing && (
            <button type="button" className="cube-engine-btn" onClick={() => assistantCapture.cancelCapture()}>
              {t("common.cancel")}
            </button>
          )}
          {assistantHotkeyDisplay && !assistantCapture.capturing && (
            <button
              type="button"
              className="cube-engine-btn"
              onClick={() => {
                void (async () => {
                  try {
                    await setAssistantHotkey(null);
                    setAssistantHotkeyDisplay("");
                    toast.success(t("toast.hotkeyCleared", { label: t("settings.assistantHotkeyLabel") }));
                  } catch (e) {
                    toast.error(e instanceof Error ? e.message : String(e));
                  }
                })();
              }}
            >
              {t("common.clear")}
            </button>
          )}
        </div>
        <p className="cube-muted" style={{ fontSize: 12, marginTop: 10 }}>
          {t("cube.hotkeyHoldHint")}
        </p>
      </section>

      <section className="cube-card">
        <h2>{t("settings.microphone")}</h2>
        <label className="cube-field">
          {t("cube.inputDevice")}
          <select
            className="cube-select"
            value={mic.selectedInputDeviceName}
            disabled={mic.deviceListLoading}
            onChange={(e) => void mic.handleInputDeviceChange(e.target.value)}
          >
            <option value="">{t("cube.defaultDevice")}</option>
            {mic.inputDevices.map((d) => (
              <option key={d.name} value={d.name}>
                {d.name}
              </option>
            ))}
          </select>
        </label>

        <label className="cube-check">
          <input
            type="checkbox"
            checked={mic.micLevelMonitorEnabled}
            onChange={(e) => mic.handleMicLevelMonitorToggle(e.target.checked)}
          />
          {t("settings.micLevelMonitor")}
        </label>

        <div className="mic-level-shell" aria-label={t("settings.micLevelPreview")} style={{ margin: "10px 0 14px" }}>
          <div
            className="mic-level-fill"
            style={{ ["--mic-level" as string]: `${Math.round(mic.micLevel * 100)}%` } as React.CSSProperties}
          />
        </div>
        <p className="cube-muted" style={{ marginTop: 0 }}>
          {!mic.micLevelMonitorEnabled
            ? t("settings.micMonitorOff")
            : isRecording
              ? t("settings.micMonitorPausedRecording")
              : mic.micMonitorReady
                ? t("settings.micMonitorLive")
                : t("settings.micMonitorStarting")}
        </p>
        <button type="button" className="cube-engine-btn" onClick={() => void mic.handleTestMicrophone()}>
          {t("settings.testMicrophone")}
        </button>

        <label className="cube-check" style={{ marginTop: 14 }}>
          <input
            type="checkbox"
            checked={recordingToggle}
            disabled={modeBusy}
            onChange={(e) => {
              const next = e.target.checked;
              setRecordingToggle(next);
              setModeBusy(true);
              void (async () => {
                try {
                  // Must update Rust AtomicBool + re-register backends (not prefs alone).
                  await setRecordingMode(next);
                  await updateAppPreferences({ recordingToggle: next });
                } catch (err) {
                  setRecordingToggle(!next);
                  toast.error(err instanceof Error ? err.message : String(err));
                } finally {
                  setModeBusy(false);
                }
              })();
            }}
          />
          {t("cube.recordingToggle")}
        </label>
        <label className="cube-check">
          <input
            type="checkbox"
            checked={soundEnabled}
            onChange={async (e) => {
              setSound(e.target.checked);
              await setSoundEnabled(e.target.checked);
              await updateAppPreferences({ soundEnabled: e.target.checked });
            }}
          />
          {t("cube.soundEnabled")}
        </label>
      </section>

      <p className="cube-muted">{t("cube.shellSettingsCloudNote")}</p>
    </div>
  );
}
