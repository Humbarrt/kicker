package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Tournament is a row of tournaments (spec 09 §13). Settings is the setup the `tournament`
// package derives everything from; the store doesn't look inside.
type Tournament struct {
	ID             int64
	Name           string
	OwnerAccountID *int64
	State          string // setup | running | paused | finished | abandoned
	Settings       json.RawMessage
	CreatedAt      time.Time
	StartedAt      *time.Time
	EndedAt        *time.Time
}

// TournamentParticipant is a player in a tournament.
type TournamentParticipant struct {
	ID        int64
	PlayerID  int64
	TeamNo    *int // team id (= seed of the team) once teams exist; nil for Monster DYP
	Seed      int  // the player's place in the tournament's order
	DroppedAt *time.Time
}

// TournamentEvent is an entry of a tournament's history.
type TournamentEvent struct {
	ID        int64
	At        time.Time
	AccountID *int64
	Kind      string
	Details   json.RawMessage
}

// PairingGame is a finished, valid game of a pairing, as the tournament counts it.
type PairingGame struct {
	GameID    int64
	ASide     string  // home | away: where team A played
	Winner    *string // home | away
	HomeGoals int
	AwayGoals int
	EndedAt   time.Time
}

// PairingResult is what the store knows about a played pairing.
type PairingResult struct {
	ID       int64
	Walkover string // "" | a | b
	Games    []PairingGame
}

// ErrTournamentState is returned when a change isn't possible in the tournament's state.
var ErrTournamentState = errors.New("not possible in this tournament state")

const tournamentCols = `id, name, owner_account_id, state, settings, created_at, started_at, ended_at`

func scanTournament(sc interface{ Scan(...any) error }) (Tournament, error) {
	var t Tournament
	var owner, started, ended sql.NullInt64
	var settings string
	var created int64
	err := sc.Scan(&t.ID, &t.Name, &owner, &t.State, &settings, &created, &started, &ended)
	t.OwnerAccountID, t.Settings, t.CreatedAt = int64Ptr(owner), json.RawMessage(settings), time.UnixMilli(created)
	if started.Valid {
		t.StartedAt = Ptr(time.UnixMilli(started.Int64))
	}
	if ended.Valid {
		t.EndedAt = Ptr(time.UnixMilli(ended.Int64))
	}
	return t, err
}

func settingsText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

// CreateTournament inserts a tournament in setup with its participants and a `created` event.
func (s *Store) CreateTournament(ctx context.Context, t Tournament, ps []TournamentParticipant, author *int64) (int64, error) {
	var id int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		now := time.Now()
		res, err := tx.ExecContext(ctx, `INSERT INTO tournaments (name, owner_account_id, state, settings, created_at)
			VALUES (?, ?, 'setup', ?, ?)`, t.Name, t.OwnerAccountID, settingsText(t.Settings), now.UnixMilli())
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		if err := setTournamentParticipants(ctx, tx, id, ps); err != nil {
			return err
		}
		return addTournamentEvent(ctx, tx, id, now, author, "created", nil)
	})
	return id, err
}

func setTournamentParticipants(ctx context.Context, tx *sql.Tx, id int64, ps []TournamentParticipant) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM tournament_participants WHERE tournament_id = ?`, id); err != nil {
		return err
	}
	for _, p := range ps {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tournament_participants (tournament_id, player_id, team_no, seed, dropped_at)
			VALUES (?, ?, ?, ?, ?)`, id, p.PlayerID, p.TeamNo, p.Seed, msPtr(p.DroppedAt)); err != nil {
			return err
		}
	}
	return nil
}

