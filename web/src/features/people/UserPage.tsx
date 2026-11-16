import { useState } from "react";
import { Link, useParams } from "react-router";
import {
  changeRole,
  deactivateUser,
  getUser,
  reactivateUser,
  resetPassword,
  setPermission,
  type AdminUser,
} from "../../api/admin";
import { useSession } from "../../app/session";
import { InlineError, PageError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { useLoad } from "../patrons/useLoad";
import { grantableRoles, PERMISSIONS, roleLabel, ROLES } from "./permissions";
import "./people.css";

/** One user: role, the 16 permission switches, deactivation and a password reset (ADMIN-2, ACCESS-7). */
export function UserPage() {
  const { id = "" } = useParams();
  const userId = Number(id);
  const { state, reload } = useLoad((signal) => getUser(userId, signal), userId);
  // What the last change answered with: it replaces the list's copy of this user.
  const [changed, setChanged] = useState<AdminUser | null>(null);

  if (state.status === "error") return <PageError error={state.error} onRetry={reload} />;
  if (state.status === "loading") {
    return (
      <div className="stack people">
        <h1>User</h1>
        <p className="muted">Loading…</p>
      </div>
    );
  }
  const current = changed?.id === userId ? changed : state.data;
  if (!current) {
    return (
      <div className="stack people">
        <p className="small">
          <Link to="/admin/people">← People & invites</Link>
        </p>
        <h1>No such user</h1>
        <p className="muted">This person may have been removed, or the address is wrong.</p>
      </div>
    );
  }
  return (
    <UserDetail user={current} onChanged={setChanged} />
  );
}

function UserDetail({ user, onChanged }: { user: AdminUser; onChanged: (u: AdminUser) => void }) {
  const { can, me } = useSession();
  // The server refuses to act on someone who holds what the viewer doesn't (security design §4b).
  const beyond = user.permissions.filter((p) => !can(p));
  const locked = beyond.length > 0;
  const own = me?.user.id === user.id;

  return (
    <div className="stack people">
      <p className="small">
        <Link to="/admin/people">← People & invites</Link>
      </p>
      <header className="card stack">
        <div className="phead">
          <div className="phead-main">
            <h1>{user.display_name}</h1>
            <p className="small muted">{user.username}{own ? " · this is you" : ""}</p>
          </div>
          <div className="prow-badges">
            <span className="badge b-plain">{roleLabel(user.role)}</span>
            {user.from_preset.differs && (
              <span className="badge b-warn">
                <Icon name="alert" />
                Differs from {roleLabel(user.role).toLowerCase()} preset
              </span>
            )}
            {user.active ? <span className="badge b-ok">Active</span> : <span className="badge b-err">Deactivated</span>}
            {user.must_change_password && <span className="badge b-plain">Must change password</span>}
          </div>
        </div>
        {locked && (
          <div className="alert alert-warn" role="note">
            <Icon name="alert" />
            <div>
              <p>You can't change this account: it holds permissions you don't ({beyond.map(permissionLabel).join(", ")}).</p>
            </div>
          </div>
        )}
      </header>

      {can("users.permissions") && <RoleSection user={user} locked={locked} onChanged={onChanged} />}
      {can("users.permissions") && <Switches user={user} locked={locked} onChanged={onChanged} />}
      {can("users.manage") && <Account user={user} locked={locked} onChanged={onChanged} />}
    </div>
  );
}

const permissionLabel = (id: string) => PERMISSIONS.find((p) => p.id === id)?.label ?? id;

type Section = { user: AdminUser; locked: boolean; onChanged: (u: AdminUser) => void };

function RoleSection({ user, locked, onChanged }: Section) {
  const { can } = useSession();
  // Roles whose permissions the viewer holds, and the user's own role so it shows what they have.
  const offered = ROLES.filter((r) => r.id === user.role || grantableRoles(can).some((g) => g.id === r.id));
  const [role, setRole] = useState(user.role);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  async function apply() {
    setBusy(true);
    setError(null);
    try {
      onChanged(await changeRole(user.id, role));
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="card stack" aria-labelledby="role-h">
      <h2 id="role-h">Role</h2>
      <div className="field">
        <label htmlFor="user-role">Role</label>
        <select id="user-role" className="input" value={role} onChange={(e) => setRole(e.target.value)} disabled={locked || busy}>
          {offered.map((r) => (
            <option key={r.id} value={r.id}>
              {r.label}: {r.summary}
            </option>
          ))}
        </select>
        <span className="help">Setting a role replaces all of this person's permissions with the role's preset.</span>
      </div>
      <InlineError error={error} />
      <div>
        <button type="button" className="btn btn-secondary" onClick={() => void apply()} disabled={locked || busy || role === user.role}>
          {busy ? "Saving…" : "Set role"}
        </button>
      </div>
    </section>
  );
}

function Switches({ user, locked, onChanged }: Section) {
  const { can } = useSession();
  const [busy, setBusy] = useState<string | null>(null);
  // The refusal stays under the switch that was refused.
  const [refused, setRefused] = useState<{ permission: string; error: unknown } | null>(null);
  const on = new Set(user.permissions);

  async function flip(permission: string, value: boolean) {
    setBusy(permission);
    setRefused(null);
    try {
      onChanged(await setPermission(user.id, permission, value));
    } catch (err) {
      setRefused({ permission, error: err }); // the last-administrator rule, or a permission the viewer lacks: the server says which
    } finally {
      setBusy(null);
    }
  }

  return (
    <section className="stack" aria-labelledby="perm-h">
      <h2 id="perm-h">Permissions</h2>
      <ul className="card switches">
        {PERMISSIONS.map((p) => {
          const held = can(p.id);
          const disabled = locked || !held || busy !== null;
          return (
            <li key={p.id}>
              <div className="perm-sw">
              <label className="perm-sw-text" htmlFor={`perm-${p.id}`}>
                <b>{p.label}</b>
                <span className="small muted">{p.help}</span>
                {!held && <span className="small" id={`why-${p.id}`}>You don't hold this permission, so you can't grant or take it away.</span>}
              </label>
              <input
                id={`perm-${p.id}`}
                type="checkbox"
                role="switch"
                checked={on.has(p.id)}
                disabled={disabled}
                aria-describedby={held ? undefined : `why-${p.id}`}
                onChange={(e) => void flip(p.id, e.target.checked)}
              />
              </div>
              {refused?.permission === p.id && (
                <div className="perm-sw-err">
                  <InlineError error={refused.error} />
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function Account({ user, locked, onChanged }: Section) {
  const [asking, setAsking] = useState<"deactivate" | "reset" | null>(null);
  const [busy, setBusy] = useState(false);
  // Shown once: it lives only here, and is gone when the administrator is done.
  const [temp, setTemp] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);

  async function run(action: () => Promise<AdminUser>) {
    setBusy(true);
    setError(null);
    try {
      onChanged(await action());
      setAsking(null);
    } catch (err) {
      setAsking(null);
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  async function reset() {
    setBusy(true);
    setError(null);
    try {
      const done = await resetPassword(user.id);
      setTemp(done.temporary_password);
      onChanged(done.user);
    } catch (err) {
      setError(err);
    } finally {
      setAsking(null);
      setBusy(false);
    }
  }

  return (
    <section className="card stack" aria-labelledby="acct-h">
      <h2 id="acct-h">Account</h2>
      <InlineError error={error} />
      {temp !== null && <TempPassword name={user.display_name} password={temp} onDone={() => setTemp(null)} />}
      {asking === "deactivate" && (
        <Confirm
          title={`Deactivate ${user.display_name}?`}
          yes="Yes, deactivate"
          busy={busy}
          onYes={() => void run(() => deactivateUser(user.id))}
          onNo={() => setAsking(null)}
        >
          They are signed out at once and can't sign in. Their open loans stay visible to staff, and a book can't be checked out to them.
        </Confirm>
      )}
      {asking === "reset" && (
        <Confirm
          title={`Reset ${user.display_name}'s password?`}
          yes="Yes, reset the password"
          busy={busy}
          onYes={() => void reset()}
          onNo={() => setAsking(null)}
        >
          Their password stops working and they are signed out. You get a temporary one to give them, and they must replace it at their next sign-in.
        </Confirm>
      )}
      {asking === null && (
        <div className="row actions">
          {user.active ? (
            <button type="button" className="btn btn-secondary" onClick={() => setAsking("deactivate")} disabled={locked}>
              Deactivate
            </button>
          ) : (
            <button type="button" className="btn btn-secondary" onClick={() => void run(() => reactivateUser(user.id))} disabled={locked || busy}>
              {busy ? "Reactivating…" : "Reactivate"}
            </button>
          )}
          <button type="button" className="btn btn-secondary" onClick={() => setAsking("reset")} disabled={locked}>
            Reset password
          </button>
        </div>
      )}
    </section>
  );
}

function Confirm({
  title,
  yes,
  busy,
  onYes,
  onNo,
  children,
}: {
  title: string;
  yes: string;
  busy: boolean;
  onYes: () => void;
  onNo: () => void;
  children: string;
}) {
  return (
    <div className="alert alert-warn" role="group" aria-label={title}>
      <Icon name="alert" />
      <div className="stack">
        <p className="t">{title}</p>
        <p>{children}</p>
        <div className="row actions">
          <button type="button" className="btn btn-primary" onClick={onYes} disabled={busy}>
            {busy ? "Working…" : yes}
          </button>
          <button type="button" className="btn btn-quiet" onClick={onNo} disabled={busy}>
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}

function TempPassword({ name, password, onDone }: { name: string; password: string; onDone: () => void }) {
  const [copied, setCopied] = useState<"yes" | "no" | null>(null);

  async function copy() {
    try {
      await navigator.clipboard.writeText(password);
      setCopied("yes");
    } catch {
      setCopied("no"); // no clipboard here (an insecure page, or refused): it can still be read and typed
    }
  }

  return (
    <div className="alert alert-ok" role="status" aria-label="Temporary password">
      <Icon name="check" />
      <div className="stack">
        <p className="t">Temporary password for {name}</p>
        <p className="temp-pw">{password}</p>
        <p>
          <b>This won't be shown again.</b> Give it to {name} now. They must choose a new password when they sign in.
        </p>
        <div className="row actions">
          <button type="button" className="btn btn-secondary" onClick={() => void copy()}>
            Copy
          </button>
          <button type="button" className="btn btn-primary" onClick={onDone}>
            I've passed it on
          </button>
        </div>
        {copied === "yes" && <p className="small">Copied.</p>}
        {copied === "no" && <p className="small">Couldn't copy: select the password above and copy it by hand.</p>}
      </div>
    </div>
  );
}
