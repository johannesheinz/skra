-- Consolidated initial schema. Integer `id` columns are internal and never
-- exposed; `public_id` columns are what appear in URLs and API responses.

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    public_id     TEXT NOT NULL UNIQUE,                 -- random >=128-bit base64url
    username      TEXT NOT NULL UNIQUE,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,                        -- argon2id
    role          TEXT NOT NULL CHECK (role IN ('admin','user')),
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE address_books (
    id          INTEGER PRIMARY KEY,
    public_id   TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    description TEXT,
    owner_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_address_books_owner ON address_books(owner_id);

CREATE TABLE contacts (
    id              INTEGER PRIMARY KEY,
    public_id       TEXT NOT NULL UNIQUE,
    address_book_id INTEGER NOT NULL REFERENCES address_books(id) ON DELETE CASCADE,
    full_name       TEXT NOT NULL,
    org             TEXT,
    primary_email   TEXT,
    primary_phone   TEXT,
    has_photo       INTEGER NOT NULL DEFAULT 0,   -- keeps list queries off the photo table
    vcard_raw       TEXT NOT NULL,                -- canonical fidelity + future CardDAV
    uid             TEXT NOT NULL,                -- stable resource id (CardDAV-ready)
    etag            TEXT NOT NULL,                -- bumps on every edit (CardDAV sync)
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (address_book_id, uid)
);
CREATE INDEX idx_contacts_book ON contacts(address_book_id);
CREATE INDEX idx_contacts_name ON contacts(address_book_id, full_name);

-- Photos isolated so the hot contacts table stays lean.
CREATE TABLE contact_photos (
    contact_id INTEGER PRIMARY KEY REFERENCES contacts(id) ON DELETE CASCADE,
    mime_type  TEXT NOT NULL,
    bytes      BLOB NOT NULL,
    etag       TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Per-book access grants.
CREATE TABLE address_book_members (
    address_book_id INTEGER NOT NULL REFERENCES address_books(id) ON DELETE CASCADE,
    user_id         INTEGER NOT NULL REFERENCES users(id)          ON DELETE CASCADE,
    access_level    TEXT NOT NULL CHECK (access_level IN ('viewer','manager')),
    granted_by      INTEGER REFERENCES users(id),
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (address_book_id, user_id)
);
CREATE INDEX idx_abm_user ON address_book_members(user_id);

-- Share links with three modes.
CREATE TABLE share_links (
    id           INTEGER PRIMARY KEY,
    token        TEXT NOT NULL UNIQUE,          -- path component
    mode         TEXT NOT NULL CHECK (mode IN ('authenticated','public_long','gated_short')),
    scope        TEXT NOT NULL CHECK (scope IN ('book','contact')),
    target_id    INTEGER NOT NULL,              -- internal id of book/contact
    secret_hash  TEXT,                          -- argon2id; REQUIRED when mode='gated_short'
    max_uses     INTEGER,
    use_count    INTEGER NOT NULL DEFAULT 0,
    failed_count INTEGER NOT NULL DEFAULT 0,    -- gate brute-force throttling
    expires_at   TEXT,
    revoked      INTEGER NOT NULL DEFAULT 0,
    created_by   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_share_links_token ON share_links(token);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,               -- random session id
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
