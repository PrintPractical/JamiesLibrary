import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import {
  addCopy,
  getBook,
  getLocations,
  removeBook,
  removeCopy,
  updateBook,
  updateCopy,
  type Copy,
  type CopyInput,
  type Location,
  type MemberBook,
} from "../../api/catalog";
import { getMe } from "../../api/auth";
import { ApiError, errorMessage } from "../../api/client";
import { Field } from "../../components/Field";
import { InlineError, PageError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { useLoad } from "../patrons/useLoad";
import { CopyCard, type CopyDraft, type CopyErrors } from "./Copies";
import { Details, type DetailErrors, type Details as DetailsState } from "./Details";
import "./addbook.css";
import "./manage.css";

const authorList = (s: string) =>
  s
    .split(",")
    .map((a) => a.trim())
    .filter(Boolean);

const COVER_TYPES = ["image/jpeg", "image/png", "image/webp"];
const COVER_LIMIT = 5 * 1024 * 1024;

/** A copy as the form holds it: a location is chosen by ID, or made in the same step. */
function inputOf(d: CopyDraft, ownBarcodes: boolean): CopyInput {
  const sticker = ownBarcodes && d.source === "sticker" ? d.sticker.trim() : "";
  return {
    ...(d.locationId === "new" ? { location_id: 0, new_location: d.newLocation.trim() } : { location_id: Number(d.locationId) }),
    condition: d.condition,
    ...(sticker ? { sticker } : {}),
  };
}

function copyErrorsOf(err: unknown): CopyErrors {
  const text = errorMessage(err);
  const field = err instanceof ApiError ? err.field : undefined;
  if (field === "sticker") return { sticker: text };
  if (field === "new_location" || field === "location_id") return { location: text };
  if (field === "condition") return { condition: text };
  return { general: text };
}

function invalidDraft(d: CopyDraft, ownBarcodes: boolean): CopyErrors | null {
  if (d.locationId === "") return { location: "Choose where this copy lives." };
  if (d.locationId === "new" && d.newLocation.trim() === "") return { location: "Name the new location." };
  if (ownBarcodes && d.source === "sticker" && d.sticker.trim() === "") return { sticker: "Enter the sticker's barcode, or choose a new label." };
  return null;
}

let nextKey = 1;
const draftOf = (over: Partial<CopyDraft> = {}): CopyDraft => ({ key: nextKey++, source: "new", sticker: "", locationId: "", newLocation: "", condition: "good", ...over });

/** /catalog/books/:id: change a book, its copies and where they live, or remove them (CAT-3, CAT-5, CAT-8). */
export function ManageBook() {
  const { id = "" } = useParams();
  const { state, reload } = useLoad(
    async (signal) => {
      const [book, locations, me] = await Promise.all([getBook(id, signal), getLocations(signal), getMe(signal)]);
      return { book, locations, mode: me.settings?.copy_id_mode ?? "isbn" };
    },
    id,
  );
  if (state.status === "error") return <PageError error={state.error} onRetry={reload} />;
  if (state.status === "loading") {
    return (
      <div className="stack addbook">
        <h1>Change a book</h1>
        <p className="muted" role="status">
          Loading the book…
        </p>
      </div>
    );
  }
  const { book, locations, mode } = state.data;
  return <Manage key={book.id} book={book} locations={locations} ownBarcodes={mode === "own" || book.isbn === ""} onChanged={reload} />;
}

type Props = { book: MemberBook; locations: Location[]; ownBarcodes: boolean; onChanged: () => void };

function Manage({ book, locations, ownBarcodes, onChanged }: Props) {
  return (
    <div className="stack addbook">
      <Link className="ab-back" to="/catalog">
        <Icon name="left" />
        Catalog
      </Link>
      <h1>{book.title}</h1>
      <div className="ab-layout">
        <div className="stack">
          <EditDetails book={book} onSaved={onChanged} />
        </div>
        <div className="stack">
          <CopyList book={book} locations={locations} ownBarcodes={ownBarcodes} onChanged={onChanged} />
          <RemoveBook book={book} />
        </div>
      </div>
    </div>
  );
}

function EditDetails({ book, onSaved }: { book: MemberBook; onSaved: () => void }) {
  const [details, setDetails] = useState<DetailsState>({
    title: book.title,
    authors: book.authors.join(", "),
    description: book.description,
    coverToken: "",
    coverUrl: book.coverUrl,
    coverFile: null,
    coverPreview: "",
  });
  const [isbn, setIsbn] = useState(book.isbn);
  const [errors, setErrors] = useState<DetailErrors>({});
  const [other, setOther] = useState<number | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  function change(c: Partial<DetailsState>) {
    setDetails((d) => ({ ...d, ...c }));
    setSaved(false);
  }

  async function save() {
    const e: DetailErrors = {};
    if (details.title.trim() === "") e.title = "Enter the book's title.";
    if (details.coverFile && !COVER_TYPES.includes(details.coverFile.type)) e.cover = "A cover must be a JPEG, PNG or WebP image.";
    else if (details.coverFile && details.coverFile.size > COVER_LIMIT) e.cover = "That cover is larger than 5 MB. Choose a smaller image.";
    setErrors(e);
    setOther(null);
    setError(null);
    if (Object.keys(e).length > 0) return;
    setSaving(true);
    try {
      await updateBook(book.id, {
        title: details.title.trim(),
        authors: authorList(details.authors),
        isbn: isbn.trim(),
        description: details.description,
        cover: details.coverFile ?? undefined,
      });
      setSaved(true);
      onSaved();
    } catch (err) {
      if (err instanceof ApiError && err.bookId) {
        setOther(err.bookId);
        setErrors({ isbn: errorMessage(err) });
      } else if (err instanceof ApiError && err.field && ["title", "authors", "description", "cover", "isbn"].includes(err.field)) {
        setErrors({ [err.field]: errorMessage(err) });
      } else {
        setError(err);
      }
    } finally {
      setSaving(false);
    }
  }

  return (
    <>
      <Details value={details} errors={errors} locked={saving} onChange={change} />
      <section className="card ab-form" aria-label="ISBN">
        <Field label="ISBN" value={isbn} disabled={saving} error={errors.isbn} help="Leave it empty for a book without one." onChange={(v) => { setIsbn(v); setSaved(false); }} />
        {other && (
          <p className="small">
            <Link to={`/catalog/books/${other}`}>Open the book that has this ISBN</Link>
          </p>
        )}
        <InlineError error={error} />
        {saved && (
          <div className="alert alert-ok" role="status">
            <Icon name="check" />
            <div>
              <p className="t">Saved.</p>
            </div>
          </div>
        )}
        <div className="ab-actions">
          <button type="button" className="btn btn-primary" disabled={saving} onClick={() => void save()}>
            {saving ? "Saving…" : "Save changes"}
          </button>
        </div>
      </section>
    </>
  );
}

function RemoveBook({ book }: { book: MemberBook }) {
  const navigate = useNavigate();
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  async function remove() {
    setBusy(true);
    setError(null);
    try {
      await removeBook(book.id);
      navigate("/catalog");
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  }

  return (
    <section className="card stack" aria-labelledby="mg-remove-book">
      <h2 id="mg-remove-book">Remove this book</h2>
      <p className="help">
        The book and its copies leave the catalog and can't be scanned. Past loans stay in patrons' histories and in reports. A book with a copy on
        loan can't be removed until it is back.
      </p>
      {asking ? (
        <div className="row">
          <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void remove()}>
            {busy ? "Removing…" : "Yes, remove the book"}
          </button>
          <button type="button" className="btn btn-quiet" disabled={busy} onClick={() => setAsking(false)}>
            Keep it
          </button>
        </div>
      ) : (
        <div className="row">
          <button type="button" className="btn btn-secondary" onClick={() => setAsking(true)}>
            Remove book…
          </button>
        </div>
      )}
      <InlineError error={error} />
    </section>
  );
}

type ListProps = { book: MemberBook; locations: Location[]; ownBarcodes: boolean; onChanged: () => void };

function CopyList({ book, locations, ownBarcodes, onChanged }: ListProps) {
  const [adding, setAdding] = useState(false);
  const nextNumber = Math.max(0, ...book.copies.map((c) => c.number)) + 1;
  return (
    <section className="card" aria-labelledby="mg-copies">
      <div className="ab-head">
        <h2 id="mg-copies">Copies</h2>
        <div className="row">
          {book.copies.length > 0 && (
            <Link className="btn btn-secondary" to={`/catalog/labels?book=${book.id}`}>
              Print labels
            </Link>
          )}
          {!adding && (
            <button type="button" className="btn btn-secondary" onClick={() => setAdding(true)}>
              Add a copy
            </button>
          )}
        </div>
      </div>
      {book.copies.length === 0 && !adding && <p className="help">This book has no copies yet.</p>}
      <ul className="mg-copies">
        {book.copies.map((c) => (
          <CopyItem key={c.id} copy={c} locations={locations} onChanged={onChanged} />
        ))}
      </ul>
      {adding && <AddCopy bookId={book.id} number={nextNumber} locations={locations} ownBarcodes={ownBarcodes} onDone={() => { setAdding(false); onChanged(); }} onCancel={() => setAdding(false)} />}
    </section>
  );
}

function AddCopy({
  bookId,
  number,
  locations,
  ownBarcodes,
  onDone,
  onCancel,
}: {
  bookId: number;
  number: number;
  locations: Location[];
  ownBarcodes: boolean;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [draft, setDraft] = useState(() => draftOf({ locationId: locations.length === 1 ? String(locations[0]?.id) : "" }));
  const [errors, setErrors] = useState<CopyErrors>({});
  const [busy, setBusy] = useState(false);

  async function save() {
    const bad = invalidDraft(draft, ownBarcodes);
    setErrors(bad ?? {});
    if (bad) return;
    setBusy(true);
    try {
      await addCopy(bookId, inputOf(draft, ownBarcodes));
      onDone();
    } catch (err) {
      setErrors(copyErrorsOf(err));
      setBusy(false);
    }
  }

  return (
    <div className="mg-form">
      <CopyCard
        copy={draft}
        number={number}
        errors={errors}
        locations={locations}
        ownBarcodes={ownBarcodes}
        busy={busy}
        onChange={(c) => {
          setDraft((d) => ({ ...d, ...c }));
          setErrors({});
        }}
      />
      <div className="ab-actions">
        <button type="button" className="btn btn-quiet" disabled={busy} onClick={onCancel}>
          Cancel
        </button>
        <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void save()}>
          {busy ? "Adding…" : "Add the copy"}
        </button>
      </div>
    </div>
  );
}

const capital = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

function CopyItem({ copy, locations, onChanged }: { copy: Copy; locations: Location[]; onChanged: () => void }) {
  const [mode, setMode] = useState<"view" | "edit" | "remove">("view");
  const [draft, setDraft] = useState<CopyDraft>(() =>
    draftOf({ locationId: String(locations.find((l) => l.name === copy.location)?.id ?? ""), condition: copy.condition }),
  );
  const [errors, setErrors] = useState<CopyErrors>({});
  const [busy, setBusy] = useState(false);
  const [removeError, setRemoveError] = useState<unknown>(null);

  async function save() {
    const bad = invalidDraft(draft, false);
    setErrors(bad ?? {});
    if (bad) return;
    setBusy(true);
    try {
      await updateCopy(copy.id, inputOf(draft, false));
      setMode("view");
      onChanged();
    } catch (err) {
      setErrors(copyErrorsOf(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    setBusy(true);
    setRemoveError(null);
    try {
      await removeCopy(copy.id);
      onChanged();
    } catch (err) {
      setRemoveError(err);
      setBusy(false);
    }
  }

  return (
    <li className="mg-copy">
      <div className="mg-copy-main">
        <b>Copy {copy.number}</b>
        <span className="small muted">
          {copy.location} · {capital(copy.condition)}
        </span>
        {copy.available ? <span className="badge b-ok">On the shelf</span> : <span className="badge b-warn"><Icon name="alert" />On loan</span>}
      </div>
      {mode === "view" && (
        <div className="row">
          <button type="button" className="btn btn-secondary" aria-label={`Edit copy ${copy.number}`} onClick={() => setMode("edit")}>
            Edit
          </button>
          <button type="button" className="btn btn-quiet" aria-label={`Remove copy ${copy.number}`} onClick={() => setMode("remove")}>
            Remove
          </button>
        </div>
      )}
      {mode === "edit" && (
        <div className="mg-form">
          <CopyCard
            copy={draft}
            number={copy.number}
            errors={errors}
            locations={locations}
            ownBarcodes={false}
            busy={busy}
            onChange={(c) => {
              setDraft((d) => ({ ...d, ...c }));
              setErrors({});
            }}
          />
          <div className="ab-actions">
            <button type="button" className="btn btn-quiet" disabled={busy} onClick={() => setMode("view")}>
              Cancel
            </button>
            <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void save()}>
              {busy ? "Saving…" : "Save the copy"}
            </button>
          </div>
        </div>
      )}
      {mode === "remove" && (
        <div className="stack">
          <p className="small">
            Remove copy {copy.number}? It leaves the catalog and its label stops working. Its past loans stay in histories and reports.
          </p>
          <div className="row">
            <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void remove()}>
              {busy ? "Removing…" : `Yes, remove copy ${copy.number}`}
            </button>
            <button
              type="button"
              className="btn btn-quiet"
              disabled={busy}
              onClick={() => {
                setMode("view");
                setRemoveError(null);
              }}
            >
              Keep it
            </button>
          </div>
          <InlineError error={removeError} />
        </div>
      )}
    </li>
  );
}
