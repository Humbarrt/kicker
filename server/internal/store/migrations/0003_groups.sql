-- kicker:foreign-keys-off
-- Groups with editable permissions replace the fixed roles (spec 03 §4).
-- Permission defaults for the built-in groups are filled in by the server (auth.Catalog), so
-- permissions added later get their defaults without a migration.

CREATE TABLE groups (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL UNIQUE COLLATE NOCASE CHECK (length(trim(name)) BETWEEN 1 AND 40),
    builtin    TEXT    UNIQUE CHECK (builtin IN ('admin', 'member', 'anybody')),
    created_at INTEGER NOT NULL
) STRICT;

INSERT INTO groups (id, name, builtin, created_at) VALUES
    (1, 'Admin',   'admin',   CAST(unixepoch('subsec') * 1000 AS INTEGER)),
    (2, 'Member',  'member',  CAST(unixepoch('subsec') * 1000 AS INTEGER)),
    (3, 'Anybody', 'anybody', CAST(unixepoch('subsec') * 1000 AS INTEGER));

CREATE TABLE group_permissions (
    group_id   INTEGER NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    permission TEXT    NOT NULL CHECK (length(permission) BETWEEN 1 AND 64),
    value      INTEGER NOT NULL CHECK (value BETWEEN -1 AND 365), -- see spec 03 §14
    PRIMARY KEY (group_id, permission)
) STRICT;

CREATE TRIGGER groups_builtin_undeletable BEFORE DELETE ON groups WHEN OLD.builtin IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'built-in groups cannot be deleted');
END;

CREATE TRIGGER groups_builtin_fixed BEFORE UPDATE OF builtin ON groups WHEN OLD.builtin IS NOT NEW.builtin
BEGIN
    SELECT RAISE(ABORT, 'built-in groups cannot change their kind');
END;

-- The Admin group has every permission by definition; nothing is stored for it.
CREATE TRIGGER group_permissions_not_admin BEFORE INSERT ON group_permissions
    WHEN (SELECT builtin FROM groups WHERE id = NEW.group_id) = 'admin'
BEGIN
    SELECT RAISE(ABORT, 'admin permissions cannot be changed');
END;

-- accounts: role → group_id (rebuild, as SQLite can't add a NOT NULL foreign key column).
CREATE TABLE accounts_new (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    username             TEXT    NOT NULL UNIQUE COLLATE NOCASE
                             CHECK (length(username) BETWEEN 3 AND 32 AND username NOT GLOB '*[^a-z0-9_.-]*'),
    password_hash        TEXT    NOT NULL CHECK (password_hash LIKE '$argon2id$%'),
    group_id             INTEGER NOT NULL REFERENCES groups (id),
    status               TEXT    NOT NULL CHECK (status IN ('pending', 'approved', 'disabled')),
    player_id            INTEGER, -- players arrive in M3
    must_change_password INTEGER NOT NULL DEFAULT 0 CHECK (must_change_password IN (0, 1)),
    created_at           INTEGER NOT NULL,
    last_login_at        INTEGER
) STRICT;

INSERT INTO accounts_new (id, username, password_hash, group_id, status, player_id, must_change_password, created_at, last_login_at)
    SELECT id, username, password_hash, CASE role WHEN 'admin' THEN 1 ELSE 2 END, status, player_id,
           must_change_password, created_at, last_login_at
    FROM accounts;

DROP TABLE accounts;
ALTER TABLE accounts_new RENAME TO accounts;
CREATE INDEX accounts_group ON accounts (group_id);

CREATE TRIGGER accounts_not_anybody_insert BEFORE INSERT ON accounts
    WHEN (SELECT builtin FROM groups WHERE id = NEW.group_id) = 'anybody'
BEGIN
    SELECT RAISE(ABORT, 'no account can be in the Anybody group');
END;

CREATE TRIGGER accounts_not_anybody_update BEFORE UPDATE OF group_id ON accounts
    WHEN (SELECT builtin FROM groups WHERE id = NEW.group_id) = 'anybody'
BEGIN
    SELECT RAISE(ABORT, 'no account can be in the Anybody group');
END;
