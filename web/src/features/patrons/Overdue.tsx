import { useState } from "react";
import { Link } from "react-router";
import { getOverdue } from "../../api/patrons";
import { PageError } from "../../components/Errors";
import { dueText, LateBadge, LoanTitle, plural } from "./loans";
import { Pager } from "./Pager";
import { useLoad } from "./useLoad";
import "./patrons.css";

/** Loans past their due date, with the patron and the days late (CIRC-6). */
export function Overdue() {
  const [page, setPage] = useState(1);
  const { state, reload } = useLoad((signal) => getOverdue(page, signal), page);
  if (state.status === "error") return <PageError error={state.error} onRetry={reload} />;
  return (
    <div className="stack patrons">
      <h1>Overdue</h1>
      {state.status === "loading" ? (
        <p className="muted">Loading…</p>
      ) : state.data.loans.length === 0 ? (
        <p className="card muted">Nothing is overdue. Every loan is within its due date.</p>
      ) : (
        <>
          <p className="muted">{plural(state.data.total, "loan")} past the due date, the longest overdue first.</p>
          <ul className="card plist">
            {state.data.loans.map((l) => (
              <li key={l.id} className="loan">
                <LoanTitle loan={l}>
                  <span className="small">
                    <Link to={`/desk/patrons/${l.patron_id}`}>{l.patron_name}</Link>
                  </span>
                </LoanTitle>
                <div className="loan-side">
                  <LateBadge days={l.days_late} />
                  <span className="small muted">Due {dueText(l.due_date)}</span>
                </div>
              </li>
            ))}
          </ul>
          <Pager page={state.data.page} perPage={state.data.per_page} total={state.data.total} onPage={setPage} />
        </>
      )}
    </div>
  );
}
