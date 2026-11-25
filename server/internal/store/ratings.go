package store

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"time"
)

// Ratings projection (D9): the rating history per game and player, and the current ratings.

// GamePoint is a game's place in the rating order: finished games are rated by end time, then id.
type GamePoint struct {
	EndedAt time.Time
	ID      int64
}

// RatedGame is a finished game as the rating replay needs it.
type RatedGame struct {
	ID           int64
	EndedAt      time.Time
	Winner       *string // nil = draw
	HomeGoals    int
	AwayGoals    int
	PairingID    *int64
	Participants []Participant
}

// RatingRow is a row of the rating history: one rated game of one player, with how it went.
type RatingRow struct {
	GameID       int64
	PlayerID     int64
	Key          string // solo | team
	Before       float64
	After        float64
	EndedAt      time.Time
	Result       string // win | loss | draw
	GoalsFor     int
	GoalsAgainst int
}

// Record counts results and goals.
type Record struct {
	Wins, Losses, Draws    int
	GoalsFor, GoalsAgainst int
}

// Add counts one result.
func (r *Record) Add(result string, goalsFor, goalsAgainst int) {
	switch result {
	case "win":
		r.Wins++
	case "loss":
		r.Losses++
	default:
		r.Draws++
	}
	r.GoalsFor += goalsFor
	r.GoalsAgainst += goalsAgainst
}

// Games is the number of games counted.
func (r Record) Games() int { return r.Wins + r.Losses + r.Draws }

// PlayerRating is a current rating with the player's totals in that key.
type PlayerRating struct {
	PlayerID int64
	Key      string
	Rating   float64
	Games    int
	Record
}

// After reports whether p comes after q in the rating order.
func (p GamePoint) After(q GamePoint) bool {
	return p.EndedAt.After(q.EndedAt) || p.EndedAt.Equal(q.EndedAt) && p.ID > q.ID
}

// RatingSpan returns where a game's ratings start and end in the rating order: its place now and
// the place its rating rows have (they differ when the end time was changed). ok is false for a
// game that is neither finished nor rated – nothing to do.
func (s *Store) RatingSpan(ctx context.Context, gameID int64) (from, to GamePoint, ok bool, err error) {
	var pts []GamePoint
	var ended sql.NullInt64
	err = s.r.QueryRowContext(ctx, `SELECT ended_at FROM games WHERE id = ?`, gameID).Scan(&ended)
	if err != nil && err != sql.ErrNoRows {
		return
	}
	if ended.Valid {
		pts = append(pts, GamePoint{EndedAt: time.UnixMilli(ended.Int64), ID: gameID})
	}
	var lo, hi sql.NullInt64
	if err = s.r.QueryRowContext(ctx, `SELECT MIN(ended_at), MAX(ended_at) FROM ratings WHERE game_id = ?`, gameID).
		Scan(&lo, &hi); err != nil {
		return
	}
	if lo.Valid {
		pts = append(pts, GamePoint{EndedAt: time.UnixMilli(lo.Int64), ID: gameID},
			GamePoint{EndedAt: time.UnixMilli(hi.Int64), ID: gameID})
	}
	if len(pts) == 0 {
		return GamePoint{}, GamePoint{}, false, nil
	}
	from, to = pts[0], pts[0]
	for _, p := range pts[1:] {
		if from.After(p) {
			from = p
		}
		if p.After(to) {
			to = p
		}
	}
	return from, to, true, nil
}

