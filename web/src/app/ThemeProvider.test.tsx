import { readFileSync } from "node:fs";
import { join } from "node:path";
import { render } from "@testing-library/react";
import { ThemeProvider } from "./ThemeProvider";
import { COLOUR_TOKENS, hearth, type Scheme } from "./theme";

const root = () => document.documentElement.style;

test("sets every token of the scheme on :root, and removes them when it goes", () => {
  const night: Scheme = { ...hearth, primary: "#112233" };
  const { unmount } = render(<ThemeProvider scheme={night}>x</ThemeProvider>);
  expect(root().getPropertyValue("--primary")).toBe("#112233");
  for (const t of COLOUR_TOKENS) expect(root().getPropertyValue(`--${t}`)).not.toBe("");
  unmount();
  expect(root().getPropertyValue("--primary")).toBe("");
});

test("a new scheme replaces the old one's values", () => {
  const { rerender } = render(<ThemeProvider>x</ThemeProvider>);
  expect(root().getPropertyValue("--primary")).toBe(hearth.primary);
  rerender(<ThemeProvider scheme={{ ...hearth, primary: "#abcdef" }}>x</ThemeProvider>);
  expect(root().getPropertyValue("--primary")).toBe("#abcdef");
});

test("styles/tokens.css holds the Hearth scheme as its defaults", () => {
  const css = readFileSync(join(process.cwd(), "src/styles/tokens.css"), "utf8");
  for (const t of COLOUR_TOKENS) {
    expect(css).toContain(`--${t}: ${hearth[t]};`);
  }
});
