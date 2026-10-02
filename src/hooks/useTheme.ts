import { useState, useEffect } from "react";
import { readLocalStorage, writeLocalStorage } from "@/lib/storage";
import { THEME_STORAGE_KEY } from "@/lib/constants";
import { prefersReducedMotion } from "@/lib/motion";

export type ThemeMode = "light" | "dark" | "system";

interface UseThemeReturn {
  theme: ThemeMode;
  isDark: boolean;
  setTheme: (mode: ThemeMode) => void;
}

function getSystemPrefersDark(): boolean {
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

function resolveIsDark(mode: ThemeMode): boolean {
  if (mode === "system") return getSystemPrefersDark();
  return mode === "dark";
}

let isFirstApply = true;
let activeTransition: ViewTransition | undefined;
let themeRevision = 0;

function applyThemeToDOM(isDark: boolean): void {
  const root = document.documentElement;

  const revision = ++themeRevision;
  activeTransition?.skipTransition();
  const nextTheme = isDark ? "dark" : "light";
  if (root.dataset.theme === nextTheme) return;
  const apply = () => {
    // A rapid reversal may happen before the earlier snapshot callback runs.
    if (revision !== themeRevision) return;
    root.classList.add("no-transition");
    root.classList.toggle("dark", isDark);
    root.dataset.theme = nextTheme;
    void root.offsetHeight; // force reflow
    root.classList.remove("no-transition");
  };
  if (isFirstApply || prefersReducedMotion() || !document.startViewTransition) {
    isFirstApply = false;
    apply();
  } else {
    activeTransition = document.startViewTransition(apply);
    void activeTransition.finished.catch(() => undefined);
  }
}

/**
 * React hook for theme management.
 * Supports light, dark, and system-following modes.
 * Persists the user's choice to localStorage and applies the
 * corresponding class / data-attribute to <html>.
 */
export function useTheme(): UseThemeReturn {
  const [theme, setThemeState] = useState<ThemeMode>(() => {
    const stored = readLocalStorage(THEME_STORAGE_KEY);
    if (stored === "light" || stored === "dark" || stored === "system") {
      return stored;
    }
    return "system";
  });

  const [isDark, setIsDark] = useState(() => resolveIsDark(theme));

  // Apply theme whenever it changes
  useEffect(() => {
    const dark = resolveIsDark(theme);
    setIsDark(dark);
    applyThemeToDOM(dark);
    writeLocalStorage(THEME_STORAGE_KEY, theme);
  }, [theme]);

  // Listen for system preference changes when in "system" mode
  useEffect(() => {
    if (theme !== "system") return;

    const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)");

    const handler = (e: MediaQueryListEvent) => {
      setIsDark(e.matches);
      applyThemeToDOM(e.matches);
    };

    mediaQuery.addEventListener("change", handler);
    return () => mediaQuery.removeEventListener("change", handler);
  }, [theme]);

  return { theme, isDark, setTheme: setThemeState };
}
