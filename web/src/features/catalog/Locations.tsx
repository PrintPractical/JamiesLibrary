import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { createLocation, deleteLocation, getLocations, renameLocation, type Location } from "../../api/catalog";
import { ApiError, errorMessage } from "../../api/client";
import { Field } from "../../components/Field";
import { InlineError, PageError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { useLoad } from "../patrons/useLoad";
import "./addbook.css";
import "./manage.css";

const copies = (n: number) => `${n} ${n === 1 ? "copy" : "copies"}`;

/** /catalog/locations: the named places copies live in (CAT-8). */
export function Locations() {
  const { state, reload } = useLoad((signal) => getLocations(signal), "locations");
  return (
    <div className="stack addbook mg-narrow">
      <Link className="ab-back" to="/catalog">
        <Icon name="left" />
        Catalog
      </Link>
      <h1>Locations</h1>
      <p className="help">Every copy lives in one location, such as a room or a shelf. Renaming a location renames it for all its copies.</p>
      {state.status === "loading" && (
        <p className="muted" role="status">
          Loading locations…
        </p>
      )}
      {state.status === "error" && <PageError error={state.error} onRetry={reload} />}
      {state.status === "ready" && (
        <>
          <ul className="card mg-list" aria-label="Locations">
            {state.data.map((l) => (
              <LocationRow key={l.id} location={l} others={state.data.filter((o) => o.id !== l.id)} onChanged={reload} />
            ))}
          </ul>
          <NewLocation onAdded={reload} />
        </>
      )}
    </div>
  );
}

function NewLocation({ onAdded }: { onAdded: () => void }) {
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (name.trim() === "") {
      setError("Enter the new location's name.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await createLocation(name.trim());
      setName("");
      onAdded();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="card ab-form" onSubmit={(e) => void submit(e)} aria-label="New location">
      <h2>Add a location</h2>
      <Field label="Name" value={name} maxLength={100} disabled={busy} error={error} onChange={setName} />
      <div className="ab-actions">
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? "Adding…" : "Add location"}
        </button>
      </div>
    </form>
  );
}

type RowMode = { kind: "view" } | { kind: "rename" } | { kind: "delete" } | { kind: "move"; count: number };

function LocationRow({ location, others, onChanged }: { location: Location; others: Location[]; onChanged: () => void }) {
  const [mode, setMode] = useState<RowMode>({ kind: "view" });
  const [name, setName] = useState(location.name);
  const [target, setTarget] = useState("");
  const [fieldError, setFieldError] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);

  const back = () => {
    setMode({ kind: "view" });
    setError(null);
    setFieldError("");
  };

  async function rename(e: FormEvent) {
    e.preventDefault();
    if (name.trim() === "") {
      setFieldError("Enter a name.");
      return;
    }
    setBusy(true);
    setFieldError("");
    try {
      await renameLocation(location.id, name.trim());
      back();
      onChanged();
    } catch (err) {
      setFieldError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  /** Tries the delete. A location that holds copies is refused, and the librarian is offered moving them. */
  async function tryDelete() {
    setBusy(true);
    setError(null);
    try {
      await deleteLocation(location.id);
      onChanged();
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && location.copies > 0) setMode({ kind: "move", count: location.copies });
      else setError(err);
      setBusy(false);
    }
  }

  async function moveAndDelete() {
    if (target === "") {
      setFieldError("Choose where to move the copies.");
      return;
    }
    setBusy(true);
    setError(null);
    setFieldError("");
    try {
      await deleteLocation(location.id, Number(target));
      onChanged();
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  }

  return (
    <li className="mg-loc">
      {mode.kind === "rename" ? (
        <form className="stack" onSubmit={(e) => void rename(e)} aria-label={`Rename ${location.name}`}>
          <Field label="Name" value={name} maxLength={100} disabled={busy} error={fieldError} onChange={setName} />
          <div className="row">
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {busy ? "Saving…" : "Save name"}
            </button>
            <button type="button" className="btn btn-quiet" disabled={busy} onClick={back}>
              Cancel
            </button>
          </div>
        </form>
      ) : (
        <div className="mg-loc-head">
          <div className="mg-loc-name">
            <b>{location.name}</b>
            <span className="small muted">{location.copies === 0 ? "No copies" : copies(location.copies)}</span>
          </div>
          {mode.kind === "view" && (
            <div className="row">
              <button
                type="button"
                className="btn btn-secondary"
                aria-label={`Rename ${location.name}`}
                onClick={() => {
                  setName(location.name);
                  setMode({ kind: "rename" });
                }}
              >
                Rename
              </button>
              <button type="button" className="btn btn-quiet" aria-label={`Delete ${location.name}`} disabled={busy} onClick={() => void tryDelete()}>
                Delete
              </button>
            </div>
          )}
        </div>
      )}
      {mode.kind === "move" && (
        <div className="alert alert-warn" role="alert">
          <Icon name="alert" />
          <div className="stack">
            <p className="t">{copies(mode.count)} {mode.count === 1 ? "is" : "are"} there — move {mode.count === 1 ? "it" : "them"} to…</p>
            <p className="small">A location can only be deleted once it is empty.</p>
            {others.length === 0 ? (
              <p className="small">There is no other location yet. Add one below, then come back.</p>
            ) : (
              <div className="field">
                <label htmlFor={`mv-${location.id}`}>Move the copies to</label>
                <select id={`mv-${location.id}`} className="input" value={target} disabled={busy} onChange={(e) => { setTarget(e.target.value); setFieldError(""); }}>
                  <option value="">Choose a location…</option>
                  {others.map((o) => (
                    <option key={o.id} value={o.id}>
                      {o.name}
                    </option>
                  ))}
                </select>
                {fieldError && (
                  <span className="field-error">
                    <Icon name="alert" />
                    {fieldError}
                  </span>
                )}
              </div>
            )}
            <div className="row">
              {others.length > 0 && (
                <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void moveAndDelete()}>
                  {busy ? "Moving…" : "Move them and delete this location"}
                </button>
              )}
              <button type="button" className="btn btn-quiet" disabled={busy} onClick={back}>
                Keep the location
              </button>
            </div>
          </div>
        </div>
      )}
      <InlineError error={error} />
    </li>
  );
}
