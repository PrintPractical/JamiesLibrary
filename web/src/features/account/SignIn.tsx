import { useState, type FormEvent } from "react";
import { Link, Navigate, useLocation } from "react-router";
import { signIn } from "../../api/auth";
import { homeFor } from "../../app/landing";
import { useSession } from "../../app/session";
import { InlineError } from "../../components/Errors";
import { Field } from "../../components/Field";
import { AuthPage } from "./AuthPage";
import { useSubmit } from "./forms";

/** Where a visitor was heading before the guard sent them here. */
export type FromState = { from?: string } | null;

export function SignIn() {
  const { me, setMe, canAny } = useSession();
  const location = useLocation();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const { busy, formError, run } = useSubmit([]);
  const from = (location.state as FromState)?.from;

  if (me) return <Navigate to={from ?? homeFor(canAny)} replace />;

  async function submit(e: FormEvent) {
    e.preventDefault();
    await run(async () => {
      const signedIn = await signIn(username.trim(), password);
      setPassword("");
      setMe(signedIn); // the page then leaves for where the visitor was going

    });
  }

  return (
    <AuthPage title="Sign in">
      <form className="stack" onSubmit={submit} noValidate>
        <InlineError error={formError} />
        <Field
          label="Username"
          value={username}
          onChange={setUsername}
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          required
        />
        <Field label="Password" type="password" value={password} onChange={setPassword} autoComplete="current-password" required />
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? "Signing in…" : "Sign in"}
        </button>
        <p className="small muted">
          Have an invitation code? <Link to="/register">Create your account</Link>.
        </p>
      </form>
    </AuthPage>
  );
}
