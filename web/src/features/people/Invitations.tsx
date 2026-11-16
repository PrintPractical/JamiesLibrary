import { useEffect, useState } from "react";
import { createInvitation, listInvitations, listUsers, revokeInvitation, type Invitation } from "../../api/admin";
import { useSession } from "../../app/session";
import { InlineError, PageError } from "../../components/Errors";
import { Pager } from "../patrons/Pager";
import { useLoad } from "../patrons/useLoad";
import { grantableRoles, roleLabel, ROLES } from "./permissions";

const STATUS: Record<string, string> = { unused: "b-ok", used: "b-plain", revoked: "b-err" };

/** Who made the codes, by name where the viewer may list users, else by what the server gave. */
function useNames(enabled: boolean, ownId: number | undefined) {
  const [names, setNames] = useState<Record<number, string>>({});
  useEffect(() => {
    if (!enabled) return;
    const ctl = new AbortController();
    listUsers(1, 100, ctl.signal).then(
      (p) => setNames(Object.fromEntries(p.users.map((u) => [u.id, u.display_name]))),
      () => undefined, // names are a nicety: the list works without them
    );
    return () => ctl.abort();
  }, [enabled]);
  return (id: number) => (id === ownId ? "You" : (names[id] ?? `User ${id}`));
}

/** Invitation codes: make one for a role the viewer may hand out, see them all, revoke (ACCESS-3). */
export function Invitations() {
  const { can, me } = useSession();
  const roles = grantableRoles(can);
  const [page, setPage] = useState(1);
  const { state, reload } = useLoad((signal) => listInvitations(page, signal), page);
  const creator = useNames(can("users.manage"), me?.user.id);

  const [role, setRole] = useState(roles[0]?.id ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  async function create() {
    setBusy(true);
    setError(null);
    try {
      await createInvitation(role);
      if (page === 1) reload();
      else setPage(1);
    } catch (err) {
      setError(err); // e.g. the role needs a permission the viewer no longer holds
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="stack" aria-labelledby="inv-h">
      <h2 id="inv-h">Invitations</h2>
      <div className="card stack">
        <div className="field">
          <label htmlFor="inv-role">Role the new account gets</label>
          <select id="inv-role" className="input" value={role} onChange={(e) => setRole(e.target.value)}>
            {roles.map((r) => (
              <option key={r.id} value={r.id}>
                {r.label}: {r.summary}
              </option>
            ))}
          </select>
          {roles.length < ROLES.length && (
            <span className="help">Roles that give a permission you don't hold aren't offered.</span>
          )}
        </div>
        <InlineError error={error} />
        <div>
          <button type="button" className="btn btn-primary" onClick={() => void create()} disabled={busy || roles.length === 0}>
            {busy ? "Making the code…" : "Create invitation"}
          </button>
        </div>
      </div>
      {state.status === "error" ? (
        <PageError error={state.error} onRetry={reload} />
      ) : state.status === "loading" ? (
        <p className="muted">Loading…</p>
      ) : state.data.invitations.length === 0 ? (
        <p className="card muted">There are no invitations yet.</p>
      ) : (
        <>
          <ul className="card plist" aria-label="Invitation codes">
            {state.data.invitations.map((i) => (
              <InvitationRow key={i.id} invitation={i} creator={creator(i.creator_id)} onChanged={reload} />
            ))}
          </ul>
          <Pager page={state.data.page} perPage={state.data.per_page} total={state.data.total} onPage={setPage} />
        </>
      )}
    </section>
  );
}

function InvitationRow({ invitation: i, creator, onChanged }: { invitation: Invitation; creator: string; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  // A code withheld by the server is above this viewer's permissions: no code, and revoking would be refused.
  const withheld = i.code === null;

  async function revoke() {
    setBusy(true);
    setError(null);
    try {
      await revokeInvitation(i.id);
      onChanged();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="loan loan-wrap">
      <div className="loan-line">
        <div className="loan-main">
          {withheld ? (
            <span className="muted">Code hidden: it gives permissions you don't hold</span>
          ) : (
            <code className="mono">{i.code}</code>
          )}
          <span className="small muted">
            {roleLabel(i.role)} · made by {creator} on {i.created_at.slice(0, 10)}
          </span>
        </div>
        <div className="loan-side">
          <span className={`badge ${STATUS[i.status] ?? "b-plain"}`}>{i.status.charAt(0).toUpperCase() + i.status.slice(1)}</span>
          {i.status === "unused" && !withheld && (
            <button type="button" className="btn btn-secondary" onClick={() => void revoke()} disabled={busy} aria-label={`Revoke ${i.code}`}>
              {busy ? "Revoking…" : "Revoke"}
            </button>
          )}
        </div>
      </div>
      <InlineError error={error} />
    </li>
  );
}
