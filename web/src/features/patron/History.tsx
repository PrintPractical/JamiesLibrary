import { useCallback, useState } from "react";
import { Link } from "react-router";
import { useSession } from "../../app/session";
import { useLoad } from "../../app/useLoad";
import { myHistory } from "../../api/loans";
import { PageError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { formatStamp } from "./dates";
import "./patron.css";

/** Every book the signed-in patron has borrowed, newest first, a page at a time (PATRON-1). */
export function History() {
  const { me } = useSession();
  const [page, setPage] = useState(1);
  const load = useCallback((signal: AbortSignal) => myHistory(page, signal), [page]);
  const { state, retry } = useLoad(load);
  if (!me) return null;
  const zone = me.settings.time_zone;

  let body;
  if (state.status === "error") {
    body = <PageError error={state.error} onRetry={retry} />;
  } else if (state.status === "loading") {
    body = <p className="muted">Loading…</p>;
  } else {
    const { loans, per_page, total } = state.data;
    const pages = Math.max(1, Math.ceil(total / Math.max(1, per_page)));
    body =
      total === 0 ? (
        <div className="card">
          <p>You haven't borrowed anything yet.</p>
        </div>
      ) : (
        <>
          <ul className="card history">
            {loans.map((l) => (
              <li key={l.id}>
                <div className="meta">
                  <h3>{l.title}</h3>
                  {!!l.authors?.length && <p className="small muted">{l.authors.join(", ")}</p>}
                </div>
                <p className="small muted dates">
                  <span>Borrowed {formatStamp(l.checked_out_at, zone)}</span>
                  <span>{l.returned_at ? `Returned ${formatStamp(l.returned_at, zone)}` : "Still out"}</span>
                </p>
              </li>
            ))}
          </ul>
          {pages > 1 && (
            <nav className="pager" aria-label="Pages">
              <button type="button" className="btn btn-secondary" disabled={page <= 1} onClick={() => setPage(page - 1)}>
                <Icon name="left" />
                Newer
              </button>
              <span className="small muted">
                Page {page} of {pages}
              </span>
              <button type="button" className="btn btn-secondary" disabled={page >= pages} onClick={() => setPage(page + 1)}>
                Older
                <Icon name="right" />
              </button>
            </nav>
          )}
        </>
      );
  }

  return (
    <div className="home">
      <div className="stack-s">
        <h1>Borrowing history</h1>
        <p>
          <Link to="/" className="small">
            Back to your books
          </Link>
        </p>
      </div>
      {body}
    </div>
  );
}
