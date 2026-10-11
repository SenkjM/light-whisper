// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Monitor, Moon, Sun } from "lucide-react";
import { useTheme } from "@/hooks/useTheme";
import { useHotkeyCapture } from "@/hooks/useHotkeyCapture";
import { useRecordingContext } from "@/contexts/RecordingContext";
import {
  listInputDevices,
  setInputDevice,
  setSoundEnabled,
} from "@/api/tauri";
import type { InputDeviceListPayload } from "@/types";
import { loadAppPreferences, updateAppPreferences } from "@/lib/appPreferences";
import { LANGUAGE_STORAGE_KEY } from "@/lib/constants";
import { writeLocalStorage } from "@/lib/storage";
import { formatHotkeyForDisplay } from "@/lib/hotkey";

export default function ShellSettingsPage() {
  const { t, i18n } = useTranslation();
  const { theme, setTheme } = useTheme();
  const { setHotkey, hotkeyDisplay } = useRecordingContext();
  const [devices, setDevices] = useState<InputDeviceListPayload | null>(null);
  const [inputDevice, setInputDeviceState] = useState<string>("");
  const [recordingToggle, setRecordingToggle] = useState(false);
  const [soundEnabled, setSound] = useState(true);
  const [micMonitor, setMicMonitor] = useState(false);

  const capture = useHotkeyCapture({
    save: async (shortcut) => {
      await setHotkey(shortcut);
      await updateAppPreferences({ hotkey: shortcut });
    },
    label: t("settings.hotkeyLabel"),
  });

  useEffect(() => {
    void (async () => {
      const prefs = await loadAppPreferences();
      setRecordingToggle(!!prefs.recordingToggle);
      setSound(prefs.soundEnabled !== false);
      setMicMonitor(!!prefs.micLevelMonitorEnabled);
      setInputDeviceState(prefs.inputDevice ?? "");
      try {
        setDevices(await listInputDevices());
      } catch {
        /* ignore in browser-only */
      }
    })();
  }, []);

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
          <strong>{formatHotkeyForDisplay(hotkeyDisplay) || "—"}</strong>
        </p>
        <button type="button" onClick={() => capture.startCapture()} disabled={capture.saving}>
          {capture.capturing ? t("cube.pressKeys") : t("cube.changeHotkey")}
        </button>
        {capture.capturing && (
          <button type="button" onClick={() => capture.cancelCapture()}>
            {t("common.cancel")}
          </button>
        )}
      </section>

      <section className="cube-card">
        <h2>{t("settings.microphone")}</h2>
        <label className="cube-field">
          {t("cube.inputDevice")}
          <select
            value={inputDevice}
            onChange={async (e) => {
              const v = e.target.value;
              setInputDeviceState(v);
              await setInputDevice(v || null);
              await updateAppPreferences({ inputDevice: v || null });
            }}
          >
            <option value="">{t("cube.defaultDevice")}</option>
            {(devices?.devices ?? []).map((d) => (
              <option key={d.name} value={d.name}>
                {d.name}
              </option>
            ))}
          </select>
        </label>
        <label className="cube-check">
          <input
            type="checkbox"
            checked={recordingToggle}
            onChange={async (e) => {
              setRecordingToggle(e.target.checked);
              await updateAppPreferences({ recordingToggle: e.target.checked });
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
        <label className="cube-check">
          <input
            type="checkbox"
            checked={micMonitor}
            onChange={async (e) => {
              setMicMonitor(e.target.checked);
              await updateAppPreferences({ micLevelMonitorEnabled: e.target.checked });
            }}
          />
          {t("settings.micLevelMonitor")}
        </label>
      </section>

      <p className="cube-muted">{t("cube.shellSettingsCloudNote")}</p>
    </div>
  );
}
