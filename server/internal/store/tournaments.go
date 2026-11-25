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
	Frozen         json.RawMessage // finished: the pairing results at the finish (map key → PairingResult)
}

// TournamentParticipant is a player in a tournament.
type TournamentParticipant struct {
	ID        int64
	PlayerID  int64
	TeamNo    *int // team id (= seed of the team) once teams exist; nil for Monster DYP
	Seed      int  // the player's place in the tournament's order
	DroppedAt *time.Time
	// ReplacedBy is the participant who took this player's place (spec 09 §10); the row stays
	// for the history and the player's profile.
	ReplacedBy *int64
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
	GameID    int64     `json:"game_id"`
	ASide     string    `json:"a_side"`           // home | away: where team A played
	Winner    *string   `json:"winner,omitempty"` // home | away
	HomeGoals int       `json:"home_goals"`
	AwayGoals int       `json:"away_goals"`
	EndedAt   time.Time `json:"ended_at"`
}

// PairingResult is what the store knows about a played pairing.
type PairingResult struct {
	ID       int64         `json:"id"`
	Walkover string        `json:"walkover,omitempty"` // "" | a | b
	Sides    string        `json:"sides,omitempty"`    // who it was put on the table for (tournament.Pairing.Sides)
	Games    []PairingGame `json:"games,omitempty"`
}

// ErrTournamentState is returned when a change isn't possible in the tournament's state.
var ErrTournamentState = errors.New("not possible in this tournament state")

const tournamentCols = `id, name, owner_account_id, state, settings, created_at, started_at, ended_at, frozen`

func scanTournament(sc interface{ Scan(...any) error }) (Tournament, error) {
	var t Tournament
	var owner, started, ended sql.NullInt64
	var settings string
	var frozen sql.NullString
	var created int64
	err := sc.Scan(&t.ID, &t.Name, &owner, &t.State, &settings, &created, &started, &ended, &frozen)
	if frozen.Valid {
		t.Frozen = json.RawMessage(frozen.String)
	}
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
	rows, err := s.r.QueryContext(ctx, `SELECT id, player_id, team_no, seed, dropped_at, replaced_by FROM tournament_participants
		WHERE tournament_id = ? ORDER BY seed, id`, id)
	if err != nil {
		return t, nil, err
	}
	defer rows.Close()
	ps := []TournamentParticipant{}
	for rows.Next() {
		var p TournamentParticipant
		var team, dropped, replaced sql.NullInt64
		if err := rows.Scan(&p.ID, &p.PlayerID, &team, &p.Seed, &dropped, &replaced); err != nil {
			return t, nil, err
		}
		p.TeamNo, p.ReplacedBy = intPtr(team), int64Ptr(replaced)
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
	{"finished", "paused"}:   "reopened", // a game of it was voided (spec 09 §11)
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
		case "reinstated", "reopened":
			q += `, ended_at = NULL, frozen = NULL`
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

// PairingID returns the row of a pairing, creating it the first time it goes on the table.
// sides (tournament.Pairing.Sides) records who it is played by; a row without games takes the
// new sides (a pairing staged before and not played).
func (s *Store) PairingID(ctx context.Context, tournamentID int64, key, sides string) (int64, error) {
	var id int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = pairingIDTx(ctx, tx, tournamentID, key, sides)
		return err
	})
	return id, err
}

