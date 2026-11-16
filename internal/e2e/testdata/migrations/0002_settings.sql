-- The library's settings: always exactly one row (id 1).
CREATE TABLE settings (
    id                   INTEGER PRIMARY KEY CHECK (id = 1),
    library_name         TEXT    NOT NULL,
    logo_file            TEXT    NOT NULL,
    scheme               TEXT    NOT NULL,
    accent               TEXT    NOT NULL,
    time_zone            TEXT    NOT NULL,
    loan_period_days     INTEGER NOT NULL,
    max_loans            INTEGER NOT NULL,
    max_renewals         INTEGER NOT NULL,
    self_checkout        INTEGER NOT NULL,
    public_catalog       INTEGER NOT NULL,
    copy_id_mode         TEXT    NOT NULL CHECK (copy_id_mode IN ('isbn', 'own')),
    session_idle_minutes INTEGER NOT NULL
);

-- An empty logo_file means no logo, an empty accent means the scheme's own colour.
INSERT INTO settings (id, library_name, logo_file, scheme, accent, time_zone,
                      loan_period_days, max_loans, max_renewals, self_checkout,
                      public_catalog, copy_id_mode, session_idle_minutes)
VALUES (1, 'Jamie''s Library', '', 'hearth', '', 'UTC', 14, 5, 2, 0, 0, 'isbn', 480);
