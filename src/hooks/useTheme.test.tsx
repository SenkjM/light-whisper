import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useTheme } from "./useTheme";

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); localStorage.clear(); });

it("changes themes immediately without support and uses one transition when supported, except for reduced motion", () => {
  let reduced = false;
  vi.stubGlobal("matchMedia", vi.fn((query: string) => ({
    matches: query.includes("reduced-motion") && reduced,
    addEventListener: vi.fn(), removeEventListener: vi.fn(),
  })));
  document.documentElement.removeAttribute("data-theme");
  const { result } = renderHook(() => useTheme());
  expect(document.documentElement.dataset.theme).toBe("light");
  act(() => result.current.setTheme("dark"));
  expect(document.documentElement.dataset.theme).toBe("dark");
  expect(document.documentElement).not.toHaveClass("no-transition");

  const skip = vi.fn();
  const transition = vi.fn((apply: () => void) => {
    apply();
    return { finished: Promise.resolve(), skipTransition: skip };
  });
  Object.defineProperty(document, "startViewTransition", { configurable: true, value: transition });
  act(() => result.current.setTheme("light"));
  expect(transition).toHaveBeenCalledOnce();
  expect(document.documentElement.dataset.theme).toBe("light");
  act(() => result.current.setTheme("dark"));
  expect(skip).toHaveBeenCalledOnce();
  reduced = true;
  act(() => result.current.setTheme("light"));
  expect(transition).toHaveBeenCalledTimes(2);
  expect(document.documentElement.dataset.theme).toBe("light");
  reduced = false;
  let pendingApply: (() => void) | undefined;
  transition.mockImplementation((apply) => {
    pendingApply = apply;
    return { finished: Promise.resolve(), skipTransition: skip };
  });
  act(() => result.current.setTheme("dark"));
  act(() => result.current.setTheme("light"));
  act(() => pendingApply?.());
  expect(document.documentElement.dataset.theme).toBe("light");
  delete (document as Partial<Document>).startViewTransition;
});
