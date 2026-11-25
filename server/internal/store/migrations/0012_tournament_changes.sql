-- Changing a running tournament (M6 task 6, spec 09 §10–11).

-- A replaced player keeps their row (history, profile) and points at who took their place.
ALTER TABLE tournament_participants ADD COLUMN replaced_by INTEGER REFERENCES tournament_participants (id);

-- Who a pairing was put on the table for ("t1-t4", tournament.Pairing.Sides): when an edit
-- changes the bracket, a pairing played by other sides than it has now is a conflict.
ALTER TABLE tournament_pairings ADD COLUMN sides TEXT;

-- A finished tournament's pairing results, frozen at the finish: later edits of its games don't
-- change its bracket or placements; voiding one of its games reopens it (spec 09 §11).
ALTER TABLE tournaments ADD COLUMN frozen TEXT CHECK (frozen IS NULL OR json_valid(frozen));

-- A new event kind: reopened (a game of the finished tournament was voided). SQLite can't change
-- a CHECK in place, so the table is made again.
CREATE TABLE tournament_events_new (
    id            INTEGER PRIMARY KEY,
    tournament_id INTEGER NOT NULL REFERENCES tournaments (id) ON DELETE CASCADE,
    at            INTEGER NOT NULL,
    account_id    INTEGER REFERENCES accounts (id) ON DELETE SET NULL,
    kind          TEXT    NOT NULL CHECK (kind IN (
                      'created', 'edited', 'started', 'paused', 'resumed', 'finished', 'abandoned',
                      'reinstated', 'player_replaced', 'dropout', 'walkover', 'manual_result', 'rebuilt',
                      'reopened')),
    details       TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(details))
) STRICT;
INSERT INTO tournament_events_new SELECT id, tournament_id, at, account_id, kind, details FROM tournament_events;
DROP TABLE tournament_events;
ALTER TABLE tournament_events_new RENAME TO tournament_events;
CREATE INDEX tournament_events_tournament ON tournament_events (tournament_id, id);
