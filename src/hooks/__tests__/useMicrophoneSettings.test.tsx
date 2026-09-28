import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useMicrophoneSettings } from "../useMicrophoneSettings";

const mocks = vi.hoisted(() => ({
  listen: vi.fn(), start: vi.fn(), stop: vi.fn(),
  devices: vi.fn(), select: vi.fn(), test: vi.fn(),
}));
const translate = (key: string) => key;
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: translate }) }));
vi.mock("@tauri-apps/api/event", () => ({ listen: mocks.listen }));
vi.mock("@/api/tauri", () => ({
  listInputDevices: mocks.devices, setInputDevice: mocks.select,
  startMicrophoneLevelMonitor: mocks.start, stopMicrophoneLevelMonitor: mocks.stop,
  testMicrophone: mocks.test,
}));
vi.mock("@/lib/storage", () => ({ readLocalStorage: () => "true", writeLocalStorage: vi.fn() }));

beforeEach(() => {
  vi.resetAllMocks();
  mocks.devices.mockResolvedValue({ devices: [], selectedDeviceName: null });
  mocks.select.mockResolvedValue(undefined);
  mocks.stop.mockResolvedValue(undefined);
  mocks.start.mockResolvedValue("test microphone");
});

describe("microphone monitor effect ownership", () => {
  it("does not restart monitoring when event registration completes after unmount", async () => {
    let resolve!: (unlisten: () => void) => void;
    mocks.listen.mockReturnValue(new Promise<() => void>((done) => { resolve = done; }));
    const unlisten = vi.fn();
    const hook = renderHook(() => useMicrophoneSettings({
      active: true, isRecording: false, closePicker: vi.fn(),
    }));
    hook.unmount();
    await act(async () => { resolve(unlisten); });
    expect(unlisten).toHaveBeenCalledOnce();
    expect(mocks.start).not.toHaveBeenCalled();
  });
});
