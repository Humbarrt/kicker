-- Accounts, sessions and the audit log (spec 03 §2, §5, §6, §14).

CREATE TABLE accounts (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    username             TEXT    NOT NULL UNIQUE COLLATE NOCASE
                             CHECK (length(username) BETWEEN 3 AND 32 AND username NOT GLOB '*[^a-z0-9_.-]*'),
    password_hash        TEXT    NOT NULL CHECK (password_hash LIKE '$argon2id$%'),
    role                 TEXT    NOT NULL CHECK (role IN ('admin', 'member')),
    status               TEXT    NOT NULL CHECK (status IN ('pending', 'approved', 'disabled')),
    player_id            INTEGER, -- players arrive in M3
    must_change_password INTEGER NOT NULL DEFAULT 0 CHECK (must_change_password IN (0, 1)),
    created_at           INTEGER NOT NULL,
    last_login_at        INTEGER
) STRICT;

CREATE TABLE sessions (
    id           INTEGER PRIMARY KEY,
    account_id   INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    token_hash   TEXT    NOT NULL UNIQUE CHECK (length(token_hash) = 64), -- hex sha256 of the token
    kind         TEXT    NOT NULL CHECK (kind IN ('web', 'app')),
    device_name  TEXT    NOT NULL DEFAULT '' CHECK (length(device_name) <= 80),
    user_agent   TEXT    NOT NULL DEFAULT '' CHECK (length(user_agent) <= 300),
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_account ON sessions (account_id);

CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY,
    at         INTEGER NOT NULL,
    account_id INTEGER, -- who did it; NULL = anonymous / system / console
    ip         TEXT    NOT NULL DEFAULT '',
    action     TEXT    NOT NULL CHECK (length(action) BETWEEN 1 AND 64),
    target     TEXT    NOT NULL DEFAULT '',
    details    TEXT    NOT NULL DEFAULT '' CHECK (length(details) <= 1000)
) STRICT;

CREATE INDEX audit_log_at ON audit_log (at);
