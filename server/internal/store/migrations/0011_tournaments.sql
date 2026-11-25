-- Tournaments (M6, spec 09 §13). A tournament's brackets and standings are never stored: the
-- `tournament` package derives them from the settings (stages, tie-breaks, draw seed) and the
-- pairing results – the games linked to each pairing, or a walkover.

CREATE TABLE tournaments (
    id               INTEGER PRIMARY KEY,
    name             TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    owner_account_id INTEGER REFERENCES accounts (id) ON DELETE SET NULL,
    state            TEXT    NOT NULL DEFAULT 'setup'
                     CHECK (state IN ('setup', 'running', 'paused', 'finished', 'abandoned')),
    settings         TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(settings)), -- stages, rules, tie-breaks, seed
    created_at       INTEGER NOT NULL,
    started_at       INTEGER,
    ended_at         INTEGER
) STRICT;

-- Players in a tournament. team_no groups them into teams (1..n = the team ids = seeds once
-- started); NULL for Monster DYP or before a DYP draw. The guest may take part several times.
CREATE TABLE tournament_participants (
    id            INTEGER PRIMARY KEY,
    tournament_id INTEGER NOT NULL REFERENCES tournaments (id) ON DELETE CASCADE,
    player_id     INTEGER NOT NULL REFERENCES players (id),
    team_no       INTEGER CHECK (team_no IS NULL OR team_no >= 1),
    seed          INTEGER NOT NULL CHECK (seed >= 1), -- order within the tournament (players) / team
    dropped_at    INTEGER
) STRICT;
CREATE INDEX tournament_participants_tournament ON tournament_participants (tournament_id, seed);

-- A pairing gets a row once it is played (or decided by walkover): games point at it.
CREATE TABLE tournament_pairings (
    id            INTEGER PRIMARY KEY,
    tournament_id INTEGER NOT NULL REFERENCES tournaments (id) ON DELETE CASCADE,
    key           TEXT    NOT NULL CHECK (length(key) BETWEEN 1 AND 40), -- tournament.Pairing.Key
    walkover      TEXT    CHECK (walkover IN ('a', 'b')),                 -- the side that advances
    created_at    INTEGER NOT NULL,
    UNIQUE (tournament_id, key)
) STRICT;

-- Which side team A of the pairing played in a game (sides swap every game, spec 09 §7.3).
ALTER TABLE games ADD COLUMN pairing_a_side TEXT CHECK (pairing_a_side IN ('home', 'away'));
CREATE INDEX games_pairing ON games (pairing_id) WHERE pairing_id IS NOT NULL;

CREATE TABLE tournament_events (
    id            INTEGER PRIMARY KEY,
    tournament_id INTEGER NOT NULL REFERENCES tournaments (id) ON DELETE CASCADE,
    at            INTEGER NOT NULL,
    account_id    INTEGER REFERENCES accounts (id) ON DELETE SET NULL,
    kind          TEXT    NOT NULL CHECK (kind IN (
                      'created', 'edited', 'started', 'paused', 'resumed', 'finished', 'abandoned',
                      'reinstated', 'player_replaced', 'dropout', 'walkover', 'manual_result', 'rebuilt')),
    details       TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(details))
) STRICT;
CREATE INDEX tournament_events_tournament ON tournament_events (tournament_id, id);
