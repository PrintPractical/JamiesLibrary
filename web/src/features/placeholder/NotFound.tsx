import { Link } from "react-router";

/** What an address nobody built shows. */
export function NotFound() {
  return (
    <main className="content">
      <h1>Page not found</h1>
      <p className="muted">There's nothing at this address.</p>
      <p>
        <Link to="/">Go to your library</Link>
      </p>
    </main>
  );
}
