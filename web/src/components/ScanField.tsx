import { type FormEvent, type ReactNode, useCallback, useEffect, useId, useRef, useState } from "react";
import { type DetectorFactory } from "./barcodeDetector";
import { api } from "../api/client";
import { Icon } from "./Icon";
import "./ScanField.css";

// The same code seen again within this long is the same scan: a barcode held in front of the
// camera is read every frame.
const REPEAT_MS = 2000;
const POLL_MS = 150;
// A decoder that fails this many frames in a row is broken (the wasm didn't load), not unlucky.
const MAX_FAILED_FRAMES = 5;

type Problem = "insecure" | "nocamera" | "denied" | "error";

const PROBLEMS: Record<Problem, string> = {
  insecure: "Camera scanning needs a one-time step on this device.",
  nocamera: "This device has no camera the app can use.",
  denied: "The camera is blocked for this site. Allow it in the browser's settings.",
  error: "The camera couldn't start.",
};

const loadDetector: DetectorFactory = () => import("./barcodeDetector").then((m) => m.createDetector());

// Where the installation serves its one-time trust step: plain HTTP on its own port (the server
// says which), same host.
function trustGuideUrl(port: number): string {
  const url = new URL("/trust/", window.location.href);
  url.protocol = "http:";
  url.port = String(port);
  return url.href;
}

function cameraProblem(): Problem | null {
  if (!window.isSecureContext) return "insecure";
  if (typeof navigator.mediaDevices?.getUserMedia !== "function") return "nocamera";
  return null;
}

function problemFromError(err: unknown): Problem {
  const name = err instanceof DOMException || err instanceof Error ? err.name : "";
  if (name === "NotAllowedError" || name === "SecurityError") return "denied";
  if (name === "NotFoundError" || name === "OverconstrainedError") return "nocamera";
  return "error";
}

interface Props {
  label: string;
  /** Called with each code: typed (Enter or the button), from a handheld scanner, or read by the camera. */
  onScan: (code: string) => void;
  placeholder?: string;
  help?: ReactNode;
  /** The submit button's words; null leaves it out (Enter and the camera still submit). */
  submitLabel?: string | null;
  /** Keep the code in the field after it is submitted or read, instead of clearing it for the next. */
  keep?: boolean;
  /** Where the field starts. */
  defaultValue?: string;
  /** Called with what the field holds each time it changes, for a form that reads it without Enter. */
  onInput?: (value: string) => void;
  /** Said over the camera view. */
  hint?: string;
  autoFocus?: boolean;
  /** Where tests supply a fake decoder. */
  detectorFactory?: DetectorFactory;
}

/**
 * The one way any screen reads a code: a field a person or a keyboard-style scanner types into
 * (Enter submits, then it clears and keeps focus for the next code), and, where the browser
 * allows it, a camera.
 */
