import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "../../api/client";
import { checkin, type CheckinResult, type DeskLoan } from "../../api/desk";
import { InlineError } from "../../components/Errors";
import { colourClass } from "../../components/Cover";
import { Icon, type IconName } from "../../components/Icon";
import { ScanField } from "../../components/ScanField";
import "./checkin.css";

// The session list is kept in this page only; a long shift shouldn't grow it without end.
const MAX_ENTRIES = 200;

type Tone = "ok" | "warn" | "err" | "plain";

/** One scan's outcome: what the large card and the session list both show. */
type Entry = {
  id: number;
  tone: Tone;
  icon: IconName;
  /** The large card's heading line. */
  head: string;
  /** The list's label on the right. */
  badge: string;
  title: string;
  code: string;
  /** What happened, said under the title in the list. */
  note: string;
  loan?: DeskLoan;
};

/** A book's open loans, waiting for the librarian to say which one came back. */
type Choice = { code: string; message: string; title: string; loans: DeskLoan[] };

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

function dueText(due: string): string {
  const d = new Date(`${due}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return due;
  return d.toLocaleDateString("en-GB", { weekday: "short", day: "numeric", month: "short", timeZone: "UTC" });
}

const copyLabel = (l: Pick<DeskLoan, "copy_code" | "copy_number">) => l.copy_code || `Copy ${l.copy_number}`;

function returned(id: number, loan: DeskLoan): Entry {
  const late = loan.days_late > 0;
  return {
    id,
    tone: late ? "warn" : "ok",
    icon: late ? "warning" : "check",
    head: late ? `Checked in · returned ${plural(loan.days_late, "day")} late` : "Checked in",
    badge: late ? `${plural(loan.days_late, "day")} late` : "On time",
    title: loan.title,
    code: copyLabel(loan),
    note: `from ${loan.patron_name}`,
    loan,
  };
}

function notOut(id: number, r: CheckinResult): Entry {
  const copy = r.copy;
  return {
    id,
    tone: "plain",
    icon: "info",
    head: copy ? "Already in · wasn't checked out" : "None is checked out",
    badge: copy ? "Already in" : "None out",
    title: copy?.title ?? r.book?.title ?? "",
    code: copy ? copyLabel({ copy_code: copy.code, copy_number: copy.number }) : "",
    note: copy ? "wasn't checked out, nothing changed" : "none is checked out",
  };
}

function unknown(id: number, code: string): Entry {
  return { id, tone: "err", icon: "close", head: "Not found", badge: "Not found", title: "Nothing matches that code", code, note: "check the label, or type it again" };
}

function Cover({ loan }: { loan: DeskLoan }) {
  const authors = (loan.authors ?? []).join(", ");
  return (
    <div className={`ci-cover ${colourClass(loan.book_id)}`} aria-hidden="true">
      <span className="t">{loan.title}</span>
      <span className="a">{authors}</span>
    </div>
  );
}

function Latest({ entry }: { entry: Entry }) {
  const loan = entry.loan;
  return (
    <section className={`ci-latest ${entry.tone}`} role="status" aria-live="polite" aria-label="Latest scan">
      <div className="ci-head">
        <Icon name={entry.icon} />
        {entry.head}
      </div>
      <div className="ci-body">
        {loan && <Cover loan={loan} />}
        <div>
          <h2>{entry.title}</h2>
          {loan ? (
            <p className="small">
              <span className="ci-code">{entry.code}</span> · borrowed by <b>{loan.patron_name}</b>, was due{" "}
              {dueText(loan.due_date)}
            </p>
          ) : (
            <p className="small">
              {entry.code && <span className="ci-code">{entry.code}</span>}
              {entry.code && " · "}
              {entry.note}
            </p>
          )}
          {loan?.location && (
            <div className="ci-where">
              <Icon name="pin" />
              Put it back: {loan.location}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}

function ChoosePanel({ choice, onPick, onCancel }: { choice: Choice; onPick: (l: DeskLoan) => void; onCancel: () => void }) {
  return (
    <section className="card ci-pad ci-choose" aria-labelledby="ci-choose-title">
      <h3 id="ci-choose-title">Which copy of {choice.title} came back?</h3>
      <p className="small muted">{choice.message}</p>
      <ul className="ci-log">
        {choice.loans.map((l) => (
          <li key={l.id}>
            <div className="m">
              <b>
                {l.patron_name} · Copy {l.copy_number}
              </b>
              <span className="small muted">
                {l.copy_code && <span className="ci-code">{l.copy_code} · </span>}
                due {dueText(l.due_date)}
                {l.days_late > 0 && ` · ${plural(l.days_late, "day")} late`}
              </span>
            </div>
            <button
              type="button"
              className="btn btn-secondary"
              aria-label={`Return copy ${l.copy_number} from ${l.patron_name}`}
              onClick={() => onPick(l)}
            >
              Return
            </button>
          </li>
        ))}
      </ul>
      <div className="actions">
        <button type="button" className="btn btn-quiet" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </section>
  );
}

/** The desk's check-in: continuous scanning with no patron, the latest outcome large and every outcome listed. */
export function CheckIn() {
  const [entries, setEntries] = useState<Entry[]>([]);
  const [choice, setChoice] = useState<Choice | null>(null);
  // What went wrong with a scan that has no outcome of its own: the server or the network failed it.
  const [problem, setProblem] = useState<unknown>(null);
  const next = useRef(1);
  // Scans are handled one at a time, in the order they were made, so a fast scanner can't
  // reorder the list or answer a choice that has already been settled.
  const queue = useRef<Promise<void>>(Promise.resolve());
  const pending = useRef<Choice | null>(null);

  const show = useCallback((c: Choice | null) => {
    pending.current = c;
    setChoice(c);
  }, []);

  const record = useCallback((e: Entry) => {
    setEntries((all) => [e, ...all].slice(0, MAX_ENTRIES));
  }, []);

  const run = useCallback(
    async (code: string, which: { loanId?: number; card?: string }): Promise<boolean> => {
      setProblem(null);
      try {
        const r = await checkin(code, which);
        const id = next.current++;
        if (r.status === "choose" && r.choices?.length) {
          show({ code, message: r.message, title: r.choices[0]?.title ?? r.book?.title ?? "", loans: r.choices });
        } else if (r.status === "returned" && r.loan) {
          show(null);
          record(returned(id, r.loan));
        } else if (r.status === "not_out") {
          show(null);
          record(notOut(id, r));
        } else {
          setProblem(new Error("unexpected reply"));
        }
        return true;
      } catch (err) {
        // No patron has that card: the caller decides what the code was instead.
        if (which.card && err instanceof ApiError && err.status === 404) return false;
        // A code nothing has is an outcome of its own. Anything else (a card that isn't this
        // loan's, the library being unreachable) is said where the scan was and changes nothing.
        if (!which.card && !which.loanId && err instanceof ApiError && err.status === 404) {
          record(unknown(next.current++, code));
        } else {
          setProblem(err);
        }
        return true;
      }
    },
    [record, show],
  );

  const scan = useCallback(
    (code: string) => {
      queue.current = queue.current.then(async () => {
        const open = pending.current;
        if (!open) {
          await run(code, {});
          return;
        }
        // With a choice open, a scan is taken for the patron's card. A code that is no patron's
        // card is the next book: the choice is dropped and that code is checked in.
        if (await run(open.code, { card: code })) return;
        show(null);
        await run(code, {});
      });
    },
    [run, show],
  );

  const pick = useCallback(
    (loan: DeskLoan) => {
      queue.current = queue.current.then(async () => {
        const open = pending.current;
        if (open) await run(open.code, { loanId: loan.id });
      });
    },
    [run],
  );

  const latest = entries[0];

  // The button that settled a choice goes with it, and focus would fall to the page: give it back
  // to the scan field so the next scan isn't lost.
  const page = useRef<HTMLDivElement>(null);
  const wasOpen = useRef(false);
  useEffect(() => {
    if (wasOpen.current && !choice) page.current?.querySelector("input")?.focus({ preventScroll: true });
    wasOpen.current = choice !== null;
  }, [choice]);

  return (
    <div ref={page}>
      <h1>Check in</h1>
      <p className="muted ci-lead">Scan each returned book. No need to pick a patron first.</p>
      <div className="ci-layout">
        <div className="stack">
          <ScanField
            label={choice ? "Patron's card" : "Copy barcode or ISBN"}
            placeholder="Scan, or type and press Enter"
            submitLabel={choice ? "Find patron" : "Check in"}
            help={choice ? "Scan the card of the patron who is returning it, or choose below." : "Ready for the next book."}
            onScan={scan}
            autoFocus
          />
          <InlineError error={problem} />
          {choice && <ChoosePanel choice={choice} onPick={pick} onCancel={() => show(null)} />}
          {latest && !choice && <Latest entry={latest} />}
        </div>
        <section className="card ci-session" aria-labelledby="ci-log-title">
          <div className="ci-card-head">
            <h3 id="ci-log-title">This session · {plural(entries.length, "scan")}</h3>
            {entries.length > 0 && (
              <button type="button" className="btn btn-quiet small" onClick={() => setEntries([])}>
                Clear
              </button>
            )}
          </div>
          {entries.length === 0 ? (
            <p className="muted small ci-empty">Nothing scanned yet.</p>
          ) : (
            <ul className="ci-log">
              {entries.map((e) => (
                <li key={e.id}>
                  <span className={`ci-dot ${e.tone}`}>
                    <Icon name={e.icon} />
                  </span>
                  <div className="m">
                    <b>{e.title}</b>
                    <span className="small muted">
                      {e.code && <span className="ci-code">{e.code}</span>}
                      {e.code && " · "}
                      {e.note}
                    </span>
                  </div>
                  <span className={`ci-badge ${e.tone}`}>{e.badge}</span>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </div>
  );
}
