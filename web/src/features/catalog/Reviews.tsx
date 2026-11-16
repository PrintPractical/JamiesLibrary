import { useState } from "react";
import { deleteMyReview, getReviews, removeReview, saveReview, type MemberBook, type Review } from "../../api/catalog";
import { InlineError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { Stars, initials, shortDate } from "./parts";

/** The limit of CAT-6, counted in characters as the server counts them (code points). */
export const REVIEW_LIMIT = 2000;
const length = (s: string) => Array.from(s).length;

function StarPicker({ value, onChange }: { value: number; onChange: (n: number) => void }) {
  return (
    <div className="rate-pick" role="radiogroup" aria-label="Your rating, out of 5">
      {[1, 2, 3, 4, 5].map((n) => (
        <button
          key={n}
          type="button"
          role="radio"
          aria-checked={value === n}
          aria-label={n === 1 ? "1 star" : `${n} stars`}
          onClick={() => onChange(value === n ? 0 : n)}
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path
              fill={n <= value ? "currentColor" : "none"}
              stroke="currentColor"
              strokeWidth="1.7"
              strokeLinejoin="round"
              d="m12 2.8 2.8 5.9 6.4.8-4.7 4.4 1.2 6.3L12 17.1l-5.7 3.1 1.2-6.3-4.7-4.4 6.4-.8z"
            />
          </svg>
        </button>
      ))}
    </div>
  );
}

/** The caller's own rating and review: write, edit or delete (CAT-6). */
function MyReview({ book, onChanged }: { book: MemberBook; onChanged: () => void }) {
  const mine = book.myReview;
  const [rating, setRating] = useState(mine?.rating ?? 0);
  const [text, setText] = useState(mine?.text ?? "");
  const [busy, setBusy] = useState<"save" | "delete" | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const count = length(text);
  const tooLong = count > REVIEW_LIMIT;
  const empty = rating === 0 && text.trim() === "";
  const unchanged = mine !== null && rating === mine.rating && text === mine.text;

  async function run(kind: "save" | "delete", action: () => Promise<unknown>) {
    setBusy(kind);
    setError(null);
    try {
      await action();
      if (kind === "delete") {
        setRating(0);
        setText("");
        setConfirming(false);
      }
      onChanged();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(null);
    }
  }

  return (
    <form
      className="card stack"
      aria-label="Your rating and review"
      onSubmit={(e) => {
        e.preventDefault();
        if (!tooLong && !empty) void run("save", () => saveReview(book.id, rating || null, text.trim() === "" ? "" : text));
      }}
    >
      <h3>Your rating</h3>
      <StarPicker value={rating} onChange={setRating} />
      <div className="field">
        <label htmlFor="review-text">
          Your review <span className="optional">(optional)</span>
        </label>
        <textarea
          id="review-text"
          className={tooLong ? "input input-error" : "input"}
          value={text}
          placeholder="What did you think? Others in the library will see your display name."
          aria-invalid={tooLong || undefined}
          aria-describedby="review-count"
          onChange={(e) => setText(e.target.value)}
        />
        <span id="review-count" className={tooLong ? "help counter over" : "help counter"}>
          {tooLong && <Icon name="alert" />}
          {tooLong ? `Too long: a review can be at most ${REVIEW_LIMIT} characters, and this one is ${count}. ` : ""}
          {count} / {REVIEW_LIMIT}
        </span>
      </div>
      <InlineError error={error} />
      <div className="review-actions">
        <button type="submit" className="btn btn-primary" disabled={busy !== null || tooLong || empty || unchanged}>
          {busy === "save" ? "Saving…" : mine ? "Save changes" : "Post review"}
        </button>
        {mine && !confirming && (
          <button type="button" className="btn btn-quiet" disabled={busy !== null} onClick={() => setConfirming(true)}>
            Delete my review
          </button>
        )}
        {mine && confirming && (
          <>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={busy !== null}
              onClick={() => void run("delete", () => deleteMyReview(book.id))}
            >
              {busy === "delete" ? "Deleting…" : "Yes, delete it"}
            </button>
            <button type="button" className="btn btn-quiet" disabled={busy !== null} onClick={() => setConfirming(false)}>
              Keep it
            </button>
          </>
        )}
      </div>
    </form>
  );
}

/** One review. Its text and name are shown as plain text, each in its own direction, so markup or a bidi override in one can't reach the other (sec §8). */
function ReviewItem({ review, onRemove, removing }: { review: Review; onRemove?: () => void; removing: boolean }) {
  const [confirming, setConfirming] = useState(false);
  return (
    <article className="review">
      <div className="row">
        <span className="avatar" aria-hidden="true">
          {initials(review.display_name)}
        </span>
        <div className="review-who">
          <b>
            <bdi>{review.display_name}</bdi>
          </b>
          {review.rating > 0 && (
            <div>
              <Stars value={review.rating} />
            </div>
          )}
        </div>
        <span className="tiny muted when">{shortDate(review.updated_at)}</span>
      </div>
      {review.text !== "" && (
        <p className="review-text" dir="auto">
          {review.text}
        </p>
      )}
      {onRemove && !confirming && (
        <button type="button" className="btn btn-quiet remove" disabled={removing} aria-label={`Remove review by ${review.display_name}`} onClick={() => setConfirming(true)}>
          Remove
        </button>
      )}
      {onRemove && confirming && (
        <div className="review-confirm">
          <p>
            Remove <bdi>{review.display_name}</bdi>'s review? They can't get it back.
          </p>
          <div className="review-actions">
            <button type="button" className="btn btn-secondary" disabled={removing} onClick={onRemove}>
              {removing ? "Removing…" : "Yes, remove it"}
            </button>
            <button type="button" className="btn btn-quiet" disabled={removing} onClick={() => setConfirming(false)}>
              Keep it
            </button>
          </div>
        </div>
      )}
    </article>
  );
}

/** Ratings and reviews for a signed-in user: the average, their own, and everyone's (CAT-6). */
export function Reviews({ book, canWrite, canRemove, onChanged }: { book: MemberBook; canWrite: boolean; canRemove: boolean; onChanged: () => void }) {
  const mineId = book.myReview?.id;
  // Pages fetched after the first. They belong to the page of reviews they follow: a reload starts again.
  const [fetched, setFetched] = useState<{ base: Review[]; list: Review[]; next: number }>({ base: book.reviews, list: [], next: 2 });
  const more = fetched.base === book.reviews ? fetched : { base: book.reviews, list: [], next: 2 };
  const [loadingMore, setLoadingMore] = useState(false);
  const [removing, setRemoving] = useState<number | null>(null);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState<unknown>(null);

  const shown = [...book.reviews, ...more.list];
  // The caller's own review is in the form above, not repeated in the list.
  const others = shown.filter((r) => r.id !== mineId);
  const hasMore = shown.length < book.reviewsTotal;

  async function loadMore() {
    setLoadingMore(true);
    setError(null);
    try {
      const r = await getReviews(book.id, more.next);
      setFetched({ base: book.reviews, list: [...more.list, ...(r.reviews ?? [])], next: more.next + 1 });
    } catch (err) {
      setError(err);
    } finally {
      setLoadingMore(false);
    }
  }

  async function remove(id: number) {
    setRemoving(id);
    setError(null);
    setNotice("");
    try {
      await removeReview(id);
      setNotice("Review removed.");
      setRemoving(null);
      onChanged();
    } catch (err) {
      setError(err);
      setRemoving(null);
    }
  }

  return (
    <section aria-labelledby="rv" className="stack reviews">
      <h2 id="rv">Ratings &amp; reviews</h2>
      {canWrite && <MyReview book={book} onChanged={onChanged} />}
      <InlineError error={error} />
      {notice && (
        <p className="alert alert-ok" role="status">
          <Icon name="check" />
          {notice}
        </p>
      )}
      {others.length === 0 ? (
        <p className="muted">{book.myReview?.text ? "No one else has reviewed this book yet." : "No reviews yet."}</p>
      ) : (
        <div className="review-list">
          {others.map((r) => (
            <ReviewItem key={r.id} review={r} removing={removing === r.id} onRemove={canRemove ? () => void remove(r.id) : undefined} />
          ))}
        </div>
      )}
      {hasMore && (
        <button type="button" className="btn btn-secondary" disabled={loadingMore} onClick={() => void loadMore()}>
          {loadingMore ? "Loading…" : "Show more reviews"}
        </button>
      )}
    </section>
  );
}
