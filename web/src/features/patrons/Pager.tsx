/** Previous and next for a list the server pages. Shows nothing when everything fits on one page. */
export function Pager({ page, perPage, total, onPage }: { page: number; perPage: number; total: number; onPage: (p: number) => void }) {
  const last = Math.max(1, Math.ceil(total / perPage));
  if (last === 1) return null;
  return (
    <nav className="pager" aria-label="Pages">
      <button type="button" className="btn btn-secondary" disabled={page <= 1} onClick={() => onPage(page - 1)}>
        Previous
      </button>
      <span className="small muted">
        Page {page} of {last}
      </span>
      <button type="button" className="btn btn-secondary" disabled={page >= last} onClick={() => onPage(page + 1)}>
        Next
      </button>
    </nav>
  );
}
