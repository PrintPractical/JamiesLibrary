-- Invitation codes (internal/invitations). The code is stored as given, in its
-- canonical form XXXX-XXXX-XXXX-XXXX, so people who manage invitations can see it.
CREATE TABLE invitations (
    id         INTEGER PRIMARY KEY,
    code       TEXT    NOT NULL UNIQUE,
    role       TEXT    NOT NULL CHECK (role IN ('patron', 'librarian', 'administrator')),
    creator_id INTEGER NOT NULL REFERENCES users (id),
    created_at TEXT    NOT NULL,
    used_by    INTEGER REFERENCES users (id),
    used_at    TEXT,
    revoked_at TEXT
);

CREATE INDEX invitations_newest ON invitations (id DESC);
