import { useState } from "react";
import { getActivity } from "../../api/admin";
import { PageError } from "../../components/Errors";
import { useSession } from "../../app/session";
import { Pager } from "../patrons/Pager";
import { useLoad } from "../patrons/useLoad";
import { actionText, detailText, subjectText } from "./words";
import { timeText } from "./zone";
import "../patrons/patrons.css";
import "./admin.css";

/** The administrative activity log, newest first (ADMIN-4). */
export function Activity() {
  const { me } = useSession();
  const zone = me?.settings?.time_zone;
  const [page, setPage] = useState(1);
  const { state, reload } = useLoad((signal) => getActivity(page, signal), page);
  if (state.status === "error") return <PageError error={state.error} onRetry={reload} />;
  return (
    <div className="stack admin">
      <h1>Activity log</h1>
      {state.status === "loading" ? (
        <p className="muted">Loading…</p>
      ) : state.data.entries.length === 0 ? (
        <p className="card muted">Nothing has been logged yet. Changes to settings, roles and invitations appear here.</p>
      ) : (
        <>
          <p className="muted">Newest first{zone ? `, times in ${zone}` : ""}.</p>
          <ul className="card plist">
            {state.data.entries.map((e) => (
              <li key={e.id} className="entry">
                <div className="entry-main">
                  <b>{actionText(e.action)}</b>
                  <span className="small">
                    {[`by ${e.actor.display_name}`, subjectText(e), detailText(e)].filter(Boolean).join(" · ")}
                  </span>
                </div>
                <time className="small muted" dateTime={e.at}>
                  {timeText(e.at, zone)}
                </time>
              </li>
            ))}
          </ul>
          <Pager page={state.data.page} perPage={state.data.per_page} total={state.data.total} onPage={setPage} />
        </>
      )}
    </div>
  );
}
