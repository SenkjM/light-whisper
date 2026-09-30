import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import zh from "@/i18n/zh";

const api = vi.hoisted(() => ({
  getGpuIdleSeconds: vi.fn(),
  setGpuIdleSeconds: vi.fn(),
}));

vi.mock("@/api/tauri", () => api);
vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));
vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => {
      const [, name] = key.split(".");
      return zh.settings[name as keyof typeof zh.settings] as string;
    },
  }),
}));

import GpuIdleUnloadControl from "./GpuIdleUnloadControl";

describe("GpuIdleUnloadControl", () => {
  beforeEach(() => {
    api.getGpuIdleSeconds.mockReset();
    api.setGpuIdleSeconds.mockReset();
    api.getGpuIdleSeconds.mockResolvedValue(0);
    api.setGpuIdleSeconds.mockImplementation(async (seconds: number) => seconds);
  });

  it("starts off and shows both reload estimates", async () => {
    render(<GpuIdleUnloadControl />);
    const off = await screen.findByRole("button", { name: zh.settings.gpuIdleOff });
    expect(off).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: zh.settings.gpuIdleOn })).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByText(zh.settings.gpuReloadR2t2)).toBeInTheDocument();
    expect(screen.getByText(zh.settings.gpuReloadQwen)).toBeInTheDocument();
    expect(screen.queryByLabelText(zh.settings.gpuIdleSeconds)).not.toBeInTheDocument();
  });

  it("turns on at the suggested 180 seconds and can be switched back off", async () => {
    render(<GpuIdleUnloadControl />);
    fireEvent.click(await screen.findByRole("button", { name: zh.settings.gpuIdleOn }));
    await waitFor(() => expect(api.setGpuIdleSeconds).toHaveBeenCalledWith(180));
    const input = await screen.findByLabelText(zh.settings.gpuIdleSeconds);
    expect(input).toHaveValue(180);
    fireEvent.click(screen.getByRole("button", { name: zh.settings.gpuIdleOff }));
    await waitFor(() => expect(api.setGpuIdleSeconds).toHaveBeenLastCalledWith(0));
    await waitFor(() => expect(screen.queryByLabelText(zh.settings.gpuIdleSeconds)).not.toBeInTheDocument());
  });
});
