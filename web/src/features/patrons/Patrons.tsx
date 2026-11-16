import { useEffect, useState } from "react";
import { Link, useLocation } from "react-router";
import { findPatrons, type Patron } from "../../api/patrons";
import { InlineError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { ScanField } from "../../components/ScanField";
import { plural } from "./loans";
import "./patrons.css";

type Result = { q: string; patrons: Patron[] };

/** Staff look-up: a name typed, or a card scanned, in the one field (CIRC-3). */
export function Patrons() {
  const [result, setResult] = useState<Result | null>(null);
  const [error, setError] = useState<unknown>(null);
  // A look-up may arrive with its search in the address.
  const first = new URLSearchParams(useLocation().search).get("q")?.trim() ?? "";
  const [busy, setBusy] = useState(first !== "");

  async function run(q: string) {
    try {
      setResult({ q, patrons: (await findPatrons(q)).patrons });
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  function search(q: string) {
    setBusy(true);
    setError(null);
    return run(q);
  }

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (first) void run(first);
    // Only the search the page was opened with.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="stack patrons">
      <h1>Patrons</h1>
      <section className="card stack">
        <ScanField
          label="Find a patron"
          placeholder="Name, or scan the card"
          submitLabel="Find"
          help="Type part of a name, or scan or type a card number."
          hint="Point at the patron's card"
          onScan={(code) => void search(code)}
          defaultValue={first || undefined}
          autoFocus
        />
        <InlineError error={error} />
      </section>
      {busy && <p className="muted">Looking…</p>}
      {result && !busy && (
        <section className="stack" aria-label="Matching patrons">
          <h2>
            {result.patrons.length === 0
              ? `No patron matches “${result.q}”`
              : `${plural(result.patrons.length, "patron")} matching “${result.q}”`}
          </h2>
          {result.patrons.length === 0 ? (
            <p className="card muted">Check the spelling, or scan the card again. A card that was re-issued no longer works.</p>
          ) : (
            <ul className="card plist">
              {result.patrons.map((p) => (
                <li key={p.id}>
                  <Link className="prow" to={`/desk/patrons/${p.id}`}>
                    <span className="prow-main">
                      <b>{p.display_name}</b>
                      <span className="small muted">
                        {p.username}
                        {p.card && <> · <span className="mono">{p.card}</span></>}
                      </span>
                    </span>
                    <span className="prow-badges">
                      {!p.active && <span className="badge b-warn">Deactivated</span>}
                      {p.overdue > 0 && (
                        <span className="badge b-err">
                          <Icon name="alert" />
                          {p.overdue} overdue
                        </span>
                      )}
                      <span className="badge b-plain">{p.out} out</span>
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
    </div>
  );
}
