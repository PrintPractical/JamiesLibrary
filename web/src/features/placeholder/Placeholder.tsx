/** Stands in for a screen that a later task builds. */
export function Placeholder({ title }: { title: string }) {
  return (
    <div className="placeholder">
      <h1>{title}</h1>
      <p className="muted">This page isn't built yet.</p>
    </div>
  );
}
