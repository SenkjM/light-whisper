import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { getGpuIdleSeconds, setGpuIdleSeconds } from "@/api/tauri";
import {
  GPU_IDLE_MAX_SECONDS,
  GPU_IDLE_OFF_SECONDS,
  GPU_IDLE_SUGGESTED_SECONDS,
  normalizeGpuIdleSeconds,
} from "@/lib/gpuIdle";

export default function GpuIdleUnloadControl() {
  const { t } = useTranslation();
  const [seconds, setSeconds] = useState(GPU_IDLE_OFF_SECONDS);
  const [draft, setDraft] = useState(String(GPU_IDLE_SUGGESTED_SECONDS));
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let cancelled = false;
    getGpuIdleSeconds()
      .then((value) => {
        if (cancelled) return;
        const next = normalizeGpuIdleSeconds(value);
        setSeconds(next);
        if (next > 0) setDraft(String(next));
      })
      .catch(() => {
        // Missing config reads as off, matching an engine.json without the key.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const persist = async (next: number) => {
    const secondsToSave = normalizeGpuIdleSeconds(next);
    setSaving(true);
    try {
      const saved = normalizeGpuIdleSeconds(await setGpuIdleSeconds(secondsToSave));
      setSeconds(saved);
      if (saved > 0) setDraft(String(saved));
    } catch {
      toast.error(t("settings.gpuIdleSaveFailed"));
    } finally {
      setSaving(false);
    }
  };

  const enabled = seconds > 0;

  const commitDraft = () => {
    const parsed = normalizeGpuIdleSeconds(draft);
    if (parsed === seconds) return;
    void persist(parsed);
  };

  return (
    <div className="settings-inline-panel" style={{ marginTop: 8 }}>
      <div className="settings-column" style={{ gap: 6 }}>
        <span className="settings-option-desc">{t("settings.gpuIdleTitle")}</span>
        <span className="settings-option-desc">{t("settings.gpuIdleDesc")}</span>
        <div className="settings-row" style={{ gap: 6 }}>
          <button
            type="button"
            className={`theme-btn${!enabled ? " active" : ""}`}
            aria-pressed={!enabled}
            disabled={saving}
            onClick={() => {
              if (!enabled) return;
              void persist(GPU_IDLE_OFF_SECONDS);
            }}
          >
            {t("settings.gpuIdleOff")}
          </button>
          <button
            type="button"
            className={`theme-btn${enabled ? " active" : ""}`}
            aria-pressed={enabled}
            disabled={saving}
            onClick={() => {
              if (enabled) return;
              void persist(GPU_IDLE_SUGGESTED_SECONDS);
            }}
          >
            {t("settings.gpuIdleOn")}
          </button>
        </div>
        {enabled && (
          <label className="settings-column" style={{ gap: 4 }}>
            <span className="settings-option-desc">{t("settings.gpuIdleSeconds")}</span>
            <input
              className="settings-input"
              type="number"
              min={0}
              max={GPU_IDLE_MAX_SECONDS}
              step={1}
              inputMode="numeric"
              aria-label={t("settings.gpuIdleSeconds")}
              value={draft}
              disabled={saving}
              onChange={(event) => setDraft(event.target.value)}
              onBlur={commitDraft}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.currentTarget.blur();
                }
              }}
              style={{ maxWidth: 140 }}
            />
          </label>
        )}
        <span className="settings-option-desc">{t("settings.gpuIdleApplyHint")}</span>
        <span className="settings-option-desc">{t("settings.gpuReloadHeading")}</span>
        <span className="settings-option-desc">{t("settings.gpuReloadR2t2")}</span>
        <span className="settings-option-desc">{t("settings.gpuReloadQwen")}</span>
      </div>
    </div>
  );
}
