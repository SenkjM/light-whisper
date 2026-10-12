// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

import { useCallback, useEffect, useState } from "react";
import { listen } from "@tauri-apps/api/event";
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
  listLocalModelStatus,
  type CudaRuntimeStatus,
} from "@/api/tauri";
import type { LocalModelsInventory } from "@/types";
import { useRecordingContext } from "@/contexts/RecordingContext";
import MainPage from "@/pages/MainPage";
import HistoryPage from "@/pages/HistoryPage";

export type WorkbenchSub = "dictation" | "history";

const LOCAL_ENGINES = [
  { key: "qwen3-asr-0.6b", labelKey: "cube.engineQwen" },
  { key: "confucius4-r2t2", labelKey: "cube.engineR2t2" },
] as const;

/** Visible progress track (0–100). Indeterminate when percent is null. */
function CubeProgressBar({
  percent,
  label,
  complete,
}: {
  percent: number | null;
  label: string;
  complete?: boolean;
}) {
  // percent null → indeterminate pulse; any number (incl. 0) → determinate fill.
  const determinate = percent != null && Number.isFinite(percent);
  const width = determinate ? Math.max(0, Math.min(100, percent)) : 0;
  return (
    <div className="cube-progress" role="status">
      <div className="cube-progress-meta">
        <span>{label}</span>
        {determinate ? <span>{Math.round(width)}%</span> : <span>…</span>}
      </div>
      <div
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={determinate ? Math.round(width) : undefined}
        aria-label={label}
        className={`cube-progress-track${complete ? " progress-complete" : ""}`}
      >
        {determinate ? (
          <div
            className="cube-progress-fill"
            style={{
              width: `${Math.max(width, width > 0 ? width : 2)}%`,
              background: complete ? "var(--color-success, #2e8b57)" : "var(--color-accent, #e11d48)",
            }}
          />
        ) : (
          <div className="download-pulse-bar" />
        )}
      </div>
    </div>
  );
}

