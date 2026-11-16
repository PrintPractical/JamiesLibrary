import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { getAdminSettings, type AdminSettings } from "../../api/setup";
import { homeFor } from "../../app/landing";
import { useSession } from "../../app/session";
import { PageError } from "../../components/Errors";
import { BrandStep } from "./BrandStep";
import { InviteStep } from "./InviteStep";
import { RulesStep } from "./RulesStep";

/** Steps 2 to 4, for the administrator who is signed in. Every step can be skipped and changed later in settings. */
export function Wizard() {
  const { canAny } = useSession();
  const navigate = useNavigate();
  const [step, setStep] = useState<2 | 3 | 4>(2);
  const [settings, setSettings] = useState<AdminSettings | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const ctl = new AbortController();
    getAdminSettings(ctl.signal).then(setSettings, (err: unknown) => {
      if (!ctl.signal.aborted) setError(err);
    });
    return () => ctl.abort();
  }, [attempt]);

  if (error) {
    return (
      <div className="content">
        <PageError error={error} onRetry={() => (setError(null), setAttempt((n) => n + 1))} />
      </div>
    );
  }
  if (!settings) {
    return (
      <p className="content muted" role="status">
        Loading…
      </p>
    );
  }
  if (step === 2) return <BrandStep settings={settings} onSaved={setSettings} onNext={() => setStep(3)} />;
  if (step === 3) {
    return <RulesStep settings={settings} onSaved={setSettings} onBack={() => setStep(2)} onNext={() => setStep(4)} />;
  }
  return <InviteStep onBack={() => setStep(3)} onDone={() => navigate(homeFor(canAny), { replace: true })} />;
}
