import { useState } from "react";
import { useSession } from "../../app/session";
import { InlineError } from "../../components/Errors";
import { ChangePasswordForm } from "./ChangePassword";

export function Account() {
  const { me, signOut } = useSession();
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  if (!me) return null;

  async function leave() {
    setBusy(true);
    setError(null);
    try {
      await signOut();
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  }

  return (
    <div className="stack account">
      <h1>Account</h1>
      <section className="card">
        <p>
          <b>{me.user.display_name}</b>
        </p>
        <p className="muted small">Signed in as {me.user.username}</p>
      </section>
      <section className="card stack">
        <h2>Change your password</h2>
        <ChangePasswordForm />
      </section>
      <section className="stack">
        <InlineError error={error} />
        <div>
          <button type="button" className="btn btn-secondary" onClick={leave} disabled={busy}>
            {busy ? "Signing out…" : "Sign out"}
          </button>
        </div>
      </section>
    </div>
  );
}
