-- Sign-in sessions (internal/auth). Only the SHA-256 of a session's token is
-- kept, as hex, so a copy of the database holds nothing that can be used as a cookie.
CREATE TABLE sessions (
    token_hash   TEXT    NOT NULL PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id),
    created_at   TEXT    NOT NULL,
    last_seen_at TEXT    NOT NULL
) WITHOUT ROWID;

CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_last_seen ON sessions (last_seen_at);
