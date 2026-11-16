import { useState, type FormEvent } from "react";
import { putSettings, type AdminSettings } from "../../api/setup";
import { InlineError } from "../../components/Errors";
import { Field } from "../../components/Field";
import { useSubmit } from "../account/forms";
import { Saved } from "./Saved";

/** How long a signed-in browser may sit unused before it must sign in again. The server holds the limits. */
export function SessionSection({ settings, onSaved }: { settings: AdminSettings; onSaved: (s: AdminSettings) => void }) {
  const [minutes, setMinutes] = useState(String(settings.session_idle_minutes));
  const [note, setNote] = useState<string>();
  const [numberError, setNumberError] = useState<string>();
  const { busy, fieldErrors, formError, run } = useSubmit(["session_idle_minutes"]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setNote(undefined);
    if (!/^\d{1,6}$/.test(minutes.trim())) {
      setNumberError("Enter a whole number of minutes.");
      return;
    }
    setNumberError(undefined);
    if (await run(async () => onSaved(await putSettings({ session_idle_minutes: Number(minutes) })))) setNote("Saved.");
  }

  return (
    <section className="stack" aria-labelledby="set-session">
      <h2 id="set-session">Signing in</h2>
      <form className="card setup-card stack" onSubmit={submit} noValidate>
        <InlineError error={formError} />
        <Field
          label="Sign out after (minutes unused)"
          help="Someone who hasn't used the library for this long has to sign in again. From 15 minutes to 30 days (43200)."
          inputMode="numeric"
          value={minutes}
          onChange={setMinutes}
          error={numberError ?? fieldErrors.session_idle_minutes}
        />
        <div className="foot">
          <span />
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {busy ? "Saving…" : "Save timeout"}
          </button>
        </div>
      </form>
      <Saved text={note} />
    </section>
  );
}
