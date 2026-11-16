-- The administrative activity log (internal/activity). A row is written in the
-- same transaction as the change it records. actor_id is NULL for the server
-- console (the reset-password subcommand), which has no account.
CREATE TABLE activity (
    id       INTEGER PRIMARY KEY,
    at       TEXT    NOT NULL,
    actor_id INTEGER REFERENCES users (id),
    action   TEXT    NOT NULL,
    target   TEXT    NOT NULL,
    details  TEXT    NOT NULL DEFAULT '{}'
);

CREATE INDEX activity_newest ON activity (at DESC, id DESC);
