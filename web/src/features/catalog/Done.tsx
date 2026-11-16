import { useState } from "react";
import { Link } from "react-router";
import { LAYOUTS, printLabels } from "../../api/catalog";
import { InlineError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import "./addbook.css";

export type DoneCopy = { id: number; number: number; barcode: string };

type Props = { bookId: number; title: string; copies: DoneCopy[]; ownBarcodes: boolean; onAnother: () => void };

/** After saving: what was made, and an offer to print the new copies' labels (CAT-7). */
export function Done({ bookId, title, copies, ownBarcodes, onAnother }: Props) {
  const [layout, setLayout] = useState<string>(LAYOUTS[0].id);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [pdf, setPdf] = useState<string | null>(null);

  async function print() {
    setBusy(true);
    setError(null);
    try {
      const blob = await printLabels(
        copies.map((c) => c.id),
        layout,
      );
      if (pdf) URL.revokeObjectURL(pdf);
      const url = URL.createObjectURL(blob);
      setPdf(url);
      window.open(url, "_blank", "noopener");
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="stack ab">
      <h1>Saved</h1>
      <section className="card stack" aria-label="Saved">
        <div className="alert alert-ok" role="status">
          <Icon name="check" />
          <div>
            <p className="t">
              “{title}” is in the catalog with {copies.length} {copies.length === 1 ? "copy" : "copies"}.
            </p>
          </div>
        </div>
        <ul className="ab-done-list">
          {copies.map((c) => (
            <li key={c.id}>
              Copy {c.number}
              {c.barcode && (
                <>
                  {" · barcode "}
                  <span className="ab-code">{c.barcode}</span>
                </>
              )}
            </li>
          ))}
        </ul>
      </section>
      <section className="card stack" aria-labelledby="ab-print">
        <h2 id="ab-print">Print labels</h2>
        <p className="help">
          {ownBarcodes
            ? "Stick a label on each new copy so it can be scanned."
            : "Each label carries the book's ISBN barcode, its title, the copy number and its location."}
        </p>
        <div className="ab-print">
          <div className="field">
            <label htmlFor="ab-layout">Label sheet</label>
            <select id="ab-layout" className="input" value={layout} onChange={(e) => setLayout(e.target.value)}>
              {LAYOUTS.map((l) => (
                <option key={l.id} value={l.id}>
                  {l.label}
                </option>
              ))}
            </select>
          </div>
          <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void print()}>
            {busy ? "Making the labels…" : `Print ${copies.length === 1 ? "label" : "labels"}`}
          </button>
        </div>
        <InlineError error={error} />
        {pdf && (
          <p className="small">
            The labels are ready.{" "}
            <a href={pdf} target="_blank" rel="noopener">
              Open the PDF
            </a>{" "}
            if it did not open.
          </p>
        )}
      </section>
      <div className="row">
        <Link className="btn btn-secondary" to={`/books/${bookId}`}>
          View the book
        </Link>
        <button type="button" className="btn btn-quiet" onClick={onAnother}>
          Add another book
        </button>
      </div>
    </div>
  );
}

