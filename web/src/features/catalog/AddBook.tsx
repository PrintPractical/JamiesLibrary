import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router";
import {
  addCopy,
  createBook,
  createLocation,
  getBook,
  getLocations,
  lookupIsbn,
  type MemberBook,
  type Location,
} from "../../api/catalog";
import { getMe } from "../../api/auth";
import { ApiError, errorMessage } from "../../api/client";
import type { DetectorFactory } from "../../components/barcodeDetector";
import { InlineError, PageError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { ScanField } from "../../components/ScanField";
import { useLoad } from "../patrons/useLoad";
import { Copies, type CopyDraft, type CopyErrors } from "./Copies";
import { Details, EMPTY_DETAILS, type DetailErrors, type Details as DetailsState } from "./Details";
import { Done, type DoneCopy } from "./Done";
import "./addbook.css";

type Note = "found" | "not_found" | "unavailable" | "manual";
// start: only the ISBN bar. details: the form for a new book. exists: the ISBN is already in the catalog.
type Stage = { kind: "start" } | { kind: "details"; note: Note } | { kind: "exists"; book: MemberBook };

let nextKey = 1;
const newCopy = (from?: CopyDraft): CopyDraft => ({
  key: nextKey++,
  source: "new",
  sticker: "",
  locationId: from?.locationId ?? "",
  newLocation: from?.newLocation ?? "",
  condition: from?.condition ?? "good",
});

const NOTES: Record<Note, { tone: "ok" | "warn"; title: string; text: string }> = {
  found: { tone: "ok", title: "Found on Open Library", text: "Check the details below. Nothing is saved until you press Add." },
  not_found: {
    tone: "warn",
    title: "Not found on Open Library",
    text: "Enter the details by hand. Nothing is saved until you press Add.",
  },
  unavailable: {
    tone: "warn",
    title: "Lookup unavailable",
    text: "Open Library could not be reached. Enter the details by hand, or look the ISBN up again later.",
  },
  manual: { tone: "ok", title: "Entering a book without an ISBN", text: "Nothing is saved until you press Add." },
};

/** What a book is called while it is being saved: authors typed with commas become a list. */
const authorList = (s: string) =>
  s
    .split(",")
    .map((a) => a.trim())
    .filter(Boolean);

const COVER_TYPES = ["image/jpeg", "image/png", "image/webp"];
const COVER_LIMIT = 5 * 1024 * 1024;

type Props = { detectorFactory?: DetectorFactory };

/** /catalog/new: starts from an ISBN, typed or scanned (SCAN-1), and loads what the library needs. */
export function AddBook({ detectorFactory }: Props) {
  const { state, reload } = useLoad(
    async (signal) => {
      const [me, locations] = await Promise.all([getMe(signal), getLocations(signal)]);
      return { mode: me.settings?.copy_id_mode ?? "isbn", locations };
    },
    "setup",
  );
  if (state.status === "error") return <PageError error={state.error} onRetry={reload} />;
  if (state.status === "loading") {
    return (
      <div className="stack addbook">
        <h1>Add a book</h1>
        <p className="muted">Loading…</p>
      </div>
    );
  }
  return <Flow mode={state.data.mode} initialLocations={state.data.locations} detectorFactory={detectorFactory} />;
}

type FlowProps = { mode: "isbn" | "own"; initialLocations: Location[]; detectorFactory?: DetectorFactory };

function Flow({ mode, initialLocations, detectorFactory }: FlowProps) {
  const [stage, setStage] = useState<Stage>({ kind: "start" });
  const [isbn, setIsbn] = useState("");
  const [looking, setLooking] = useState(false);
  const [lookupError, setLookupError] = useState<unknown>(null);
  const lookupCtl = useRef<AbortController | null>(null);
  // Bumped to start the ISBN field over empty: it keeps what it holds otherwise.
  const [fieldKey, setFieldKey] = useState(0);

  const [details, setDetails] = useState<DetailsState>(EMPTY_DETAILS);
  const [detailErrors, setDetailErrors] = useState<DetailErrors>({});
  const [locations, setLocations] = useState(initialLocations);
  const [copies, setCopies] = useState<CopyDraft[]>(() => [newCopy()]);
  const [copyErrors, setCopyErrors] = useState<Record<number, CopyErrors>>({});
  // Bumped when Add is refused: focus then moves to the first field in error.
  const [refused, setRefused] = useState(0);
  const root = useRef<HTMLDivElement>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  // The new book, once the server has it: a retry after a copy failed adds only what is left.
  const [savedBook, setSavedBook] = useState<{ id: number; title: string } | null>(null);
  const [done, setDone] = useState<{ bookId: number; title: string; added: boolean; copies: DoneCopy[] } | null>(null);

  useEffect(() => () => lookupCtl.current?.abort(), []);

  // The errors are drawn by now: go to the first one in page order, so the screen reader says it.
  useEffect(() => {
    if (refused === 0) return;
    root.current?.querySelector<HTMLElement>('[aria-invalid="true"], [data-error-target]')?.focus();
  }, [refused]);

  const existing = stage.kind === "exists" ? stage.book : null;
  const bookIsbn = existing ? existing.isbn : stage.kind === "details" ? isbn : "";
  // A book with no ISBN always carries its own barcodes (CAT-9).
  const ownBarcodes = mode === "own" || (stage.kind !== "start" && bookIsbn === "");
  const firstNumber = existing ? Math.max(0, ...existing.copies.map((c) => c.number)) + 1 : 1;
  const locked = savedBook !== null;

  function resetForm(next: Stage, fill: DetailsState = EMPTY_DETAILS) {
    if (details.coverPreview) URL.revokeObjectURL(details.coverPreview);
    setDetails(fill);
    setDetailErrors({});
    setCopyErrors({});
    setSaveError(null);
    setCopies([newCopy()]);
    setStage(next);
  }

  async function lookup(code: string) {
    if (locked) return;
    lookupCtl.current?.abort();
    const ctl = new AbortController();
    lookupCtl.current = ctl;
    setIsbn(code);
    setLookupError(null);
    setLooking(true);
    try {
      const found = await lookupIsbn(code, ctl.signal);
      if (ctl.signal.aborted) return;
      const clean = found.isbn ?? code;
      setIsbn(clean);
      if (found.status === "exists" && found.book_id) {
        resetForm({ kind: "exists", book: await getBook(String(found.book_id), ctl.signal) });
      } else if (found.status === "found") {
        resetForm(
          { kind: "details", note: "found" },
          {
            title: found.title ?? "",
            authors: (found.authors ?? []).join(", "),
            description: found.description ?? "",
            coverToken: found.cover_token ?? "",
            coverUrl: found.cover_url ?? "",
            coverFile: null,
            coverPreview: "",
          },
        );
      } else {
        resetForm({ kind: "details", note: "not_found" });
      }
    } catch (err) {
      if (ctl.signal.aborted) return;
      if (err instanceof ApiError && err.status === 503) resetForm({ kind: "details", note: "unavailable" });
      else setLookupError(err);
    } finally {
      if (lookupCtl.current === ctl) setLooking(false);
    }
  }

  function withoutIsbn() {
    lookupCtl.current?.abort();
    setLooking(false);
    setLookupError(null);
    setIsbn("");
    setFieldKey((k) => k + 1);
    resetForm({ kind: "details", note: "manual" });
  }

  function setCount(n: number) {
    setCopies((cur) => (n > cur.length ? [...cur, newCopy(cur[cur.length - 1])] : cur.slice(0, n)));
  }

  function changeCopy(key: number, change: Partial<CopyDraft>) {
    setCopies((cur) => cur.map((c) => (c.key === key ? { ...c, ...change } : c)));
    setCopyErrors((cur) => {
      const rest = { ...cur };
      delete rest[key];
      return rest;
    });
  }

  /** What is wrong before anything is sent: the title, and a location for each copy. */
  function validate(): boolean {
    const d: DetailErrors = {};
    if (!existing && !locked) {
      if (details.title.trim() === "") d.title = "Enter the book's title.";
      if (details.coverFile && !COVER_TYPES.includes(details.coverFile.type)) d.cover = "A cover must be a JPEG, PNG or WebP image.";
      else if (details.coverFile && details.coverFile.size > COVER_LIMIT) d.cover = "That cover is larger than 5 MB. Choose a smaller image.";
    }
    const c: Record<number, CopyErrors> = {};
    for (const cp of copies) {
      if (cp.saved) continue;
      if (cp.locationId === "") c[cp.key] = { location: "Choose where this copy lives." };
      else if (cp.locationId === "new" && cp.newLocation.trim() === "") c[cp.key] = { location: "Name the new location." };
      else if (ownBarcodes && cp.source === "sticker" && cp.sticker.trim() === "") {
        c[cp.key] = { sticker: "Enter the sticker's barcode, or choose a new label." };
      }
    }
    setDetailErrors(d);
    setCopyErrors(c);
    const ok = Object.keys(d).length === 0 && Object.keys(c).length === 0;
    if (!ok) setRefused((n) => n + 1);
    return ok;
  }

  async function save() {
    if (saving || !validate()) return;
    setSaving(true);
    setSaveError(null);
    try {
      let bookId = existing?.id ?? savedBook?.id;
      let title = existing?.title ?? savedBook?.title ?? "";
      if (bookId === undefined) {
        try {
          const book = await createBook({
            title: details.title.trim(),
            authors: authorList(details.authors),
            isbn,
            description: details.description,
            coverToken: details.coverFile ? undefined : details.coverToken,
            cover: details.coverFile ?? undefined,
          });
          bookId = book.id;
          title = book.title;
          setSavedBook({ id: book.id, title: book.title });
        } catch (err) {
          if (err instanceof ApiError && err.bookId) {
            // Someone added this ISBN meanwhile: show that book instead of making a second.
            resetForm({ kind: "exists", book: await getBook(String(err.bookId)) });
            return;
          }
          if (err instanceof ApiError && err.field && ["title", "authors", "description", "cover", "cover_token", "isbn"].includes(err.field)) {
            setDetailErrors({ [err.field === "cover_token" ? "cover" : err.field]: errorMessage(err) });
            setRefused((n) => n + 1);
          } else {
            setSaveError(err);
          }
          return;
        }
      }
      await saveCopies(bookId, title);
    } catch (err) {
      setSaveError(err);
    } finally {
      setSaving(false);
    }
  }

  async function saveCopies(bookId: number, title: string) {
    const made = new Map<string, number>(); // location names made in this save, lower-cased
    let work = copies;
    const update = (key: number, change: Partial<CopyDraft>) => {
      work = work.map((c) => (c.key === key ? { ...c, ...change } : c));
      setCopies(work);
    };
    for (const cp of work) {
      if (cp.saved) continue;
      let locationId = cp.locationId;
      try {
        if (locationId === "new") {
          const name = cp.newLocation.trim();
          const known = locations.find((l) => l.name.toLowerCase() === name.toLowerCase())?.id ?? made.get(name.toLowerCase());
          let id = known;
          if (id === undefined) {
            const loc = await createLocation(name);
            id = loc.id;
            setLocations((cur) => [...cur, loc]);
          }
          made.set(name.toLowerCase(), id);
          locationId = String(id);
          update(cp.key, { locationId });
        }
        const sticker = ownBarcodes && cp.source === "sticker" ? cp.sticker.trim() : "";
        const saved = await addCopy(bookId, {
          location_id: Number(locationId),
          condition: cp.condition,
          ...(sticker ? { sticker } : {}),
        });
        update(cp.key, { saved: { id: saved.id, number: saved.number, barcode: saved.barcode } });
      } catch (err) {
        const field = err instanceof ApiError ? err.field : undefined;
        const text = errorMessage(err);
        const error: CopyErrors =
          field === "sticker" ? { sticker: text } : field === "new_location" ? { location: text } : field === "condition" ? { condition: text } : { general: text };
        setCopyErrors({ [cp.key]: error });
        return;
      }
    }
    setDone({
      bookId,
      title,
      added: existing !== null,
      copies: work.flatMap((c) =>
        c.saved
          ? [
              {
                id: c.saved.id,
                number: c.saved.number,
                barcode: c.saved.barcode,
                condition: c.condition,
                location: locations.find((l) => String(l.id) === c.locationId)?.name ?? "",
              },
            ]
          : [],
      ),
    });
  }

  function another() {
    setIsbn("");
    setFieldKey((k) => k + 1);
    setSavedBook(null);
    setDone(null);
    setLookupError(null);
    resetForm({ kind: "start" });
  }

  const unsaved = copies.filter((c) => !c.saved).length;
  const label = useMemo(() => {
    const n = (k: number) => `${k} ${k === 1 ? "copy" : "copies"}`;
    if (existing) return `Add ${n(unsaved)}`;
    if (locked) return `Save ${n(unsaved)}`;
    return `Add book & ${n(unsaved)}`;
  }, [existing, locked, unsaved]);

  if (done) return <Done {...done} ownBarcodes={ownBarcodes} onAnother={another} />;

  const note = stage.kind === "details" && !savedBook ? NOTES[stage.note] : null;
  return (
    <div className="stack addbook" ref={root}>
      <Link className="ab-back" to="/catalog">
        <Icon name="left" />
        Catalog
      </Link>
      <h1>Add a book</h1>

      <section className="card stack" aria-label="Find the book">
        <div className="ab-isbn">
          {locked ? (
            <p>
              ISBN <span className="mono">{isbn || "none"}</span>
            </p>
          ) : (
            <>
              <ScanField
                key={fieldKey}
                label="ISBN"
                placeholder="Scan the barcode, or type the ISBN"
                submitLabel="Look up"
                hint="Point at the book's ISBN barcode"
                keep
                defaultValue={isbn}
                onScan={(code) => void lookup(code)}
                autoFocus
                detectorFactory={detectorFactory}
              />
              <button type="button" className="btn btn-quiet" onClick={withoutIsbn}>
                Enter without an ISBN
              </button>
            </>
          )}
        </div>
        {looking && <p className="muted" role="status">Looking up the book…</p>}
        <InlineError error={lookupError} />
        {note && !looking && (
          <div className={`alert alert-${note.tone}`} role="status">
            <Icon name={note.tone === "ok" ? "check" : "alert"} />
            <div>
              <p className="t">{note.title}</p>
              <p className="small">{note.text}</p>
            </div>
          </div>
        )}
        {savedBook && (
          <div className="alert alert-warn" role="status">
            <Icon name="alert" />
            <div>
              <p className="t">“{savedBook.title}” is saved, but not all its copies</p>
              <p className="small">
                Fix the copy marked below and press Save. The book's details are locked now: change them from{" "}
                <Link to={`/books/${savedBook.id}`}>the book's page</Link>.
              </p>
            </div>
          </div>
        )}
        {existing && (
          <div className="alert alert-warn" role="status">
            <Icon name="alert" />
            <div>
              <p className="t">This book is already in the catalog</p>
              <p className="small">
                <Link to={`/books/${existing.id}`}>{existing.title}</Link>
                {existing.authors.length > 0 && ` by ${existing.authors.join(", ")}`} has {existing.total}{" "}
                {existing.total === 1 ? "copy" : "copies"}. No second book is made: add a copy of it below.
              </p>
            </div>
          </div>
        )}
      </section>

      {stage.kind !== "start" && (
        <div className="ab-layout">
          {stage.kind === "details" && (
            <Details value={details} errors={detailErrors} locked={locked || saving} onChange={(d) => setDetails((cur) => ({ ...cur, ...d }))} />
          )}
          <div className="stack">
            <Copies
              copies={copies}
              errors={copyErrors}
              locations={locations}
              firstNumber={firstNumber}
              ownBarcodes={ownBarcodes}
              busy={saving}
              onChange={changeCopy}
              onCount={setCount}
              detectorFactory={detectorFactory}
            />
            <InlineError error={saveError} />
            <div className="ab-actions">
              <Link className="btn btn-quiet" to="/catalog">
                Cancel
              </Link>
              <button type="button" className="btn btn-primary" disabled={saving} onClick={() => void save()}>
                <Icon name="check" />
                {saving ? "Saving…" : label}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
