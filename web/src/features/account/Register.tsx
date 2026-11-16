import { useState, type FormEvent } from "react";
import { Link, Navigate } from "react-router";
import { register } from "../../api/auth";
import { homeFor } from "../../app/landing";
import { useSession } from "../../app/session";
import { InlineError } from "../../components/Errors";
import { Field } from "../../components/Field";
import { AuthPage } from "./AuthPage";
import { useSubmit } from "./forms";

const FIELDS = ["code", "username", "display_name", "password"] as const;

export function Register() {
  const { me, setMe, canAny } = useSession();
  const [code, setCode] = useState("");
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const { busy, fieldErrors, formError, run } = useSubmit(FIELDS);

  if (me) return <Navigate to={homeFor(canAny)} replace />;

  async function submit(e: FormEvent) {
    e.preventDefault();
    await run(async () => {
      const created = await register({ code, username: username.trim(), display_name: displayName.trim(), password });
      setPassword("");
      setMe(created);
    });
  }

  return (
    <AuthPage title="Create your account">
      <form className="stack" onSubmit={submit} noValidate>
        <InlineError error={formError} />
        <Field
          label="Invitation code"
          help="From a librarian or an administrator, like XXXX-XXXX-XXXX-XXXX."
          value={code}
          onChange={setCode}
          error={fieldErrors.code}
          autoComplete="off"
          autoCapitalize="characters"
          spellCheck={false}
          required
        />
        <Field
          label="Your name"
          help="How the library shows you."
          value={displayName}
          onChange={setDisplayName}
          error={fieldErrors.display_name}
          autoComplete="name"
          required
        />
        <Field
          label="Username"
          help="What you type to sign in."
          value={username}
          onChange={setUsername}
          error={fieldErrors.username}
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          required
        />
        <Field
          label="Password"
          help="At least 10 characters."
          type="password"
          value={password}
          onChange={setPassword}
          error={fieldErrors.password}
          autoComplete="new-password"
          required
        />
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? "Creating your account…" : "Create account"}
        </button>
        <p className="small muted">
          Already have an account? <Link to="/signin">Sign in</Link>.
        </p>
      </form>
    </AuthPage>
  );
}
