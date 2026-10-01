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

  it("keeps the settings copy short and does not mention reload times", () => {
    for (const copy of [zh.settings, en.settings]) {
      expect(copy.gpuIdleDesc.toLowerCase()).toMatch(/off by default|默认关闭/);
      const shown = `${copy.gpuIdleTitle}\n${copy.gpuIdleDesc}\n${copy.gpuIdleSeconds}`.toLowerCase();
      expect(shown).not.toMatch(/4\.8|5\.5|0\.19|冷启动|预热|warmup|qwen3-asr|r2t2/);
      expect(copy.gpuIdleDesc.split(/\n/).length).toBeLessThanOrEqual(2);
    }
  });
});
