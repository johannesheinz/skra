-- Staging area for an upload between its dry-run preview and the confirmed
-- commit, so the file need not be re-uploaded. Rows are short-lived and GC'd.
CREATE TABLE import_uploads (
    id         INTEGER PRIMARY KEY,
    token      TEXT NOT NULL UNIQUE,
    book_id    INTEGER NOT NULL REFERENCES address_books(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    format     TEXT NOT NULL,
    bytes      BLOB NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_import_uploads_token ON import_uploads(token);
