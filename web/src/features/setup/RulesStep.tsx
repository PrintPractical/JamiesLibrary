import { useMemo, useState, type FormEvent } from "react";
import { putRules, type AdminSettings } from "../../api/setup";
import { InlineError } from "../../components/Errors";
import { Field } from "../../components/Field";
import { Icon } from "../../components/Icon";
import { useSubmit } from "../account/forms";
import { SetupFrame } from "./SetupFrame";

const FIELDS = ["loan_period_days", "max_loans", "max_renewals", "time_zone"] as const;

/** The browser's own time zone, when it will say. A new installation starts at UTC. */
function browserZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "";
  } catch {
    return "";
  }
}

function knownZones(): string[] {
  try {
    return (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.("timeZone") ?? [];
  } catch {
    return [];
  }
}

const whole = (s: string) => (/^\d{1,4}$/.test(s.trim()) ? Number(s) : NaN);

export function RulesStep({
  settings,
  onSaved,
  onBack,
  onNext,
}: {
  settings: AdminSettings;
  onSaved: (s: AdminSettings) => void;
  onBack: () => void;
  onNext: () => void;
}) {
  // A library still at the installation's UTC takes the zone this browser is in; one already set keeps it.
  const [zone, setZone] = useState(settings.time_zone === "UTC" ? browserZone() || "UTC" : settings.time_zone);
  const [days, setDays] = useState(String(settings.loan_period_days));
  const [loans, setLoans] = useState(String(settings.max_loans));
  const [renewals, setRenewals] = useState(String(settings.max_renewals));
  const [numberErrors, setNumberErrors] = useState<Record<string, string>>({});
  const { busy, fieldErrors, formError, run } = useSubmit(FIELDS);
  const zones = useMemo(() => knownZones(), []);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const nums = { loan_period_days: whole(days), max_loans: whole(loans), max_renewals: whole(renewals) };
    const bad: Record<string, string> = {};
    if (Number.isNaN(nums.loan_period_days)) bad.loan_period_days = "Enter a whole number of days.";
    if (Number.isNaN(nums.max_loans)) bad.max_loans = "Enter a whole number.";
    if (Number.isNaN(nums.max_renewals)) bad.max_renewals = "Enter a whole number (0 for none).";
    setNumberErrors(bad);
    if (Object.keys(bad).length > 0) return;
    await run(async () => {
      onSaved(await putRules({ time_zone: zone.trim(), ...nums }));
      onNext();
    });
  }

  const err = (f: string) => numberErrors[f] ?? fieldErrors[f];
  return (
    <SetupFrame step={3} title="How borrowing works" intro="Start with these. You can change all of this later in Settings.">
      <form className="card setup-card stack" onSubmit={submit} noValidate>
        <InlineError error={formError} />
        <Field
          label="Loan period (days)"
          help="How long a book can be kept before it is due."
          inputMode="numeric"
          value={days}
          onChange={setDays}
          error={err("loan_period_days")}
        />
        <Field
          label="Books out at once"
          help="The most one person can have checked out."
          inputMode="numeric"
          value={loans}
          onChange={setLoans}
          error={err("max_loans")}
        />
        <Field
          label="Renewals"
          help="How many times a loan can be renewed. 0 means never."
          inputMode="numeric"
          value={renewals}
          onChange={setRenewals}
          error={err("max_renewals")}
        />
        <Field
          label="Time zone"
          help="Due dates are calendar days in this zone. We've filled in the one this browser is in."
          list="setup-zones"
          value={zone}
          onChange={setZone}
          error={fieldErrors.time_zone}
          autoCapitalize="none"
          spellCheck={false}
        />
        <datalist id="setup-zones">
          {zones.map((z) => (
            <option key={z} value={z} />
          ))}
        </datalist>
        <div className="foot">
          <button type="button" className="btn btn-quiet" onClick={onBack}>
            <Icon name="left" />
            Back
          </button>
          <div className="row">
            <button type="button" className="btn btn-quiet" onClick={onNext}>
              Skip
            </button>
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {busy ? "Saving…" : "Continue to invitations"}
              <Icon name="right" />
            </button>
          </div>
        </div>
      </form>
    </SetupFrame>
  );
}
