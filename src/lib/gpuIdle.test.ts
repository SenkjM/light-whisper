import { describe, expect, it } from "vitest";
import en from "@/i18n/en";
import zh from "@/i18n/zh";
import { GPU_IDLE_OFF_SECONDS, GPU_IDLE_SUGGESTED_SECONDS, normalizeGpuIdleSeconds } from "@/lib/gpuIdle";

describe("gpu idle setting", () => {
  it("defaults to off and only accepts a positive whole number of seconds", () => {
    expect(GPU_IDLE_OFF_SECONDS).toBe(0);
    expect(normalizeGpuIdleSeconds(undefined)).toBe(0);
    expect(normalizeGpuIdleSeconds(0)).toBe(0);
    expect(normalizeGpuIdleSeconds(-4)).toBe(0);
    expect(normalizeGpuIdleSeconds(Number.NaN)).toBe(0);
    expect(normalizeGpuIdleSeconds(180.9)).toBe(180);
    expect(GPU_IDLE_SUGGESTED_SECONDS).toBe(180);
  });

  it("shows an honest reload estimate for both local models", () => {
    for (const copy of [zh.settings, en.settings]) {
      expect(copy.gpuReloadR2t2).toContain("4.8");
      expect(copy.gpuReloadR2t2).toContain("5.5");
      expect(copy.gpuReloadR2t2).toContain("0.19");
      expect(copy.gpuReloadR2t2).toContain("RTX 4070 SUPER");
      expect(copy.gpuReloadR2t2).toContain("docs/r2t2-native.md");
      expect(copy.gpuReloadQwen).toMatch(/850/);
      expect(copy.gpuReloadQwen.toLowerCase()).toContain("32k");
      expect(copy.gpuReloadQwen.toLowerCase()).toContain("f16");
      expect(copy.gpuReloadQwen.toLowerCase()).toMatch(/unknown|未知/);
      expect(copy.gpuIdleDesc.toLowerCase()).toMatch(/off by default|默认关闭/);
    }
  });
});
