import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  listen: vi.fn(),
  check: vi.fn(),
  start: vi.fn(),
  restart: vi.fn(),
  download: vi.fn(),
  cancel: vi.fn(),
}));

vi.mock("@tauri-apps/api/event", () => ({ listen: mocks.listen }));
vi.mock("@/api/tauri", () => ({
  checkFunASRStatus: mocks.check,
  startFunASR: mocks.start,
  restartFunASR: mocks.restart,
  downloadModels: mocks.download,
  cancelModelDownload: mocks.cancel,
}));
vi.mock("@/i18n", () => ({ default: { t: (key: string) => key } }));

import { useModelStatus } from "../useModelStatus";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

async function flushMicrotasks() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("useModelStatus funasr-status listener ownership", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.check.mockResolvedValue({
      running: true,
      ready: true,
      device: "cpu",
      gpu_name: null,
    });
    mocks.start.mockResolvedValue(undefined);
    mocks.restart.mockResolvedValue(undefined);
    mocks.download.mockResolvedValue(undefined);
    mocks.cancel.mockResolvedValue(undefined);
  });

  it("unlistens exactly once when funasr registration resolves after unmount", async () => {
    const registration = deferred<() => void>();
    const lateUnlisten = vi.fn();

    mocks.listen.mockImplementation((event: string) => {
      if (event === "funasr-status") return registration.promise;
      return Promise.resolve(vi.fn());
    });

    const hook = renderHook(() => useModelStatus());
    await flushMicrotasks();
    hook.unmount();

    await act(async () => {
      registration.resolve(lateUnlisten);
      await Promise.resolve();
    });

    expect(lateUnlisten).toHaveBeenCalledOnce();
  });

  it("ignores a late funasr-status event after the hook is disposed", async () => {
    const registration = deferred<() => void>();
    let handler:
      | ((event: { payload: { status: string; message?: string } }) => void)
      | undefined;

    mocks.listen.mockImplementation((event: string, callback: typeof handler) => {
      if (event === "funasr-status") {
        handler = callback;
        return registration.promise;
      }
      return Promise.resolve(vi.fn());
    });

    const hook = renderHook(() => useModelStatus());
    await flushMicrotasks();
    const stageBeforeUnmount = hook.result.current.stage;
    const checksBeforeEvent = mocks.check.mock.calls.length;
    hook.unmount();

    await act(async () => {
      registration.resolve(vi.fn());
      await Promise.resolve();
    });
    expect(handler).toBeDefined();

    await act(async () => {
      handler?.({ payload: { status: "crashed", message: "late event" } });
    });

    expect(mocks.check).toHaveBeenCalledTimes(checksBeforeEvent);
    expect(mocks.start).not.toHaveBeenCalled();
    expect(hook.result.current.stage).toBe(stageBeforeUnmount);
  });
});
