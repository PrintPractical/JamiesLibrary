import { useEffect, useState, type FormEvent } from "react";
import { Navigate } from "react-router";
import { createAdmin, setupStatus } from "../../api/setup";
import { ApiError } from "../../api/client";
import { homeFor } from "../../app/landing";
import { useSession } from "../../app/session";
import { InlineError, PageError } from "../../components/Errors";
import { Field } from "../../components/Field";
import { useSubmit } from "../account/forms";
import { SetupFrame } from "./SetupFrame";
import { Wizard } from "./Wizard";

/**
 * /setup. A visitor sees step 1 while the server says setup is open, and is sent
 * to sign in once it is closed. The administrator it creates is signed in, and
 * carries on with steps 2 to 4 here (DEPLOY-3).
 */
export function Setup() {
  const { me, canAny } = useSession();
  if (me) return canAny(["settings.manage"]) ? <Wizard /> : <Navigate to={homeFor(canAny)} replace />;
  return <AdminStep />;
}

type Status = "loading" | "open" | "closed" | { error: unknown };

const FIELDS = ["code", "display_name", "username", "password"] as const;

function AdminStep() {
  const { setMe } = useSession();
  const [status, setStatus] = useState<Status>("loading");
  const [attempt, setAttempt] = useState(0);
  const [code, setCode] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const { busy, fieldErrors, formError, run } = useSubmit(FIELDS);

  useEffect(() => {
    const ctl = new AbortController();
    setupStatus(ctl.signal).then(
      () => setStatus("open"),
      (error: unknown) => {
        if (ctl.signal.aborted) return;
        setStatus(error instanceof ApiError && error.status === 404 ? "closed" : { error });
      },
    );
    return () => ctl.abort();
  }, [attempt]);

  if (status === "closed") return <Navigate to="/signin" replace />;
  if (typeof status === "object") {
    return (
      <div className="content">
        <PageError error={status.error} onRetry={() => (setStatus("loading"), setAttempt((n) => n + 1))} />
      </div>
    );
  }
  if (status === "loading") {
    return (
      <p className="content muted" role="status">
        Loading…
      </p>
    );
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    await run(async () => {
      const created = await createAdmin({ code, username: username.trim(), display_name: displayName.trim(), password });
      setPassword("");
      setMe(created); // the page now shows step 2
    });
  }

  return (
    <SetupFrame step={1} title="Create the administrator" intro="You'll run the library from this account. Only someone who can see the server can start it.">
      <form className="card setup-card stack" onSubmit={submit} noValidate>
        <InlineError error={formError} />
        <Field
          label="Setup code"
          help="Printed by the server when it started, on the line that begins SETUP CODE. In Docker, see the container's logs."
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
          {busy ? "Creating the account…" : "Create account and continue"}
        </button>
      </form>
    </SetupFrame>
  );
}
