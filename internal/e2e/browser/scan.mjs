// Drives headless Chromium for the scanning tests: a fake camera (a video file the
// browser plays as its camera), keystrokes sent as real key events the way a typist or
// a keyboard-style hand scanner makes them, and a record of everything the page asked
// for and complained about. Usage: node scan.mjs SCENARIO.json
//
// Scenario: chrome, args (extra browser flags), base, signIn {username,password},
// width, height, noNativeDetector, steps [{path, actions}].
// Actions: click {selector}, type {selector, text, delayMs} (then Enter unless
// enter:false), burst {selector, codes, delayMs} (codes one after another with no
// pause, as a scanner makes them), waitText, absentText, sleepMs, eval {expr, as}.
// Anything outside the installation's origin is recorded and refused.
import { spawn } from "node:child_process";
import { mkdtempSync, readFileSync, existsSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const sc = JSON.parse(readFileSync(process.argv[2], "utf8"));
const origin = new URL(sc.base).origin;
const profile = mkdtempSync(join(tmpdir(), "jl-scan-"));
const chrome = spawn(
  sc.chrome,
  ["--headless=new", "--no-sandbox", "--disable-gpu", "--no-first-run", "--disable-dev-shm-usage",
    `--user-data-dir=${profile}`, "--remote-debugging-port=0", ...(sc.args ?? []), "about:blank"],
  { stdio: "ignore" },
);
const report = { requests: [], blocked: [], console: [], dialogs: [], visits: [], errors: [] };
const done = (code) => {
  try { chrome.kill("SIGKILL"); } catch {}
  try { rmSync(profile, { recursive: true, force: true }); } catch {}
  console.log(JSON.stringify(report));
  process.exit(code);
};
setTimeout(() => { report.errors.push("driver timed out"); done(1); }, (sc.timeoutSeconds ?? 120) * 1000);

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
  // A browser with no BarcodeDetector of its own (iOS Safari, Firefox): the page has to bring its decoder.
  if (sc.noNativeDetector) await cmd("Page.addScriptToEvaluateOnNewDocument", { source: "delete window.BarcodeDetector;" });
  await cmd("Runtime.enable");
  await cmd("Log.enable");
  await cmd("Network.enable");
  await cmd("Fetch.enable", { patterns: [{ urlPattern: "*" }] });
  await cmd("Emulation.setDeviceMetricsOverride", { width: sc.width ?? 390, height: sc.height ?? 844, deviceScaleFactor: 1, mobile: false });
  listeners.push((m) => {
    if (m.sessionId !== sessionId) return;
    if (m.method === "Fetch.requestPaused") {
      const url = m.params.request.url;
      const same = url.startsWith(origin + "/") || url === origin || url.startsWith("data:") || url.startsWith("blob:") || url === "about:blank";
      report.requests.push(url);
      if (!same) report.blocked.push(url);
      cmd(same ? "Fetch.continueRequest" : "Fetch.failRequest", same ? { requestId: m.params.requestId } : { requestId: m.params.requestId, errorReason: "BlockedByClient" }).catch(() => {});
    }
    if (m.method === "Page.javascriptDialogOpening") {
      report.dialogs.push(m.params.message);
      cmd("Page.handleJavaScriptDialog", { accept: false }).catch(() => {});
    }
    // What the page complained about, CSP refusals included.
    if (m.method === "Log.entryAdded" && ["error", "warning"].includes(m.params.entry.level)) {
      report.console.push(`${m.params.entry.level}: ${m.params.entry.text} ${m.params.entry.url ?? ""}`);
    }
    if (m.method === "Runtime.exceptionThrown") {
      report.console.push("exception: " + (m.params.exceptionDetails.exception?.description ?? m.params.exceptionDetails.text));
    }
    if (m.method === "Runtime.consoleAPICalled" && m.params.type === "error") {
      report.console.push("console.error: " + m.params.args.map((a) => a.value ?? a.description ?? "").join(" "));
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
    await waitFor(`document.querySelector("#root")?.children.length > 0`, 20);
    await sleep(sc.settleMs ?? 400);
  };

  // One character as a keyboard makes it: key down (with the text), key up.
  const press = async (ch) => {
    if (ch === "\n") {
      await cmd("Input.dispatchKeyEvent", { type: "keyDown", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, text: "\r" });
      await cmd("Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13 });
      return;
    }
    await cmd("Input.dispatchKeyEvent", { type: "keyDown", key: ch, text: ch, unmodifiedText: ch });
    await cmd("Input.dispatchKeyEvent", { type: "keyUp", key: ch });
  };
  const focus = (selector) => evaluate(`(() => { const el = document.querySelector(${q(selector)}); if (!el) throw new Error("nothing matches " + ${q(selector)}); el.focus(); return true; })()`);

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
    // Before signing in the app asks who is there and is told nobody: not a complaint about the scan.
    report.console.length = 0;
  }

  for (const step of sc.steps) {
    const v = { path: step.path, notes: {}, evals: {} };
    if (step.path) await goto(step.path);
    for (const a of step.actions ?? []) {
      if (a.click) {
        await evaluate(`(() => { const el = document.querySelector(${q(a.click)}); if (!el) throw new Error("nothing matches " + ${q(a.click)}); el.click(); })()`);
      }
      if (a.clickText) {
        await evaluate(`(() => { const b = [...document.querySelectorAll("button,a")].find((e) => e.textContent.trim() === ${q(a.clickText)}); if (!b) throw new Error("no button " + ${q(a.clickText)}); b.click(); })()`);
      }
      if (a.type) {
        await focus(a.type.selector);
        for (const ch of a.type.text) { await press(ch); if (a.type.delayMs) await sleep(a.type.delayMs); }
        if (a.type.enter !== false) await press("\n");
      }
      if (a.burst) {
        await focus(a.burst.selector);
        for (const code of a.burst.codes) {
          for (const ch of code) { await press(ch); if (a.burst.delayMs) await sleep(a.burst.delayMs); }
          await press("\n");
        }
      }
      if (a.waitText) {
        const t1 = Date.now();
        const ok = await waitFor(`document.body.innerText.includes(${q(a.waitText)})`, a.seconds ?? 15);
        v.notes[a.waitText] = { found: ok, seconds: (Date.now() - t1) / 1000 };
      }
      if (a.absentText) {
        await sleep(a.sleepMs ?? 1500);
        v.notes["absent:" + a.absentText] = { found: await evaluate(`document.body.innerText.includes(${q(a.absentText)})`) };
      }
      if (a.sleepMs && !a.absentText) await sleep(a.sleepMs);
      if (a.eval) v.evals[a.eval.as] = await evaluate(a.eval.expr);
    }
    v.url = await evaluate("location.pathname + location.search");
    v.text = await evaluate(`document.body.innerText.slice(0, 20000)`);
    v.fields = await evaluate(`[...document.querySelectorAll("input,textarea")].map((e) => [e.id, e.value])`);
    // A picture of each named element as the screen draws it, to read a code back from.
    v.clips = {};
    for (const sel of step.clips ?? []) {
      const r = await evaluate(`(() => { const el = document.querySelector(${q(sel)}); if (!el) return null; el.scrollIntoView(); const b = el.getBoundingClientRect(); return { x: b.x + scrollX, y: b.y + scrollY, width: b.width, height: b.height }; })()`);
      if (!r) { report.errors.push("no element to picture: " + sel); continue; }
      v.clips[sel] = (await cmd("Page.captureScreenshot", { format: "png", captureBeyondViewport: true, clip: { ...r, scale: 2 } })).data;
    }
    if (step.shot) v.shot = (await cmd("Page.captureScreenshot", { format: "png" })).data;
    report.visits.push(v);
  }
  done(0);
} catch (e) {
  report.errors.push(String(e?.stack ?? e));
  done(1);
}
