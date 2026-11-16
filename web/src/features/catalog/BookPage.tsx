import { Link, useParams } from "react-router";
import { getBook, getPublicBook, type BookPageData, type Copy, type MemberBook } from "../../api/catalog";
import { useSession } from "../../app/session";
import { PageError } from "../../components/Errors";
import { Icon } from "../../components/Icon";
import { useLoad } from "../patrons/useLoad";
import { Availability, AverageRating, Cover, shortDate } from "./parts";
import { Reviews } from "./Reviews";
import "./catalog.css";

function CopyRow({ copy }: { copy: Copy }) {
  return (
    <li>
      <span className="loc">
        <Icon name="pin" />
        <span>{copy.location}</span>
      </span>
      {copy.available ? (
        <span className="badge b-ok">On the shelf</span>
      ) : (
        <span className="badge b-plain">Out{copy.due_date ? ` · due ${shortDate(copy.due_date)}` : ""}</span>
      )}
    </li>
  );
}

function About({ book }: { book: BookPageData }) {
  return (
    <section className="desc">
      <h2>About this book</h2>
      {book.description ? (
        book.description.split(/\n{2,}/).map((p, i) => <p key={i}>{p}</p>)
      ) : (
        <p className="muted">There is no description of this book yet.</p>
      )}
      {book.isbn && (
        <dl className="facts">
          <dt>ISBN</dt>
          <dd className="mono">{book.isbn}</dd>
        </dl>
      )}
    </section>
  );
}

function MemberSide({ book, onChanged }: { book: MemberBook; onChanged: () => void }) {
  const { can } = useSession();
  return <Reviews book={book} canWrite={can("reviews.write")} canRemove={can("reviews.remove")} onChanged={onChanged} />;
}

/** A book's page: copies and where they are, the average rating, and reviews (CAT-4, CAT-6, CAT-8). A visitor gets counts and the average only (ACCESS-4). */
export function BookPage() {
  const { id = "" } = useParams();
  const { me } = useSession();
  const { state, reload } = useLoad<BookPageData>((signal) => (me ? getBook(id, signal) : getPublicBook(id, signal)), `${me ? "m" : "v"}|${id}`);
  const back = (
    <Link className="back" to="/browse">
      <Icon name="left" />
      Browse
    </Link>
  );

  if (state.status === "loading") {
    return (
      <div className="stack">
        {back}
        <p className="muted" role="status">
          Loading the book…
        </p>
      </div>
    );
  }
  if (state.status === "error") return <PageError error={state.error} onRetry={reload} />;

  const book = state.data;
  return (
    <div className="bookpage">
      {back}
      <div className="hero">
        <Cover book={book} />
        <div className="hero-main">
          <h1>{book.title}</h1>
          {book.authors.length > 0 && <p className="byline">{book.authors.join(", ")}</p>}
          <AverageRating average={book.ratingAverage} count={book.ratingCount} />
        </div>
        <div className="avail card">
          <Availability available={book.available} total={book.total} size="big" />
          {book.kind === "member" && book.copies.length > 0 && (
            <ul className="copies" aria-label="Copies">
              {book.copies.map((c) => (
                <CopyRow key={c.id} copy={c} />
              ))}
            </ul>
          )}
        </div>
      </div>
      <div className="cols">
        <About book={book} />
        {book.kind === "member" ? (
          <MemberSide book={book} onChanged={reload} />
        ) : (
          <section aria-labelledby="rv">
            <h2 id="rv">Ratings &amp; reviews</h2>
            <p className="card muted">
              <Link to="/signin">Sign in</Link> to read what others thought of this book.
            </p>
          </section>
        )}
      </div>
    </div>
  );
}
