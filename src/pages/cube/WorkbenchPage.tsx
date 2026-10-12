// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { getEngine, setEngine } from "@/api/tauri";
import { useRecordingContext } from "@/contexts/RecordingContext";
import MainPage from "@/pages/MainPage";
import HistoryPage from "@/pages/HistoryPage";

export type WorkbenchSub = "dictation" | "history";

const LOCAL_ENGINES = [
  { key: "qwen3-asr-0.6b", labelKey: "cube.engineQwen" },
  { key: "confucius4-r2t2", labelKey: "cube.engineR2t2" },
] as const;

export default function WorkbenchPage({
  sub,
  onSubChange,
  onOpenShell,
}: {
  sub: WorkbenchSub;
  onSubChange: (s: WorkbenchSub) => void;
  onOpenShell: () => void;
}) {
  const { t } = useTranslation();
  const {
    downloadModels,
    cancelDownload,
    isDownloading,
    downloadProgress,
    downloadMessage,
    stage,
    retryModel,
    modelError,
    isReady,
  } = useRecordingContext();

  const [engine, setEngineState] = useState("qwen3-asr-0.6b");
  const [engineBusy, setEngineBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void getEngine()
      .then((value) => {
        if (!cancelled) setEngineState(value);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  const handleNavigate = (v: "main" | "settings" | "history" | "dev-engine") => {
    if (v === "history") onSubChange("history");
    else if (v === "settings") onOpenShell();
    else if (v === "main") onSubChange("dictation");
  };

  const switchLocalEngine = useCallback(
    async (key: string) => {
      if (engineBusy || key === engine) return;
      setEngineBusy(true);
      try {
        await setEngine(key);
        setEngineState(key);
        toast.success(t("toast.switchedToEngine", { label: t(
          LOCAL_ENGINES.find((e) => e.key === key)?.labelKey ?? "cube.engineQwen",
        ) }));
        retryModel();
      } catch {
        toast.error(t("toast.switchEngineFailed"));
      } finally {
        setEngineBusy(false);
      }
    },
    [engine, engineBusy, retryModel, t],
  );

  const needsInstall =
    stage === "need_download" ||
    stage === "error" ||
    Boolean(modelError) ||
    (!isReady && !isDownloading && stage !== "ready" && stage !== "loading" && stage !== "checking");

  const installLabel = isDownloading
    ? t("cube.installingModels")
    : needsInstall
      ? t("cube.oneClickInstall")
      : t("cube.reinstallModels");

  return (
    <div className="cube-page cube-workbench">
      <header className="cube-page-header cube-workbench-header">
        <div>
          <h1>{t("cube.workbenchTitle")}</h1>
          <p className="cube-muted">{t("cube.workbenchHint")}</p>
        </div>
        <div className="cube-subtabs" role="tablist" aria-label={t("cube.workbenchTitle")}>
          <button
            type="button"
            role="tab"
            aria-selected={sub === "dictation"}
            className={sub === "dictation" ? "is-active" : ""}
            onClick={() => onSubChange("dictation")}
          >
            {t("cube.dictation")}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={sub === "history"}
            className={sub === "history" ? "is-active" : ""}
            onClick={() => onSubChange("history")}
          >
            {t("cube.history")}
          </button>
        </div>
      </header>

      <section className="cube-workbench-controls" aria-label={t("cube.engineSection")}>
        <div className="cube-engine-group">
          <span>{t("cube.engineSection")}</span>
          {LOCAL_ENGINES.map(({ key, labelKey }) => (
            <button
              key={key}
              type="button"
              className={`cube-engine-btn${engine === key ? " is-active" : ""}`}
              disabled={engineBusy || isDownloading}
              aria-pressed={engine === key}
              onClick={() => void switchLocalEngine(key)}
            >
              {t(labelKey)}
            </button>
          ))}
        </div>
        <button
          type="button"
          className="cube-install-btn"
          disabled={false}
          onClick={() => {
            if (isDownloading) {
              void cancelDownload();
              return;
            }
            void downloadModels();
          }}
        >
          {isDownloading ? t("cube.cancelInstall") : installLabel}
        </button>
        {(isDownloading || downloadMessage || modelError) && (
          <p className="cube-install-status" role="status">
            {isDownloading
              ? `${downloadMessage || t("cube.installingModels")}${
                  downloadProgress >= 0 ? ` (${Math.round(downloadProgress)}%)` : ""
                }`
              : modelError || downloadMessage}
          </p>
        )}
        <p className="cube-muted cube-install-status">{t("cube.engineInstallHint")}</p>
      </section>

      <div className="cube-workbench-body">
        {sub === "dictation" ? (
          <MainPage embedded onNavigate={handleNavigate} />
        ) : (
          <HistoryPage embedded onNavigate={handleNavigate} />
        )}
      </div>
    </div>
  );
}
