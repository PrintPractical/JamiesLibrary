import { useState } from "react";
import { getAdminSettings, putCopyIdMode, type AdminSettings, type CopyIdMode } from "../../api/setup";
import { InlineError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { Saved } from "./Saved";

const NAME: Record<CopyIdMode, string> = { isbn: "By ISBN", own: "By own barcode" };

const EXPLAINED: Record<CopyIdMode, string> = {
  isbn: "Copies of a book share its ISBN barcode and need none of their own. Scanning an ISBN picks an available copy, so which physical copy changes hands is not tracked, and a copy's number, condition and location are records only.",
  own: "Each copy carries a barcode of its own, and that is what gets scanned.",
};

const SWITCHING: Record<CopyIdMode, string> = {
  own: "Every copy that has no barcode yet is given a generated one, so you will want to print and stick labels on them. Patrons and librarians will then scan each copy's own barcode, and scanning an ISBN will no longer check a book out. All copies and loans are kept.",
  isbn: "Every copy keeps its barcode, and scanning one still finds that copy. Books with an ISBN can then be checked out by scanning the ISBN. A book without an ISBN still needs its copies' own barcodes. All copies and loans are kept.",
};

/** How copies are identified when scanned (CAT-9). Switching asks first, because it changes what everyone scans. */
export function CopyIdSection({ settings, onSaved }: { settings: AdminSettings; onSaved: (s: AdminSettings) => void }) {
  const mode = settings.copy_id_mode;
  const other: CopyIdMode = mode === "isbn" ? "own" : "isbn";
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [note, setNote] = useState<string>();

  async function confirm() {
    setBusy(true);
    setError(null);
    try {
      await putCopyIdMode(other);
      onSaved(await getAdminSettings());
      setAsking(false);
      setNote(`Copies are now identified ${NAME[other].toLowerCase()}.`);
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  const label = other === "own" ? "Switch to own barcodes" : "Switch to ISBN scanning";
  return (
    <section className="stack" aria-labelledby="set-copyid">
      <h2 id="set-copyid">How copies are identified</h2>
      <div className="card setup-card stack">
        <p>
          <b>{NAME[mode]}</b>
        </p>
        <p className="help">{EXPLAINED[mode]}</p>
        <p className="help">A book without an ISBN always uses its copies' own barcodes.</p>
        {asking ? (
          <div className="alert alert-warn" role="alertdialog" aria-labelledby="copyid-q" aria-describedby="copyid-why">
            <Icon name="alert" />
            <div>
              <p className="t" id="copyid-q">
                {label}?
              </p>
              <p className="small" id="copyid-why">
                {SWITCHING[other]}
              </p>
              <InlineError error={error} />
              <div className="row confirm-row">
                <button type="button" className="btn btn-primary" disabled={busy} onClick={confirm}>
                  {busy ? "Switching…" : `Yes, ${label.charAt(0).toLowerCase()}${label.slice(1)}`}
                </button>
                <button type="button" className="btn btn-quiet" disabled={busy} onClick={() => (setAsking(false), setError(null))}>
                  Cancel
                </button>
              </div>
            </div>
          </div>
        ) : (
          <div>
            <button type="button" className="btn btn-secondary" onClick={() => (setNote(undefined), setAsking(true))}>
              {label}
            </button>
          </div>
        )}
      </div>
      <Saved text={note} />
    </section>
  );
}
