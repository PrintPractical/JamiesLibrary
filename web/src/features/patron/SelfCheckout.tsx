import { useRef, useState } from "react";
import { Link } from "react-router";
import { useSession } from "../../app/session";
import { errorMessage } from "../../api/client";
import { selfCheckout, type Loan } from "../../api/loans";
import { Icon } from "../../components/Icon";
import { ScanField } from "../../components/ScanField";
import { formatDay } from "./dates";
import "./patron.css";

// What the screen keeps of this visit's scans, newest first: enough to see what happened, bounded.
const KEEP = 20;

type Outcome = { key: number; code: string } & ({ loan: Loan } | { refusal: string });

/** A patron lends a book to themselves by scanning it (CIRC-7). The server decides every time. */
export function SelfCheckout() {
  const { me } = useSession();
  const [outcomes, setOutcomes] = useState<Outcome[]>([]);
  const next = useRef(0);
  // A code being sent: the camera reads the same barcode over and over while it is held up.
  const sending = useRef(new Set<string>());
  if (!me) return null;

  if (!me.settings.self_checkout) {
    return (
      <div className="home">
        <h1>Check out a book</h1>
        <div className="alert" role="alert">
          <Icon name="info" />
          <div>
            <p className="t">Self-checkout is switched off</p>
            <p>Take the book to a librarian to check it out.</p>
          </div>
        </div>
      </div>
    );
  }

  async function scan(code: string) {
    if (sending.current.has(code)) return;
    sending.current.add(code);
    let outcome: Outcome;
    try {
      outcome = { key: next.current++, code, loan: await selfCheckout(code) };
    } catch (err) {
      outcome = { key: next.current++, code, refusal: errorMessage(err) };
    } finally {
      sending.current.delete(code);
    }
    setOutcomes((all) => [outcome, ...all].slice(0, KEEP));
  }

  return (
    <div className="home checkout">
      <div className="stack-s">
        <h1>Check out a book</h1>
        <p className="muted">Scan the barcode on the book, or type it in. Books are returned at the desk.</p>
      </div>
      <div className="card stack-s">
        <ScanField label="Book code" onScan={scan} submitLabel="Check out" autoFocus />
      </div>
      <section aria-labelledby="done" aria-live="polite" className="stack-s">
        <h2 id="done">This visit</h2>
        {outcomes.length === 0 && <p className="muted">Nothing scanned yet.</p>}
        {outcomes.map((o) =>
          "loan" in o ? (
            <div key={o.key} className="alert alert-ok">
              <Icon name="check" />
              <div>
                <p className="t">Checked out: {o.loan.title}</p>
                <p>
                  Copy {o.loan.copy_number}
                  {o.loan.copy_code && (
                    <>
                      {" "}
                      (<span className="code">{o.loan.copy_code}</span>)
                    </>
                  )}
                  , due {formatDay(o.loan.due_date)}
                </p>
              </div>
            </div>
          ) : (
            <div key={o.key} className="alert" role="alert">
              <Icon name="alert" />
              <div>
                <p className="t">Not checked out</p>
                <p>{o.refusal}</p>
                <p className="small">
                  Scanned: <span className="code">{o.code}</span>
                </p>
              </div>
            </div>
          ),
        )}
      </section>
      <p>
        <Link to="/" className="small">
          Back to your books
        </Link>
      </p>
    </div>
  );
}
