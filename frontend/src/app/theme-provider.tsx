import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
export type ThemeMode = "dark" | "light";
interface ThemeContextValue {
  mode: ThemeMode;
  setMode: (mode: ThemeMode) => void;
}
const ThemeContext = createContext<ThemeContextValue | null>(null);
const themeStorageKey = "account-hub-theme:v1";
function loadTheme(): ThemeMode {
  try {
    const value = localStorage.getItem(themeStorageKey);
    return value === "dark" || value === "light" ? value : "dark";
  }
  catch {
    return "dark";
  }
}
export function ThemeProvider({ children }: {
  children: ReactNode;
}) {
  const [mode, setMode] = useState<ThemeMode>(loadTheme);
  useEffect(() => {
    const dark = mode === "dark";
    document.documentElement.classList.toggle("dark", dark);
    document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')?.setAttribute("content", dark ? "#070807" : "#f5f6f7");
    try {
      localStorage.setItem(themeStorageKey, mode);
    }
    catch { /* Storage may be disabled. */ }
  }, [mode]);
  const value = useMemo(() => ({ mode, setMode }), [mode]);
  return <ThemeContext.Provider value={value}>
    {children}</ThemeContext.Provider>;
}
export function useTheme() {
  const value = useContext(ThemeContext);
  if (!value)
    throw new Error("useTheme must be used inside ThemeProvider");
  return value;
}
