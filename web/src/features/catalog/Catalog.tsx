import { useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { searchBooks } from "../../api/catalog";
import { PageError } from "../../components/Errors";
import { Pager } from "../patrons/Pager";
import { useLoad } from "../patrons/useLoad";
import { Availability, Cover } from "./parts";
import "./catalog.css";
import "./manage.css";

/** /catalog: where staff find a book to edit, and the way to the other catalog tools (CAT-5, CAT-7, CAT-8). */
export function Catalog() {
  const params = new URLSearchParams(useLocation().search);
  const navigate = useNavigate();
  const q = params.get("q") ?? "";
  const page = Math.max(1, Number(params.get("page")) || 1);
  const [term, setTerm] = useState(q);
  const results = useLoad((signal) => searchBooks(q, 0, page, signal), `${q}|${page}`);

  function go(nextQ: string, nextPage: number) {
    const p = new URLSearchParams();
    if (nextQ) p.set("q", nextQ);
    if (nextPage > 1) p.set("page", String(nextPage));
    const qs = p.toString();
    navigate(qs ? `?${qs}` : ".");
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    go(term.trim(), 1);
  }

  return (
    <div className="stack browse">
      <h1>Catalog</h1>
      <div className="row mg-tools">
        <Link className="btn btn-primary" to="/catalog/new">
          Add a book
        </Link>
        <Link className="btn btn-secondary" to="/catalog/locations">
          Locations
        </Link>
        <Link className="btn btn-secondary" to="/catalog/labels">
          Print labels
        </Link>
      </div>
      <form className="card search" role="search" onSubmit={submit}>
        <div className="field grow">
          <label htmlFor="cat-q">Find a book to change</label>
          <input
            id="cat-q"
            className="input"
            type="search"
            value={term}
            maxLength={200}
            placeholder="Title, author or ISBN"
            onChange={(e) => setTerm(e.target.value)}
          />
        </div>
        <button type="submit" className="btn btn-primary">
          Search
        </button>
      </form>
      {results.state.status === "loading" && (
        <p className="muted" role="status">
          Loading books…
        </p>
      )}
      {results.state.status === "error" && <PageError error={results.state.error} onRetry={results.reload} />}
      {results.state.status === "ready" &&
        (results.state.data.books.length === 0 ? (
          <div className="card">
            <h2>{q ? "Nothing found" : "No books yet"}</h2>
            <p className="muted">{q ? `No book matches “${q}”. Check the spelling, or try fewer words.` : "Add the first book to start the catalog."}</p>
          </div>
        ) : (
          <>
            <ul className="card books" aria-label="Books">
              {results.state.data.books.map((b) => (
                <li key={b.id}>
                  <Link className="book-row" to={`/catalog/books/${b.id}`}>
                    <Cover book={b} className="mini" />
                    <span className="book-main">
                      <b className="book-title">{b.title}</b>
                      {b.authors.length > 0 && <span className="small muted">{b.authors.join(", ")}</span>}
                    </span>
                    <Availability available={b.available} total={b.total} />
                  </Link>
                </li>
              ))}
            </ul>
            <Pager page={results.state.data.page} perPage={results.state.data.perPage} total={results.state.data.total} onPage={(p) => go(q, p)} />
          </>
        ))}
    </div>
  );
}
