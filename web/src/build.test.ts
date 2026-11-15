import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
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
  // ScanField's link to the trust guide: the host is the page's own (location.hostname).
  "http://${window.location.hostname}:8080/trust/",
  // zxing-wasm's default place to fetch its wasm. It is only text here: barcodeDetector.ts
  // overrides it to the bundled file (its test proves that), and the build test below checks
  // the wasm is in the build. Allowed by this exact text, so a different CDN still fails.
  "https://fastly.jsdelivr.net/npm/zxing-wasm@",
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

// vite's own CLI builds index.html's graph; the camera decoder is only imported by ScanField, so
// this builds a stand-in entry that imports it (as a screen will), with the project's config.
const BUILD_ENTRY = `
import { build } from "vite";
await build({
  configFile: "vite.config.ts",
  logLevel: "warn",
  build: { outDir: process.argv[1], emptyOutDir: true, rollupOptions: { input: process.argv[2] } },
});
`;

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

test("DEPLOY-T5, SCAN-T2: the camera decoder's wasm is in the build as a file of ours, and nothing else is fetched", () => {
  const work = mkdtempSync(join(tmpdir(), "jl-scan-"));
  try {
    const entry = join(work, "entry.mjs");
    writeFileSync(entry, `import { ScanField } from ${JSON.stringify(join(process.cwd(), "src/components/ScanField"))}; console.log(ScanField);`);
    const out = join(work, "out");
    execFileSync(process.execPath, ["--input-type=module", "-e", BUILD_ENTRY, out, entry], {
      cwd: process.cwd(),
      env: { ...process.env, NODE_ENV: "production" },
      stdio: "pipe",
    });
    const names = readdirSync(out, { recursive: true, encoding: "utf8" });
    expect(names.some((f) => /zxing_reader.*\.wasm$/.test(f))).toBe(true);
    // The wasm is a separate file (the CSP allows no data: URLs), asked for by the bundled loader.
    const files = readOutput(out);
    expect(Object.values(files).join("")).not.toContain("data:application/wasm");
    expect(externalUrls(files)).toEqual([]);
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
}, 120_000);
