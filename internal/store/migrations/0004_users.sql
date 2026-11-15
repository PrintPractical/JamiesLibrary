-- Accounts and their permissions (internal/users). Passwords and sessions use
-- the password_hash and must_change_password columns from the next migration on.

CREATE TABLE users (
    id                   INTEGER PRIMARY KEY,
    username             TEXT    NOT NULL COLLATE NOCASE,
    display_name         TEXT    NOT NULL,
    password_hash        TEXT    NOT NULL,
    must_change_password INTEGER NOT NULL DEFAULT 0,
    -- the preset applied last; the permissions below are what the user has
    role                 TEXT    NOT NULL CHECK (role IN ('patron', 'librarian', 'administrator')),
    active               INTEGER NOT NULL DEFAULT 1,
    created_at           TEXT    NOT NULL
);

CREATE UNIQUE INDEX users_username ON users (username);

-- The user's effective permissions, one row each.
CREATE TABLE user_permissions (
    user_id    INTEGER NOT NULL REFERENCES users (id),
    permission TEXT    NOT NULL,
    PRIMARY KEY (user_id, permission)
) WITHOUT ROWID;
