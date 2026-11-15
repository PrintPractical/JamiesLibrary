import { errorMessage } from "../api/client";
import { Icon } from "./Icon";

/** An error where the action was: a form's submit, a button. Shows nothing without one. */
export function InlineError({ error }: { error: unknown }) {
  if (error === null || error === undefined) return null;
  return (
    <div className="alert" role="alert">
      <Icon name="alert" />
      <div>
        <p>{errorMessage(error)}</p>
      </div>
    </div>
  );
}

/** A page that couldn't load: what went wrong, and a way to try again. */
export function PageError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  return (
    <div className="page-error" role="alert">
      <h1>Couldn't load this page</h1>
      <p>{errorMessage(error)}</p>
      <button type="button" className="btn btn-secondary" onClick={onRetry}>
        Retry
      </button>
    </div>
  );
}
