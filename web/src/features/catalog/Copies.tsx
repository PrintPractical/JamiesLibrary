import { useId } from "react";
import { CONDITIONS, type Location } from "../../api/catalog";
import { Icon } from "../../components/Icon";
import { ScanField } from "../../components/ScanField";
import type { DetectorFactory } from "../../components/barcodeDetector";

/** One copy being added: what the librarian has chosen so far. */
export type CopyDraft = {
  key: number;
  source: "new" | "sticker";
  sticker: string;
  /** A location's ID, "new" for one typed in below, "" for none chosen yet. */
  locationId: string;
  newLocation: string;
  condition: string;
  /** Set once the server has the copy: it is not sent again. */
  saved?: { id: number; number: number; barcode: string };
};

export type CopyErrors = { location?: string; sticker?: string; condition?: string; general?: string };

const MAX_COPIES = 20;

const capital = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

type Props = {
  copies: CopyDraft[];
  errors: Record<number, CopyErrors>;
  locations: Location[];
  /** The number of the first copy being added: one more than the book has. */
  firstNumber: number;
  /** Whether each copy may carry a sticker, or gets a generated barcode: own-barcode mode, or a book with no ISBN. */
  ownBarcodes: boolean;
  busy: boolean;
  onChange: (key: number, change: Partial<CopyDraft>) => void;
  onCount: (n: number) => void;
  detectorFactory?: DetectorFactory;
};

/** The copies card: how many, and for each its barcode, location and condition (CAT-3, CAT-9). */
export function Copies({ copies, errors, locations, firstNumber, ownBarcodes, busy, onChange, onCount, detectorFactory }: Props) {
  const minimum = Math.max(1, copies.filter((c) => c.saved).length);
  return (
    <section className="card" aria-labelledby="ab-copies">
      <div className="ab-head">
        <h2 id="ab-copies">Copies</h2>
        <div className="ab-stepper" role="group" aria-label="Number of copies">
          <button type="button" aria-label="One fewer" disabled={busy || copies.length <= minimum} onClick={() => onCount(copies.length - 1)}>
            −
          </button>
          <b aria-live="polite">{copies.length}</b>
          <button type="button" aria-label="One more" disabled={busy || copies.length >= MAX_COPIES} onClick={() => onCount(copies.length + 1)}>
            +
          </button>
        </div>
      </div>
      <p className="help" style={{ margin: "4px 0 14px" }}>
        {ownBarcodes
          ? "Each copy gets its own barcode label."
          : "Copies of this book share its ISBN barcode, so none needs a barcode of its own."}
      </p>
      {copies.map((c, i) => (
        <CopyCard
          key={c.key}
          copy={c}
          number={firstNumber + i}
          errors={errors[c.key] ?? {}}
          locations={locations}
          ownBarcodes={ownBarcodes}
          busy={busy}
          onChange={(change) => onChange(c.key, change)}
          detectorFactory={detectorFactory}
        />
      ))}
    </section>
  );
}

type CardProps = {
  copy: CopyDraft;
  number: number;
  errors: CopyErrors;
  locations: Location[];
  ownBarcodes: boolean;
  busy: boolean;
  onChange: (change: Partial<CopyDraft>) => void;
  detectorFactory?: DetectorFactory;
};

export function CopyCard({ copy, number, errors, locations, ownBarcodes, busy, onChange, detectorFactory }: CardProps) {
  const id = useId();
  const locked = busy || !!copy.saved;
  return (
    <div className="ab-copy" role="group" aria-label={`Copy ${number}`}>
      <div className="ab-copy-head">
        <b>Copy {number}</b>
        {ownBarcodes && !copy.saved && (
          <div className="ab-seg" role="radiogroup" aria-label={`Barcode for copy ${number}`}>
            {(["new", "sticker"] as const).map((source) => (
              <button
                key={source}
                type="button"
                role="radio"
                aria-checked={copy.source === source}
                disabled={locked}
                onClick={() => onChange({ source })}
              >
                {source === "new" ? "New label" : "Use a sticker"}
              </button>
            ))}
          </div>
        )}
        {copy.saved && <span className="badge b-ok"><Icon name="check" />Saved</span>}
      </div>

      {copy.saved ? (
        copy.saved.barcode && (
          <p className="small muted">
            Barcode <span className="ab-code">{copy.saved.barcode}</span>
          </p>
        )
      ) : ownBarcodes && copy.source === "new" ? (
        <p className="small muted">A barcode is generated when the copy is saved.</p>
      ) : ownBarcodes ? (
        <div>
          <ScanField
            label="Sticker barcode"
            placeholder="Scan or type the sticker's barcode"
            submitLabel={null}
            keep
            defaultValue={copy.sticker}
            onInput={(sticker) => onChange({ sticker })}
            onScan={(sticker) => onChange({ sticker })}
            hint="Point at the sticker"
            detectorFactory={detectorFactory}
          />
          {errors.sticker && <FieldError text={errors.sticker} />}
        </div>
      ) : null}

      <div className="ab-two">
        <div className="field">
          <label htmlFor={`${id}-loc`}>Location</label>
          <select
            id={`${id}-loc`}
            className={errors.location ? "input input-error" : "input"}
            value={copy.locationId}
            disabled={locked}
            onChange={(e) => onChange({ locationId: e.target.value })}
            aria-invalid={errors.location ? true : undefined}
            aria-describedby={errors.location ? `${id}-loc-error` : undefined}
          >
            <option value="">Choose a location…</option>
            {locations.map((l) => (
              <option key={l.id} value={String(l.id)}>
                {l.name}
              </option>
            ))}
            <option value="new">New location…</option>
          </select>
          {errors.location && <FieldError id={`${id}-loc-error`} text={errors.location} />}
        </div>
        <div className="field">
          <label htmlFor={`${id}-cond`}>Condition</label>
          <select
            id={`${id}-cond`}
            className="input"
            value={copy.condition}
            disabled={locked}
            onChange={(e) => onChange({ condition: e.target.value })}
          >
            {CONDITIONS.map((c) => (
              <option key={c} value={c}>
                {capital(c)}
              </option>
            ))}
          </select>
        </div>
      </div>
      {copy.locationId === "new" && (
        <div className="field" style={{ marginTop: 10 }}>
          <label htmlFor={`${id}-newloc`}>New location's name</label>
          <input
            id={`${id}-newloc`}
            className="input"
            value={copy.newLocation}
            disabled={locked}
            maxLength={100}
            onChange={(e) => onChange({ newLocation: e.target.value })}
          />
        </div>
      )}
      {errors.condition && <FieldError text={errors.condition} />}
      {errors.general && <FieldError text={errors.general} />}
    </div>
  );
}

function FieldError({ text, id }: { text: string; id?: string }) {
  return (
    <span className="field-error" id={id} role="alert" style={{ marginTop: 6 }}>
      <Icon name="alert" />
      {text}
    </span>
  );
}
