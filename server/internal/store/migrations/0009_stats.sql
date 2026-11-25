-- Stats (M3 task 8): every rating row also says how the game went for that player, so profiles,
-- the leaderboard and the overview are plain queries. The rows are rebuilt by the stats projector
-- (stats.Version) at the first start after this migration.

ALTER TABLE ratings ADD COLUMN ended_at INTEGER NOT NULL DEFAULT 0; -- the game's end (rating order)
ALTER TABLE ratings ADD COLUMN result TEXT NOT NULL DEFAULT 'draw' CHECK (result IN ('win', 'loss', 'draw'));
ALTER TABLE ratings ADD COLUMN goals_for INTEGER NOT NULL DEFAULT 0 CHECK (goals_for >= 0);
ALTER TABLE ratings ADD COLUMN goals_against INTEGER NOT NULL DEFAULT 0 CHECK (goals_against >= 0);

DROP INDEX ratings_player;
CREATE INDEX ratings_player ON ratings (player_id, ended_at, game_id);
CREATE INDEX ratings_time ON ratings (ended_at);

-- Totals per player and key, next to the current rating.
ALTER TABLE player_ratings ADD COLUMN wins INTEGER NOT NULL DEFAULT 0 CHECK (wins >= 0);
ALTER TABLE player_ratings ADD COLUMN losses INTEGER NOT NULL DEFAULT 0 CHECK (losses >= 0);
ALTER TABLE player_ratings ADD COLUMN draws INTEGER NOT NULL DEFAULT 0 CHECK (draws >= 0);
ALTER TABLE player_ratings ADD COLUMN goals_for INTEGER NOT NULL DEFAULT 0 CHECK (goals_for >= 0);
ALTER TABLE player_ratings ADD COLUMN goals_against INTEGER NOT NULL DEFAULT 0 CHECK (goals_against >= 0);
