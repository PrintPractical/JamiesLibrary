import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CodeDetector } from "./barcodeDetector";
import { ScanField } from "./ScanField";

function setSecure(value: boolean) {
  Object.defineProperty(window, "isSecureContext", { value, configurable: true });
}

function fakeTrack() {
  return { stop: vi.fn(), onended: null as null | (() => void), getCapabilities: () => ({}) };
}

/** A camera: getUserMedia hands out one stream whose tracks can be watched. */
function fakeCamera(opts: { devices?: { kind: string }[]; reject?: string } = {}) {
  const track = fakeTrack();
  const media = { getTracks: () => [track], getVideoTracks: () => [track] };
  const getUserMedia = vi.fn(async () => {
    if (opts.reject) throw new DOMException("no", opts.reject);
    return media;
  });
  Object.defineProperty(navigator, "mediaDevices", {
    value: { getUserMedia, enumerateDevices: async () => opts.devices ?? [{ kind: "videoinput" }] },
    configurable: true,
  });
  return { track, getUserMedia };
}

/** A decoder that answers each frame with the next list of codes (the last one repeats). */
function fakeDetector(frames: string[][]) {
  let i = 0;
  const detect = vi.fn(async () => (frames[Math.min(i++, frames.length - 1)] ?? []).map((rawValue) => ({ rawValue })));
  const detector: CodeDetector = { detect };
  return { factory: vi.fn(async () => detector), detect };
}

beforeEach(() => {
  setSecure(true);
  // jsdom has no video playback: a frame is always ready, and play() does nothing.
  vi.spyOn(HTMLMediaElement.prototype, "readyState", "get").mockReturnValue(4);
  vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

test("Enter submits the typed code, then the field is empty and still has focus", async () => {
  fakeCamera();
  const onScan = vi.fn();
  render(<ScanField label="Copy barcode" onScan={onScan} />);
  const field = screen.getByLabelText("Copy barcode");
  await userEvent.type(field, "C-000412{Enter}");
  expect(onScan).toHaveBeenCalledExactlyOnceWith("C-000412");
  expect(field).toHaveValue("");
  expect(field).toHaveFocus();
  // continuous scanning: the next scan goes straight in
  await userEvent.keyboard("9780141439518{Enter}");
  expect(onScan).toHaveBeenLastCalledWith("9780141439518");
});

test("an empty or blank field submits nothing, and the Add button submits like Enter", async () => {
  fakeCamera();
  const onScan = vi.fn();
  render(<ScanField label="Code" onScan={onScan} />);
  await userEvent.type(screen.getByLabelText("Code"), "   {Enter}");
  expect(onScan).not.toHaveBeenCalled();
  await userEvent.type(screen.getByLabelText("Code"), " 123 ");
  await userEvent.click(screen.getByRole("button", { name: "Add" }));
  expect(onScan).toHaveBeenCalledExactlyOnceWith("123");
});

test("SCAN-T2: outside a secure context there is no camera button, the trust guide is linked from this host, and typing works", async () => {
  setSecure(false);
  fakeCamera();
  const onScan = vi.fn();
  render(<ScanField label="Code" onScan={onScan} />);
  expect(screen.queryByRole("button", { name: /camera/i })).not.toBeInTheDocument();
  const link = screen.getByRole("link", { name: /setup guide/i });
  expect(link).toHaveAttribute("href", `http://${window.location.hostname}:8080/trust/`);
  expect(screen.getByRole("status")).toHaveTextContent(/one-time step/i);
  await userEvent.type(screen.getByLabelText("Code"), "ABC{Enter}");
  expect(onScan).toHaveBeenCalledWith("ABC");
});

test("SCAN-1 Unreadable barcode: a device with no camera offers none, and the ISBN can be typed", async () => {
  fakeCamera({ devices: [{ kind: "audioinput" }] });
  const onScan = vi.fn();
  render(<ScanField label="ISBN" onScan={onScan} />);
  expect(await screen.findByRole("status")).toHaveTextContent(/no camera/i);
  expect(screen.queryByRole("button", { name: /camera/i })).not.toBeInTheDocument();
  await userEvent.type(screen.getByLabelText("ISBN"), "9780141439518{Enter}");
  expect(onScan).toHaveBeenCalledWith("9780141439518");
});

test("a camera that is there is offered, and no stream starts before it's asked for", async () => {
  const cam = fakeCamera();
  render(<ScanField label="Code" onScan={vi.fn()} />);
  expect(await screen.findByRole("button", { name: "Scan with the camera" })).toBeInTheDocument();
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
  expect(cam.getUserMedia).not.toHaveBeenCalled();
});

test("permission denied: the camera button goes, the reason and guide link show, typing still works", async () => {
  fakeCamera({ reject: "NotAllowedError" });
  const onScan = vi.fn();
  render(<ScanField label="Code" onScan={onScan} />);
  await userEvent.click(await screen.findByRole("button", { name: "Scan with the camera" }));
  expect(await screen.findByRole("status")).toHaveTextContent(/blocked/i);
  expect(screen.getByRole("link", { name: /setup guide/i })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /camera/i })).not.toBeInTheDocument();
  await userEvent.type(screen.getByLabelText("Code"), "X1{Enter}");
  expect(onScan).toHaveBeenCalledWith("X1");
});

