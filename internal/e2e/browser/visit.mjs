// Drives headless Chromium over the DevTools protocol (no packages: Node's own
// WebSocket) for the end-to-end tests. Usage: node visit.mjs SCENARIO.json
//
// The scenario says which browser to start, the installation's address, who to
// sign in as, and a list of steps. The report (JSON on stdout) has, for every
// request the browser made, its address, and for each visit what the page held.
// Anything outside the installation's own origin is both recorded and refused,
// so a page that needs the outside world fails visibly instead of waiting.
import { spawn } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync, existsSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const sc = JSON.parse(readFileSync(process.argv[2], "utf8"));
const origin = new URL(sc.base).origin;
const profile = mkdtempSync(join(tmpdir(), "jl-chrome-"));
const chrome = spawn(
  sc.chrome,
  ["--headless=new", "--no-sandbox", "--disable-gpu", "--no-first-run", "--disable-dev-shm-usage",
    `--user-data-dir=${profile}`, "--remote-debugging-port=0", "about:blank"],
  { stdio: "ignore" },
);
let cur = null; // the visit in progress, to say where a console error came from
const report = { requests: [], blocked: [], dialogs: [], visits: [], errors: [] };
const done = (code) => {
  try { chrome.kill("SIGKILL"); } catch {}
  try { rmSync(profile, { recursive: true, force: true }); } catch {}
  // A report with pictures is too big to leave on a pipe that exit would cut off: it goes to a file when told to.
  if (sc.out) writeFileSync(sc.out, JSON.stringify(report));
  else console.log(JSON.stringify(report));
  process.exit(code);
};
setTimeout(() => { report.errors.push("driver timed out"); done(1); }, (sc.timeoutSeconds ?? 120) * 1000);

// Measured in the page: what sticks out past the right or left edge, what is
// scrolled sideways inside the page, and which controls a finger cannot reach
// (something else is on top of them even after they are scrolled into view).
const LAYOUT = `(() => {
  const vw = document.documentElement.clientWidth;
  const desc = (el) => el.tagName.toLowerCase() + (el.id ? "#" + el.id : "") + (typeof el.className === "string" && el.className ? "." + el.className.trim().split(/\\s+/).join(".") : "") + " " + (el.textContent || el.getAttribute("aria-label") || "").trim().replace(/\\s+/g, " ").slice(0, 40);
  const shown = (el) => { const cs = getComputedStyle(el), r = el.getBoundingClientRect(); return cs.display !== "none" && cs.visibility !== "hidden" && r.width > 1 && r.height > 1 && cs.opacity !== "0"; };
  const out = { vw, iw: innerWidth, scrollWidth: document.documentElement.scrollWidth, bodyScrollWidth: document.body.scrollWidth, sticksOut: [], innerScroll: [], covered: [], controls: 0 };
  const scrollParent = (el) => { for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) { const o = getComputedStyle(p).overflowX; if (o === "auto" || o === "scroll") return p; } return null; };
  for (const el of document.querySelectorAll("body *")) {
    if (!shown(el)) continue;
    const r = el.getBoundingClientRect();
    if (r.right > vw + 0.5 || r.left < -0.5) {
      const sp = scrollParent(el);
      (sp ? out.innerScroll : out.sticksOut).push(desc(el) + " [" + Math.round(r.left) + ".." + Math.round(r.right) + "]");
    }
  }
  const controls = [...document.querySelectorAll("a[href], button, input:not([type=hidden]), select, textarea, [role=radio], [role=tab], [role=button]")].filter((e) => !e.disabled && shown(e) && !e.closest("[aria-hidden=true]"));
  out.controls = controls.length;
  for (const el of controls) {
    el.scrollIntoView({ block: "center", inline: "center" });
    const r = el.getBoundingClientRect();
    if (r.right > vw + 0.5 || r.left < -0.5) { out.covered.push("off-screen: " + desc(el)); continue; }
    const top = document.elementFromPoint(Math.min(Math.max(r.left + r.width / 2, 0), vw - 1), Math.min(Math.max(r.top + r.height / 2, 0), innerHeight - 1));
    if (!top || !(el === top || el.contains(top) || top.contains(el) || (el.labels && [...el.labels].some((l) => l === top || l.contains(top)))))
      out.covered.push(desc(el) + " is under " + desc(top ?? document.body));
  }
  window.scrollTo(0, 0);
  return out;
})()`;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function endpoint() {
  const f = join(profile, "DevToolsActivePort");
  for (let i = 0; i < 200; i++) {
    if (existsSync(f)) {
      const [port, path] = readFileSync(f, "utf8").trim().split("\n");
      return `ws://127.0.0.1:${port}${path}`;
    }
    await sleep(100);
  }
  throw new Error("the browser did not start");
}