export function ScanField({
  label,
  onScan,
  placeholder = "Scan, or type then Enter",
  help,
  submitLabel = "Add",
  keep = false,
  defaultValue,
  onInput,
  hint = "Point at a barcode",
  autoFocus = false,
  detectorFactory = loadDetector,
}: Props) {
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const video = useRef<HTMLVideoElement>(null);
  const stream = useRef<MediaStream | null>(null);
  // Bumped when the camera is closed, so a permission prompt answered later is ignored.
  const attempt = useRef(0);
  const onScanRef = useRef(onScan);
  const [problem, setProblem] = useState<Problem | null>(cameraProblem);
  const [camera, setCamera] = useState<"off" | "starting" | "on">("off");
  // The plain-HTTP port of the setup guide; 0 or null until the server has said (or when it has none).
  const [trustPort, setTrustPort] = useState<number | null>(null);
  const [torch, setTorch] = useState<boolean | null>(null); // null: this camera has none

  const onInputRef = useRef(onInput);
  useEffect(() => {
    onScanRef.current = onScan;
    onInputRef.current = onInput;
  });

  // A code the camera read goes through here: a field that keeps its code shows it.
  const keepRef = useRef(keep);
  useEffect(() => {
    keepRef.current = keep;
  });
  const deliver = useCallback((code: string) => {
    if (keepRef.current && input.current) {
      input.current.value = code;
      onInputRef.current?.(code);
    }
    onScanRef.current(code);
  }, []);

  const stopCamera = useCallback(() => {
    attempt.current++;
    stream.current?.getTracks().forEach((t) => t.stop());
    stream.current = null;
    setCamera("off");
    setTorch(null);
  }, []);

  // The camera button goes when it's used, closed over, or refused, and focus would fall to the
  // page: put it back in the field, so a handheld scanner keeps working. Not on first render.
  const changed = useRef(false);
  useEffect(() => {
    if (changed.current) input.current?.focus({ preventScroll: true });
    changed.current = true;
  }, [camera, problem]);

  // A device with no camera at all shouldn't be offered one.
  useEffect(() => {
    if (cameraProblem() !== null) return;
    let live = true;
    navigator.mediaDevices.enumerateDevices?.().then(
      (devices) => {
        if (live && !devices.some((d) => d.kind === "videoinput")) setProblem("nocamera");
      },
      () => {},
    );
    return () => {
      live = false;
    };
  }, []);

  // The guide is only linked when something is wrong, so only then ask where it is.
  const needsGuide = problem !== null;
  useEffect(() => {
    if (!needsGuide) return;
    const ctl = new AbortController();
    api<{ trust_port?: number }>("/api/client-config", { signal: ctl.signal }).then(
      (c) => setTrustPort(typeof c.trust_port === "number" ? c.trust_port : 0),
      () => {}, // no link rather than a wrong one; typing still works
    );
    return () => ctl.abort();
  }, [needsGuide]);

  // The camera is off when the component goes, and when the page is hidden.
  useEffect(() => {
    const onVisibility = () => {
      if (document.hidden) stopCamera();
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      stopCamera();
    };
  }, [stopCamera]);

  // Reading: while the camera is on, decode frames until it is closed.
  useEffect(() => {
    const el = video.current;
    const media = stream.current;
    if (camera !== "on" || !el || !media) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const seen = new Map<string, number>();
    el.srcObject = media;
    void el.play?.()?.catch(() => {});

    const submit = (code: string) => {
      const now = Date.now();
      const last = seen.get(code);
      seen.set(code, now); // still in view: stays the same scan
      if (last === undefined || now - last >= REPEAT_MS) deliver(code);
    };

    void (async () => {
      let detector;
      try {
        detector = await detectorFactory();
      } catch {
        if (!cancelled) {
          stopCamera();
          setProblem("error");
        }
        return;
      }
      let failed = 0;
      const tick = async () => {
        if (cancelled) return;
        if (el.readyState >= 2) {
          try {
            const found = await detector.detect(el);
            failed = 0;
            if (cancelled) return;
            for (const code of new Set(found.map((f) => f.rawValue.trim()))) if (code) submit(code);
          } catch {
            if (++failed >= MAX_FAILED_FRAMES && !cancelled) {
              stopCamera();
              setProblem("error");
              return;
            }
          }
        }
        if (!cancelled) timer = setTimeout(tick, POLL_MS);
      };
      void tick();
    })();

    return () => {
      cancelled = true;
      clearTimeout(timer);
      el.srcObject = null;
    };
  }, [camera, detectorFactory, stopCamera, deliver]);

  // Opened from the button's click: iOS only starts a camera from a user's gesture.
  async function openCamera() {
    const mine = ++attempt.current;
    setCamera("starting");
    let media: MediaStream;
    try {
      media = await navigator.mediaDevices.getUserMedia({
        video: { facingMode: { ideal: "environment" } },
        audio: false,
      });
    } catch (err) {
      if (mine !== attempt.current) return;
      setCamera("off");
      setProblem(problemFromError(err));
      return;
    }
    if (mine !== attempt.current) {
      media.getTracks().forEach((t) => t.stop());
      return;
    }
    stream.current = media;
    const track = media.getVideoTracks()[0];
    if (track) {
      track.onended = stopCamera;
      const caps = track.getCapabilities?.() as { torch?: boolean } | undefined;
      setTorch(caps?.torch ? false : null);
    }
    setCamera("on");
  }

  async function toggleTorch() {
    const track = stream.current?.getVideoTracks()[0];
    if (!track || torch === null) return;
    try {
      await track.applyConstraints({ advanced: [{ torch: !torch } as MediaTrackConstraintSet] });
      setTorch(!torch);
    } catch {
      setTorch(null); // it can't be switched: stop offering it
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    const field = input.current;
    if (!field) return;
    const code = field.value.trim();
    if (!keep) field.value = "";
    field.focus();
    if (code) onScan(code);
  }

  return (
    <div className="stack">
      {camera === "on" && (
        <div className="viewfinder">
          <video ref={video} muted playsInline aria-label="Camera view" />
          <div className="frame" />
          <div className="laser" />
          <div className="vbtns">
            {torch !== null && (
              <button type="button" aria-label="Light" aria-pressed={torch} onClick={() => void toggleTorch()}>
                <Icon name="torch" />
              </button>
            )}
            <button type="button" aria-label="Close camera" onClick={stopCamera}>
              <Icon name="close" />
            </button>
          </div>
          <div className="hint">{hint}</div>
        </div>
      )}
      <form className="field" onSubmit={submit}>
        <label htmlFor={id}>{label}</label>
        <div className="scanrow">
          <input
            ref={input}
            id={id}
            className="input"
            placeholder={placeholder}
            autoFocus={autoFocus}
            defaultValue={defaultValue}
            onChange={onInput ? (e) => onInput(e.target.value) : undefined}
            autoComplete="off"
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
          />
          {problem === null && camera === "off" && (
            <button type="button" className="btn btn-secondary scan-cam" onClick={() => void openCamera()}>
              <Icon name="camera" />
              <span className="sr">Scan with the camera</span>
            </button>
          )}
          {submitLabel !== null && (
            <button type="submit" className="btn btn-secondary">
              {submitLabel}
            </button>
          )}
        </div>
        {help && <span className="help">{help}</span>}
        {problem !== null && (
          <p className="scan-note" role="status">
            {PROBLEMS[problem]} You can still type the code.
            {trustPort ? (
              <>
                {" "}
                <a href={trustGuideUrl(trustPort)}>Camera setup guide</a>
              </>
            ) : null}
          </p>
        )}
      </form>
    </div>
  );
}
