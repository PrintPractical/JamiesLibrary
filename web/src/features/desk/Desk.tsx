import { type FormEvent, useState } from "react";
import { Link, useNavigate } from "react-router";
import { getOverdue } from "../../api/patrons";
import { useSession } from "../../app/session";
import { useLoad } from "../../app/useLoad";
import { formatLongDay, today as todayIn } from "../patron/dates";
import { InlineError } from "../../components/Errors";
import { Field } from "../../components/Field";
import { Icon } from "../../components/Icon";
import { plural } from "../patrons/loans";
import "./desk.css";

const overdueLoad = (signal: AbortSignal) => getOverdue(1, signal).then((r) => r.total);

function OverdueCard() {
  const { state, retry } = useLoad(overdueLoad);
  return (
    <section className="card desk-overdue" aria-labelledby="desk-overdue">
      <h2 id="desk-overdue">Overdue today</h2>
      {state.status === "loading" && <p className="muted">Loading…</p>}
      {state.status === "error" && (
        <>
          <InlineError error={state.error} />
          <button type="button" className="btn btn-secondary" onClick={retry}>
            Retry
          </button>
        </>
      )}
      {state.status === "ready" &&
        (state.data === 0 ? (
          <p className="muted">Nothing is overdue. Every loan is within its due date.</p>
        ) : (
          <Link className="desk-count" to="/desk/overdue">
            <b className="desk-num">{state.data}</b>
            <span>
              {plural(state.data, "loan")} past the due date
              <span className="small"> · See the overdue list</span>
            </span>
            <Icon name="right" />
          </Link>
        ))}
    </section>
  );
}

function PatronLookup() {
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [empty, setEmpty] = useState(false);
  function submit(e: FormEvent) {
    e.preventDefault();
    const term = q.trim();
    if (!term) return setEmpty(true);
    navigate(`/desk/patrons?q=${encodeURIComponent(term)}`);
  }
  return (
    <form className="card stack" onSubmit={submit} role="search" aria-labelledby="desk-find">
      <h2 id="desk-find">Find a patron</h2>
      <div className="desk-find-row">
        <Field
          label="Name or card number"
          type="search"
          value={q}
          placeholder="Name, or card number"
          autoComplete="off"
          error={empty ? "Type a name or a card number to look for." : undefined}
          onChange={(v) => {
            setQ(v);
            setEmpty(false);
          }}
        />
        <button type="submit" className="btn btn-secondary">
          Find
        </button>
      </div>
    </form>
  );
}

/** The staff home: the desk's everyday actions, each only for a user who may do it (UX-2). */
export function Desk() {
  const { me, can } = useSession();
  if (!me) return null;
  const first = me.user.display_name.split(/\s+/)[0] ?? "";
  const circ = can("circ.desk");
  const lookup = can("patrons.lookup");
  const manage = can("catalog.manage");
  const any = circ || lookup || manage;
  return (
    <div className="desk stack">
      <div className="stack-s">
        <p className="eyebrow">{formatLongDay(todayIn(me.settings.time_zone))}</p>
        <h1>Desk{first ? `, ${first}` : ""}</h1>
      </div>
      {circ && (
        <div className="desk-big">
          <Link className="btn btn-primary desk-action" to="/desk/checkout">
            <Icon name="checkout" />
            Check out
          </Link>
          <Link className="btn btn-primary desk-action" to="/desk/checkin">
            <Icon name="checkin" />
            Check in
          </Link>
        </div>
      )}
      <div className="desk-grid">
        {circ && <OverdueCard />}
        {lookup && <PatronLookup />}
      </div>
      {manage && (
        <Link className="btn btn-secondary desk-add" to="/catalog/new">
          <Icon name="catalog" />
          Add a book
        </Link>
      )}
      {!any && (
        <div className="card">
          <p>Your account has no desk actions. Use the menu for what you can open, or ask an administrator.</p>
        </div>
      )}
    </div>
  );
}
