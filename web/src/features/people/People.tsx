import { useState } from "react";
import { Link } from "react-router";
import { listUsers } from "../../api/admin";
import { useSession } from "../../app/session";
import { PageError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { Pager } from "../patrons/Pager";
import { useLoad } from "../patrons/useLoad";
import { Invitations } from "./Invitations";
import { roleLabel } from "./permissions";
import "./people.css";

/** People & invites: who has an account (ADMIN-2) and the codes that make new ones (ACCESS-3). */
export function People() {
  const { can } = useSession();
  return (
    <div className="stack people">
      <h1>People & invites</h1>
      {can("users.manage") && <UserList />}
      {can("invitations.manage") && <Invitations />}
      {!can("users.manage") && !can("invitations.manage") && (
        <p className="card muted">Changing roles and permissions is done from a person's own page, which needs the permission to manage users.</p>
      )}
    </div>
  );
}

function UserList() {
  const [page, setPage] = useState(1);
  const { state, reload } = useLoad((signal) => listUsers(page, 25, signal), page);
  return (
    <section className="stack" aria-labelledby="users-h">
      <h2 id="users-h">Users</h2>
      {state.status === "error" ? (
        <PageError error={state.error} onRetry={reload} />
      ) : state.status === "loading" ? (
        <p className="muted">Loading…</p>
      ) : (
        <>
          <ul className="card plist">
            {state.data.users.map((u) => (
              <li key={u.id}>
                <Link className="prow" to={`/admin/people/${u.id}`}>
                  <span className="prow-main">
                    <b>{u.display_name}</b>
                    <span className="small muted">{u.username}</span>
                  </span>
                  <span className="prow-badges">
                    <span className="badge b-plain">{roleLabel(u.role)}</span>
                    {u.from_preset.differs && (
                      <span className="badge b-warn">
                        <Icon name="alert" />
                        Differs from {roleLabel(u.role).toLowerCase()} preset
                      </span>
                    )}
                    {u.active ? <span className="badge b-ok">Active</span> : <span className="badge b-err">Deactivated</span>}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
          <Pager page={state.data.page} perPage={state.data.per_page} total={state.data.total} onPage={setPage} />
        </>
      )}
    </section>
  );
}
