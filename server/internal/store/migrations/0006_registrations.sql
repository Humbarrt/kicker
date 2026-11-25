-- kicker:foreign-keys-off
-- Registrations (spec 02 §4) and real player references in the game event log.

-- game_events.player_id was a placeholder TEXT column until players existed (M1). Rebuilt as a
-- reference to players; SQLite can't change a column's type in place.
CREATE TABLE game_events_new (
    id                INTEGER PRIMARY KEY,
    game_id           INTEGER NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    seq               INTEGER NOT NULL CHECK (seq >= 1),
    type              TEXT    NOT NULL CHECK (type IN (
                          'game_started', 'finished', 'paused', 'resumed', 'abandoned', 'reinstated',
                          'game_voided', 'player_joined', 'player_left', 'player_assigned',
                          'rules_changed', 'ball_released', 'goal', 'goal_voided', 'sides_swapped',
                          'clock_expired', 'golden_goal')),
    side              TEXT    CHECK (side IN ('home', 'away')),
    slot              INTEGER CHECK (slot IN (1, 2)),
    player_id         INTEGER REFERENCES players (id),
    coin_uid          TEXT    CHECK (coin_uid IS NULL OR (length(coin_uid) IN (8, 14, 20) AND coin_uid NOT GLOB '*[^0-9A-F]*')),
    via               TEXT    CHECK (via IN ('coin', 'corner', 'manual')),
    target_seq        INTEGER CHECK (target_seq IS NULL OR target_seq >= 1),
    value             INTEGER,
    rule              TEXT    CHECK (rule IN ('win_score', 'win_by', 'max_score', 'time_limit_s', 'time_tie')),
    rule_value        TEXT,
    source            TEXT    NOT NULL CHECK (source IN ('sensor', 'table', 'manual', 'system')),
    author_account_id INTEGER,
    reason            TEXT    CHECK (reason IS NULL OR length(reason) <= 200),
    created_at        INTEGER NOT NULL,
    play_time_ms      INTEGER CHECK (play_time_ms IS NULL OR play_time_ms >= 0),
    UNIQUE (game_id, seq)
) STRICT;

-- No player events were ever written before this migration, so player_id is NULL everywhere.
INSERT INTO game_events_new (id, game_id, seq, type, side, slot, player_id, coin_uid, via, target_seq,
    value, rule, rule_value, source, author_account_id, reason, created_at, play_time_ms)
SELECT id, game_id, seq, type, side, slot, NULL, coin_uid, via, target_seq,
    value, rule, rule_value, source, author_account_id, reason, created_at, play_time_ms
FROM game_events;

DROP TABLE game_events;
ALTER TABLE game_events_new RENAME TO game_events;
CREATE INDEX game_events_type ON game_events (type, game_id);
CREATE INDEX game_events_player ON game_events (player_id) WHERE player_id IS NOT NULL;

-- Who is registered on the table right now: a player (or an unknown coin) in a slot. Kept so
-- registrations survive restarts and updates; the coin acceptors' state sync corrects them.
CREATE TABLE table_registrations (
    side      TEXT    NOT NULL CHECK (side IN ('home', 'away')),
    slot      INTEGER NOT NULL CHECK (slot IN (1, 2)),
    player_id INTEGER REFERENCES players (id), -- NULL = unknown coin
    coin_uid  TEXT    CHECK (coin_uid IS NULL OR (length(coin_uid) IN (8, 14, 20) AND coin_uid NOT GLOB '*[^0-9A-F]*')),
    CHECK (player_id IS NOT NULL OR coin_uid IS NOT NULL),
    PRIMARY KEY (side, slot)
) STRICT, WITHOUT ROWID;

-- Why a registration exists. A registration is stale when all its sources are stale; its status
-- is derived, not stored.
CREATE TABLE table_registration_sources (
    side  TEXT    NOT NULL,
    slot  INTEGER NOT NULL,
    via   TEXT    NOT NULL CHECK (via IN ('coin', 'corner', 'manual')),
    ref   TEXT    NOT NULL DEFAULT '' CHECK (length(ref) <= 64), -- coin uid / corner id / '' (manual)
    stale INTEGER NOT NULL DEFAULT 0 CHECK (stale IN (0, 1)),
    position INTEGER NOT NULL CHECK (position >= 0), -- order of arrival; the first says how the player joined
    PRIMARY KEY (side, slot, via, ref),
    FOREIGN KEY (side, slot) REFERENCES table_registrations (side, slot) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;
