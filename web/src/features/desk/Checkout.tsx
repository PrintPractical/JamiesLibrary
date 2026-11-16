import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { ApiError, errorMessage } from "../../api/client";
import { checkout, currentLoans, resolvePatron, type DeskLoan, type DeskPatron } from "../../api/desk";
import { useSession } from "../../app/session";
import { colourClass } from "../../components/Cover";
import { Icon } from "../../components/Icon";
import { ScanField } from "../../components/ScanField";
import { books, dueLabel, initials } from "./format";
import "./Checkout.css";

type Refusal = { message: string; checkIn: boolean };

function Cover({ id, title }: { id: number; title: string }) {
  return <span className={`mini-cover ${colourClass(id)}`} role="img" aria-label={`Cover of ${title}`} />;
}

function LoanRow({ loan, trailing }: { loan: DeskLoan; trailing: React.ReactNode }) {
  return (
    <>
      <Cover id={loan.book_id} title={loan.title} />
      <div className="m">
        <b>{loan.title}</b>
        <span className="code">{loan.copy_code}</span>
      </div>
      {trailing}
    </>
  );
}

function Borrowed({ patron, loans }: { patron: DeskPatron; loans: DeskLoan[] }) {
  return (
    <aside className="card desk-only" aria-labelledby="already">
      <h3 id="already">Already borrowed</h3>
      <p className="small muted">Loans from before this checkout</p>
      {loans.length === 0 ? (
        <p className="small muted">{patron.display_name} has nothing out.</p>
      ) : (
        <ul className="loans">
          {loans.map((l) => (
            <li key={l.id}>
              <LoanRow
                loan={l}
                trailing={
                  l.overdue ? (
                    <span className="badge b-err">
                      <Icon name="alert" />
                      Overdue {l.days_late} {l.days_late === 1 ? "day" : "days"}
                    </span>
                  ) : (
                    <span className="small muted">Due {dueLabel(l.due_date).replace(/^\w+ /, "")}</span>
                  )
                }
              />
            </li>
          ))}
        </ul>
      )}
    </aside>
  );
}

