import { useId, useState } from "react";
import { putSettings, type AdminSettings, type SettingsChange } from "../../api/setup";
import { useSession } from "../../app/session";
import { InlineError } from "../../components/Errors";
import { Saved } from "./Saved";

type Row = { key: "self_checkout" | "public_catalog"; label: string; says: string };

const ROWS: Row[] = [
  {
    key: "self_checkout",
    label: "Self-checkout",
    says: "Lets a patron check out an available copy to themselves by scanning or typing its code. Patrons still return books only through a librarian.",
  },
  {
    key: "public_catalog",
    label: "Public catalog",
    says: "Lets anyone, signed in or not, search and look at the books and see whether one is available. They never see patrons, who has a loan, or loan history.",
  },
];

function Switch({ row, on, busy, onChange }: { row: Row; on: boolean; busy: boolean; onChange: (on: boolean) => void }) {
  const id = useId();
  return (
    <div className="switch-row">
      <input id={id} type="checkbox" role="switch" className="switch" checked={on} disabled={busy} aria-describedby={`${id}-says`} onChange={(e) => onChange(e.target.checked)} />
      <label htmlFor={id}>
        <span className="lbl">{row.label}</span>
        <span className="help" id={`${id}-says`}>
          {row.says} It is {on ? "on" : "off"}.
        </span>
      </label>
    </div>
  );
}

/** Self-checkout (CIRC-7) and the public catalog (ACCESS-4). Each saves as soon as it is switched. */
export function PatronSwitches({ settings, onSaved }: { settings: AdminSettings; onSaved: (s: AdminSettings) => void }) {
  const { reloadLibrary } = useSession();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [note, setNote] = useState<string>();

  async function change(row: Row, on: boolean) {
    setBusy(true);
    setError(null);
    setNote(undefined);
    try {
      const body: SettingsChange = { [row.key]: on };
      onSaved(await putSettings(body));
      if (row.key === "public_catalog") await reloadLibrary();
      setNote(`${row.label} is now ${on ? "on" : "off"}.`);
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  const value = (row: Row) => (row.key === "self_checkout" ? settings.self_checkout : settings.public_catalog);
  return (
    <section className="stack" aria-labelledby="set-switches">
      <h2 id="set-switches">What patrons can do</h2>
      <div className="card setup-card stack">
        <InlineError error={error} />
        {ROWS.map((row) => (
          <Switch key={row.key} row={row} on={value(row)} busy={busy} onChange={(on) => change(row, on)} />
        ))}
      </div>
      <Saved text={note} />
    </section>
  );
}
