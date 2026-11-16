-- Copies of books, and every scannable code the library has issued or accepted
-- (internal/catalog).

CREATE TABLE copies (
    id          INTEGER PRIMARY KEY,
    book_id     INTEGER NOT NULL REFERENCES books (id),
    -- 1, 2, 3... within the book; never reused, so a removed copy keeps its number
    number      INTEGER NOT NULL,
    -- one of catalog.Conditions: checked by the service, so the list can grow
    -- without a migration
    condition   TEXT NOT NULL,
    -- Deliberately not a foreign key: a removed copy keeps its loan history and
    -- must not stop its location being deleted once no live copy is there.
    -- The service checks the location exists whenever it sets one.
    location_id INTEGER NOT NULL,
    created_at  TEXT NOT NULL,
    removed_at  TEXT,
    UNIQUE (book_id, number)
);

CREATE INDEX copies_location ON copies (location_id) WHERE removed_at IS NULL;

-- One table for the codes of copies and of patron cards, so the two can never
-- overlap. A copy has at most one code. user_id has no foreign key: the users
-- table arrives with a later migration.
CREATE TABLE identifiers (
    id      INTEGER PRIMARY KEY,
    code    TEXT NOT NULL UNIQUE,
    kind    TEXT NOT NULL CHECK (kind IN ('card', 'copy')),
    user_id INTEGER,
    copy_id INTEGER REFERENCES copies (id),
    CHECK ((kind = 'card' AND user_id IS NOT NULL AND copy_id IS NULL)
        OR (kind = 'copy' AND copy_id IS NOT NULL AND user_id IS NULL))
);

CREATE UNIQUE INDEX identifiers_copy ON identifiers (copy_id) WHERE copy_id IS NOT NULL;
CREATE UNIQUE INDEX identifiers_user ON identifiers (user_id) WHERE user_id IS NOT NULL;
