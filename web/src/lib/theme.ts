import { readStorage } from "@/lib/storage";

export type Theme = "light" | "dark" | "system";
export const THEME_STORAGE_KEY = "tolarix-theme";

const isTheme = (v: unknown): v is Theme => v === "light" || v === "dark" || v === "system";

export function readStoredTheme(): Theme {
  const v = readStorage(THEME_STORAGE_KEY);
  return isTheme(v) ? v : "system";
}

function systemPrefersDark(): boolean {
  return typeof window.matchMedia === "function"
    ? window.matchMedia("(prefers-color-scheme: dark)").matches
    : false;
}

/** Applies the theme class to <html>. Called before the first render to avoid a flash. */
export function applyTheme(theme: Theme) {
  const dark = theme === "dark" || (theme === "system" && systemPrefersDark());
  const root = document.documentElement;
  root.classList.toggle("dark", dark);
  root.style.colorScheme = dark ? "dark" : "light";
}
