-- Coins (D2): NFC coins with their factory UID, mapped to a player. A player has 0..n coins;
-- guest coins map to the guest player.

CREATE TABLE coins (
    uid          TEXT    PRIMARY KEY CHECK (length(uid) IN (8, 14, 20) AND uid NOT GLOB '*[^0-9A-F]*'),
    player_id    INTEGER NOT NULL REFERENCES players (id),
    label        TEXT    NOT NULL DEFAULT '' CHECK (length(label) <= 40),
    active       INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER -- last time it was inserted
) STRICT, WITHOUT ROWID;

CREATE INDEX coins_player ON coins (player_id);
