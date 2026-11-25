-- Players (D2): someone who appears in games. Separate from accounts; an approved account is
-- linked to exactly one player, a player has at most one account.

CREATE TABLE players (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL UNIQUE COLLATE NOCASE CHECK (length(trim(name)) BETWEEN 1 AND 40 AND name = trim(name)),
    color       TEXT    NOT NULL CHECK (length(color) = 7 AND color GLOB '#[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]'),
    avatar_hash TEXT    CHECK (avatar_hash IS NULL OR (length(avatar_hash) = 64 AND avatar_hash NOT GLOB '*[^0-9a-f]*')),
    is_guest    INTEGER NOT NULL DEFAULT 0 CHECK (is_guest IN (0, 1)),
    archived_at INTEGER, -- hidden from pickers, kept for history
    created_at  INTEGER NOT NULL,
    CHECK (is_guest = 0 OR archived_at IS NULL)
) STRICT;

-- Exactly one guest (D2): it can fill several slots and has a fixed rating.
CREATE UNIQUE INDEX players_one_guest ON players (is_guest) WHERE is_guest = 1;
INSERT INTO players (name, color, is_guest, created_at)
    VALUES ('Guest', '#78716c', 1, CAST(unixepoch('subsec') * 1000 AS INTEGER));

CREATE TRIGGER players_guest_undeletable BEFORE DELETE ON players WHEN OLD.is_guest = 1
BEGIN
    SELECT RAISE(ABORT, 'the guest player cannot be deleted');
END;

-- A player has at most one account; the guest has none.
CREATE UNIQUE INDEX accounts_player ON accounts (player_id) WHERE player_id IS NOT NULL;

CREATE TRIGGER accounts_player_insert BEFORE INSERT ON accounts WHEN NEW.player_id IS NOT NULL
    AND COALESCE((SELECT is_guest FROM players WHERE id = NEW.player_id), 1) = 1
BEGIN
    SELECT RAISE(ABORT, 'an account must be linked to an existing, non-guest player');
END;

CREATE TRIGGER accounts_player_update BEFORE UPDATE OF player_id ON accounts WHEN NEW.player_id IS NOT NULL
    AND COALESCE((SELECT is_guest FROM players WHERE id = NEW.player_id), 1) = 1
BEGIN
    SELECT RAISE(ABORT, 'an account must be linked to an existing, non-guest player');
END;
