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

  it("starts off with the shared settings switch and no reload estimates", async () => {
    render(<GpuIdleUnloadControl />);
    const toggle = await screen.findByRole("switch", { name: zh.settings.gpuIdleTitle });
    expect(toggle).toHaveAttribute("aria-checked", "false");
    expect(toggle).toHaveClass("toggle-switch");
    expect(screen.getByText(zh.settings.gpuIdleDesc)).toBeInTheDocument();
    expect(screen.queryByRole("spinbutton")).not.toBeInTheDocument();
    expect(screen.queryByLabelText(zh.settings.gpuIdleSeconds)).not.toBeInTheDocument();
    expect(screen.queryByText(/冷启动|预热|4\.8|5\.5|RTX 4070/)).not.toBeInTheDocument();
  });

  it("turns on at the suggested 180 seconds with a plain text field and can be switched back off", async () => {
    render(<GpuIdleUnloadControl />);
    fireEvent.click(await screen.findByRole("switch", { name: zh.settings.gpuIdleTitle }));
    await waitFor(() => expect(api.setGpuIdleSeconds).toHaveBeenCalledWith(180));
    const input = await screen.findByLabelText(zh.settings.gpuIdleSeconds);
    expect(input).toHaveAttribute("type", "text");
    expect(input).toHaveValue("180");
    expect(screen.queryByRole("spinbutton")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("switch", { name: zh.settings.gpuIdleTitle }));
    await waitFor(() => expect(api.setGpuIdleSeconds).toHaveBeenLastCalledWith(0));
    await waitFor(() => expect(screen.queryByLabelText(zh.settings.gpuIdleSeconds)).not.toBeInTheDocument());
  });
});
