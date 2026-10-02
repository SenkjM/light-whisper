import { act, render } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
const events = await vi.hoisted(async () => {
  const { createTauriEventController } = await import("@/test/tauriEventMock");
  return createTauriEventController();
});
vi.mock("@tauri-apps/api/event", () => events.module);
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
import RecordingButton from "./RecordingButton";

afterEach(() => events.reset());

it("renders live centered waveform bars, ignores older sessions, and disposes its subscription", async () => {
  const view = render(<RecordingButton isStarting={false} isRecording isProcessing={false} isReady onToggle={vi.fn()} />);
  await act(async () => { await Promise.resolve(); });
  act(() => {
    events.emit("waveform", { sessionId: 2, bars: [0.1, 0.2, 0.3, 0.4, 0.5] });
  });
  const heights = () => [...view.container.querySelectorAll<HTMLElement>(".eq-bar")].map((bar) => bar.style.transform);
  expect(new Set(heights()).size).toBeGreaterThan(1);
  const previous = heights();
  act(() => events.emit("waveform", { sessionId: 1, bars: [1, 1, 1, 1, 1] }));
  expect(heights()).toEqual(previous);
  expect(view.container.querySelectorAll(".record-icon")).toHaveLength(3);
  const disposer = vi.fn<() => void>();
  // A second mount covers a subscription that resolves after unmount.
  events.listen.mockResolvedValueOnce(disposer);
  const late = render(<RecordingButton isStarting={false} isRecording isProcessing={false} isReady onToggle={vi.fn()} />);
  late.unmount();
  await act(async () => { await Promise.resolve(); });
  expect(disposer).toHaveBeenCalledOnce();
});