func pairingIDTx(ctx context.Context, tx *sql.Tx, tournamentID int64, key, sides string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO tournament_pairings (tournament_id, key, sides, created_at) VALUES (?, ?, NULLIF(?, ''), ?)
		ON CONFLICT (tournament_id, key) DO UPDATE SET sides = CASE
			WHEN excluded.sides IS NULL THEN sides
			WHEN sides IS NULL OR NOT EXISTS (SELECT 1 FROM games g WHERE g.pairing_id = tournament_pairings.id) THEN excluded.sides
			ELSE sides END
		RETURNING id`,
		tournamentID, key, sides, time.Now().UnixMilli()).Scan(&id)
	return id, err
}

// SetWalkover decides a pairing without playing: side a or b advances; "" takes it back.
func (s *Store) SetWalkover(ctx context.Context, tournamentID int64, key, side string, author *int64) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		id, err := pairingIDTx(ctx, tx, tournamentID, key, "")
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
	rows, err := s.r.QueryContext(ctx, `SELECT id, key, walkover, sides FROM tournament_pairings WHERE tournament_id = ?`, tournamentID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r PairingResult
		var key string
		var w, sides sql.NullString
		if err := rows.Scan(&r.ID, &key, &w, &sides); err != nil {
			rows.Close()
			return nil, err
		}
		r.Walkover, r.Sides = w.String, sides.String
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

// GamePace measures this table's games for the tournament duration estimate (spec 09 §4): play
// seconds per goal and the loser's goals per winner goal, over the last 200 finished games that
// count and lasted 1–30 minutes (shorter ones were entered by hand or simulated, longer ones
// were paused). n is how many games it used.
func (s *Store) GamePace(ctx context.Context) (secondsPerGoal, loserShare float64, n int, err error) {
	var ms, goals, winner, loser sql.NullFloat64
	err = s.r.QueryRowContext(ctx, `SELECT COUNT(*), SUM(ended_at - started_at), SUM(home_goals + away_goals),
			SUM(max(home_goals, away_goals)), SUM(min(home_goals, away_goals))
		FROM (SELECT * FROM games INDEXED BY games_finished_order
			WHERE state = 'finished' AND NOT voided AND winner IS NOT NULL
				AND ended_at - started_at BETWEEN 60000 AND 1800000
			ORDER BY ended_at DESC, id DESC LIMIT 200)`).Scan(&n, &ms, &goals, &winner, &loser)
	if err != nil || n == 0 || goals.Float64 == 0 || winner.Float64 == 0 {
		return 0, 0, n, err
	}
	return ms.Float64 / 1000 / goals.Float64, loser.Float64 / winner.Float64, n, nil
}

// PlayerTournament is a tournament a player took part in, with the placements from its
// `finished` event (nil while it runs, or abandoned).
type PlayerTournament struct {
	ID         int64
	Name       string
	State      string
	EndedAt    *time.Time
	Placements json.RawMessage
	Replaced   bool // somebody took the player's place (spec 09 §10)
	Dropped    bool // the player's team dropped out
}

// PlayerTournaments lists the started tournaments a player took part in, newest first.
func (s *Store) PlayerTournaments(ctx context.Context, playerID int64) ([]PlayerTournament, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT t.id, t.name, t.state, t.ended_at,
			(SELECT e.details FROM tournament_events e WHERE e.tournament_id = t.id AND e.kind = 'finished'
			 ORDER BY e.id DESC LIMIT 1),
			(SELECT MIN(p.replaced_by IS NOT NULL) FROM tournament_participants p WHERE p.tournament_id = t.id AND p.player_id = ?1),
			(SELECT MIN(p.dropped_at IS NOT NULL) FROM tournament_participants p WHERE p.tournament_id = t.id AND p.player_id = ?1)
		FROM tournaments t
		WHERE t.state <> 'setup' AND EXISTS (SELECT 1 FROM tournament_participants p
			WHERE p.tournament_id = t.id AND p.player_id = ?1)
		ORDER BY t.started_at DESC, t.id DESC`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlayerTournament{}
	for rows.Next() {
		var pt PlayerTournament
		var ended sql.NullInt64
		var details sql.NullString
		if err := rows.Scan(&pt.ID, &pt.Name, &pt.State, &ended, &details, &pt.Replaced, &pt.Dropped); err != nil {
			return nil, err
		}
		if ended.Valid {
			pt.EndedAt = Ptr(time.UnixMilli(ended.Int64))
		}
		if details.Valid && pt.State == "finished" {
			pt.Placements = json.RawMessage(details.String)
		}
		out = append(out, pt)
	}
	return out, rows.Err()
}

