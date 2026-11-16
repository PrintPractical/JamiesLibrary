import { type FormEvent, type ReactNode, useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { getOverdue } from "../../api/patrons";
import { adminItems, allowed } from "../../app/navigation";
import { useSession } from "../../app/session";
import { useLoad } from "../../app/useLoad";
import { Icon } from "../../components/Icon";
import { formatLongDay, today as todayIn } from "../patron/dates";
import "./desk.css";

const overdueLoad = (signal: AbortSignal) => getOverdue(1, signal).then((r) => r.total);

// "Loading…" waits this long, so a quick answer doesn't flash it.
const LOADING_DELAY_MS = 300;

function useAfter(ms: number, active: boolean): boolean {
  const [late, setLate] = useState(false);
  useEffect(() => {
    if (!active) return;
    const timer = setTimeout(() => setLate(true), ms);
    return () => {
      clearTimeout(timer);
      setLate(false);
    };
  }, [ms, active]);
  return late && active;
}

function OverdueCard() {
  const { state, retry } = useLoad(overdueLoad);
  const slow = useAfter(LOADING_DELAY_MS, state.status === "loading");
  return (
    <section className="card desk-pad" aria-labelledby="desk-od">
      <h2 id="desk-od" className="sr">
        Overdue
      </h2>
      {state.status === "loading" && <p className="muted desk-od-wait">{slow ? "Loading…" : ""}</p>}
      {state.status === "error" && (
        <div className="desk-od-fail" role="alert">
          <p>Couldn't count overdue loans.</p>
          <button type="button" className="btn btn-quiet" onClick={retry}>
            Retry
          </button>
        </div>
      )}
      {state.status === "ready" && (
        <Link className="desk-od" to="/desk/overdue">
          <span className={state.data > 0 ? "desk-od-n" : "desk-od-n desk-od-zero"}>{state.data}</span>{" "}
          <span className="desk-od-m">
            <b>{state.data === 0 ? "Nothing overdue" : `${state.data === 1 ? "loan" : "loans"} overdue`}</b>
            <br />
            <span className="small muted">See who has them</span>
          </span>
          <Icon name="right" />
        </Link>
      )}
    </section>
  );
}

function PatronLookup() {
  const navigate = useNavigate();
  const input = useRef<HTMLInputElement>(null);
  function submit(e: FormEvent) {
    e.preventDefault();
    const term = input.current?.value.trim() ?? "";
    if (!term) return input.current?.focus();
    navigate(`/desk/patrons?q=${encodeURIComponent(term)}`);
  }
  return (
    <section className="card desk-pad">
      <form className="stack" onSubmit={submit} role="search">
        <label htmlFor="desk-q" className="desk-find-label">
          Find a patron
        </label>
        <div className="desk-find-row">
          <input id="desk-q" ref={input} className="input" placeholder="Name or card number" autoComplete="off" />
          <button type="submit" className="btn btn-secondary">
            <Icon name="search" />
            <span className="sr">Search</span>
          </button>
        </div>
        <p className="help">Shows their loans, overdue books and history.</p>
      </form>
    </section>
  );
}

function Big({ to, icon, title, hint, alt }: { to: string; icon: "checkout" | "checkin"; title: string; hint: string; alt?: boolean }) {
  return (
    <Link className={alt ? "desk-big desk-big-alt" : "desk-big"} to={to}>
      <Icon name={icon} />
      <span>
        <b>{title}</b>
        <br />
        <span className="small">{hint}</span>
      </span>
    </Link>
  );
}

function Nothing({ children }: { children?: ReactNode }) {
  return (
    <div className="stack">
      <div className="alert desk-info">
        <Icon name="info" />
        <div>
          <p className="t">There's nothing for you at the desk.</p>
          <p>Your administrator decides what you can do here.</p>
        </div>
      </div>
      {children}
    </div>
  );
}

/** The staff home: the desk's everyday actions, each only for a user who may do it (UX-2). */
export function Desk() {
  const { me, can, canAny } = useSession();
  if (!me) return null;
  const circ = can("circ.desk");
  const lookup = can("patrons.lookup");
  const manage = can("catalog.manage");
  const admin = allowed(adminItems, canAny);
  return (
    <div className="desk">
      <p className="eyebrow">{formatLongDay(todayIn(me.settings.time_zone))}</p>
      <h1>Desk</h1>
      {circ && (
        <div className="desk-acts">
          <Big to="/desk/checkout" icon="checkout" title="Check out" hint="Scan a card, then books" />
          <Big to="/desk/checkin" icon="checkin" title="Check in" hint="Scan returned books" alt />
        </div>
      )}
      {(circ || lookup || manage) && (
        <div className="desk-grid">
          {circ && <OverdueCard />}
          {lookup && <PatronLookup />}
          {manage && (
            <section className="card desk-pad" aria-labelledby="desk-cat">
              <h2 id="desk-cat" className="sr">
                Catalog
              </h2>
              <Link className="desk-add" to="/catalog/new">
                <span className="desk-add-ic">
                  <Icon name="plus" />
                </span>
                <span>
                  <b>Add a book</b>
                  <br />
                  <span className="small muted">Scan or type its ISBN</span>
                </span>
                <Icon name="right" />
              </Link>
            </section>
          )}
        </div>
      )}
      {!circ && !lookup && !manage && (
        <Nothing>
          {admin.length > 0 && (
            <nav aria-label="Admin pages" className="card desk-pad">
              <ul className="desk-admin">
                {admin.map((i) => (
                  <li key={i.to}>
                    <Link to={i.to}>{i.label}</Link>
                  </li>
                ))}
              </ul>
            </nav>
          )}
        </Nothing>
      )}
    </div>
  );
}
