CREATE TABLE IF NOT EXISTS posts (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    reply_to  INTEGER REFERENCES posts(id) ON DELETE CASCADE,
    posted_at INTEGER NOT NULL,
    author    TEXT    NOT NULL DEFAULT '名無しさん',
    email     TEXT    NOT NULL DEFAULT '',
    subject   TEXT    NOT NULL DEFAULT '',
    body      TEXT    NOT NULL DEFAULT '',
    file_path      TEXT NOT NULL DEFAULT '',
    thumbnail_path TEXT NOT NULL DEFAULT '',
    thumbnail_width     INTEGER NOT NULL DEFAULT 0,
    thumbnail_height    INTEGER NOT NULL DEFAULT 0,
    file_size INTEGER NOT NULL DEFAULT 0,
    mime_type TEXT    NOT NULL DEFAULT '',
    bumped_at INTEGER NOT NULL DEFAULT 0,
    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_reply_to ON posts(reply_to);
CREATE INDEX IF NOT EXISTS idx_reply_to ON posts(reply_to, bumped_at);

CREATE TABLE IF NOT EXISTS admins (
    id INTEGER PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_sessions (
    token  TEXT PRIMARY KEY,
    admin_id INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL
);