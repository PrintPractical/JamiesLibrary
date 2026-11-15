import { FORMATS } from "./barcodeDetector";

afterEach(() => {
  vi.resetModules();
  vi.doUnmock("barcode-detector/ponyfill");
  delete (window as { BarcodeDetector?: unknown }).BarcodeDetector;
});

async function withZxing() {
  const overrides: { locateFile?: (name: string, prefix: string) => string }[] = [];
  const Zxing = vi.fn();
  vi.doMock("barcode-detector/ponyfill", () => ({
    BarcodeDetector: Zxing,
    setZXingModuleOverrides: (o: (typeof overrides)[number]) => overrides.push(o),
  }));
  const { createDetector } = await import("./barcodeDetector");
  return { createDetector, overrides, Zxing };
}

test("DEPLOY-T5: without a native BarcodeDetector, the zxing decoder loads its wasm from our own build, never a CDN", async () => {
  const { createDetector, overrides, Zxing } = await withZxing();
  await createDetector();
  expect(Zxing).toHaveBeenCalledWith({ formats: [...FORMATS] });
  const locate = overrides.at(-1)?.locateFile;
  expect(locate).toBeDefined();
  const wasm = locate!("zxing_reader.wasm", "https://cdn.example.com/");
  expect(wasm).toMatch(/zxing_reader.*\.wasm$/);
  expect(wasm).not.toMatch(/^https?:/);
  // anything that isn't the wasm keeps the library's own resolution
  expect(locate!("other.js", "/p/")).toBe("/p/other.js");
});

test("the browser's own BarcodeDetector is used when it reads EAN-13, Code 128 and QR", async () => {
  const detect = vi.fn();
  const Native = vi.fn(function () {
    return { detect };
  }) as unknown as { getSupportedFormats: () => Promise<string[]> };
  Native.getSupportedFormats = async () => ["ean_13", "code_128", "qr_code", "upc_a"];
  (window as { BarcodeDetector?: unknown }).BarcodeDetector = Native;
  const { createDetector, Zxing } = await withZxing();
  const d = await createDetector();
  expect(d.detect).toBe(detect);
  expect(Zxing).not.toHaveBeenCalled();
});

test("a native detector that lacks one of our formats is not used", async () => {
  const Native = vi.fn() as unknown as { getSupportedFormats: () => Promise<string[]> };
  Native.getSupportedFormats = async () => ["qr_code"];
  (window as { BarcodeDetector?: unknown }).BarcodeDetector = Native;
  const { createDetector, Zxing } = await withZxing();
  await createDetector();
  expect(Zxing).toHaveBeenCalled();
});