// RatedGames returns up to limit finished games after a point (or from it on, if inclusive), in
// rating order, with their participants. Two plain queries merged here: SQLite's planner sorts
// the join of 100 000 games in a temp b-tree otherwise.
func (s *Store) RatedGames(ctx context.Context, after GamePoint, inclusive bool, limit int) ([]RatedGame, error) {
	op := ">"
	if inclusive {
		op = ">="
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id, ended_at, winner, home_goals, away_goals, pairing_id
		FROM games INDEXED BY games_finished_order
		WHERE state = 'finished' AND ended_at >= ? AND (ended_at, id) `+op+` (?, ?) AND NOT voided
		ORDER BY ended_at, id LIMIT ?`, after.EndedAt.UnixMilli(), after.EndedAt.UnixMilli(), after.ID, limit)
	if err != nil {
		return nil, err
	}
	var out []RatedGame
	byID := map[int64]int{}
	lo, hi := int64(math.MaxInt64), int64(math.MinInt64)
	for rows.Next() {
		var g RatedGame
		var ended int64
		var winner sql.NullString
		var pairing sql.NullInt64
		if err := rows.Scan(&g.ID, &ended, &winner, &g.HomeGoals, &g.AwayGoals, &pairing); err != nil {
			rows.Close()
			return nil, err
		}
		g.EndedAt, g.Winner, g.PairingID = time.UnixMilli(ended), strPtr(winner), int64Ptr(pairing)
		byID[g.ID] = len(out)
		out = append(out, g)
		lo, hi = min(lo, g.ID), max(hi, g.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	// Ids grow with time, so the range holds few other games' participants.
	prows, err := s.r.QueryContext(ctx, `SELECT game_id, side, slot, player_id, coin_uid FROM game_participants
		WHERE game_id BETWEEN ? AND ? ORDER BY game_id, side = 'away', slot`, lo, hi)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var id int64
		var p Participant
		var player sql.NullInt64
		var coin sql.NullString
		if err := prows.Scan(&id, &p.Side, &p.Slot, &player, &coin); err != nil {
			return nil, err
		}
		if i, ok := byID[id]; ok {
			p.PlayerID, p.CoinUID = int64Ptr(player), strPtr(coin)
			out[i].Participants = append(out[i].Participants, p)
		}
	}
	return out, prows.Err()
}

// RatingRowsIn returns the rating rows after a point (or from it on, if inclusive) up to and
// including another one (nil = to the end), in rating order.
func (s *Store) RatingRowsIn(ctx context.Context, after GamePoint, inclusive bool, upTo *GamePoint) ([]RatingRow, error) {
	op := ">"
	if inclusive {
		op = ">="
	}
	q := `SELECT ` + ratingCols + ` FROM ratings WHERE ended_at >= ? AND (ended_at, game_id) ` + op + ` (?, ?)`
	args := []any{after.EndedAt.UnixMilli(), after.EndedAt.UnixMilli(), after.ID}
	if upTo != nil {
		q += ` AND ended_at <= ? AND (ended_at, game_id) <= (?, ?)`
		args = append(args, upTo.EndedAt.UnixMilli(), upTo.EndedAt.UnixMilli(), upTo.ID)
	}
	return scanRatingRows(s.r.QueryContext(ctx, q+` ORDER BY ended_at, game_id, player_id`, args...))
}

// RatingsBefore returns every rating as it was just before a point: per player and key the last
// row before it (an index lookup each, however far back the point is).
func (s *Store) RatingsBefore(ctx context.Context, before GamePoint) ([]PlayerRating, error) {
	keys, err := s.queryRatings(ctx, `SELECT player_id, rating_key, rating, games, wins, losses, draws, goals_for, goals_against
		FROM player_ratings ORDER BY player_id, rating_key`)
	if err != nil {
		return nil, err
	}
	stmt, err := s.r.PrepareContext(ctx, `SELECT after FROM ratings INDEXED BY ratings_player
		WHERE player_id = ? AND rating_key = ? AND ended_at <= ? AND (ended_at, game_id) < (?, ?)
		ORDER BY ended_at DESC, game_id DESC LIMIT 1`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	out := []PlayerRating{}
	for _, k := range keys {
		var after float64
		err := stmt.QueryRowContext(ctx, k.PlayerID, k.Key, before.EndedAt.UnixMilli(), before.EndedAt.UnixMilli(), before.ID).Scan(&after)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, PlayerRating{PlayerID: k.PlayerID, Key: k.Key, Rating: after})
	}
	return out, nil
}

// Same reports whether two rows of the same game, player and key hold the same values.
func (r RatingRow) Same(o RatingRow) bool {
	return r.Before == o.Before && r.After == o.After && r.SameGame(o)
}

// SameGame reports whether two rows differ at most in the rating.
func (r RatingRow) SameGame(o RatingRow) bool {
	return r.EndedAt.Equal(o.EndedAt) && r.Result == o.Result && r.GoalsFor == o.GoalsFor && r.GoalsAgainst == o.GoalsAgainst
}

// ChangeRatings writes the differences a replay found – rows whose rating changed (update), new
// rows and rows that are gone – and updates the current ratings of the players concerned, in one
// transaction. A row whose game changed otherwise is deleted and inserted.
func (s *Store) ChangeRatings(ctx context.Context, update, insert, del []RatingRow) error {
	if len(update) == 0 && len(insert) == 0 && len(del) == 0 {
		return nil
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		// Only the rating columns: they are in no index (an upsert of 200 000 rows takes three times as long).
		up, err := tx.PrepareContext(ctx, `UPDATE ratings SET before = ?, after = ?
			WHERE game_id = ? AND player_id = ? AND rating_key = ?`)
		if err != nil {
			return err
		}
		defer up.Close()
		ins, err := tx.PrepareContext(ctx, `INSERT INTO ratings (game_id, player_id, rating_key, before, after,
			ended_at, result, goals_for, goals_against) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer ins.Close()
		counts, values := map[int64]bool{}, map[int64]bool{}
		// Deletes first: a game whose end time moved deletes its rows at the old place and writes them at the new.
		for _, r := range del {
			if _, err := tx.ExecContext(ctx, `DELETE FROM ratings WHERE game_id = ? AND player_id = ? AND rating_key = ?`,
				r.GameID, r.PlayerID, r.Key); err != nil {
				return err
			}
			counts[r.PlayerID] = true
		}
		for _, r := range update {
			if _, err := up.ExecContext(ctx, r.Before, r.After, r.GameID, r.PlayerID, r.Key); err != nil {
				return err
			}
			values[r.PlayerID] = true
		}
		for _, r := range insert {
			if _, err := ins.ExecContext(ctx, r.GameID, r.PlayerID, r.Key, r.Before, r.After, r.EndedAt.UnixMilli(),
				r.Result, r.GoalsFor, r.GoalsAgainst); err != nil {
				return err
			}
			counts[r.PlayerID] = true
		}
		if err := refreshPlayerRatings(ctx, tx, counts); err != nil {
			return err
		}
		// Players whose games are the same only need the rating of their last row.
		last, err := tx.PrepareContext(ctx, `UPDATE player_ratings SET (rating, last_game_id) = (SELECT x.after, x.game_id
			FROM ratings x INDEXED BY ratings_player WHERE x.player_id = player_ratings.player_id
			AND x.rating_key = player_ratings.rating_key ORDER BY x.ended_at DESC, x.game_id DESC LIMIT 1)
			WHERE player_id = ?`)
		if err != nil {
			return err
		}
		defer last.Close()
		for id := range values {
			if counts[id] {
				continue
			}
			if _, err := last.ExecContext(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// RoundRating rounds a rating to what is stored: 0.01.
func RoundRating(v float64) float64 { return math.Round(v*100) / 100 }

// refreshPlayerRatings rebuilds the current ratings of some players (nil = everyone).
func refreshPlayerRatings(ctx context.Context, tx *sql.Tx, players map[int64]bool) error {
	if players != nil && len(players) == 0 {
		return nil
	}
	where, args := "", []any{}
	if players != nil && len(players) <= 500 {
		marks := make([]string, 0, len(players))
		for id := range players {
			marks = append(marks, "?")
			args = append(args, id)
		}
		where = " WHERE r.player_id IN (" + strings.Join(marks, ",") + ")"
		if _, err := tx.ExecContext(ctx, `DELETE FROM player_ratings WHERE player_id IN (`+strings.Join(marks, ",")+`)`, args...); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, `DELETE FROM player_ratings`); err != nil {
		return err
	}
	// Totals by grouping (a window over 300 000 rows sorts them all: 4 s), the rating from each
	// key's last row (an index lookup).
	_, err := tx.ExecContext(ctx, `INSERT INTO player_ratings (player_id, rating_key, rating, games, last_game_id,
			wins, losses, draws, goals_for, goals_against)
		SELECT t.player_id, t.rating_key, l.after, t.n, l.game_id, t.w, t.l, t.d, t.gf, t.ga FROM (
			SELECT r.player_id, r.rating_key, COUNT(*) AS n, SUM(r.result = 'win') AS w, SUM(r.result = 'loss') AS l,
				SUM(r.result = 'draw') AS d, SUM(r.goals_for) AS gf, SUM(r.goals_against) AS ga
			FROM ratings r`+where+` GROUP BY r.player_id, r.rating_key) t
		JOIN ratings l ON (l.game_id, l.player_id, l.rating_key) = (SELECT x.game_id, x.player_id, x.rating_key
			FROM ratings x INDEXED BY ratings_player WHERE x.player_id = t.player_id AND x.rating_key = t.rating_key
			ORDER BY x.ended_at DESC, x.game_id DESC LIMIT 1)`, args...)
	return err
}

// PlayerRatings returns a player's current ratings (one per key that has rated games).
func (s *Store) PlayerRatings(ctx context.Context, playerID int64) ([]PlayerRating, error) {
	return s.queryRatings(ctx, `SELECT player_id, rating_key, rating, games, wins, losses, draws, goals_for, goals_against
		FROM player_ratings WHERE player_id = ? ORDER BY rating_key`, playerID)
}

// Leaderboard returns the current ratings of one key, best first, of players that aren't archived.
func (s *Store) Leaderboard(ctx context.Context, key string, limit int) ([]PlayerRating, error) {
	return s.queryRatings(ctx, `SELECT r.player_id, r.rating_key, r.rating, r.games, r.wins, r.losses, r.draws,
		r.goals_for, r.goals_against FROM player_ratings r
		JOIN players p ON p.id = r.player_id WHERE r.rating_key = ? AND p.archived_at IS NULL
		ORDER BY r.rating DESC, r.games DESC, r.player_id LIMIT ?`, key, limit)
}

func (s *Store) queryRatings(ctx context.Context, q string, args ...any) ([]PlayerRating, error) {
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlayerRating{}
	for rows.Next() {
		var r PlayerRating
		if err := rows.Scan(&r.PlayerID, &r.Key, &r.Rating, &r.Games, &r.Wins, &r.Losses, &r.Draws, &r.GoalsFor,
			&r.GoalsAgainst); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GameRatings returns the rating changes of one game.
func (s *Store) GameRatings(ctx context.Context, gameID int64) ([]RatingRow, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT game_id, player_id, rating_key, before, after FROM ratings
		WHERE game_id = ? ORDER BY player_id`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RatingRow{}
	for rows.Next() {
		var r RatingRow
		if err := rows.Scan(&r.GameID, &r.PlayerID, &r.Key, &r.Before, &r.After); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
