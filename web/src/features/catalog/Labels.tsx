import { useState, type FormEvent } from "react";
import { Link, useLocation } from "react-router";
import { LAYOUTS, getBook, printLabels, searchBooks, type Copy, type MemberBook } from "../../api/catalog";
import { InlineError, PageError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { useLoad } from "../patrons/useLoad";
import "./addbook.css";
import "./manage.css";

/** A copy chosen for a label, with what the list shows for it. */
type Chosen = { id: number; text: string };

const capital = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/** /catalog/labels: choose copies and a sheet layout, and get the labels as a PDF (CAT-7). */
export function Labels() {
  const start = Number(new URLSearchParams(useLocation().search).get("book")) || 0;
  const [chosen, setChosen] = useState<Map<number, Chosen>>(new Map());
  const [layout, setLayout] = useState<string>(LAYOUTS[0].id);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [pdf, setPdf] = useState<string | null>(null);

  const toggle = (c: Chosen, on: boolean) =>
    setChosen((cur) => {
      const next = new Map(cur);
      if (on) next.set(c.id, c);
      else next.delete(c.id);
      return next;
    });

  async function print() {
    setBusy(true);
    setError(null);
    try {
      const blob = await printLabels([...chosen.keys()], layout);
      if (pdf) URL.revokeObjectURL(pdf);
      const url = URL.createObjectURL(blob);
      setPdf(url);
      window.open(url, "_blank", "noopener");
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="stack addbook">
      <Link className="ab-back" to="/catalog">
        <Icon name="left" />
        Catalog
      </Link>
      <h1>Print labels</h1>
      <div className="ab-layout">
        <Picker start={start} chosen={chosen} onToggle={toggle} />
        <section className="card stack" aria-labelledby="lb-sheet">
          <h2 id="lb-sheet">The sheet</h2>
          <div className="field">
            <label htmlFor="lb-layout">Label sheet</label>
            <select id="lb-layout" className="input" value={layout} onChange={(e) => setLayout(e.target.value)}>
              {LAYOUTS.map((l) => (
                <option key={l.id} value={l.id}>
                  {l.label}
                </option>
              ))}
            </select>
          </div>
          <p className="small" role="status">
            {chosen.size === 0 ? "No copies chosen yet." : `${chosen.size} ${chosen.size === 1 ? "copy" : "copies"} chosen.`}
          </p>
          {chosen.size > 0 && (
            <ul className="mg-chosen" aria-label="Chosen copies">
              {[...chosen.values()].map((c) => (
                <li key={c.id}>
                  <span>{c.text}</span>
                  <button type="button" className="btn btn-quiet" aria-label={`Leave out ${c.text}`} onClick={() => toggle(c, false)}>
                    Remove
                  </button>
                </li>
              ))}
            </ul>
          )}
          <button type="button" className="btn btn-primary" disabled={busy || chosen.size === 0} onClick={() => void print()}>
            {busy ? "Making the labels…" : "Make the labels"}
          </button>
          {chosen.size === 0 && <p className="help">Choose at least one copy to print its label.</p>}
          <InlineError error={error} />
          {pdf && (
            <p className="small">
              The labels are ready.{" "}
              <a href={pdf} target="_blank" rel="noopener">
                Open the PDF
              </a>{" "}
              if it did not open.
            </p>
          )}
        </section>
      </div>
    </div>
  );
}

function Picker({ start, chosen, onToggle }: { start: number; chosen: Map<number, Chosen>; onToggle: (c: Chosen, on: boolean) => void }) {
  const [term, setTerm] = useState("");
  const [query, setQuery] = useState<string | null>(null);
  const [bookId, setBookId] = useState(start);

  const results = useLoad((signal) => (query === null ? Promise.resolve(null) : searchBooks(query, 0, 1, signal)), `q|${query}`);
  const book = useLoad((signal) => (bookId ? getBook(String(bookId), signal) : Promise.resolve(null)), `b|${bookId}`);

  function submit(e: FormEvent) {
    e.preventDefault();
    setQuery(term.trim());
    setBookId(0);
  }

  return (
    <section className="card stack" aria-labelledby="lb-pick">
      <h2 id="lb-pick">Choose copies</h2>
      <form className="search" role="search" onSubmit={submit}>
        <div className="field grow">
          <label htmlFor="lb-q">Find a book</label>
          <input id="lb-q" className="input" type="search" value={term} maxLength={200} placeholder="Title, author or ISBN" onChange={(e) => setTerm(e.target.value)} />
        </div>
        <button type="submit" className="btn btn-secondary">
          Search
        </button>
      </form>
      {results.state.status === "error" && <PageError error={results.state.error} onRetry={results.reload} />}
      {results.state.status === "ready" && results.state.data && (
        <ul className="mg-list" aria-label="Books found">
          {results.state.data.books.length === 0 && <li className="muted">No book matches “{query}”.</li>}
          {results.state.data.books.map((b) => (
            <li key={b.id}>
              <button type="button" className="mg-pick" onClick={() => setBookId(b.id)}>
                <b>{b.title}</b>
                {b.authors.length > 0 && <span className="small muted"> · {b.authors.join(", ")}</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
      {bookId > 0 && book.state.status === "loading" && (
        <p className="muted" role="status">
          Loading copies…
        </p>
      )}
      {bookId > 0 && book.state.status === "error" && <PageError error={book.state.error} onRetry={book.reload} />}
      {bookId > 0 && book.state.status === "ready" && book.state.data && <BookCopies book={book.state.data} chosen={chosen} onToggle={onToggle} />}
    </section>
  );
}

function BookCopies({ book, chosen, onToggle }: { book: MemberBook; chosen: Map<number, Chosen>; onToggle: (c: Chosen, on: boolean) => void }) {
  if (book.copies.length === 0) return <p className="muted">“{book.title}” has no copies yet.</p>;
  const label = (c: Copy): Chosen => ({ id: c.id, text: `${book.title} · copy ${c.number} · ${c.location}` });
  const all = book.copies.map(label);
  const every = all.every((c) => chosen.has(c.id));
  return (
    <fieldset className="mg-copies-pick">
      <legend>Copies of “{book.title}”</legend>
      <label className="mg-check">
        <input type="checkbox" checked={every} onChange={(e) => all.forEach((c) => onToggle(c, e.target.checked))} />
        <span>All copies</span>
      </label>
      {book.copies.map((c) => (
        <label key={c.id} className="mg-check">
          <input type="checkbox" checked={chosen.has(c.id)} onChange={(e) => onToggle(label(c), e.target.checked)} />
          <span>
            Copy {c.number} · {c.location} · {capital(c.condition)}
          </span>
        </label>
      ))}
    </fieldset>
  );
}
