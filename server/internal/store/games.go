package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a row doesn't exist.
var ErrNotFound = errors.New("not found")

// Rules are the win conditions of a preset or game (D10). Nil pointers are NULL ("not set").
type Rules struct {
	WinScore   int
	WinBy      int
	MaxScore   *int
	TimeLimitS *int
	TimeTie    string // golden_goal | draw
}

// Preset is a rule preset.
type Preset struct {
	ID      int64
	Label   string
	Rules   Rules
	Builtin bool
}

// Game is one row of games.
type Game struct {
	ID           int64
	PresetID     *int64
	Rules        Rules
	PairingID    *int64
	State        string // active | paused | finished | abandoned
	StartedAt    time.Time
	EndedAt      *time.Time
	NextBallSide *string
	// Projections (D2): the result and who played; rebuilt from the events.
	HomeGoals, AwayGoals int
	Winner               *string       // home | away; nil while running, for a draw or without result
	Voided               bool          // game_voided in force: counts for nothing
	Edits                int           // edits of the finished game in force
	Participants         []Participant // only written by CreateGame / AppendEvents, not read back by Game
}

// Participant is a row of game_participants: a seat of the game's roster.
type Participant struct {
	Side     string
	Slot     int
	PlayerID *int64 // nil = unknown coin (counts as guest)
	CoinUID  *string
}

// Event is one row of the game event log (spec 02 §11). Nil pointers are NULL.
type Event struct {
	ID              int64
	GameID          int64
	Seq             int
	Type            string
	Side            *string
	Slot            *int
	PlayerID        *int64
	CoinUID         *string
	Via             *string
	TargetSeq       *int
	Value           *int64
	Rule            *string
	RuleValue       *string
	Source          string
	AuthorAccountID *int64
	Reason          *string
	CreatedAt       time.Time
	PlayTimeMs      *int64
}

// GameUpdate changes the mutable columns of a game together with appended events.
type GameUpdate struct {
	State                string
	EndedAt              *time.Time
	NextBallSide         *string
	HomeGoals, AwayGoals int
	Winner               *string
	Voided               bool
	Edits                int
	Participants         []Participant // replaces the game's participants
}

// Ptr returns a pointer to v (for the nullable fields).
func Ptr[T any](v T) *T { return &v }

// --- presets ----------------------------------------------------------------------------------

const presetCols = `id, label, win_score, win_by, max_score, time_limit_s, time_tie, is_builtin`

func scanPreset(sc interface{ Scan(...any) error }) (Preset, error) {
	var p Preset
	var maxScore, timeLimit sql.NullInt64
	err := sc.Scan(&p.ID, &p.Label, &p.Rules.WinScore, &p.Rules.WinBy, &maxScore, &timeLimit, &p.Rules.TimeTie, &p.Builtin)
	p.Rules.MaxScore, p.Rules.TimeLimitS = intPtr(maxScore), intPtr(timeLimit)
	return p, err
}

// Presets lists all rule presets, built-in ones first.
func (s *Store) Presets(ctx context.Context) ([]Preset, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+presetCols+` FROM rule_presets ORDER BY is_builtin DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Preset
	for rows.Next() {
		p, err := scanPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Preset returns one preset.
func (s *Store) Preset(ctx context.Context, id int64) (Preset, error) {
	p, err := scanPreset(s.r.QueryRowContext(ctx, `SELECT `+presetCols+` FROM rule_presets WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// PresetByLabel returns a preset by its label.
func (s *Store) PresetByLabel(ctx context.Context, label string) (Preset, error) {
	p, err := scanPreset(s.r.QueryRowContext(ctx, `SELECT `+presetCols+` FROM rule_presets WHERE label = ?`, label))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// --- games ------------------------------------------------------------------------------------

const gameCols = `id, preset_id, win_score, win_by, max_score, time_limit_s, time_tie, pairing_id, state, started_at, ended_at, next_ball_side, home_goals, away_goals, winner, voided, edits`

func scanGame(sc interface{ Scan(...any) error }) (Game, error) {
	var g Game
	var presetID, maxScore, timeLimit, pairingID, endedAt sql.NullInt64
	var startedAt int64
	var nextBall, winner sql.NullString
	err := sc.Scan(&g.ID, &presetID, &g.Rules.WinScore, &g.Rules.WinBy, &maxScore, &timeLimit, &g.Rules.TimeTie,
		&pairingID, &g.State, &startedAt, &endedAt, &nextBall, &g.HomeGoals, &g.AwayGoals, &winner, &g.Voided, &g.Edits)
	g.Winner = strPtr(winner)
	g.PresetID, g.PairingID = int64Ptr(presetID), int64Ptr(pairingID)
	g.Rules.MaxScore, g.Rules.TimeLimitS = intPtr(maxScore), intPtr(timeLimit)
	g.StartedAt = time.UnixMilli(startedAt)
	if endedAt.Valid {
		g.EndedAt = Ptr(time.UnixMilli(endedAt.Int64))
	}
	g.NextBallSide = strPtr(nextBall)
	return g, err
}

// CreateGame inserts a game together with its first events (e.g. game_started, player_joined)
// in one transaction. It returns the game id; events get seq 1, 2, …
func (s *Store) CreateGame(ctx context.Context, g Game, events []Event) (int64, error) {
	var id int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = createGameTx(ctx, tx, g, events)
		return err
	})
	return id, err
}

