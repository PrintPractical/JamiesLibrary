import { useState, type FormEvent } from "react";
import { changePassword } from "../../api/auth";
import { useSession } from "../../app/session";
import { InlineError } from "../../components/Errors";
import { Field } from "../../components/Field";
import { Icon } from "../../components/Icon";
import { useSubmit } from "./forms";

const FIELDS = ["current_password", "new_password"] as const;

/** Changing one's own password. `onDone` runs once the server has accepted it. */
export function ChangePasswordForm({ onDone }: { onDone?: () => void }) {
  const { refresh } = useSession();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [changed, setChanged] = useState(false);
  const { busy, fieldErrors, formError, run } = useSubmit(FIELDS);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setChanged(false);
    const ok = await run(async () => {
      await changePassword(current, next);
      setCurrent("");
      setNext("");
      await refresh();
    });
    if (ok) {
      setChanged(true);
      onDone?.();
    }
  }

  return (
    <form className="stack" onSubmit={submit} noValidate>
      <InlineError error={formError} />
      {changed && (
        <div className="alert alert-ok" role="status">
          <Icon name="check" />
          <div>
            <p>Your password is changed.</p>
          </div>
        </div>
      )}
      <Field
        label="Current password"
        type="password"
        value={current}
        onChange={setCurrent}
        error={fieldErrors.current_password}
        autoComplete="current-password"
        required
      />
      <Field
        label="New password"
        help="At least 10 characters."
        type="password"
        value={next}
        onChange={setNext}
        error={fieldErrors.new_password}
        autoComplete="new-password"
        required
      />
      <button type="submit" className="btn btn-primary" disabled={busy}>
        {busy ? "Saving…" : "Change password"}
      </button>
    </form>
  );
}
