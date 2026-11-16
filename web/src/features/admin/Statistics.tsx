import { useState, type FormEvent } from "react";
import { getCirculation, getStatistics, type Circulation } from "../../api/admin";
import { ApiError, errorMessage } from "../../api/client";
import { InlineError, PageError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { useSession } from "../../app/session";
import { dueText } from "../patrons/loans";
import { useLoad } from "../patrons/useLoad";
import { dayText } from "./zone";
import "./admin.css";

const LABELS = [
  ["books", "Books"],
  ["copies", "Copies"],
  ["patrons", "Patrons"],
  ["current_loans", "On loan now"],
  ["overdue_loans", "Overdue"],
] as const;

/** Library statistics and the checkouts and returns report (ADMIN-3). Counts only: no patron is named. */
export function Statistics() {
  const { state, reload } = useLoad((signal) => getStatistics(signal), 0);
  if (state.status === "error") return <PageError error={state.error} onRetry={reload} />;
  return (
    <div className="stack admin">
      <h1>Statistics</h1>
      {state.status === "loading" ? (
        <p className="muted">Loading…</p>
      ) : (
        <section aria-label="Library statistics">
          <dl className="stats">
            {LABELS.map(([key, label]) => (
              <div key={key} className={key === "overdue_loans" && state.data[key] > 0 ? "card stat stat-late" : "card stat"}>
                <dt className="small muted">{label}</dt>
                <dd>{state.data[key].toLocaleString()}</dd>
              </div>
            ))}
          </dl>
        </section>
      )}
      <CirculationReport />
    </div>
  );
}

function CirculationReport() {
  const { me } = useSession();
  const zone = me?.settings?.time_zone;
  const [from, setFrom] = useState(() => dayText(new Date(), zone, 29));
  const [to, setTo] = useState(() => dayText(new Date(), zone));
  const [result, setResult] = useState<Circulation | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);

  async function run(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      setResult(await getCirculation(from, to));
    } catch (err) {
      setResult(null);
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  const field = error instanceof ApiError ? error.field : undefined;
  const fieldError = (name: string) => (field === name ? errorMessage(error) : undefined);
  return (
    <section className="card stack" aria-labelledby="circ-h">
      <h2 id="circ-h">Checkouts and returns</h2>
      <form className="range" onSubmit={run} noValidate>
        <DateField label="From" value={from} onChange={setFrom} error={fieldError("from")} />
        <DateField label="To" value={to} onChange={setTo} error={fieldError("to")} />
        <button type="submit" className="btn btn-primary" disabled={busy}>
          Show report
        </button>
      </form>
      {field !== "from" && field !== "to" && <InlineError error={error} />}
      {result && (
        <div role="status" className="result">
          <p className="muted small">
            {dueText(result.from)} to {dueText(result.to)}, both days included
          </p>
          <dl className="stats">
            <div className="card stat">
              <dt className="small muted">Checkouts</dt>
              <dd>{result.checkouts.toLocaleString()}</dd>
            </div>
            <div className="card stat">
              <dt className="small muted">Returns</dt>
              <dd>{result.returns.toLocaleString()}</dd>
            </div>
          </dl>
        </div>
      )}
    </section>
  );
}

function DateField({ label, value, onChange, error }: { label: string; value: string; onChange: (v: string) => void; error?: string }) {
  return (
    <div className="field">
      <label>
        {label}
        <input
          type="date"
          className={error ? "input input-error" : "input"}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          aria-invalid={error ? true : undefined}
        />
      </label>
      {error && (
        <span className="field-error" role="alert">
          <Icon name="alert" />
          {error}
        </span>
      )}
    </div>
  );
}