async function openWith(frames: string[][], onScan = vi.fn()) {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const cam = fakeCamera();
  const det = fakeDetector(frames);
  const view = render(<ScanField label="Code" onScan={onScan} detectorFactory={det.factory} />);
  fireEvent.click(await screen.findByRole("button", { name: "Scan with the camera" }));
  await screen.findByRole("button", { name: "Close camera" });
  return { cam, det, onScan, ...view };
}

const advance = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms));

test("SCAN-1 Scan ISBN: a code the decoder reads is submitted once, however many frames repeat it", async () => {
  const { onScan, det } = await openWith([["9780141439518"]]);
  await advance(1000);
  expect(det.detect.mock.calls.length).toBeGreaterThan(3);
  expect(onScan).toHaveBeenCalledExactlyOnceWith("9780141439518");
});

test("the same code again after it left the view for 2 s is a new scan; a different code is never held back", async () => {
  const { onScan } = await openWith([["AAA"], ["AAA", "BBB"], [], [], [], [], [], [], [], [], [], [], [], [], [], [], [], [], [], [], [], ["AAA"]]);
  await advance(1000);
  expect(onScan.mock.calls.map((c) => c[0])).toEqual(["AAA", "BBB"]);
  await advance(3000);
  expect(onScan.mock.calls.map((c) => c[0])).toEqual(["AAA", "BBB", "AAA"]);
});

test("closing the camera stops its tracks and the view goes", async () => {
  const { cam } = await openWith([[]]);
  fireEvent.click(screen.getByRole("button", { name: "Close camera" }));
  expect(cam.track.stop).toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Close camera" })).not.toBeInTheDocument();
});

test("unmounting stops the camera's tracks", async () => {
  const { cam, unmount } = await openWith([[]]);
  expect(cam.track.stop).not.toHaveBeenCalled();
  unmount();
  expect(cam.track.stop).toHaveBeenCalled();
});

test("hiding the page stops the camera", async () => {
  const { cam } = await openWith([[]]);
  vi.spyOn(document, "hidden", "get").mockReturnValue(true);
  fireEvent(document, new Event("visibilitychange"));
  expect(cam.track.stop).toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Close camera" })).not.toBeInTheDocument();
});

test("a decoder that cannot load closes the camera and says so, typing still works", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const cam = fakeCamera();
  const onScan = vi.fn();
  render(
    <ScanField
      label="Code"
      onScan={onScan}
      detectorFactory={async () => {
        throw new Error("wasm failed");
      }}
    />,
  );
  fireEvent.click(await screen.findByRole("button", { name: "Scan with the camera" }));
  expect(await screen.findByRole("status")).toHaveTextContent(/couldn't start/i);
  expect(cam.track.stop).toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Code"), { target: { value: "Z9" } });
  fireEvent.submit(screen.getByLabelText("Code"));
  expect(onScan).toHaveBeenCalledWith("Z9");
});

test("a decoder that fails every frame closes the camera instead of failing silently", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const cam = fakeCamera();
  const detector: CodeDetector = {
    detect: async () => {
      throw new Error("no module");
    },
  };
  render(<ScanField label="Code" onScan={vi.fn()} detectorFactory={async () => detector} />);
  fireEvent.click(await screen.findByRole("button", { name: "Scan with the camera" }));
  await screen.findByRole("button", { name: "Close camera" });
  await advance(2000);
  expect(cam.track.stop).toHaveBeenCalled();
  expect(screen.getByRole("status")).toHaveTextContent(/couldn't start/i);
});
