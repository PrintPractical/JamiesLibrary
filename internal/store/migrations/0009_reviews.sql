-- Ratings and reviews (internal/reviews): one row per user per book. Either
-- the rating or the text may be NULL, not both. A review a librarian removed
-- keeps its row with removed_at/removed_by, so the patron cannot post it again.
CREATE TABLE reviews (
    id         INTEGER PRIMARY KEY,
    book_id    INTEGER NOT NULL REFERENCES books (id),
    user_id    INTEGER NOT NULL REFERENCES users (id),
    rating     INTEGER CHECK (rating BETWEEN 1 AND 5),
    -- at most 2000 code points: the service counts them, SQLite's length() on
    -- text counts code points too, so the limit is held here as well
    text       TEXT    CHECK (text IS NULL OR length(text) BETWEEN 1 AND 2000),
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL,
    removed_at TEXT,
    removed_by INTEGER REFERENCES users (id),
    CHECK (rating IS NOT NULL OR text IS NOT NULL),
    UNIQUE (book_id, user_id)
);

CREATE INDEX reviews_book ON reviews (book_id, updated_at DESC, id DESC) WHERE removed_at IS NULL;
