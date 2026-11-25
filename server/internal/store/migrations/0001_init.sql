-- M1 schema: settings, rule presets, module adoptions, games and the game event log
-- (D2, D10, spec 02 §11). Times are Unix milliseconds (UTC). Every table is STRICT.

CREATE TABLE settings (
    key   TEXT PRIMARY KEY CHECK (length(key) BETWEEN 1 AND 64),
    value TEXT NOT NULL CHECK (json_valid(value))
) STRICT;

CREATE TABLE rule_presets (
    id           INTEGER PRIMARY KEY,
    label        TEXT    NOT NULL UNIQUE CHECK (length(label) BETWEEN 1 AND 40),
    win_score    INTEGER NOT NULL CHECK (win_score >= 1),
    win_by       INTEGER NOT NULL CHECK (win_by >= 1),
    max_score    INTEGER CHECK (max_score IS NULL OR max_score >= win_score),
    time_limit_s INTEGER CHECK (time_limit_s IS NULL OR time_limit_s > 0),
    time_tie     TEXT    NOT NULL DEFAULT 'golden_goal' CHECK (time_tie IN ('golden_goal', 'draw')),
    is_builtin   INTEGER NOT NULL DEFAULT 0 CHECK (is_builtin IN (0, 1))
) STRICT;

INSERT INTO rule_presets (label, win_score, win_by, max_score, time_limit_s, time_tie, is_builtin) VALUES
    ('Long',     10, 2, 15,   NULL, 'golden_goal', 1),
    ('Short',     6, 1,  6,   NULL, 'golden_goal', 1),
    ('Official',  5, 2,  8,   NULL, 'golden_goal', 1),
    ('Timed',    10, 1, NULL, 300,  'golden_goal', 1);

-- Adopted modules (D7). Unadopted modules are not stored.
CREATE TABLE modules (
    uid        TEXT    PRIMARY KEY CHECK (length(uid) BETWEEN 1 AND 64),
    name       TEXT    NOT NULL CHECK (length(name) <= 80),
    config     TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(config)), -- module-level config
    adopted_at INTEGER NOT NULL
) STRICT;

CREATE TABLE module_capabilities (
    uid        TEXT NOT NULL REFERENCES modules (uid) ON DELETE CASCADE,
    instance   TEXT NOT NULL CHECK (length(instance) BETWEEN 1 AND 32),
    capability TEXT NOT NULL CHECK (length(capability) BETWEEN 1 AND 64),
    role       TEXT CHECK (role IS NULL OR length(role) BETWEEN 1 AND 32),
    config     TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(config)), -- open-ended, so JSON (D2)
    PRIMARY KEY (uid, instance)
) STRICT;

-- A role belongs to exactly one instance on the table – except display roles (mirrors).
CREATE UNIQUE INDEX module_roles_exclusive ON module_capabilities (capability, role)
    WHERE role IS NOT NULL AND capability <> 'display';

-- Games (D10, D16). The rule columns are a copy of the preset at kickoff.
-- AUTOINCREMENT: ids of discarded games are never reused, so a game id always means one game.
CREATE TABLE games (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    preset_id      INTEGER REFERENCES rule_presets (id) ON DELETE SET NULL,
    win_score      INTEGER NOT NULL CHECK (win_score >= 1),
    win_by         INTEGER NOT NULL CHECK (win_by >= 1),
    max_score      INTEGER CHECK (max_score IS NULL OR max_score >= 1),
    time_limit_s   INTEGER CHECK (time_limit_s IS NULL OR time_limit_s > 0),
    time_tie       TEXT    NOT NULL CHECK (time_tie IN ('golden_goal', 'draw')),
    pairing_id     INTEGER, -- tournaments (M6)
    state          TEXT    NOT NULL CHECK (state IN ('active', 'paused', 'finished', 'abandoned')),
    started_at     INTEGER NOT NULL,
    ended_at       INTEGER CHECK (ended_at IS NULL OR ended_at >= started_at),
    next_ball_side TEXT CHECK (next_ball_side IN ('home', 'away'))
) STRICT;

CREATE INDEX games_state ON games (state);
CREATE INDEX games_ended_at ON games (ended_at);

-- The append-only game event log (spec 02 §11). Columns only, no JSON.
CREATE TABLE game_events (
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
    player_id         TEXT,    -- players arrive in M3
    coin_uid          TEXT,
    via               TEXT    CHECK (via IN ('coin', 'corner', 'manual')),
    target_seq        INTEGER CHECK (target_seq IS NULL OR target_seq >= 1),
    value             INTEGER,
    rule              TEXT    CHECK (rule IN ('win_score', 'win_by', 'max_score', 'time_limit_s', 'time_tie')),
    rule_value        TEXT,
    source            TEXT    NOT NULL CHECK (source IN ('sensor', 'table', 'manual', 'system')),
    author_account_id INTEGER, -- accounts arrive with M1 task 10
    reason            TEXT    CHECK (reason IS NULL OR length(reason) <= 200),
    created_at        INTEGER NOT NULL,
    play_time_ms      INTEGER CHECK (play_time_ms IS NULL OR play_time_ms >= 0),
    UNIQUE (game_id, seq)
) STRICT;

CREATE INDEX game_events_type ON game_events (type, game_id);