// Tournament returns a tournament with its participants in seed order.
func (s *Store) Tournament(ctx context.Context, id int64) (Tournament, []TournamentParticipant, error) {
	t, err := scanTournament(s.r.QueryRowContext(ctx, `SELECT `+tournamentCols+` FROM tournaments WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, nil, ErrNotFound
	}
	if err != nil {
		return t, nil, err
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id, player_id, team_no, seed, dropped_at FROM tournament_participants
		WHERE tournament_id = ? ORDER BY seed, id`, id)
	if err != nil {
		return t, nil, err
	}
	defer rows.Close()
	ps := []TournamentParticipant{}
	for rows.Next() {
		var p TournamentParticipant
		var team, dropped sql.NullInt64
		if err := rows.Scan(&p.ID, &p.PlayerID, &team, &p.Seed, &dropped); err != nil {
			return t, nil, err
		}
		p.TeamNo = intPtr(team)
		if dropped.Valid {
			p.DroppedAt = Ptr(time.UnixMilli(dropped.Int64))
		}
		ps = append(ps, p)
	}
	return t, ps, rows.Err()
}

// Tournaments lists all tournaments: running and paused first, then setup, then the rest; newest
// first within each.
func (s *Store) Tournaments(ctx context.Context) ([]Tournament, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+tournamentCols+` FROM tournaments
		ORDER BY CASE state WHEN 'running' THEN 0 WHEN 'paused' THEN 1 WHEN 'setup' THEN 2 ELSE 3 END,
		created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tournament{}
	for rows.Next() {
		t, err := scanTournament(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ActiveTournament returns the running tournament, if any (only one runs at a time: it owns the
// table, spec 09 §8).
func (s *Store) ActiveTournament(ctx context.Context) (*Tournament, error) {
	t, err := scanTournament(s.r.QueryRowContext(ctx, `SELECT `+tournamentCols+` FROM tournaments
		WHERE state = 'running' ORDER BY id LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTournamentSetup changes name, settings and participants – only while in setup.
func (s *Store) UpdateTournamentSetup(ctx context.Context, t Tournament, ps []TournamentParticipant, author *int64) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE tournaments SET name = ?, settings = ? WHERE id = ? AND state = 'setup'`,
			t.Name, settingsText(t.Settings), t.ID)
		if err != nil {
			return err
		}
		if err := mustChangeOne(ctx, tx, res, t.ID); err != nil {
			return err
		}
		if err := setTournamentParticipants(ctx, tx, t.ID, ps); err != nil {
			return err
		}
		return addTournamentEvent(ctx, tx, t.ID, time.Now(), author, "edited", nil)
	})
}

// mustChangeOne tells "no such tournament" from "wrong state" after a conditional update.
func mustChangeOne(ctx context.Context, tx *sql.Tx, res sql.Result, id int64) error {
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return err
	}
	var x int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM tournaments WHERE id = ?`, id).Scan(&x); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return ErrTournamentState
}

// tournamentMoves are the state changes of spec 09 §3, with the event each one writes.
var tournamentMoves = map[[2]string]string{
	{"setup", "running"}:     "started",
	{"running", "paused"}:    "paused",
	{"paused", "running"}:    "resumed",
	{"running", "finished"}:  "finished",
	{"running", "abandoned"}: "abandoned",
	{"paused", "abandoned"}:  "abandoned",
	{"abandoned", "paused"}:  "reinstated",
}

// SetTournamentState moves a tournament to another state (spec 09 §3) and writes the event.
// Starting may also store the final participants (teams drawn, seeds fixed); nil keeps them.
func (s *Store) SetTournamentState(ctx context.Context, id int64, from, to string, ps []TournamentParticipant,
	author *int64, details any) error {
	kind, ok := tournamentMoves[[2]string{from, to}]
	if !ok {
		return ErrTournamentState
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		q, args := `UPDATE tournaments SET state = ?`, []any{to}
		switch kind {
		case "started":
			q, args = q+`, started_at = ?`, append(args, now)
		case "finished", "abandoned":
			q, args = q+`, ended_at = ?`, append(args, now)
		case "reinstated":
			q += `, ended_at = NULL`
		}
		res, err := tx.ExecContext(ctx, q+` WHERE id = ? AND state = ?`, append(args, id, from)...)
		if err != nil {
			return err
		}
		if err := mustChangeOne(ctx, tx, res, id); err != nil {
			return err
		}
		if ps != nil {
			if err := setTournamentParticipants(ctx, tx, id, ps); err != nil {
				return err
			}
		}
		return addTournamentEvent(ctx, tx, id, time.UnixMilli(now), author, kind, details)
	})
}

// DeleteTournament removes a tournament – refused once a game was played in it (abandon it
// instead, spec 09 §11).
func (s *Store) DeleteTournament(ctx context.Context, id int64) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM games g JOIN tournament_pairings p ON p.id = g.pairing_id
			WHERE p.tournament_id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrInUse
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM tournaments WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// PairingID returns the row of a pairing, creating it the first time it is played.
func (s *Store) PairingID(ctx context.Context, tournamentID int64, key string) (int64, error) {
	var id int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = pairingIDTx(ctx, tx, tournamentID, key)
		return err
	})
	return id, err
}

func pairingIDTx(ctx context.Context, tx *sql.Tx, tournamentID int64, key string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO tournament_pairings (tournament_id, key, created_at) VALUES (?, ?, ?)
		ON CONFLICT (tournament_id, key) DO UPDATE SET key = excluded.key RETURNING id`,
		tournamentID, key, time.Now().UnixMilli()).Scan(&id)
	return id, err
}

// SetWalkover decides a pairing without playing: side a or b advances; "" takes it back.
func (s *Store) SetWalkover(ctx context.Context, tournamentID int64, key, side string, author *int64) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		id, err := pairingIDTx(ctx, tx, tournamentID, key)
		if err != nil {
			return err
		}
		var w any
		if side != "" {
			w = side
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tournament_pairings SET walkover = ? WHERE id = ?`, w, id); err != nil {
			return err
		}
		return addTournamentEvent(ctx, tx, tournamentID, time.Now(), author, "walkover",
			map[string]string{"pairing": key, "side": side})
	})
}

