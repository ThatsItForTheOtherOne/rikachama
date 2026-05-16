CREATE TABLE IF NOT EXISTS posts (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    reply_to  INTEGER NOT NULL DEFAULT 0,
    posted_at INTEGER NOT NULL,
    author    TEXT    NOT NULL DEFAULT '名無しさん',
    email     TEXT    NOT NULL DEFAULT '',
    subject   TEXT    NOT NULL DEFAULT '',
    body      TEXT    NOT NULL DEFAULT '',
    file_path      TEXT NOT NULL DEFAULT '',
    thumbnail_path TEXT NOT NULL DEFAULT '',
    thumb_width     INTEGER NOT NULL DEFAULT 0,
    thumb_height    INTEGER NOT NULL DEFAULT 0,
    file_size INTEGER NOT NULL DEFAULT 0,
    mime_type TEXT    NOT NULL DEFAULT '',
    bumped_at INTEGER NOT NULL DEFAULT 0,
    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_reply_to ON posts(reply_to);
