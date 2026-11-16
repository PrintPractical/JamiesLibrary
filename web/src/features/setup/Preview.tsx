import type { CSSProperties } from "react";
import { Logo } from "../../components/Icon";

// Bars of the sample card's barcode: x, width.
const BARS: [number, number][] = [[10, 2], [13, 1], [16, 3], [21, 2], [24, 2], [27, 2], [32, 2], [35, 3], [39, 1], [41, 1], [46, 3], [50, 2], [55, 4], [61, 4], [67, 3], [73, 3], [79, 3], [85, 1], [88, 1], [91, 3], [97, 2], [103, 2], [106, 3], [111, 4], [117, 2], [122, 3], [126, 1], [128, 2]];

/** A phone-sized sample of the library in the colours being chosen (the colours are set on it alone). */
export function Preview({ name, logoUrl, colours }: { name: string; logoUrl: string; colours: Record<string, string> }) {
  return (
    <aside className="aside">
      <p className="eyebrow preview-label">Preview</p>
      <div className="prev" aria-hidden="true" data-testid="preview" style={colours as CSSProperties}>
        <div className="ab">
          {logoUrl ? <img className="logo" src={logoUrl} alt="" /> : <Logo />}
          <span>{name}</span>
        </div>
        <div className="pb">
          <p className="hello">Good evening, Maya</p>
          <p className="tiny muted">You have 2 books out.</p>
          <div className="mc">
            Your library card
            <div className="bc">
              <svg viewBox="0 0 140 30" preserveAspectRatio="none" className="barcode">
                <g fill="#000">
                  {BARS.map(([x, w]) => (
                    <rect key={x} x={x} y="0" width={w} height="30" />
                  ))}
                </g>
              </svg>
            </div>
          </div>
          <span className="bt">Renew</span> <span className="badge b-ok">Due Sat 28 Nov</span>
        </div>
        <div className="tb">
          <span className="on">Home</span>
          <span>Browse</span>
          <span>My card</span>
          <span>My books</span>
        </div>
      </div>
    </aside>
  );
}
