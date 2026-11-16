import { useState } from "react";
import { errorMessage } from "../../api/client";
import { renew, type Loan } from "../../api/loans";
import { Cover } from "../../components/Cover";
import { Icon } from "../../components/Icon";
import { daysBetween, formatDay } from "./dates";

type Props = {
  loan: Loan;
  today: string;
  maxRenewals: number;
  /** Called with the loan as the server has it after a renewal. */
  onRenewed: (loan: Loan) => void;
  /** Called when a renewal was refused, so the list can be read again. */
  onRefused: () => void;
};

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

/** What the due date says: a label, a colour and an icon, so it is never colour alone, and the date where the label has none. */
function dueBadge(loan: Loan, today: string) {
  const day = formatDay(loan.due_date);
  if (loan.overdue) return { tone: "b-err", icon: "alert", text: `Overdue · ${plural(loan.days_late, "day")}`, extra: `Was due ${day}` } as const;
  const left = daysBetween(today, loan.due_date);
  if (left === 0) return { tone: "b-warn", icon: "clock", text: "Due today", extra: day } as const;
  if (left === 1) return { tone: "b-warn", icon: "clock", text: "Due tomorrow", extra: day } as const;
  return { tone: "b-ok", icon: "check", text: `Due ${day}`, extra: "" } as const;
}

export function LoanCard({ loan, today, maxRenewals, onRenewed, onRefused }: Props) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const badge = dueBadge(loan, today);
  const atLimit = loan.renewals >= maxRenewals;

  async function doRenew() {
    setBusy(true);
    setError(null);
    try {
      onRenewed(await renew(loan.id));
    } catch (err) {
      setError(errorMessage(err));
      onRefused();
    } finally {
      setBusy(false);
    }
  }

  return (
    <article className="my-loan">
      <Cover book={{ id: loan.book_id, title: loan.title, authors: loan.authors ?? [], coverUrl: "" }} size="card" />
      <div className="meta">
        <h3>{loan.title}</h3>
        {!!loan.authors?.length && <p className="small muted">{loan.authors.join(", ")}</p>}
        <div className="acts">
          <span className={`badge ${badge.tone}`}>
            <Icon name={badge.icon} />
            {badge.text}
          </span>
          {badge.extra && <span className="small muted">{badge.extra}</span>}
        </div>
        {loan.overdue ? (
          <p className="note note-err">
            <Icon name="info" />
            <span>Overdue books can't be renewed. Please bring it back when you can.</span>
          </p>
        ) : atLimit ? (
          <p className="note">
            <Icon name="info" />
            <span>
              Renewed the most times allowed ({maxRenewals} of {maxRenewals}), so it can't be renewed again. Please bring it back by{" "}
              {formatDay(loan.due_date)}.
            </span>
          </p>
        ) : (
          <div className="acts">
            <button type="button" className="btn btn-secondary" onClick={doRenew} disabled={busy}>
              <Icon name="renew" />
              {busy ? "Renewing…" : "Renew"}
              <span className="sr"> {loan.title}</span>
            </button>
            <span className="tiny muted">
              Renewed {loan.renewals} of {maxRenewals} times
            </span>
          </div>
        )}
        {error && (
          <p className="note note-err" role="alert">
            <Icon name="alert" />
            <span>{error}</span>
          </p>
        )}
      </div>
    </article>
  );
}
