import type { ReactNode } from "react";
import type { Loan } from "../../api/patrons";
import { Icon } from "../../components/Icon";

/** A due date ("2026-11-30") as people read it ("30 Nov 2026"), with no time zone shifting it. */
export function dueText(date: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date);
  if (!m) return date;
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  return d.toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });
}

export const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

/** "Overdue · 3 days": the words carry the meaning, not the colour. */
export function LateBadge({ days }: { days: number }) {
  return (
    <span className="badge b-err">
      <Icon name="alert" />
      Overdue · {plural(days, "day")}
    </span>
  );
}

/** A book and its authors, the first line of every loan row. */
export function LoanTitle({ loan, children }: { loan: Loan; children?: ReactNode }) {
  return (
    <div className="loan-main">
      <b className="loan-title">{loan.title}</b>
      {loan.authors.length > 0 && <span className="small muted">{loan.authors.join(", ")}</span>}
      {children}
    </div>
  );
}
