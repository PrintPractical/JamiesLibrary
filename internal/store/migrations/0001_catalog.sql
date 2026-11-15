-- Books, their authors and the named locations (internal/catalog).

CREATE TABLE books (
    id          INTEGER PRIMARY KEY,
    item_type   TEXT NOT NULL DEFAULT 'book' CHECK (item_type = 'book'),
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    isbn13      TEXT,
    cover       TEXT,
    -- lower-cased title and author names, one per line: what searches match
    search_text TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    removed_at  TEXT
);

-- A removed book gives its ISBN back, so the book can be added again.
CREATE UNIQUE INDEX books_isbn13 ON books (isbn13) WHERE isbn13 IS NOT NULL AND removed_at IS NULL;

CREATE TABLE book_authors (
    book_id  INTEGER NOT NULL REFERENCES books (id),
    position INTEGER NOT NULL,
    name     TEXT NOT NULL,
    PRIMARY KEY (book_id, position)
);

CREATE TABLE locations (
    id       INTEGER PRIMARY KEY,
    name     TEXT NOT NULL,
    -- the name lower-cased: names are unique ignoring case
    name_key TEXT NOT NULL UNIQUE
);

INSERT INTO locations (name, name_key) VALUES ('Main shelf', 'main shelf');
