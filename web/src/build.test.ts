import { mkdtempSync, readdirSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFileSync } from "node:child_process";

// DEPLOY-T5: the built app may not reference any other host. Every http(s) URL
// in the output must start with one of these; each is text that is never
// requested.
const ALLOWED_PREFIXES = [
  // XML namespace URIs (SVG, XLink, MathML, XML): identifiers, not addresses.
  "http://www.w3.org/",
  // React's and React Router's error messages name a docs page; nothing fetches it.
  "https://react.dev/errors/",
  "https://reactrouter.com/",
  // Router code resolves relative URLs against a dummy base to parse them.
  "http://localhost",
];

export function externalUrls(files: Record<string, string>): string[] {
  const found: string[] = [];
  for (const [name, text] of Object.entries(files)) {
    for (const url of text.match(/https?:\/\/[^\s"'`)<>\\]+/gi) ?? []) {
      if (!ALLOWED_PREFIXES.some((p) => url.startsWith(p))) found.push(`${name}: ${url}`);
    }
  }
  return found;
}

function readOutput(dir: string): Record<string, string> {
  const files: Record<string, string> = {};
  for (const entry of readdirSync(dir, { recursive: true, withFileTypes: true })) {
    if (entry.isFile() && /\.(html|css|js|mjs)$/.test(entry.name)) {
      const path = join(entry.parentPath, entry.name);
      files[path.slice(dir.length + 1)] = readFileSync(path, "utf8");
    }
  }
  return files;
}

test("the scan itself finds a foreign host and lets the allowed ones by", () => {
  expect(
    externalUrls({
      "a.css": '@import url("https://fonts.googleapis.com/css2?family=Nunito");',
      "b.js": 'x="http://www.w3.org/2000/svg";y="https://cdn.example.com/lib.js"',
      "c.html": '<script src="//cdn.example.com/x.js"></script><link href="http://example.org/s.css">',
    }),
  ).toEqual([
    "a.css: https://fonts.googleapis.com/css2?family=Nunito",
    "b.js: https://cdn.example.com/lib.js",
    "c.html: http://example.org/s.css",
  ]);
});

test("DEPLOY-T5: the built app loads nothing from another host", () => {
  const out = mkdtempSync(join(tmpdir(), "jl-build-"));
  try {
    // A child process, because vitest runs with NODE_ENV=test, which would build React's
    // development bundle (more link texts) instead of what `npm run build` ships.
    execFileSync(
      process.execPath,
      [join(process.cwd(), "node_modules/vite/bin/vite.js"), "build", "--outDir", out, "--emptyOutDir"],
      { cwd: process.cwd(), env: { ...process.env, NODE_ENV: "production" }, stdio: "pipe" },
    );
    const files = readOutput(out);
    expect(Object.keys(files).some((f) => f === "index.html")).toBe(true);
    expect(Object.keys(files).some((f) => f.endsWith(".css"))).toBe(true);
    expect(Object.keys(files).some((f) => f.endsWith(".js"))).toBe(true);
    expect(externalUrls(files)).toEqual([]);
    // Fonts are files of ours, not data: URLs (the CSP's font-src is 'self').
    const woff = readdirSync(out, { recursive: true, encoding: "utf8" }).filter((f) => f.endsWith(".woff2"));
    expect(woff.length).toBeGreaterThan(0);
    expect(Object.values(files).join("")).not.toContain("data:font");
  } finally {
    rmSync(out, { recursive: true, force: true });
  }
}, 120_000);