let nextId = 1;
const pending = new Map();
const listeners = [];
const ws = new WebSocket(await endpoint());
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error("no devtools socket")); });
ws.onmessage = (ev) => {
  const m = JSON.parse(ev.data);
  if (m.id && pending.has(m.id)) {
    const { res, rej } = pending.get(m.id);
    pending.delete(m.id);
    m.error ? rej(new Error(JSON.stringify(m.error))) : res(m.result);
  } else if (m.method) listeners.forEach((l) => l(m));
};
const send = (method, params = {}, sessionId) =>
  new Promise((res, rej) => {
    const id = nextId++;
    pending.set(id, { res, rej });
    ws.send(JSON.stringify({ id, method, params, sessionId }));
  });

try {
  const { targetId } = await send("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
  const cmd = (m, p) => send(m, p, sessionId);
  await cmd("Page.enable");
  await cmd("Runtime.enable");
  await cmd("Network.enable");
  await cmd("Log.enable");
  await cmd("Fetch.enable", { patterns: [{ urlPattern: "*" }] });
  await cmd("Emulation.setDeviceMetricsOverride", { width: sc.width ?? 1280, height: sc.height ?? 900, deviceScaleFactor: 1, mobile: !!sc.mobile });
  listeners.push((m) => {
    if (m.sessionId !== sessionId) return;
    if (m.method === "Fetch.requestPaused") {
      const url = m.params.request.url;
      const same = url.startsWith(origin + "/") || url === origin || url.startsWith("data:") || url.startsWith("blob:") || url === "about:blank";
      report.requests.push(url);
      if (!same) report.blocked.push(url);
      cmd(same ? "Fetch.continueRequest" : "Fetch.failRequest", same ? { requestId: m.params.requestId } : { requestId: m.params.requestId, errorReason: "BlockedByClient" }).catch(() => {});
    }
    // What the page itself reported as wrong: uncaught errors, console errors, failed loads.
    if (m.method === "Runtime.exceptionThrown") (cur?.console ?? report.errorsOutsideVisit ?? (report.errorsOutsideVisit = [])).push("exception: " + (m.params.exceptionDetails.exception?.description ?? m.params.exceptionDetails.text));
    if (m.method === "Runtime.consoleAPICalled" && m.params.type === "error") (cur?.console ?? []).push("console.error: " + m.params.args.map((a) => a.value ?? a.description ?? "").join(" "));
    if (m.method === "Log.entryAdded" && m.params.entry.level === "error") (cur?.console ?? []).push("log: " + m.params.entry.text + " " + (m.params.entry.url ?? ""));
    if (m.method === "Page.javascriptDialogOpening") {
      report.dialogs.push(m.params.message);
      cmd("Page.handleJavaScriptDialog", { accept: false }).catch(() => {});
    }
  });

  const evaluate = async (expression) => {
    const r = await cmd("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
    if (r.exceptionDetails) throw new Error("page script failed: " + JSON.stringify(r.exceptionDetails).slice(0, 300));
    return r.result.value;
  };
  const waitFor = async (expression, seconds) => {
    const end = Date.now() + seconds * 1000;
    while (Date.now() < end) {
      try { if (await evaluate(expression)) return true; } catch {}
      await sleep(100);
    }
    return false;
  };
  const q = JSON.stringify;
  const goto = async (path) => {
    await cmd("Page.navigate", { url: origin + path });
    await sleep(100);
    await waitFor(`document.readyState === "complete"`, 20);
    // the app draws after the script runs: wait until the root has content and no spinner is up
    await waitFor(`document.querySelector("#root")?.children.length > 0`, 20);
    await sleep(sc.settleMs ?? 400);
  };

  if (sc.signIn) {
    await goto("/signin");
    await evaluate(`(() => {
      const set = (el, v) => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(el, v); el.dispatchEvent(new Event("input", { bubbles: true })); };
      const inputs = [...document.querySelectorAll("input")];
      set(inputs.find((i) => i.type !== "password"), ${q(sc.signIn.username)});
      set(inputs.find((i) => i.type === "password"), ${q(sc.signIn.password)});
      document.querySelector("form button[type=submit]").click();
    })()`);
    if (!(await waitFor(`location.pathname !== "/signin"`, 15))) report.errors.push("sign-in did not leave the sign-in page");
    await sleep(500);
  }

  for (const step of sc.steps) {
    const v = { path: step.path, notes: {}, evals: [], console: [] };
    cur = v;
    const t0 = Date.now();
    if (step.path) await goto(step.path);
    for (const a of step.actions ?? []) {
      if (a.type) {
        await evaluate(`(() => { const el = document.querySelector(${q(a.type.selector)}); el.focus();
          Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(el, ${q(a.type.text)});
          el.dispatchEvent(new Event("input", { bubbles: true })); })()`);
      }
      if (a.clickText) {
        await evaluate(`(() => { const b = [...document.querySelectorAll("button,a")].find((e) => e.textContent.trim() === ${q(a.clickText)}); if (!b) throw new Error("no button " + ${q(a.clickText)}); b.click(); })()`);
      }
      if (a.click) await evaluate(`(() => { const el = document.querySelector(${q(a.click)}); if (!el) throw new Error("nothing matches " + ${q(a.click)}); el.click(); })()`);
      if (a.waitText) {
        const t1 = Date.now();
        const ok = await waitFor(`document.body.innerText.includes(${q(a.waitText)})`, a.seconds ?? 15);
        v.notes[a.waitText] = { found: ok, seconds: (Date.now() - t1) / 1000 };
      }
      if (a.eval) v.evals.push(await evaluate(a.eval));
      if (a.waitFor) {
        const t1 = Date.now();
        v.notes[a.waitFor] = { found: await waitFor(a.waitFor, a.seconds ?? 15), seconds: (Date.now() - t1) / 1000 };
      }
      if (a.sleepMs) await sleep(a.sleepMs);
    }
    v.seconds = (Date.now() - t0) / 1000;
    v.url = await evaluate("location.pathname + location.search");
    v.text = await evaluate(`document.body.innerText.slice(0, 20000)`);
    v.pwned = await evaluate(`window.__pwned ?? null`);
    v.injected = await evaluate(`(${q(step.probes ?? [])}).map((s) => [s, document.querySelectorAll(s).length])`);
    v.images = await evaluate(`[...document.images].map((i) => ({ src: i.currentSrc || i.src, complete: i.complete, w: i.naturalWidth }))`);
    v.scrollWidth = await evaluate(`document.documentElement.scrollWidth`);
    if (step.layout) v.layout = await evaluate(LAYOUT);
    if (step.shot) {
      await evaluate("window.scrollTo(0, 0)");
      const { contentSize } = await cmd("Page.getLayoutMetrics");
      const clip = { x: 0, y: 0, width: contentSize.width, height: Math.min(contentSize.height, 4000), scale: 1 };
      const { data } = await cmd("Page.captureScreenshot", { format: "png", captureBeyondViewport: true, clip });
      v.shot = data;
    }
    cur = null;
    report.visits.push(v);
  }
  done(0);
} catch (e) {
  report.errors.push(String(e?.stack ?? e));
  done(1);
}