// TournamentResults returns the played pairings of a tournament by key: walkovers and the
// finished games that count (not voided), in the order they ended.
func (s *Store) TournamentResults(ctx context.Context, tournamentID int64) (map[string]PairingResult, error) {
	out := map[string]PairingResult{}
	byID := map[int64]string{}
	rows, err := s.r.QueryContext(ctx, `SELECT id, key, walkover FROM tournament_pairings WHERE tournament_id = ?`, tournamentID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r PairingResult
		var key string
		var w sql.NullString
		if err := rows.Scan(&r.ID, &key, &w); err != nil {
			rows.Close()
			return nil, err
		}
		r.Walkover = w.String
		out[key], byID[r.ID] = r, key
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.r.QueryContext(ctx, `SELECT g.pairing_id, g.id, COALESCE(g.pairing_a_side, 'home'), g.winner,
			g.home_goals, g.away_goals, g.ended_at
		FROM games g JOIN tournament_pairings p ON p.id = g.pairing_id
		WHERE p.tournament_id = ? AND g.state = 'finished' AND NOT g.voided
		ORDER BY g.ended_at, g.id`, tournamentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid, ended int64
		var g PairingGame
		var winner sql.NullString
		if err := rows.Scan(&pid, &g.GameID, &g.ASide, &winner, &g.HomeGoals, &g.AwayGoals, &ended); err != nil {
			return nil, err
		}
		g.Winner, g.EndedAt = strPtr(winner), time.UnixMilli(ended)
		key := byID[pid]
		r := out[key]
		r.Games = append(r.Games, g)
		out[key] = r
	}
	return out, rows.Err()
}

// TournamentOfPairing returns the tournament and key of a pairing row (to find the tournament an
// edited game belongs to).
func (s *Store) TournamentOfPairing(ctx context.Context, pairingID int64) (int64, string, error) {
	var tid int64
	var key string
	err := s.r.QueryRowContext(ctx, `SELECT tournament_id, key FROM tournament_pairings WHERE id = ?`, pairingID).Scan(&tid, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrNotFound
	}
	return tid, key, err
}

// AddTournamentEvent writes an entry to a tournament's history.
func (s *Store) AddTournamentEvent(ctx context.Context, tournamentID int64, author *int64, kind string, details any) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		return addTournamentEvent(ctx, tx, tournamentID, time.Now(), author, kind, details)
	})
}

func addTournamentEvent(ctx context.Context, tx *sql.Tx, tournamentID int64, at time.Time, author *int64, kind string, details any) error {
	d := "{}"
	if details != nil {
		b, err := json.Marshal(details)
		if err != nil {
			return err
		}
		d = string(b)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO tournament_events (tournament_id, at, account_id, kind, details)
		VALUES (?, ?, ?, ?, ?)`, tournamentID, at.UnixMilli(), author, kind, d)
	return err
}

// TournamentEvents returns a tournament's history, oldest first.
func (s *Store) TournamentEvents(ctx context.Context, tournamentID int64) ([]TournamentEvent, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id, at, account_id, kind, details FROM tournament_events
		WHERE tournament_id = ? ORDER BY id`, tournamentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TournamentEvent{}
	for rows.Next() {
		var e TournamentEvent
		var at int64
		var acc sql.NullInt64
		var d string
		if err := rows.Scan(&e.ID, &at, &acc, &e.Kind, &d); err != nil {
			return nil, err
		}
		e.At, e.AccountID, e.Details = time.UnixMilli(at), int64Ptr(acc), json.RawMessage(d)
		out = append(out, e)
	}
	return out, rows.Err()
}

// StartTournament moves a tournament from setup to running with its final settings (the draw
// seed) and participants (teams drawn and seeded), in one transaction.
func (s *Store) StartTournament(ctx context.Context, id int64, settings json.RawMessage, ps []TournamentParticipant, author *int64) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		now := time.Now()
		res, err := tx.ExecContext(ctx, `UPDATE tournaments SET state = 'running', settings = ?, started_at = ?
			WHERE id = ? AND state = 'setup'`, settingsText(settings), now.UnixMilli(), id)
		if err != nil {
			return err
		}
		if err := mustChangeOne(ctx, tx, res, id); err != nil {
			return err
		}
		if err := setTournamentParticipants(ctx, tx, id, ps); err != nil {
			return err
		}
		return addTournamentEvent(ctx, tx, id, now, author, "started", nil)
	})
}

// TournamentPausedGame returns a paused game of a tournament (paused when the table went to
// sleep, or switched off the table), if there is one.
func (s *Store) TournamentPausedGame(ctx context.Context, tournamentID int64) (int64, bool, error) {
	var id int64
	err := s.r.QueryRowContext(ctx, `SELECT g.id FROM games g JOIN tournament_pairings p ON p.id = g.pairing_id
		WHERE p.tournament_id = ? AND g.state = 'paused' ORDER BY g.id DESC LIMIT 1`, tournamentID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}
