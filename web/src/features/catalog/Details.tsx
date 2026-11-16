import { useRef } from "react";
import { Field } from "../../components/Field";
import { Icon } from "../../components/Icon";

export type Details = {
  title: string;
  authors: string;
  description: string;
  /** A cover the lookup fetched and the server holds, until the book is saved. */
  coverToken: string;
  coverUrl: string;
  /** A cover the librarian chose instead. */
  coverFile: File | null;
  /** A local address to show the chosen file by. */
  coverPreview: string;
};

export const EMPTY_DETAILS: Details = { title: "", authors: "", description: "", coverToken: "", coverUrl: "", coverFile: null, coverPreview: "" };

export type DetailErrors = { title?: string; authors?: string; description?: string; cover?: string; isbn?: string };

type Props = {
  value: Details;
  errors: DetailErrors;
  locked: boolean;
  onChange: (change: Partial<Details>) => void;
};

/** The book's own fields, prefilled by the lookup for the librarian to check (SCAN-2, CAT-1). */
export function Details({ value, errors, locked, onChange }: Props) {
  const file = useRef<HTMLInputElement>(null);
  const shown = value.coverPreview || value.coverUrl;
  return (
    <section className="card ab-form" aria-labelledby="ab-details">
      <h2 id="ab-details">Book details</h2>
      <div className="ab-coverbox">
        <div className="ab-cover" aria-hidden="true">
          {shown ? (
            <img src={shown} alt="" />
          ) : (
            <>
              <span className="ab-cover-title">{value.title}</span>
              <span className="ab-cover-author">{value.authors}</span>
            </>
          )}
        </div>
        <div className="stack">
          <input
            ref={file}
            type="file"
            className="ab-file"
            accept="image/jpeg,image/png,image/webp"
            aria-label="Cover image file"
            tabIndex={-1}
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) {
                if (value.coverPreview) URL.revokeObjectURL(value.coverPreview);
                onChange({ coverFile: f, coverPreview: URL.createObjectURL(f) });
              }
              e.target.value = "";
            }}
          />
          <button type="button" className="btn btn-secondary" disabled={locked} onClick={() => file.current?.click()}>
            <Icon name="upload" />
            {shown ? "Replace cover" : "Add a cover"}
          </button>
          <p className="help">JPEG, PNG or WebP, up to 5 MB. Saved on your server.</p>
        </div>
      </div>
      {errors.cover && (
        <span className="field-error" role="alert" tabIndex={-1} data-error-target>
          <Icon name="alert" />
          {errors.cover}
        </span>
      )}
      <Field label="Title (required)" value={value.title} disabled={locked} error={errors.title} maxLength={500} onChange={(title) => onChange({ title })} />
      <Field
        label="Authors (required)"
        value={value.authors}
        disabled={locked}
        error={errors.authors}
        help="Separate several authors with a comma."
        onChange={(authors) => onChange({ authors })}
      />
      <div className="field">
        <label htmlFor="ab-description">Description</label>
        <textarea
          id="ab-description"
          className="input"
          value={value.description}
          disabled={locked}
          maxLength={10000}
          onChange={(e) => onChange({ description: e.target.value })}
        />
        {errors.description && (
          <span className="field-error" role="alert">
            <Icon name="alert" />
            {errors.description}
          </span>
        )}
      </div>
    </section>
  );
}
