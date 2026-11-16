import { useSession } from "../../app/session";
import { AuthPage } from "./AuthPage";
import { ChangePasswordForm } from "./ChangePassword";
import { useState } from "react";
import { InlineError } from "../../components/Errors";

/** What a user sees, and all they can reach, after an administrator reset their password (ACCESS-7). */
export function ForcedChange() {
  const { signOut } = useSession();
  const [error, setError] = useState<unknown>(null);
  return (
    <AuthPage title="Choose a new password">
      <div className="stack">
        <p>An administrator reset your password. Choose a new one to carry on.</p>
        <ChangePasswordForm />
        <InlineError error={error} />
        <button type="button" className="btn btn-quiet" onClick={() => signOut().catch(setError)}>
          Sign out
        </button>
      </div>
    </AuthPage>
  );
}
