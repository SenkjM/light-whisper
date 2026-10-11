import { createContext, useContext, useEffect, useMemo, type ReactNode } from "react";
import { useRecording } from "@/hooks/useRecording";
import { useModelStatus, type ModelStage } from "@/hooks/useModelStatus";
import { useHotkey } from "@/hooks/useHotkey";
import { setInputMethodCommand, setAiPolishConfig, setInputDevice, setSoundEnabled, setRecordingMode } from "@/api/tauri";
import { loadAppPreferences } from "@/lib/appPreferences";
import type {
  EditGrabStatus,
  HistoryItem,
  HotkeyDiagnostic,
  RecordingMode,
  TranscriptionTiming,
} from "@/types";

interface RecordingContextValue {
  polishAuditWarning: boolean;
  // recording
  isStarting: boolean;
  isRecording: boolean;
  isProcessing: boolean;
  startRecording: () => Promise<void>;
  stopRecording: () => Promise<void>;
  recordingError: string | null;
  transcriptionResult: string | null;
  setTranscriptionResult: (text: string) => void;
  originalAsrText: string | null;
  editBaselineText: string | null;
  setEditBaselineText: (text: string | null) => void;
  durationSec: number | null;
  charCount: number | null;
  detectedLanguage: string | null;
  editGrabStatus: EditGrabStatus | null;
  timing: TranscriptionTiming | null;
  history: HistoryItem[];
  recordingMode: RecordingMode;
  // model
  stage: ModelStage;
  isReady: boolean;
  device: string | null;
  gpuName: string | null;
  downloadProgress: number;
  downloadMessage: string | null;
  isDownloading: boolean;
  modelError: string | null;
  downloadModels: () => void;
  cancelDownload: () => void;
  retryModel: () => void;
  // hotkey
  hotkeyDisplay: string;
  hotkeyError: string | null;
  setHotkey: (shortcut: string) => Promise<void>;
  hotkeyDiagnostic: HotkeyDiagnostic | null;
}

const RecordingContext = createContext<RecordingContextValue | null>(null);

export function RecordingProvider({ children }: { children: ReactNode }) {
  const {
    polishAuditWarning,
    isStarting,
    isRecording,
    isProcessing,
    startRecording,
    stopRecording,
    error: recordingError,
    transcriptionResult,
    setTranscriptionResult,
    originalAsrText,
    editBaselineText,
    setEditBaselineText,
    durationSec,
    charCount,
    detectedLanguage,
    editGrabStatus,
    timing,
    history,
    resultMode,
  } = useRecording();

  const {
    stage,
    isReady,
    device,
    gpuName,
    downloadProgress,
    downloadMessage,
    isDownloading,
    error: modelError,
    downloadModels,
    cancelDownload,
    retry: retryModel,
  } = useModelStatus();

  const {
    hotkeyDisplay,
    setHotkey,
    error: hotkeyError,
    diagnostic: hotkeyDiagnostic,
  } = useHotkey();

  // PLAN 步骤 6: shell app_preferences.json is the source of truth for business
  // prefs. loadAppPreferences migrates leftover localStorage once, then we push
  // the shell snapshot into the Rust runtime.
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const prefs = await loadAppPreferences();
        if (cancelled) return;
        const tasks: Array<{ name: string; run: () => Promise<unknown> }> = [];
        if (prefs.inputMethod === "clipboard") {
          tasks.push({
            name: "setInputMethodCommand",
            run: () => setInputMethodCommand("clipboard"),
          });
        }
        if (prefs.inputDevice) {
          tasks.push({
            name: "setInputDevice",
            run: () => setInputDevice(prefs.inputDevice ?? null),
          });
        }
        if (!prefs.soundEnabled) {
          tasks.push({
            name: "setSoundEnabled",
            run: () => setSoundEnabled(false),
          });
        }
        if (prefs.recordingToggle) {
          tasks.push({
            name: "setRecordingMode",
            run: () => setRecordingMode(true),
          });
        }
        if (prefs.aiPolishEnabled) {
          tasks.push({
            name: "setAiPolishConfig",
            run: () => setAiPolishConfig(true, ""),
          });
        }
        const results = await Promise.allSettled(tasks.map((t) => t.run()));
        results.forEach((result, idx) => {
          if (result.status === "rejected") {
            console.error(
              `[RecordingProvider startup] ${tasks[idx].name} failed:`,
              result.reason,
            );
          }
        });
      } catch (err) {
        console.error("[RecordingProvider startup] loadAppPreferences failed:", err);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const contextValue: RecordingContextValue = useMemo(() => ({
    polishAuditWarning,
    isStarting,
    isRecording,
    isProcessing,
    startRecording,
    stopRecording,
    recordingError,
    transcriptionResult,
    setTranscriptionResult,
    originalAsrText,
    editBaselineText,
    setEditBaselineText,
    durationSec,
    charCount,
    detectedLanguage,
    editGrabStatus,
    timing,
    history,
    recordingMode: resultMode,
    stage,
    isReady,
    device,
    gpuName,
    downloadProgress,
    downloadMessage,
    isDownloading,
    modelError,
    downloadModels,
    cancelDownload,
    retryModel,
    hotkeyDisplay,
    hotkeyError,
    setHotkey,
    hotkeyDiagnostic,
  }), [
    polishAuditWarning,
    isStarting, isRecording, isProcessing, startRecording, stopRecording, recordingError,
    transcriptionResult, setTranscriptionResult, originalAsrText,
    editBaselineText, setEditBaselineText,
    durationSec, charCount, detectedLanguage, editGrabStatus, timing, history, resultMode,
    stage, isReady, device, gpuName, downloadProgress, downloadMessage, isDownloading,
    modelError, downloadModels, cancelDownload, retryModel,
    hotkeyDisplay, hotkeyError, setHotkey, hotkeyDiagnostic,
  ]);

  return (
    <RecordingContext.Provider value={contextValue}>
      {children}
    </RecordingContext.Provider>
  );
}

export function useRecordingContext() {
  const ctx = useContext(RecordingContext);
  if (!ctx) throw new Error("useRecordingContext must be used within RecordingProvider");
  return ctx;
}