func createGameTx(ctx context.Context, tx *sql.Tx, g Game, events []Event) (int64, error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO games (preset_id, win_score, win_by, max_score, time_limit_s, time_tie,
		pairing_id, state, started_at, ended_at, next_ball_side, home_goals, away_goals, winner)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.PresetID, g.Rules.WinScore, g.Rules.WinBy, g.Rules.MaxScore, g.Rules.TimeLimitS, g.Rules.TimeTie,
		g.PairingID, g.State, g.StartedAt.UnixMilli(), msPtr(g.EndedAt), g.NextBallSide, g.HomeGoals, g.AwayGoals, g.Winner)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err = insertEvents(ctx, tx, id, 0, events); err != nil {
		return 0, err
	}
	return id, setParticipants(ctx, tx, id, g.Participants)
}

// ImportGame is a complete game – its final row and all its events – for ImportGames.
type ImportGame struct {
	Game   Game
	Events []Event
}

// ImportGames inserts complete games in one transaction (the load test's generator, spec 04 §4).
func (s *Store) ImportGames(ctx context.Context, games []ImportGame) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		for _, g := range games {
			if _, err := createGameTx(ctx, tx, g.Game, g.Events); err != nil {
				return err
			}
		}
		return nil
	})
}

// AppendEvents appends events to a game's log (and optionally updates the game) atomically.
// It returns the events with their id and seq filled in.
func (s *Store) AppendEvents(ctx context.Context, gameID int64, events []Event, update *GameUpdate) ([]Event, error) {
	var out []Event
	err := s.Write(ctx, func(tx *sql.Tx) error {
		var last int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM game_events WHERE game_id = ?`, gameID).Scan(&last); err != nil {
			return err
		}
		var err error
		if out, err = insertEvents(ctx, tx, gameID, last, events); err != nil {
			return err
		}
		if update != nil {
			res, err := tx.ExecContext(ctx, `UPDATE games SET state = ?, ended_at = ?, next_ball_side = ?,
				home_goals = ?, away_goals = ?, winner = ?, voided = ?, edits = ? WHERE id = ?`,
				update.State, msPtr(update.EndedAt), update.NextBallSide, update.HomeGoals, update.AwayGoals, update.Winner,
				update.Voided, update.Edits, gameID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("game %d: %w", gameID, ErrNotFound)
			}
			return setParticipants(ctx, tx, gameID, update.Participants)
		}
		return nil
	})
	return out, err
}

func insertEvents(ctx context.Context, tx *sql.Tx, gameID int64, lastSeq int, events []Event) ([]Event, error) {
	out := make([]Event, 0, len(events))
	for _, e := range events {
		lastSeq++
		e.GameID, e.Seq = gameID, lastSeq
		res, err := tx.ExecContext(ctx, `INSERT INTO game_events (game_id, seq, type, side, slot, player_id, coin_uid,
			via, target_seq, value, rule, rule_value, source, author_account_id, reason, created_at, play_time_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.GameID, e.Seq, e.Type, e.Side, e.Slot, e.PlayerID, e.CoinUID, e.Via, e.TargetSeq, e.Value,
			e.Rule, e.RuleValue, e.Source, e.AuthorAccountID, e.Reason, e.CreatedAt.UnixMilli(), e.PlayTimeMs)
		if err != nil {
			return nil, fmt.Errorf("event %d (%s): %w", e.Seq, e.Type, err)
		}
		if e.ID, err = res.LastInsertId(); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// Game returns a game and its full event log in seq order.
func (s *Store) Game(ctx context.Context, id int64) (Game, []Event, error) {
	g, err := scanGame(s.r.QueryRowContext(ctx, `SELECT `+gameCols+` FROM games WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return g, nil, ErrNotFound
	}
	if err != nil {
		return g, nil, err
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id, game_id, seq, type, side, slot, player_id, coin_uid, via, target_seq,
		value, rule, rule_value, source, author_account_id, reason, created_at, play_time_ms
		FROM game_events WHERE game_id = ? ORDER BY seq`, id)
	if err != nil {
		return g, nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var e Event
		var side, coin, via, rule, ruleValue, reason sql.NullString
		var slot, player, target, value, author, playTime sql.NullInt64
		var created int64
		if err := rows.Scan(&e.ID, &e.GameID, &e.Seq, &e.Type, &side, &slot, &player, &coin, &via, &target,
			&value, &rule, &ruleValue, &e.Source, &author, &reason, &created, &playTime); err != nil {
			return g, nil, err
		}
		e.Side, e.PlayerID, e.CoinUID, e.Via = strPtr(side), int64Ptr(player), strPtr(coin), strPtr(via)
		e.Rule, e.RuleValue, e.Reason = strPtr(rule), strPtr(ruleValue), strPtr(reason)
		e.Slot, e.TargetSeq = intPtr(slot), intPtr(target)
		e.Value, e.AuthorAccountID, e.PlayTimeMs = int64Ptr(value), int64Ptr(author), int64Ptr(playTime)
		e.CreatedAt = time.UnixMilli(created)
		events = append(events, e)
	}
	return g, events, rows.Err()
}

// OpenGames returns games that are active or paused, newest first.
func (s *Store) OpenGames(ctx context.Context) ([]Game, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+gameCols+` FROM games WHERE state IN ('active', 'paused') ORDER BY started_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// LastFinished returns the most recently finished game with its events, or ErrNotFound.
func (s *Store) LastFinished(ctx context.Context) (Game, []Event, error) {
	var id int64
	err := s.r.QueryRowContext(ctx, `SELECT id FROM games WHERE state = 'finished' ORDER BY ended_at DESC, id DESC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Game{}, nil, ErrNotFound
	} else if err != nil {
		return Game{}, nil, err
	}
	return s.Game(ctx, id)
}

// DeleteGame removes a game and its events (D16: discarded games leave no trace).
func (s *Store) DeleteGame(ctx context.Context, id int64) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM games WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// --- NULL helpers -------------------------------------------------------------------------------

func intPtr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int64)
	return &i
}

func int64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func strPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func msPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

// GamesByState returns games in a state, most recently started first; limit 0 = all.
func (s *Store) GamesByState(ctx context.Context, state string, limit int) ([]Game, error) {
	q := `SELECT ` + gameCols + ` FROM games WHERE state = ? ORDER BY started_at DESC, id DESC`
	args := []any{state}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// setParticipants replaces a game's roster projection.
func setParticipants(ctx context.Context, tx *sql.Tx, gameID int64, ps []Participant) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_participants WHERE game_id = ?`, gameID); err != nil {
		return err
	}
	for _, p := range ps {
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_participants (game_id, side, slot, player_id, coin_uid)
			VALUES (?, ?, ?, ?, ?)`, gameID, p.Side, p.Slot, p.PlayerID, p.CoinUID); err != nil {
			return fmt.Errorf("participant %s %d: %w", p.Side, p.Slot, err)
		}
	}
	return nil
}

// SetProjection writes a game's result and roster projection without new events (rebuilds).
func (s *Store) SetProjection(ctx context.Context, gameID int64, u GameUpdate) error {
	_, err := s.AppendEvents(ctx, gameID, nil, &u)
	return err
}

// Participants returns a game's roster projection.
func (s *Store) Participants(ctx context.Context, gameID int64) ([]Participant, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT side, slot, player_id, coin_uid FROM game_participants
		WHERE game_id = ? ORDER BY side = 'away', slot`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Participant
	for rows.Next() {
		var p Participant
		var player sql.NullInt64
		var coin sql.NullString
		if err := rows.Scan(&p.Side, &p.Slot, &player, &coin); err != nil {
			return nil, err
		}
		p.PlayerID, p.CoinUID = int64Ptr(player), strPtr(coin)
		out = append(out, p)
	}
	return out, rows.Err()
}

// AllGameIDs returns the ids of all games, oldest first (for rebuilding projections).
func (s *Store) AllGameIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id FROM games ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