/** The desk's check-out: a patron's card, then each copy scanned goes out at once (CIRC-1, SCAN-3, SCAN-4). */
export function Checkout() {
  const { can } = useSession();
  const [patron, setPatron] = useState<DeskPatron | null>(null);
  const [loans, setLoans] = useState<DeskLoan[]>([]);
  const [before, setBefore] = useState<DeskLoan[] | null>(null);
  const [refusal, setRefusal] = useState<Refusal | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Scans are handled one at a time, in order, so the limit and the list agree with the server.
  const queue = useRef<Promise<void>>(Promise.resolve());
  // Bumped when a session ends: an answer that arrives after it belongs to nobody.
  const session = useRef(0);
  const canLookup = can("patrons.lookup");

  useEffect(() => {
    if (!patron || !canLookup) return;
    const ctl = new AbortController();
    currentLoans(patron.id).then(
      (l) => !ctl.signal.aborted && setBefore(l),
      () => {}, // the panel is extra; checking out doesn't need it
    );
    return () => ctl.abort();
  }, [patron?.id, canLookup]); // eslint-disable-line react-hooks/exhaustive-deps

  const end = useCallback(
    (summary: string | null) => {
      session.current++;
      // A request of the ended session that never settles must not hold up the next patron.
      queue.current = Promise.resolve();
      setBusy(false);
      setPatron(null);
      setLoans([]);
      setBefore(null);
      setRefusal(null);
      setDone(summary);
    },
    [],
  );

  // A deactivated patron's refusal isn't cleared by checking anything in.
  const refuse = (err: unknown) => {
    const message = errorMessage(err);
    // A conflict (copy already out, none available, limit reached) is cleared at check-in.
    setRefusal({ message, checkIn: err instanceof ApiError && err.status === 409 && patron?.active !== false });
  };

  function scanCard(code: string) {
    const mine = session.current;
    setBusy(true);
    setRefusal(null);
    setDone(null);
    queue.current = queue.current.then(async () => {
      try {
        const p = await resolvePatron(code);
        if (mine !== session.current) return;
        // The first card read wins: a second one still waiting belongs to no session now.
        session.current++;
        setLoans([]);
        setBefore(null);
        setRefusal(null);
        setBusy(false);
        setPatron(p);
      } catch (err) {
        if (mine !== session.current) return;
        setBusy(false);
        refuse(err);
      }
    });
  }

  function scanCopy(code: string) {
    if (!patron) return;
    const mine = session.current;
    const card = patron.card;
    queue.current = queue.current.then(async () => {
      if (mine !== session.current) return;
      try {
        const loan = await checkout(card, code);
        if (mine !== session.current) return;
        setRefusal(null);
        setLoans((l) => [...l, loan]);
      } catch (err) {
        if (mine === session.current) refuse(err);
      }
    });
  }

  if (!patron) {
    return (
      <div className="stack checkout">
        <h1>Check out</h1>
        {done && (
          <div className="alert alert-ok" role="status">
            <Icon name="check" />
            <div>{done}</div>
          </div>
        )}
        <ScanField
          label="Library card"
          placeholder="Scan the card, or type its number and press Enter"
          help="Scan or type the patron's card to begin."
          hint="Point at the library card"
          autoFocus
          onScan={scanCard}
        />
        {refusal && <Problem refusal={refusal} title="Card not read" />}
        {busy && <p className="muted small">Looking up the card…</p>}
      </div>
    );
  }

  const out = patron.out + loans.length;
  const due = loans[0]?.due_date;
  return (
    <div className="stack checkout">
      <div className="ptitle">
        <h1>Check out</h1>
        <button type="button" className="btn btn-quiet" onClick={() => end(null)}>
          <Icon name="close" />
          Next patron
        </button>
      </div>
      <div className="layout">
        <div className="stack">
          <section className="card patron" aria-label="Patron">
            <span className="avatar" aria-hidden="true">
              {initials(patron.display_name)}
            </span>
            <div className="who-patron">
              <h2>{patron.display_name}</h2>
              <div className="facts">
                <span className="code">{patron.card}</span>
                <span className="badge b-plain">
                  {out} of {books(patron.limit)} out
                </span>
                {patron.overdue > 0 && (
                  <span className="badge b-err">
                    <Icon name="alert" />
                    {patron.overdue} overdue
                  </span>
                )}
                {!patron.active && <span className="badge b-err">Deactivated</span>}
              </div>
            </div>
          </section>

          <ScanField
            label="Copy barcode"
            placeholder="Scan, or type and press Enter"
            help="Handheld scanners work here too. Each scan is added straight away."
            hint="Point at a copy's barcode"
            autoFocus
            onScan={scanCopy}
          />

          {refusal && (
            <Problem refusal={refusal} title="Not checked out">
              {refusal.checkIn && (
                <p className="goto">
                  <Link to="/desk/checkin" className="small">
                    Go to check in
                  </Link>
                </p>
              )}
            </Problem>
          )}

          <section className="card" aria-labelledby="sess">
            <div className="between">
              <h3 id="sess">This checkout · {books(loans.length)}</h3>
              {due && <span className="small muted">Due {dueLabel(due)}</span>}
            </div>
            {loans.length === 0 ? (
              <p className="small muted">Nothing checked out yet. Scan a copy to begin.</p>
            ) : (
              <ul className="loans">
                {loans.map((l) => (
                  <li key={l.id}>
                    <span className="okdot">
                      <Icon name="check" />
                    </span>
                    <LoanRow
                      loan={l}
                      trailing={
                        <span className="small muted">
                          Copy {l.copy_number} · Due {dueLabel(l.due_date).replace(/^\w+ /, "")}
                        </span>
                      }
                    />
                  </li>
                ))}
              </ul>
            )}
          </section>

          <div className="finish">
            <span className="small muted">Done scanning?</span>
            <button
              type="button"
              className="btn btn-primary"
              onClick={() => end(loans.length ? `Checked out ${books(loans.length)} to ${patron.display_name}.` : null)}
            >
              <Icon name="check" />
              {loans.length ? `Finish · ${books(loans.length)}` : "Finish"}
            </button>
          </div>
        </div>

        {before && <Borrowed patron={patron} loans={before} />}
      </div>
    </div>
  );
}

function Problem({ refusal, title, children }: { refusal: Refusal; title: string; children?: React.ReactNode }) {
  return (
    <div className="alert" role="alert">
      <Icon name="alert" />
      <div>
        <p className="t">{title}</p>
        <p className="small">{refusal.message}</p>
        {children}
      </div>
    </div>
  );
}
