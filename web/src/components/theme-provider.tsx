import { useEffect, useMemo, useState } from "react";

import { writeStorage } from "@/lib/storage";
import { ThemeContext, type ThemeContextValue } from "@/lib/theme-context";
import { applyTheme, readStoredTheme, THEME_STORAGE_KEY, type Theme } from "@/lib/theme";

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(readStoredTheme);

  useEffect(() => {
    applyTheme(theme);
    if (theme !== "system" || typeof window.matchMedia !== "function") return;
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => applyTheme("system");
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [theme]);

  const value = useMemo<ThemeContextValue>(
    () => ({
      theme,
      setTheme: (t) => {
        writeStorage(THEME_STORAGE_KEY, t);
        setThemeState(t);
      },
    }),
    [theme],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}
