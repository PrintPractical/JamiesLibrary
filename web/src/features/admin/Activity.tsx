import { useState } from "react";
import { getActivity, type ActivityEntry } from "../../api/admin";
import { PageError } from "../../components/Errors";
import { useSession } from "../../app/session";
import { Pager } from "../patrons/Pager";
import { useLoad } from "../patrons/useLoad";
import { timeText } from "./zone";
import "../patrons/patrons.css";
import "./admin.css";

const ACTIONS: Record<string, string> = {
  "settings.changed": "Changed settings",
  "user.role_changed": "Changed a role",
  "user.permission_changed": "Changed a permission",
  "user.deactivated": "Deactivated an account",
  "user.reactivated": "Reactivated an account",
  "user.password_reset": "Reset a password",
  "invitation.created": "Created an invitation",
  "invitation.revoked": "Revoked an invitation",
  "card.reissued": "Reissued a card",
  "review.removed": "Removed a review",
};

/** "user:7" as "user 7". */
const targetText = (target: string) => target.replace(":", " ");

const str = (v: unknown) => (typeof v === "string" || typeof v === "number" ? String(v) : "");

/** A short sentence about what changed, for the actions whose details say it; "" otherwise. */
function detailText(e: ActivityEntry): string {
  const d = e.details ?? {};
  if (e.action === "user.role_changed" && d.from && d.to) return `${str(d.from)} to ${str(d.to)}`;
  if (e.action === "user.permission_changed" && d.permission) return `${str(d.permission)} ${d.on ? "granted" : "removed"}`;
  if (e.action === "invitation.created" && d.role) return `for ${str(d.role)}`;
  return "";
}

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
                  <b>{ACTIONS[e.action] ?? e.action}</b>
                  <span className="small">
                    {e.actor.display_name}
                    {e.target && <span className="muted"> · {targetText(e.target)}</span>}
                    {detailText(e) && <span className="muted"> · {detailText(e)}</span>}
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
