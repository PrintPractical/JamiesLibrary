import { useState } from "react";
import { createInvitation } from "../../api/setup";
import { useSubmit } from "../account/forms";
import { InlineError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { SetupFrame } from "./SetupFrame";

const ROLES = [
  { value: "patron", label: "Patron: borrows books" },
  { value: "librarian", label: "Librarian: runs the desk and the catalog" },
  { value: "administrator", label: "Administrator: everything" },
];

export function InviteStep({ onBack, onDone }: { onBack: () => void; onDone: () => void }) {
  const [role, setRole] = useState("patron");
  const [made, setMade] = useState<{ id: number; code: string; role: string }[]>([]);
  const { busy, formError, run } = useSubmit([]);

  async function create() {
    await run(async () => {
      const inv = await createInvitation(role);
      if (inv.code) setMade((m) => [...m, { id: inv.id, code: inv.code as string, role: inv.role }]);
    });
  }

  return (
    <SetupFrame
      step={4}
      title="Invite people"
      intro="Make a code for each person. They enter it at the register page to create their own account. You can make more later in People & invites."
    >
      <section className="card setup-card stack">
        <InlineError error={formError} />
        <div className="field">
          <label htmlFor="setup-role">Who is it for?</label>
          <select id="setup-role" className="input" value={role} onChange={(e) => setRole(e.target.value)}>
            {ROLES.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </select>
        </div>
        <button type="button" className="btn btn-secondary" onClick={create} disabled={busy}>
          {busy ? "Making the code…" : "Create invitation"}
        </button>
        {made.length > 0 && (
          <div className="stack">
            <p className="help">
              Each code works once. Send it to the person along with the address of this library, {window.location.origin}/register.
            </p>
            <ul className="codes" aria-label="Invitations you made">
              {made.map((m) => (
                <li key={m.id}>
                  <code>{m.code}</code>
                  <span className="badge b-plain">{m.role}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
        <div className="foot">
          <button type="button" className="btn btn-quiet" onClick={onBack}>
            <Icon name="left" />
            Back
          </button>
          <button type="button" className="btn btn-primary" onClick={onDone}>
            {made.length > 0 ? "Finish setup" : "Skip and finish"}
            <Icon name="check" />
          </button>
        </div>
      </section>
    </SetupFrame>
  );
}
