import { useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { getLocations, searchBooks, searchPublic } from "../../api/catalog";
import { PageError } from "../../components/Errors";
import { Pager } from "../patrons/Pager";
import { useLoad } from "../patrons/useLoad";
import { useSession } from "../../app/session";
import { Cover } from "../../components/Cover";
import { Availability } from "./parts";
import "./catalog.css";

/** Browse and search the catalog (CAT-2, CAT-8). A visitor under the public catalog searches by term only (ACCESS-4). */
export function Browse() {
  const { me } = useSession();
  // The search lives in the address, so it can be bookmarked and the back button works. (Not react-router's
  // useSearchParams: its bundled warning text names a URL, which the build check for foreign hosts would refuse.)
  const params = new URLSearchParams(useLocation().search);
  const navigate = useNavigate();
  const q = params.get("q") ?? "";
  const location = me ? Number(params.get("location")) || 0 : 0;
  const page = Math.max(1, Number(params.get("page")) || 1);

  const results = useLoad(
    (signal) => (me ? searchBooks(q, location, page, signal) : searchPublic(q, page, signal)),
    `${me ? "m" : "v"}|${q}|${location}|${page}`,
  );
  const locations = useLoad((signal) => (me ? getLocations(signal) : Promise.resolve([])), me ? "m" : "v");

  const [term, setTerm] = useState(q);

  function go(next: { q?: string; location?: number; page?: number }) {
    const p = new URLSearchParams();
    const nq = next.q ?? q;
    const nl = next.location ?? location;
    const np = next.page ?? 1;
    if (nq) p.set("q", nq);
    if (nl) p.set("location", String(nl));
    if (np > 1) p.set("page", String(np));
    const qs = p.toString();
    navigate(qs ? `?${qs}` : ".");
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    go({ q: term.trim(), page: 1 });
  }

  const locationList = locations.state.status === "ready" ? locations.state.data : [];
  const chosen = locationList.find((l) => l.id === location);

  return (
    <div className="stack browse">
      <h1>Browse</h1>
      <form className="card catalog-search" role="search" onSubmit={submit}>
        <div className="field grow">
          <label htmlFor="browse-q">Search the library</label>
          <input
            id="browse-q"
            className="input"
            type="search"
            value={term}
            maxLength={200}
            placeholder="Title, author or ISBN"
            onChange={(e) => setTerm(e.target.value)}
          />
        </div>
        {me && locationList.length > 0 && (
          <div className="field">
            <label htmlFor="browse-loc">Location</label>
            <select id="browse-loc" className="input" value={location} onChange={(e) => go({ location: Number(e.target.value) })}>
              <option value={0}>All locations</option>
              {locationList.map((l) => (
                <option key={l.id} value={l.id}>
                  {l.name}
                </option>
              ))}
            </select>
          </div>
        )}
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
      {results.state.status === "ready" && (
        <section className="stack" aria-label="Results">
          {results.state.data.books.length === 0 ? (
            <div className="card">
              <h2>Nothing found</h2>
              <p className="muted">
                {q || chosen
                  ? `No book matches${q ? ` “${q}”` : ""}${chosen ? ` in ${chosen.name}` : ""}. Check the spelling, or try fewer words${chosen ? " or another location" : ""}.`
                  : "There are no books in the library yet."}
              </p>
            </div>
          ) : (
            <>
              <p className="small muted" role="status">
                {results.state.data.total === 1 ? "1 book" : `${results.state.data.total} books`}
                {q ? ` matching “${q}”` : ""}
                {chosen ? ` in ${chosen.name}` : ""}
              </p>
              <ul className="card books">
                {results.state.data.books.map((b) => (
                  <li key={b.id}>
                    <Link className="book-row" to={`/books/${b.id}`}>
                      <Cover book={b} size="mini" />
                      <span className="book-main">
                        <b className="book-title">{b.title}</b>
                        {b.authors.length > 0 && <span className="small muted">{b.authors.join(", ")}</span>}
                      </span>
                      <Availability available={b.available} total={b.total} />
                    </Link>
                  </li>
                ))}
              </ul>
              <Pager
                page={results.state.data.page}
                perPage={results.state.data.perPage}
                total={results.state.data.total}
                onPage={(p) => go({ page: p })}
              />
            </>
          )}
        </section>
      )}
    </div>
  );
}
