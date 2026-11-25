-- Projections for ratings and stats (D2, D9): the result of each game, who played it, the rating
-- history and the current ratings. All of it can be rebuilt from game_events; the server fills it
-- for existing games at the first start after this migration (setting "projections").

ALTER TABLE games ADD COLUMN home_goals INTEGER NOT NULL DEFAULT 0 CHECK (home_goals >= 0);
ALTER TABLE games ADD COLUMN away_goals INTEGER NOT NULL DEFAULT 0 CHECK (away_goals >= 0);
ALTER TABLE games ADD COLUMN winner TEXT CHECK (winner IN ('home', 'away')); -- NULL: running, draw or not finished

-- Who played: the game's roster (spec 02 §4). player_id NULL = an unknown coin (counts as guest).
CREATE TABLE game_participants (
    game_id   INTEGER NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    side      TEXT    NOT NULL CHECK (side IN ('home', 'away')),
    slot      INTEGER NOT NULL CHECK (slot IN (1, 2)),
    player_id INTEGER REFERENCES players (id),
    coin_uid  TEXT,
    CHECK (player_id IS NOT NULL OR coin_uid IS NOT NULL),
    PRIMARY KEY (game_id, side, slot)
) STRICT, WITHOUT ROWID;
CREATE INDEX game_participants_player ON game_participants (player_id) WHERE player_id IS NOT NULL;

-- Rating history: one row per rated game and player (not for the guest, whose rating is fixed).
CREATE TABLE ratings (
    game_id    INTEGER NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    player_id  INTEGER NOT NULL REFERENCES players (id),
    rating_key TEXT    NOT NULL CHECK (rating_key IN ('solo', 'team')),
    before     REAL    NOT NULL,
    after      REAL    NOT NULL,
    PRIMARY KEY (game_id, player_id, rating_key)
) STRICT, WITHOUT ROWID;
CREATE INDEX ratings_player ON ratings (player_id, rating_key);

-- Current ratings: the last row of each player's history.
CREATE TABLE player_ratings (
    player_id    INTEGER NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    rating_key   TEXT    NOT NULL CHECK (rating_key IN ('solo', 'team')),
    rating       REAL    NOT NULL,
    games        INTEGER NOT NULL CHECK (games >= 1),
    last_game_id INTEGER NOT NULL,
    PRIMARY KEY (player_id, rating_key)
) STRICT, WITHOUT ROWID;
CREATE INDEX player_ratings_rank ON player_ratings (rating_key, rating DESC);
CREATE INDEX games_finished_order ON games (ended_at, id) WHERE state = 'finished';
