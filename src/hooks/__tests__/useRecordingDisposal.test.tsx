import { StrictMode } from "react";
import { act, renderHook } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

const events = vi.hoisted(() => ({
  registrations: [] as {
    event: string;
    handler: (event: { payload: unknown }) => void;
    resolve: (unlisten: () => void) => void;
  }[],
  success: vi.fn(),
}));

vi.mock("@tauri-apps/api/event", () => ({
  listen: (event: string, handler: (event: { payload: unknown }) => void) =>
    new Promise<() => void>((resolve) => {
      events.registrations.push({ event, handler, resolve });
    }),
}));
vi.mock("@/api/tauri", () => ({ startRecording: vi.fn(), stopRecording: vi.fn() }));
vi.mock("sonner", () => ({ toast: { success: events.success, error: vi.fn(), warning: vi.fn() } }));
vi.mock("@/i18n", () => ({ default: { t: (key: string) => key } }));

import { useRecording } from "@/hooks/useRecording";

beforeEach(() => {
  events.registrations.length = 0;
  events.success.mockClear();
});

it("ignores events after unmount while listener registration is pending", async () => {
  const { unmount } = renderHook(() => useRecording());
  const registration = events.registrations.find((entry) => entry.event === "ai-polish-status")!;
  unmount();
  act(() => registration.handler({ payload: { status: "applied", error: "" } }));
  expect(events.success).not.toHaveBeenCalled();
  const unlisten = vi.fn();
  await act(async () => registration.resolve(unlisten));
  expect(unlisten).toHaveBeenCalledOnce();
});

it("ignores the retired listener after StrictMode replays the effect", () => {
  const { unmount } = renderHook(() => useRecording(), { wrapper: StrictMode });
  const registrations = events.registrations.filter((entry) => entry.event === "ai-polish-status");
  expect(registrations).toHaveLength(2);
  act(() => registrations[0].handler({ payload: { status: "applied", error: "" } }));
  expect(events.success).not.toHaveBeenCalled();
  act(() => registrations[1].handler({ payload: { status: "applied", error: "" } }));
  expect(events.success).toHaveBeenCalledOnce();
  unmount();
});
