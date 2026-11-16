import { useState } from "react";
import { getAdminSettings, type AdminSettings } from "../../api/setup";
import { PageError } from "../../components/Errors";
import { useLoad } from "../patrons/useLoad";
import { BrandingForm } from "../setup/BrandStep";
import { RulesForm } from "../setup/RulesStep";
import { CopyIdSection } from "./CopyIdSection";
import { HttpsSection } from "./HttpsSection";
import { PatronSwitches } from "./PatronSwitches";
import { SessionSection } from "./SessionSection";
import { Saved } from "./Saved";
import "./settings.css";

/** /admin/settings: what an administrator changes after setup (ADMIN-1, CIRC-4, CIRC-7, ACCESS-4, CAT-9). */
export function Settings() {
  const { state, reload } = useLoad(getAdminSettings, "settings");
  // What a section just saved is newer than what was loaded. (The fingerprint never changes, so it is always the loaded one.)
  const [saved, setSaved] = useState<AdminSettings | null>(null);
  const [notes, setNotes] = useState<Record<string, string>>({});
  const note = (section: string) => () => setNotes((n) => ({ ...n, [section]: "Saved." }));

  if (state.status === "error") return <PageError error={state.error} onRetry={reload} />;
  if (state.status === "loading") return <p className="muted">Loading…</p>;
  const settings = saved ?? state.data;

  return (
    <div className="stack settings">
      <h1>Settings</h1>
      <p className="muted">Each section is saved on its own, and everyone sees a change straight away.</p>

      <section className="stack" aria-labelledby="set-brand">
        <h2 id="set-brand">Name and look</h2>
        <BrandingForm settings={settings} onSaved={setSaved} onDone={note("brand")} submitLabel="Save name and look" />
        <Saved text={notes.brand} />
      </section>

      <section className="stack" aria-labelledby="set-rules">
        <h2 id="set-rules">Borrowing rules</h2>
        <RulesForm settings={settings} onSaved={setSaved} onDone={note("rules")} submitLabel="Save borrowing rules" />
        <Saved text={notes.rules} />
      </section>

      <SessionSection settings={settings} onSaved={setSaved} />
      <PatronSwitches settings={settings} onSaved={setSaved} />
      <CopyIdSection settings={settings} onSaved={setSaved} />
      <HttpsSection fingerprint={state.data.ca_fingerprint} />
    </div>
  );
}
