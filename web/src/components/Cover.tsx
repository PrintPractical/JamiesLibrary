import "./Cover.css";

// The one cover of the product: the book's picture, or when it has none a generated cover of the
// mockups (a coloured spine, the title, the author). The colour is the book's own, so it stays the same.
const COLOURS = 8;

/** The palette class (c1 to c8, defined in Cover.css) of a book's generated cover. */
export function colourClass(id: number): string {
  return `c${(Math.abs(id) % COLOURS) + 1}`;
}

/** "Frances Hodgson Burnett" as "F. H. Burnett". */
export function shortName(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length < 2) return parts[0] ?? "";
  const initials = parts.slice(0, -1).map((p) => `${Array.from(p)[0]}.`);
  return `${initials.join(" ")} ${parts[parts.length - 1]}`;
}

/** mini: Browse rows; card: loan cards; hero: a book's page; none: the standard size. */
export type CoverSize = "mini" | "card" | "hero";

type CoverBook = { id: number; title: string; authors: string[]; coverUrl?: string };

export function Cover({ book, size, className = "" }: { book: CoverBook; size?: CoverSize; className?: string }) {
  const classes = ["cover", size && `cover-${size}`, className].filter(Boolean).join(" ");
  if (book.coverUrl) return <img className={`${classes} cover-img`} src={book.coverUrl} alt="" loading="lazy" />;
  // A card is small, so it names the first author by initials.
  const author = size === "card" ? (book.authors[0] ? shortName(book.authors[0]) : "") : book.authors.join(", ");
  return (
    <div className={`${classes} ${colourClass(book.id)}`} aria-hidden="true">
      <span className="ct">{book.title}</span>
      {author && <span className="ca">{author}</span>}
    </div>
  );
}