function engineLabelKey(engine: string): string {
  return LOCAL_ENGINES.find((e) => e.key === engine)?.labelKey ?? "cube.engineQwen";
}

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
  const [device, setDeviceState] = useState("auto");
  const [deviceBusy, setDeviceBusy] = useState(false);
  const [cudaLibsPresent, setCudaLibsPresent] = useState<boolean | null>(null);
  const [inferenceBackend, setInferenceBackend] = useState<string | null>(null);
  const [nativeLibsPresent, setNativeLibsPresent] = useState<boolean | null>(null);
  const [modelInventory, setModelInventory] = useState<LocalModelsInventory | null>(null);

  const refreshCuda = useCallback(async () => {
    try {
      setCudaStatus(await getCudaRuntimeStatus());
    } catch {
      setCudaStatus(null);
    }
  }, []);

  const refreshModels = useCallback(async () => {
    try {
      setModelInventory(await listLocalModelStatus());
    } catch {
      setModelInventory(null);
    }
  }, []);

  const refreshConnectionMeta = useCallback(async () => {
    try {
      const c = await getManagementConnection();
      setCudaLibsPresent(!!c.cudaR2t2Present);
      setInferenceBackend(c.inferenceBackend ?? null);
      setNativeLibsPresent(
        typeof c.nativeLibsPresent === "boolean" ? c.nativeLibsPresent : null,
      );
    } catch {
      setCudaLibsPresent(null);
      setInferenceBackend(null);
      setNativeLibsPresent(null);
    }
  }, []);

  useEffect(() => {
    void refreshCuda();
    void refreshModels();
    void refreshConnectionMeta();
    const id = window.setInterval(() => {
      void refreshCuda();
      void refreshModels();
      void refreshConnectionMeta();
    }, 1500);
    let disposed = false;
    let unlisten: (() => void) | undefined;
    void listen("go-engine-download-progress", () => {
      if (!disposed) void refreshCuda();
    }).then((fn) => {
      unlisten = fn;
    });
    return () => {
      disposed = true;
      window.clearInterval(id);
      unlisten?.();
    };
  }, [refreshCuda, refreshModels, refreshConnectionMeta]);

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
    return () => {
      cancelled = true;
    };
  }, []);

  // Refresh model inventory when download finishes / engine becomes ready.
  useEffect(() => {
    if (!isDownloading) {
      void refreshModels();
    }
  }, [isDownloading, isReady, stage, refreshModels]);

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
        await refreshModels();
      } catch {
        toast.error(t("toast.switchEngineFailed"));
      } finally {
        setEngineBusy(false);
      }
    },
    [engine, engineBusy, retryModel, refreshModels, t],
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
    async (_acknowledgeMismatch: boolean, preferSystem: boolean) => {
      setCudaBusy(true);
      try {
        // Version mismatch is remind-only — always acknowledge so enable never blocks.
        await enableCudaRuntime({
          preferSystem,
          acknowledgeVersionMismatch: true,
          reload: true,
        });
        toast.success(t("cube.cudaEnable"));
        await refreshCuda();
        retryModel();
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        toast.error(msg || t("toast.switchEngineFailed"));
        await refreshCuda();
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


  const nvidiaDetected = !!(
    cudaStatus?.nvidiaDetected ??
    (cudaStatus as { nvidia_detected?: boolean } | null)?.nvidia_detected
  );
  const cudaDllPresent =
    cudaStatus?.cudaR2t2DllPresent ??
    (cudaStatus as { cuda_r2t2_dll_present?: boolean } | null)?.cuda_r2t2_dll_present;
  const driverOk =
    cudaStatus?.driverOk ??
    (cudaStatus as { driver_ok?: boolean } | null)?.driver_ok;
  const downloadedAvailable =
    cudaStatus?.downloadedAvailable ??
    (cudaStatus as { downloaded_available?: boolean } | null)?.downloaded_available;
  const usingCuda =
    !!(
      cudaStatus?.usingCudaBackend ??
      (cudaStatus as { using_cuda_backend?: boolean } | null)?.using_cuda_backend
    ) || !!(cudaStatus?.activeSource && cudaStatus.activeSource !== "none");
  const driverMessage =
    cudaStatus?.driverMessage ??
    (cudaStatus as { driver_message?: string } | null)?.driver_message;
  const versionWarning =
    cudaStatus?.versionWarning ??
    (cudaStatus as { version_warning?: string } | null)?.version_warning
    ?? (cudaStatus?.versionMismatch ? t("cube.cudaMismatchRemind") : undefined);

  const cudaDownloadActive = !!cudaStatus?.download?.active;
  const cudaProgressRaw = cudaStatus?.download?.progress;
  const cudaBytesDone = cudaStatus?.download?.bytesDone;
  const cudaBytesTotal = cudaStatus?.download?.bytesTotal;
  // Engine reports 0–1; tolerate accidental 0–100; fall back to bytes ratio.
  let cudaProgressPercent: number | null =
    cudaProgressRaw == null
      ? null
      : cudaProgressRaw <= 1
        ? cudaProgressRaw * 100
        : cudaProgressRaw;
  if (
    cudaProgressPercent == null &&
    typeof cudaBytesDone === "number" &&
    typeof cudaBytesTotal === "number" &&
    cudaBytesTotal > 0
  ) {
    cudaProgressPercent = (cudaBytesDone / cudaBytesTotal) * 100;
  }
  const modelProgressPercent =
    isDownloading
      ? downloadProgress > 0
        ? downloadProgress
        : null // indeterminate pulse until first byte event
      : null;

  const isMockBackend = (inferenceBackend ?? "").toLowerCase() === "mock";

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

      {isMockBackend ? (
        <p className="cube-error cube-banner" role="alert">
          {t("cube.inferenceMockWarning")}
        </p>
      ) : null}

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
            void refreshModels();
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
        {isDownloading ? (
          <CubeProgressBar
            percent={modelProgressPercent}
            label={downloadMessage || t("cube.installingModels")}
            complete={downloadProgress >= 100}
          />
        ) : null}
        <p className="cube-muted cube-install-status">{t("cube.engineInstallHint")}</p>
        {(isDownloading === false && modelError) && (
          <p className="cube-error" role="alert">{modelError}</p>
        )}
      </section>

      <section className="cube-workbench-controls" aria-label={t("cube.modelInventoryTitle")}>
        <div className="cube-engine-group">
          <span>{t("cube.modelInventoryTitle")}</span>
        </div>
        <p className="cube-muted cube-install-status">{t("cube.modelInventoryHint")}</p>
        <ul className="cube-model-list">
          {(modelInventory?.engines?.length
            ? modelInventory.engines
            : LOCAL_ENGINES.map(({ key }) => ({
                engine: key,
                present: false,
                missing_models: [] as string[],
              }))
          ).map((row) => {
            const isCurrent =
              (modelInventory?.current_engine || engine) === row.engine ||
              engine === row.engine;
            const known = !!modelInventory;
            return (
              <li key={row.engine} className="cube-model-row">
                <span className="cube-model-name">
                  {t(engineLabelKey(row.engine))}
                  {isCurrent ? (
                    <span className="cube-model-badge">{t("cube.modelCurrent")}</span>
                  ) : null}
                </span>
                <span
                  className={
                    known
                      ? row.present
                        ? "cube-model-ok"
                        : "cube-model-missing"
                      : "cube-muted"
                  }
                >
                  {known
                    ? row.present
                      ? t("cube.modelPresent")
                      : t("cube.modelMissing")
                    : "…"}
                </span>
                {!row.present && row.missing_models?.length ? (
                  <span className="cube-muted cube-model-detail">
                    {row.missing_models.join(", ")}
                  </span>
                ) : null}
              </li>
            );
          })}
        </ul>
        {modelInventory?.cache_path ? (
          <p className="cube-muted cube-install-status mono">
            {t("cube.modelCachePath")}: {modelInventory.cache_path}
          </p>
        ) : null}
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
        <p className="cube-muted cube-install-status">
          {t("cube.inferenceBackend")}:{" "}
          <strong className="mono">{inferenceBackend ?? "—"}</strong>
          {" · "}
          {t("cube.nativeLibs")}:{" "}
          {nativeLibsPresent == null
            ? "—"
            : nativeLibsPresent
              ? t("cube.inferenceNativeOk")
              : t("cube.inferenceNativeMissing")}
        </p>
      </section>

      {nvidiaDetected &&
      (cudaDllPresent === false ||
        (cudaDllPresent == null && cudaLibsPresent === false)) ? (
        <p className="cube-error" role="status">{t("cube.cudaLibsMissing")}</p>
      ) : null}

      {nvidiaDetected ? (
        <section className="cube-workbench-controls" aria-label={t("cube.cudaSection")}>
          <div className="cube-engine-group">
            <span>{t("cube.cudaSection")}</span>
            <span className="cube-muted">
              {usingCuda
                ? t("cube.cudaStatusActive", { source: cudaStatus?.activeSource || "—" })
                : t("cube.cudaStatusIdle")}
            </span>
          </div>
          {!driverOk && driverMessage ? (
            <p className="cube-error">{driverMessage || t("cube.cudaDriverOld")}</p>
          ) : null}
          {cudaDownloadActive ? (
            <>
              <p className="cube-install-status" role="status">
                {t("cube.cudaDownloading")}
                {cudaStatus?.download?.message
                  ? ` — ${cudaStatus?.download?.message}`
                  : ""}
              </p>
              <CubeProgressBar
                percent={cudaProgressPercent}
                label={t("cube.cudaDownloading")}
                complete={(cudaProgressPercent ?? 0) >= 100}
              />
            </>
          ) : null}
          <div className="cube-engine-group">
            <button
              type="button"
              className="cube-install-btn"
              disabled={cudaBusy || cudaDownloadActive || !driverOk}
              onClick={() => void enableGpu(false, true)}
            >
              {t("cube.cudaEnable")}
            </button>
            {!downloadedAvailable && (
              <button
                type="button"
                className="cube-engine-btn"
                disabled={cudaBusy || cudaDownloadActive}
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
            {cudaDownloadActive && (
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
            {usingCuda && (
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
          {versionWarning ? (
            <div className="cube-card cube-warn-banner" role="status" aria-label={t("cube.cudaMismatchTitle")}>
              <h2>{t("cube.cudaMismatchTitle")}</h2>
              <p className="cube-muted">{t("cube.cudaMismatchRemind")}</p>
              <p className="cube-muted">{versionWarning}</p>
              <div className="cube-engine-group">
                <button
                  type="button"
                  className="cube-engine-btn"
                  disabled={cudaBusy || cudaDownloadActive}
                  onClick={() => {
                    void (async () => {
                      setCudaBusy(true);
                      try {
                        if (!downloadedAvailable) {
                          await startCudaRuntimeDownload();
                        } else {
                          await enableGpu(true, false);
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
          ) : null}
        </section>
      ) : cudaStatus && !nvidiaDetected ? (
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
