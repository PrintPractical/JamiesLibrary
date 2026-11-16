import { useId } from "react";
import { Icon } from "../../components/Icon";

const STAR = "m12 2.8 2.8 5.9 6.4.8-4.7 4.4 1.2 6.3L12 17.1l-5.7 3.1 1.2-6.3-4.7-4.4 6.4-.8z";

/** A book's average (or a review's rating) as stars: whole, half and empty. Says its value in words for screen readers. */
export function Stars({ value }: { value: number }) {
  const gradient = useId();
  return (
    <>
      <span className="stars" aria-hidden="true">
        {[1, 2, 3, 4, 5].map((i) => {
          const fill = value >= i - 0.25 ? "full" : value >= i - 0.75 ? "half" : "empty";
          return (
            <svg key={i} viewBox="0 0 24 24" aria-hidden="true">
              {fill === "half" && (
                <defs>
                  <linearGradient id={gradient}>
                    <stop offset="50%" stopColor="currentColor" />
                    <stop offset="50%" stopColor="transparent" />
                  </linearGradient>
                </defs>
              )}
              <path
                d={STAR}
                fill={fill === "full" ? "currentColor" : fill === "half" ? `url(#${gradient})` : "none"}
                stroke="currentColor"
                strokeWidth={fill === "full" ? 0 : 1.7}
                strokeLinejoin="round"
              />
            </svg>
          );
        })}
      </span>
      <span className="sr">{Number.isInteger(value) ? value : value.toFixed(1)} out of 5 stars</span>
    </>
  );
}

/** The average rating and how many ratings it rests on. */
export function AverageRating({ average, count }: { average: number; count: number }) {
  if (count === 0) return <p className="rating muted">No ratings yet</p>;
  return (
    <p className="rating">
      <Stars value={average} />
      <b>{average.toFixed(1)}</b>
      <span className="muted">· {count === 1 ? "1 rating" : `${count} ratings`}</span>
    </p>
  );
}

/** Whether a book can be borrowed, and how many copies are on the shelf. The words carry the meaning. */
export function Availability({ available, total, size = "" }: { available: number; total: number; size?: string }) {
  if (total === 0) return <span className={`badge b-plain ${size}`}>No copies yet</span>;
  if (available === 0) {
    return (
      <span className={`badge b-warn ${size}`}>
        <Icon name="alert" />
        Unavailable · {total === 1 ? "the copy is out" : `all ${total} copies are out`}
      </span>
    );
  }
  return (
    <span className={`badge b-ok ${size}`}>
      <Icon name="check" />
      {available} of {total} {total === 1 ? "copy" : "copies"} on the shelf
    </span>
  );
}

/** "24 Nov", or with the year when it isn't this one. */
export function shortDate(iso: string): string {
  const d = new Date(/^\d{4}-\d{2}-\d{2}$/.test(iso) ? `${iso}T12:00:00` : iso);
  if (Number.isNaN(d.getTime())) return "";
  const sameYear = d.getFullYear() === new Date().getFullYear();
  return d.toLocaleDateString(undefined, { day: "numeric", month: "short", ...(sameYear ? {} : { year: "numeric" }) });
}

export function initials(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => Array.from(w)[0] ?? "")
    .join("")
    .toUpperCase();
}
