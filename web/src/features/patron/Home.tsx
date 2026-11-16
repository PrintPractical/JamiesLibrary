import { Link } from "react-router";
import { useSession } from "../../app/session";
import { useLoad } from "../../app/useLoad";
import { myLoans, type Loan } from "../../api/loans";
import { PageError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { formatLongDay, today as todayIn } from "./dates";
import { LoanCard } from "./LoanCard";
import "./patron.css";

/** Overdue first (longest first), then by due date, so what needs attention is at the top. */
export function inOrder(loans: Loan[]): Loan[] {
  return [...loans].sort((a, b) => Number(b.overdue) - Number(a.overdue) || a.due_date.localeCompare(b.due_date) || a.id - b.id);
}

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

function summary(loans: Loan[]): string {
  if (loans.length === 0) return "You have no books out.";
  const late = loans.filter((l) => l.overdue).length;
  const out = `You have ${plural(loans.length, "book")} out.`;
  if (late === 0) return out;
  return `${out} ${late === 1 ? "One is" : `${late} are`} overdue.`;
}

function greeting(now: Date): string {
  const h = now.getHours();
  return h < 12 ? "Good morning" : h < 18 ? "Good afternoon" : "Good evening";
}

function MyBooks({ loans, zone, maxRenewals, onChange, onRefused }: {
  loans: Loan[];
  zone: string;
  maxRenewals: number;
  onChange: (loans: Loan[]) => void;
  onRefused: () => void;
}) {
  const today = todayIn(zone);
  return (
    <section aria-labelledby="out" className="books">
      <div className="sechead">
        <h2 id="out">Your books</h2>
        <Link to="/loans" className="small">
          Borrowing history
        </Link>
      </div>
      {loans.length === 0 ? (
        <div className="card">
          <p>Nothing is out on your card right now.</p>
        </div>
      ) : (
        <div className="card loans">
          {loans.map((loan) => (
            <LoanCard
              key={loan.id}
              loan={loan}
              today={today}
              maxRenewals={maxRenewals}
              onRenewed={(done) => onChange(loans.map((l) => (l.id === done.id ? done : l)))}
              onRefused={onRefused}
            />
          ))}
        </div>
      )}
    </section>
  );
}

const load = (signal: AbortSignal) => myLoans(signal).then((r) => r.loans);

const none = async (): Promise<Loan[]> => [];

export function Home() {
  const { me, can } = useSession();
  const showLoans = can("loans.own.view");
  // Without the permission the server would refuse, so nothing is asked.
  const { state, reload, retry, set } = useLoad(showLoans ? load : none);
  if (!me) return null;
  const zone = me.settings.time_zone;
  const first = me.user.display_name.split(/\s+/)[0] ?? "";
  const selfCheckout = me.settings.self_checkout && can("circ.self_checkout");
  const loans = state.status === "ready" ? inOrder(state.data) : null;
  return (
    <div className="home">
      <div className="hello">
        <div className="stack-s">
          <p className="eyebrow">{formatLongDay(todayIn(zone))}</p>
          <h1>
            {greeting(new Date())}, {first}
          </h1>
          {showLoans && loans && <p className="muted">{summary(loans)}</p>}
        </div>
        <Link className="minicard" to="/card">
          <span className="bc">
            <img src="/api/me/card/barcode.svg" alt="" />
          </span>
          <span>
            <b>Your library card</b>
            <br />
            <span className="small">Show it at the desk</span>
          </span>
          <Icon name="right" />
        </Link>
      </div>
      {can("catalog.browse") && (
        <Link className="home-search" to="/browse">
          <Icon name="search" />
          <span>Search by title, author or ISBN</span>
        </Link>
      )}
      <div className={selfCheckout ? "grid" : undefined}>
        <div className="stack-s">
          {showLoans && state.status === "error" && <PageError error={state.error} onRetry={retry} />}
          {showLoans && state.status === "loading" && <p className="muted">Loading…</p>}
          {showLoans && loans && (
            <MyBooks loans={loans} zone={zone} maxRenewals={me.settings.max_renewals} onChange={set} onRefused={reload} />
          )}
        </div>
        {selfCheckout && (
          <aside className="card selfco" aria-labelledby="selfco">
            <h2 id="selfco">Check out a book</h2>
            <p className="small muted">Scan a book's barcode to borrow it yourself, without waiting at the desk.</p>
            <Link className="btn btn-primary" to="/checkout">
              <Icon name="checkout" />
              Check out a book
            </Link>
          </aside>
        )}
      </div>
    </div>
  );
}
