import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFileSync } from "node:child_process";

// DEPLOY-T5: the built app may not reference any other host. Every http(s), ws(s)
// or scheme-relative (//host) URL in the output must name one of these hosts
// exactly (not as a prefix of a longer one); each is text that is never requested.
const ALLOWED: { host: string; pathPrefix: string }[] = [
  // XML namespace URIs (SVG, XLink, MathML, XML): identifiers, not addresses.
  { host: "www.w3.org", pathPrefix: "/" },
  // React's and React Router's error messages name a docs page; nothing fetches it.
  { host: "react.dev", pathPrefix: "/errors/" },
  { host: "reactrouter.com", pathPrefix: "/" },
  // Router code resolves relative URLs against a dummy base to parse them.
  { host: "localhost", pathPrefix: "/" },
];

// A scheme'd URL anywhere, or a bare //host that does not continue a longer path.
const URL_PATTERN = /(?:(?:https?|wss?):\/\/|(?<![\w:/.\-])\/\/(?=[\w-]+\.[\w-]))[^\s"'`)<>\\]+/gi;

function allowed(url: string): boolean {
  let parsed: URL;
  try {
    parsed = new URL(url.startsWith("//") ? `http:${url}` : url);
  } catch {
    return false;
  }
  return ALLOWED.some((a) => parsed.hostname === a.host && parsed.pathname.startsWith(a.pathPrefix));
}

export function externalUrls(files: Record<string, string>): string[] {
  const found: string[] = [];
  for (const [name, text] of Object.entries(files)) {
    for (const url of text.match(URL_PATTERN) ?? []) {
      if (!allowed(url)) found.push(`${name}: ${url}`);
    }
  }
  return found;
}

// Fonts and images are binary; everything else the build writes is text and is scanned.
const BINARY = /\.(woff2?|ttf|otf|eot|png|jpe?g|gif|webp|avif|ico)$/i;

function readOutput(dir: string): Record<string, string> {
  const files: Record<string, string> = {};
  for (const entry of readdirSync(dir, { recursive: true, withFileTypes: true })) {
    if (entry.isFile() && !BINARY.test(entry.name)) {
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
    "c.html: //cdn.example.com/x.js",
    "c.html: http://example.org/s.css",
  ]);
});

test("the scan finds scheme-relative, ws(s) and look-alike hosts the old scan let by", () => {
  expect(
    externalUrls({
      "a.css": "@font-face{src:url(//fonts.gstatic.com/x.woff2)}",
      "b.js": 'fetch("http://localhost.evil.example/t");new WebSocket("wss://evil.example");new WebSocket("ws://localhost.evil.example")',
      "c.html": '<script src="//cdn.example.com/x.js"></script>',
      "d.svg": '<image href="https://www.w3.org.evil.example/i.png"/>',
      "e.js": 'a="http://localhost:5173/x";b="https://react.dev/errors/1";c="https://react.dev/other"',
      "f.webmanifest": '{"start_url":"http://evil.example/"}',
    }),
  ).toEqual([
    "a.css: //fonts.gstatic.com/x.woff2",
    "b.js: http://localhost.evil.example/t",
    "b.js: wss://evil.example",
    "b.js: ws://localhost.evil.example",
    "c.html: //cdn.example.com/x.js",
    "d.svg: https://www.w3.org.evil.example/i.png",
    "e.js: https://react.dev/other",
    "f.webmanifest: http://evil.example/",
  ]);
});

test("the scan reads every text file, and not binary ones", () => {
  const out = mkdtempSync(join(tmpdir(), "jl-read-"));
  try {
    writeFileSync(join(out, "m.webmanifest"), "{}");
    writeFileSync(join(out, "i.svg"), "<svg/>");
    writeFileSync(join(out, "f.woff2"), Buffer.from([0xff, 0xfe]));
    expect(Object.keys(readOutput(out)).sort()).toEqual(["i.svg", "m.webmanifest"]);
  } finally {
    rmSync(out, { recursive: true, force: true });
  }
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
