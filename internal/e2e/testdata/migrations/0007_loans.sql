-- Loans (internal/circ): one row per checkout, kept after the return as the
-- patron's history. An open loan has no returned_at.

CREATE TABLE loans (
    id             INTEGER PRIMARY KEY,
    copy_id        INTEGER NOT NULL REFERENCES copies (id),
    patron_id      INTEGER NOT NULL REFERENCES users (id),
    checked_out_at TEXT    NOT NULL,
    checked_out_by INTEGER NOT NULL REFERENCES users (id),
    -- a calendar date, YYYY-MM-DD, in the library's time zone
    due_date       TEXT    NOT NULL,
    renewals       INTEGER NOT NULL DEFAULT 0,
    returned_at    TEXT,
    returned_by    INTEGER REFERENCES users (id)
);

-- One open loan per copy: the last guard against two checkouts of the same copy.
CREATE UNIQUE INDEX loans_open_copy ON loans (copy_id) WHERE returned_at IS NULL;
CREATE INDEX loans_open_patron ON loans (patron_id) WHERE returned_at IS NULL;
CREATE INDEX loans_patron ON loans (patron_id, checked_out_at);
