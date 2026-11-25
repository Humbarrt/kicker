package store

import (
	"context"
	"database/sql"
	"time"
)

// Stats queries (M3 task 8) over the rating rows: every rated game of a player, with its result.

const ratingCols = `game_id, player_id, rating_key, before, after, ended_at, result, goals_for, goals_against`

func scanRatingRows(rows *sql.Rows, err error) ([]RatingRow, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RatingRow{}
	for rows.Next() {
		var r RatingRow
		var ended int64
		if err := rows.Scan(&r.GameID, &r.PlayerID, &r.Key, &r.Before, &r.After, &ended, &r.Result, &r.GoalsFor,
			&r.GoalsAgainst); err != nil {
			return nil, err
		}
		r.EndedAt = time.UnixMilli(ended)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PlayerResults returns every rated game of a player, oldest first.
func (s *Store) PlayerResults(ctx context.Context, playerID int64) ([]RatingRow, error) {
	return scanRatingRows(s.r.QueryContext(ctx, `SELECT `+ratingCols+` FROM ratings WHERE player_id = ?
		ORDER BY ended_at, game_id`, playerID))
}

// LastResults returns a player's last n rated games in one key ("" = both), newest first.
func (s *Store) LastResults(ctx context.Context, playerID int64, key string, n int) ([]RatingRow, error) {
	return scanRatingRows(s.r.QueryContext(ctx, `SELECT `+ratingCols+` FROM ratings
		WHERE player_id = ? AND (? = '' OR rating_key = ?) ORDER BY ended_at DESC, game_id DESC LIMIT ?`,
		playerID, key, key, n))
}

// RatingChange is how many rated games a player played in one key since a time, and how their
// rating changed.
type RatingChange struct {
	PlayerID int64
	Key      string
	Games    int
	Change   float64
}

// RatingChangesSince returns the rating changes of every player since a time, by player and key.
func (s *Store) RatingChangesSince(ctx context.Context, since time.Time) ([]RatingChange, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT player_id, rating_key, COUNT(*), SUM(after - before) FROM ratings
		INDEXED BY ratings_time WHERE ended_at >= ? GROUP BY player_id, rating_key ORDER BY player_id, rating_key`,
		since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RatingChange{}
	for rows.Next() {
		var c RatingChange
		if err := rows.Scan(&c.PlayerID, &c.Key, &c.Games, &c.Change); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Relation is how a player did with (teammate) or against (opponent) another player.
type Relation struct {
	PlayerID int64
	Teammate bool
	Record
}

// PlayerRelations returns, per other player and per teammate/opponent, the record of a player in
// the rated games they played together. The guest and unknown coins are left out. results are the
// player's rating rows (PlayerResults); the other players come from game_participants. (Counting
// in SQL instead, joined with ratings, is slower: 120 ms against 95 ms for 16 000 games.)
func (s *Store) PlayerRelations(ctx context.Context, playerID, guestID int64, results []RatingRow) ([]Relation, error) {
	byGame := make(map[int64]RatingRow, len(results))
	for _, r := range results {
		byGame[r.GameID] = r
	}
	rows, err := s.r.QueryContext(ctx, `SELECT o.game_id, o.side = me.side, o.player_id
		FROM game_participants me JOIN game_participants o ON o.game_id = me.game_id
		WHERE me.player_id = ? AND o.player_id IS NOT NULL AND o.player_id NOT IN (?, ?)`, playerID, playerID, guestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct {
		player int64
		mate   bool
	}
	rel := map[key]*Relation{}
	var order []key
	for rows.Next() {
		var game, other int64
		var mate bool
		if err := rows.Scan(&game, &mate, &other); err != nil {
			return nil, err
		}
		r, ok := byGame[game]
		if !ok {
			continue // not rated (abandoned, voided, …)
		}
		k := key{other, mate}
		x := rel[k]
		if x == nil {
			x = &Relation{PlayerID: other, Teammate: mate}
			rel[k] = x
			order = append(order, k)
		}
		x.Add(r.Result, r.GoalsFor, r.GoalsAgainst)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Relation, 0, len(order))
	for _, k := range order {
		out = append(out, *rel[k])
	}
	return out, nil
}

// GameTotals counts the finished, not voided games since a time and their goals.
type GameTotals struct {
	Games, Goals int
}

// GamesSince counts the finished games since a time (abandoned and voided ones don't count).
func (s *Store) GamesSince(ctx context.Context, since time.Time) (GameTotals, error) {
	var t GameTotals
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(home_goals + away_goals), 0)
		FROM games INDEXED BY games_finished_order WHERE state = 'finished' AND NOT voided AND ended_at >= ?`, since.UnixMilli()).Scan(&t.Games, &t.Goals)
	return t, err
}

// BiggestWin returns the finished game since a time with the largest goal difference (the latest
// of equal ones); ok is false if there is none with a winner.
func (s *Store) BiggestWin(ctx context.Context, since time.Time) (id int64, ok bool, err error) {
	err = s.r.QueryRowContext(ctx, `SELECT id FROM games INDEXED BY games_finished_order
		WHERE state = 'finished' AND NOT voided AND winner IS NOT NULL AND ended_at >= ?
		ORDER BY abs(home_goals - away_goals) DESC, ended_at DESC, id DESC LIMIT 1`, since.UnixMilli()).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return id, err == nil, err
}
