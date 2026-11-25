-- kicker:foreign-keys-off
-- Editing finished games (D2, M3 task 7): edits are correction events; a `reverted` event
-- (target_seq = the edit) takes one back. Voided games stay, but count for nothing.

ALTER TABLE games ADD COLUMN voided INTEGER NOT NULL DEFAULT 0 CHECK (voided IN (0, 1));
ALTER TABLE games ADD COLUMN edits INTEGER NOT NULL DEFAULT 0 CHECK (edits >= 0); -- edits in force

-- game_events: the type list gets 'reverted' (SQLite can't change a CHECK in place).
CREATE TABLE game_events_new (
    id                INTEGER PRIMARY KEY,
    game_id           INTEGER NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    seq               INTEGER NOT NULL CHECK (seq >= 1),
    type              TEXT    NOT NULL CHECK (type IN (
                          'game_started', 'finished', 'paused', 'resumed', 'abandoned', 'reinstated',
                          'game_voided', 'player_joined', 'player_left', 'player_assigned',
                          'rules_changed', 'ball_released', 'goal', 'goal_voided', 'sides_swapped',
                          'clock_expired', 'golden_goal', 'reverted')),
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

INSERT INTO game_events_new SELECT * FROM game_events;
DROP TABLE game_events;
ALTER TABLE game_events_new RENAME TO game_events;
CREATE INDEX game_events_type ON game_events (type, game_id);
CREATE INDEX game_events_player ON game_events (player_id) WHERE player_id IS NOT NULL;
-- The feed of recent edits (admins).
CREATE INDEX game_events_edits ON game_events (created_at) WHERE source = 'manual';
