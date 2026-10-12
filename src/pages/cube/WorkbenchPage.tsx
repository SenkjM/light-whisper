// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {
  getEngine,
  setEngine,
  getDevice,
  setDevice,
  getManagementConnection,
  getCudaRuntimeStatus,
  startCudaRuntimeDownload,
  cancelCudaRuntimeDownload,
  enableCudaRuntime,
  disableCudaRuntime,
  type CudaRuntimeStatus,
} from "@/api/tauri";
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
  const [cudaStatus, setCudaStatus] = useState<CudaRuntimeStatus | null>(null);
  const [cudaBusy, setCudaBusy] = useState(false);
  const [cudaMismatchOpen, setCudaMismatchOpen] = useState(false);
  const [device, setDeviceState] = useState("auto");
  const [deviceBusy, setDeviceBusy] = useState(false);
  const [cudaLibsPresent, setCudaLibsPresent] = useState<boolean | null>(null);

  const refreshCuda = useCallback(async () => {
    try {
      setCudaStatus(await getCudaRuntimeStatus());
    } catch {
      setCudaStatus(null);
    }
  }, []);

  useEffect(() => {
    void refreshCuda();
    const id = window.setInterval(() => {
      void refreshCuda();
    }, 2000);
    return () => window.clearInterval(id);
  }, [refreshCuda]);

  useEffect(() => {
    let cancelled = false;
    void getEngine()
      .then((value) => {
        if (!cancelled) setEngineState(value);
      })
      .catch(() => {});
    void getDevice()
      .then((value) => {
        if (!cancelled) setDeviceState(value);
      })
      .catch(() => {});
    void getManagementConnection()
      .then((c) => {
        if (!cancelled) setCudaLibsPresent(!!c.cudaR2t2Present);
      })
      .catch(() => {
        if (!cancelled) setCudaLibsPresent(null);
      });
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

  const switchDevice = useCallback(
    async (next: string) => {
      if (deviceBusy || next === device) return;
      setDeviceBusy(true);
      try {
        const v = await setDevice(next);
        setDeviceState(v);
        toast.success(t("cube.deviceSet", { device: v }));
        retryModel();
      } catch (err) {
        toast.error(err instanceof Error ? err.message : t("toast.switchEngineFailed"));
      } finally {
        setDeviceBusy(false);
      }
    },
    [device, deviceBusy, retryModel, t],
  );

  const needsInstall =
    stage === "need_download" ||
    stage === "error" ||
    Boolean(modelError) ||
    (!isReady && !isDownloading && stage !== "ready" && stage !== "loading" && stage !== "checking");

  const enableGpu = useCallback(
    async (acknowledgeMismatch: boolean, preferSystem: boolean) => {
      setCudaBusy(true);
      try {
        await enableCudaRuntime({
          preferSystem,
          acknowledgeVersionMismatch: acknowledgeMismatch,
          reload: true,
        });
        setCudaMismatchOpen(false);
        toast.success(t("cube.cudaEnable"));
        await refreshCuda();
        retryModel();
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        const code = typeof err === "object" && err && "code" in err ? String((err as { code?: string }).code) : "";
        if (code === "version_mismatch" || msg.includes("version_mismatch") || msg.includes("version mismatch")) {
          setCudaMismatchOpen(true);
          await refreshCuda();
        } else {
          toast.error(msg || t("toast.switchEngineFailed"));
          await refreshCuda();
        }
      } finally {
        setCudaBusy(false);
      }
    },
    [refreshCuda, retryModel, t],
  );

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
        {(isDownloading === false && modelError) && (
          <p className="cube-error" role="alert">{modelError}</p>
        )}
      </section>

      <section className="cube-workbench-controls" aria-label={t("cube.deviceSection")}>
        <div className="cube-engine-group">
          <span>{t("cube.deviceSection")}</span>
          {(["auto", "cpu", "cuda", "vulkan"] as const).map((d) => (
            <button
              key={d}
              type="button"
              className={`cube-engine-btn${device === d ? " is-active" : ""}`}
              disabled={deviceBusy || isDownloading}
              aria-pressed={device === d}
              onClick={() => void switchDevice(d)}
            >
              {t(`cube.device_${d}`)}
            </button>
          ))}
        </div>
        <p className="cube-muted cube-install-status">{t("cube.deviceHint")}</p>
      </section>

      {cudaStatus?.nvidiaDetected &&
      (cudaStatus.cudaR2t2DllPresent === false ||
        (cudaStatus.cudaR2t2DllPresent == null && cudaLibsPresent === false)) ? (
        <p className="cube-error" role="status">{t("cube.cudaLibsMissing")}</p>
      ) : null}

      {cudaStatus?.nvidiaDetected ? (
        <section className="cube-workbench-controls" aria-label={t("cube.cudaSection")}>
          <div className="cube-engine-group">
            <span>{t("cube.cudaSection")}</span>
            <span className="cube-muted">
              {cudaStatus.usingCudaBackend || (cudaStatus.activeSource && cudaStatus.activeSource !== "none")
                ? t("cube.cudaStatusActive", { source: cudaStatus.activeSource || "—" })
                : t("cube.cudaStatusIdle")}
            </span>
          </div>
          {!cudaStatus.driverOk && cudaStatus.driverMessage ? (
            <p className="cube-error">{cudaStatus.driverMessage || t("cube.cudaDriverOld")}</p>
          ) : null}
          {cudaStatus.download?.active ? (
            <p className="cube-install-status" role="status">
              {t("cube.cudaDownloading")}{" "}
              {cudaStatus.download.progress != null
                ? `(${Math.round(cudaStatus.download.progress * 100)}%)`
                : ""}
              {cudaStatus.download.message ? ` — ${cudaStatus.download.message}` : ""}
            </p>
          ) : null}
          <div className="cube-engine-group">
            <button
              type="button"
              className="cube-install-btn"
              disabled={cudaBusy || !!cudaStatus.download?.active || !cudaStatus.driverOk}
              onClick={() => void enableGpu(false, true)}
            >
              {t("cube.cudaEnable")}
            </button>
            {!cudaStatus.downloadedAvailable && (
              <button
                type="button"
                className="cube-engine-btn"
                disabled={cudaBusy || !!cudaStatus.download?.active}
                onClick={() => {
                  void (async () => {
                    setCudaBusy(true);
                    try {
                      await startCudaRuntimeDownload();
                      await refreshCuda();
                    } catch (e) {
                      toast.error(e instanceof Error ? e.message : String(e));
                    } finally {
                      setCudaBusy(false);
                    }
                  })();
                }}
              >
                {t("cube.cudaDownload")}
              </button>
            )}
            {cudaStatus.download?.active && (
              <button
                type="button"
                className="cube-engine-btn"
                onClick={() => {
                  void cancelCudaRuntimeDownload().then(() => refreshCuda());
                }}
              >
                {t("cube.cudaCancelDownload")}
              </button>
            )}
            {(cudaStatus.usingCudaBackend || (cudaStatus.activeSource && cudaStatus.activeSource !== "none")) && (
              <button
                type="button"
                className="cube-engine-btn"
                disabled={cudaBusy}
                onClick={() => {
                  void (async () => {
                    setCudaBusy(true);
                    try {
                      await disableCudaRuntime();
                      await refreshCuda();
                      retryModel();
                    } catch (e) {
                      toast.error(e instanceof Error ? e.message : String(e));
                    } finally {
                      setCudaBusy(false);
                    }
                  })();
                }}
              >
                {t("cube.cudaDisable")}
              </button>
            )}
          </div>
          {cudaMismatchOpen && cudaStatus.versionWarning && (
            <div className="cube-card" role="alertdialog" aria-label={t("cube.cudaMismatchTitle")}>
              <h2>{t("cube.cudaMismatchTitle")}</h2>
              <p className="cube-muted">{cudaStatus.versionWarning}</p>
              <div className="cube-engine-group">
                <button
                  type="button"
                  className="cube-install-btn"
                  onClick={() => void enableGpu(true, true)}
                >
                  {t("cube.cudaMismatchConfirm")}
                </button>
                <button
                  type="button"
                  className="cube-engine-btn"
                  onClick={() => {
                    setCudaMismatchOpen(false);
                    void (async () => {
                      setCudaBusy(true);
                      try {
                        if (!cudaStatus.downloadedAvailable) {
                          await startCudaRuntimeDownload();
                        } else {
                          await enableGpu(false, false);
                        }
                      } finally {
                        setCudaBusy(false);
                        await refreshCuda();
                      }
                    })();
                  }}
                >
                  {t("cube.cudaMismatchDownload")}
                </button>
              </div>
            </div>
          )}
        </section>
      ) : cudaStatus && !cudaStatus.nvidiaDetected ? (
        <p className="cube-muted">{t("cube.cudaNoGpu")}</p>
      ) : null}

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
