import { useState } from "react";
import { Link, useParams } from "react-router";
import { getHistory, getPatron, reissueCard, renewLoan, type Loan, type Patron } from "../../api/patrons";
import { useSession } from "../../app/session";
import { InlineError, PageError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { dueText, LateBadge, LoanTitle, plural } from "./loans";
import { Pager } from "./Pager";
import { useLoad } from "./useLoad";
import "./patrons.css";

/** One patron: their loans now, and what they have had (CIRC-3), with renewing (CIRC-5) and a new card (CARD-1). */
export function PatronPage() {
  const { id = "" } = useParams();
  const { can } = useSession();
  const { state, reload } = useLoad((signal) => getPatron(id, signal), id);
  // What the last renewal did, to say at the top of the loans: a refusal stays on its row instead.
  const [renewed, setRenewed] = useState<string | null>(null);
  // A new card number, shown once to the librarian who made it.
  const [newCard, setNewCard] = useState<string | null>(null);

  if (state.status === "error") return <PageError error={state.error} onRetry={reload} />;
  if (state.status === "loading") {
    return (
      <div className="stack patrons">
        <h1>Patron</h1>
        <p className="muted">Loading…</p>
      </div>
    );
  }
  const { patron, current } = state.data;
  return (
    <div className="stack patrons">
      <p className="small">
        <Link to="/desk/patrons">← All patrons</Link>
      </p>
      <header className="card stack">
        <div className="phead">
          <div className="phead-main">
            <h1>{patron.display_name}</h1>
            <p className="small muted">
              {patron.username} · Card <span className="mono">{newCard ?? (patron.card || "none")}</span>
            </p>
          </div>
          <div className="prow-badges">
            {!patron.active && <span className="badge b-warn">Deactivated</span>}
            {patron.overdue > 0 && (
              <span className="badge b-err">
                <Icon name="alert" />
                {patron.overdue} overdue
              </span>
            )}
            <span className="badge b-plain">{patron.out} out</span>
          </div>
        </div>
        {can("cards.reissue") && <Reissue patron={patron} onDone={(n) => setNewCard(n)} />}
        {newCard && (
          <div className="alert alert-ok" role="status">
            <Icon name="check" />
            <div>
              <p className="t">New card issued</p>
              <p>
                {patron.display_name}'s card number is now <b className="mono">{newCard}</b>. The old card no longer works.
              </p>
            </div>
          </div>
        )}
      </header>

      <section className="stack" aria-labelledby="loans-h">
        <h2 id="loans-h">On loan now</h2>
        {renewed && (
          <div className="alert alert-ok" role="status">
            <Icon name="check" />
            <div>
              <p>{renewed}</p>
            </div>
          </div>
        )}
        {current.length === 0 ? (
          <p className="card muted">Nothing is out on loan.</p>
        ) : (
          <ul className="card plist">
            {current.map((l) => (
              <CurrentLoan key={l.id} loan={l} canRenew={can("circ.desk")} onRenewed={(msg) => { setRenewed(msg); reload(); }} />
            ))}
          </ul>
        )}
      </section>

      <History patronId={id} />
    </div>
  );
}

function CurrentLoan({ loan, canRenew, onRenewed }: { loan: Loan; canRenew: boolean; onRenewed: (message: string) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  async function renew() {
    setBusy(true);
    setError(null);
    try {
      const next = await renewLoan(loan.id);
      onRenewed(`Renewed ${loan.title}: now due ${dueText(next.due_date)}.`);
    } catch (err) {
      setError(err); // overdue, or renewed the most times allowed: the server says which
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="loan loan-wrap">
      <div className="loan-line">
        <LoanTitle loan={loan}>
          <span className="small muted">
            Due {dueText(loan.due_date)} · {plural(loan.renewals, "renewal")} so far
          </span>
        </LoanTitle>
        <div className="loan-side">
          {loan.days_late > 0 && <LateBadge days={loan.days_late} />}
          {canRenew && (
            <button type="button" className="btn btn-secondary" onClick={() => void renew()} disabled={busy} aria-label={`Renew ${loan.title}`}>
              {busy ? "Renewing…" : "Renew"}
            </button>
          )}
        </div>
      </div>
      <InlineError error={error} />
    </li>
  );
}

function Reissue({ patron, onDone }: { patron: Patron; onDone: (number: string) => void }) {
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  async function confirm() {
    setBusy(true);
    setError(null);
    try {
      onDone((await reissueCard(patron.id)).number);
      setAsking(false);
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  if (!asking) {
    return (
      <div>
        <button type="button" className="btn btn-secondary" onClick={() => setAsking(true)}>
          Re-issue card
        </button>
      </div>
    );
  }
  return (
    <div className="alert alert-warn" role="group" aria-label="Confirm re-issuing the card">
      <Icon name="alert" />
      <div className="stack">
        <p className="t">Re-issue {patron.display_name}'s card?</p>
        <p>The old card will stop working straight away: scanning it will find no one. A new number is made.</p>
        <InlineError error={error} />
        <div className="row patron-actions">
          <button type="button" className="btn btn-primary" onClick={() => void confirm()} disabled={busy}>
            {busy ? "Re-issuing…" : "Yes, re-issue the card"}
          </button>
          <button type="button" className="btn btn-quiet" onClick={() => setAsking(false)} disabled={busy}>
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}

function History({ patronId }: { patronId: string }) {
  const [page, setPage] = useState(1);
  const { state, reload } = useLoad((signal) => getHistory(patronId, page, signal), `${patronId}/${page}`);
  return (
    <section className="stack" aria-labelledby="hist-h">
      <h2 id="hist-h">Borrowing history</h2>
      {state.status === "error" ? (
        <PageError error={state.error} onRetry={reload} />
      ) : state.status === "loading" ? (
        <p className="muted">Loading…</p>
      ) : state.data.loans.length === 0 ? (
        <p className="card muted">This patron hasn't borrowed anything yet.</p>
      ) : (
        <>
          <ul className="card plist">
            {state.data.loans.map((l) => (
              <li key={l.id} className="loan">
                <LoanTitle loan={l} />
                <div className="loan-side small muted">
                  <span>
                    Borrowed {dueText(l.checked_out_at.slice(0, 10))} · {l.returned_at ? `Returned ${dueText(l.returned_at.slice(0, 10))}` : "Still out"}
                  </span>
                </div>
              </li>
            ))}
          </ul>
          <Pager page={state.data.page} perPage={state.data.per_page} total={state.data.total} onPage={setPage} />
        </>
      )}
    </section>
  );
}
