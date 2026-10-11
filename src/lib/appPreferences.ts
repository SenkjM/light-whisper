// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Shell-owned business preferences (PLAN 步骤 6).
 * Theme / UI language / onboarding remain in localStorage (`@/lib/storage`).
 */

import {
  getAppPreferences,
  migrateAppPreferences,
  patchAppPreferences,
  type AppPreferences,
} from "@/api/tauri";
import { DEFAULT_HOTKEY } from "@/lib/constants";
import { normalizeHotkey } from "@/lib/hotkey";
import { readLocalStorage } from "@/lib/storage";
import {
  AI_POLISH_ENABLED_KEY,
  HOTKEY_STORAGE_KEY,
  INPUT_DEVICE_STORAGE_KEY,
  INPUT_METHOD_KEY,
  MIC_LEVEL_MONITOR_ENABLED_KEY,
  RECORDING_MODE_KEY,
  SOUND_ENABLED_KEY,
} from "@/lib/constants";

let cache: AppPreferences | null = null;
let loadPromise: Promise<AppPreferences> | null = null;

export async function loadAppPreferences(force = false): Promise<AppPreferences> {
  if (!force && cache) return cache;
  if (!force && loadPromise) return loadPromise;
  loadPromise = (async () => {
    let prefs = await getAppPreferences();
    if (!prefs.migratedFromLocalStorage) {
      prefs = await migrateAppPreferences({
        schemaVersion: 1,
        hotkey: readLocalStorage(HOTKEY_STORAGE_KEY),
        recordingToggle: readLocalStorage(RECORDING_MODE_KEY) === "toggle",
        inputDevice: readLocalStorage(INPUT_DEVICE_STORAGE_KEY),
        inputMethod:
          readLocalStorage(INPUT_METHOD_KEY) === "clipboard" ? "clipboard" : "sendInput",
        soundEnabled: readLocalStorage(SOUND_ENABLED_KEY) !== "false",
        micLevelMonitorEnabled: readLocalStorage(MIC_LEVEL_MONITOR_ENABLED_KEY) === "true",
        aiPolishEnabled: readLocalStorage(AI_POLISH_ENABLED_KEY) === "true",
        migratedFromLocalStorage: true,
      });
    }
    cache = prefs;
    return prefs;
  })();
  try {
    return await loadPromise;
  } finally {
    loadPromise = null;
  }
}

export function peekAppPreferences(): AppPreferences | null {
  return cache;
}

export async function updateAppPreferences(
  patch: Partial<AppPreferences>,
): Promise<AppPreferences> {
  const prefs = await patchAppPreferences(patch);
  cache = prefs;
  return prefs;
}

export function hotkeyFromPrefs(prefs: AppPreferences): string {
  const raw = prefs.hotkey?.trim();
  if (raw) return normalizeHotkey(raw);
  return DEFAULT_HOTKEY;
}