// FinishTournament ends a running tournament with its placements, freezing the pairing results
// it ended with (spec 09 §11).
func (s *Store) FinishTournament(ctx context.Context, id int64, placements any, frozen map[string]PairingResult) error {
	fz, err := json.Marshal(frozen)
	if err != nil {
		return err
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		now := time.Now()
		res, err := tx.ExecContext(ctx, `UPDATE tournaments SET state = 'finished', ended_at = ?, frozen = ?
			WHERE id = ? AND state = 'running'`, now.UnixMilli(), string(fz), id)
		if err != nil {
			return err
		}
		if err := mustChangeOne(ctx, tx, res, id); err != nil {
			return err
		}
		return addTournamentEvent(ctx, tx, id, now, nil, "finished", placements)
	})
}

// FrozenResults returns the pairing results a finished tournament was frozen with (nil if none).
func (t Tournament) FrozenResults() (map[string]PairingResult, error) {
	if len(t.Frozen) == 0 {
		return nil, nil
	}
	out := map[string]PairingResult{}
	return out, json.Unmarshal(t.Frozen, &out)
}

// ChangeTournament changes a started tournament's name and settings (rules of later stages, the
// dropouts) and writes the event – not while in setup or once it has ended.
func (s *Store) ChangeTournament(ctx context.Context, id int64, name string, settings json.RawMessage, author *int64,
	kind string, details any) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE tournaments SET name = ?, settings = ? WHERE id = ? AND state IN ('running', 'paused')`,
			name, settingsText(settings), id)
		if err != nil {
			return err
		}
		if err := mustChangeOne(ctx, tx, res, id); err != nil {
			return err
		}
		return addTournamentEvent(ctx, tx, id, time.Now(), author, kind, details)
	})
}

// DropOut stores a tournament's settings with a new dropout and marks the team's players.
func (s *Store) DropOut(ctx context.Context, id int64, settings json.RawMessage, participants []int64, author *int64, details any) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		now := time.Now()
		res, err := tx.ExecContext(ctx, `UPDATE tournaments SET settings = ? WHERE id = ? AND state IN ('running', 'paused')`,
			settingsText(settings), id)
		if err != nil {
			return err
		}
		if err := mustChangeOne(ctx, tx, res, id); err != nil {
			return err
		}
		for _, p := range participants {
			if _, err := tx.ExecContext(ctx, `UPDATE tournament_participants SET dropped_at = ? WHERE id = ? AND tournament_id = ?`,
				now.UnixMilli(), p, id); err != nil {
				return err
			}
		}
		return addTournamentEvent(ctx, tx, id, now, author, "dropout", details)
	})
}

// ReplaceParticipant puts another player in a participant's place (same team, same seed) from
// the next game on; the old row stays, pointing at the new one.
func (s *Store) ReplaceParticipant(ctx context.Context, id, participant, player int64, author *int64, details any) (int64, error) {
	var newID int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM tournaments WHERE id = ?`, id).Scan(&state); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if state != "running" && state != "paused" {
			return ErrTournamentState
		}
		var team sql.NullInt64
		var seed int
		err := tx.QueryRowContext(ctx, `SELECT team_no, seed FROM tournament_participants
			WHERE id = ? AND tournament_id = ? AND replaced_by IS NULL`, participant, id).Scan(&team, &seed)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO tournament_participants (tournament_id, player_id, team_no, seed)
			VALUES (?, ?, ?, ?) RETURNING id`, id, player, intPtr(team), seed).Scan(&newID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tournament_participants SET replaced_by = ? WHERE id = ?`, newID, participant); err != nil {
			return err
		}
		return addTournamentEvent(ctx, tx, id, time.Now(), author, "player_replaced", details)
	})
	return newID, err
}

// UnlinkPairings takes the games of pairings out of a tournament (spec 09 §11 rebuild): they stay
// as normal games; the pairings are played again.
func (s *Store) UnlinkPairings(ctx context.Context, id int64, keys []string, author *int64, details any) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		for _, k := range keys {
			if _, err := tx.ExecContext(ctx, `UPDATE games SET pairing_id = NULL, pairing_a_side = NULL
				WHERE pairing_id = (SELECT id FROM tournament_pairings WHERE tournament_id = ? AND key = ?)`, id, k); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM tournament_pairings WHERE tournament_id = ? AND key = ?`, id, k); err != nil {
				return err
			}
		}
		return addTournamentEvent(ctx, tx, id, time.Now(), author, "rebuilt", details)
	})
}
