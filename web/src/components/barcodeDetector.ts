// The camera decoder. Loaded on the first use of a camera, so the decoder and its
// wasm stay out of every page that never opens one.
import type { BarcodeDetector as BarcodeDetectorType } from "barcode-detector/ponyfill";
import wasmUrl from "zxing-wasm/reader/zxing_reader.wasm?url";

/** What ScanField needs of a decoder: the codes in the current video frame. */
export interface CodeDetector {
  detect(video: HTMLVideoElement): Promise<{ rawValue: string }[]>;
}

export type DetectorFactory = () => Promise<CodeDetector>;

// ISBNs (EAN-13), copy barcodes and card numbers (Code 128), and card QR codes.
export const FORMATS = ["ean_13", "code_128", "qr_code"] as const;

async function nativeDetector(): Promise<CodeDetector | null> {
  const Native = (window as { BarcodeDetector?: typeof BarcodeDetectorType }).BarcodeDetector;
  if (!Native) return null;
  try {
    const supported = await Native.getSupportedFormats();
    if (!FORMATS.every((f) => supported.includes(f))) return null;
    return new Native({ formats: [...FORMATS] });
  } catch {
    return null;
  }
}

/** The browser's own BarcodeDetector where it reads our formats, else zxing-wasm from our own origin. */
export const createDetector: DetectorFactory = async () => {
  const native = await nativeDetector();
  if (native) return native;
  const { BarcodeDetector: Zxing, setZXingModuleOverrides } = await import("barcode-detector/ponyfill");
  // The library's default fetches the wasm from a public CDN: that breaks a LAN with no internet
  // and the CSP. Point it at the copy the build bundles.
  setZXingModuleOverrides({ locateFile: (name: string, prefix: string) => (name.endsWith(".wasm") ? wasmUrl : prefix + name) });
  return new Zxing({ formats: [...FORMATS] });
};
