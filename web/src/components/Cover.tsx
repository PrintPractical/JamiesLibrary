import "./Cover.css";

// The generated cover of the mockups: a coloured spine, the title, the author. Books have no
// picture on the loan lists, and the colour is the book's own, so it stays the same.
const COLOURS = 8;

function colourOf(id: number): number {
  return (Math.abs(id) % COLOURS) + 1;
}

/** "Frances Hodgson Burnett" as "F. H. Burnett". */
export function shortName(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length < 2) return parts[0] ?? "";
  const initials = parts.slice(0, -1).map((p) => `${Array.from(p)[0]}.`);
  return `${initials.join(" ")} ${parts[parts.length - 1]}`;
}

export function Cover({ id, title, author, width = 60 }: { id: number; title: string; author?: string; width?: number }) {
  return (
    <div className={`cover c${colourOf(id)}`} style={{ width }} aria-hidden="true">
      <span className="ct">{title}</span>
      {author && <span className="ca">{shortName(author)}</span>}
    </div>
  );
}
