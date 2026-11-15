import { useLayoutEffect, type ReactNode } from "react";
import { COLOUR_TOKENS, hearth, type Scheme } from "./theme";

/** Sets each colour of the scheme as a custom property on :root (the CSP forbids inline <style>). */
export function ThemeProvider({ scheme = hearth, children }: { scheme?: Scheme; children: ReactNode }) {
  useLayoutEffect(() => {
    const root = document.documentElement;
    for (const token of COLOUR_TOKENS) root.style.setProperty(`--${token}`, scheme[token]);
    return () => {
      for (const token of COLOUR_TOKENS) root.style.removeProperty(`--${token}`);
    };
  }, [scheme]);
  return children;
}
