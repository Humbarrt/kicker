package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Game history and the feed of edits (M3 task 7).

// GameFilter selects games for the history list. Zero values don't filter.
type GameFilter struct {
	BeforeID  int64   // only games with a smaller id (paging); 0 = from the newest
	PlayerIDs []int64 // only games all of these players played in
	// Relation of the first two players: "together" (same side) or "against"; "" = either.
	Relation string
	// Result for the first player: win | loss | draw. Without a player only "draw" filters.
	Result string
	Kind   string // solo (1 against 1) | team (everything else)
	Status string // finished | abandoned; "" = both
	// Only games that are all of these: voided, edited, unassigned (an unknown coin played),
	// unknown (someone unknown played: the guest, an unknown coin, or a side nobody registered
	// for), shutout (the loser scored nothing), close (won by one goal).
	Flags    []string
	From, To time.Time // started in [From, To); zero = open
	Limit    int
}

// GameFlags are the valid GameFilter.Flags.
var GameFlags = []string{"voided", "edited", "unassigned", "unknown", "shutout", "close"}

// ListGames returns finished and abandoned games, newest first, with their participants.
func (s *Store) ListGames(ctx context.Context, f GameFilter) ([]Game, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 30
	}
	where := []string{`state IN ('finished', 'abandoned')`}
	var args []any
	add := func(cond string, a ...any) {
		where = append(where, cond)
		args = append(args, a...)
	}
	if f.BeforeID > 0 {
		add(`id < ?`, f.BeforeID)
	}
	for _, p := range f.PlayerIDs {
		add(`id IN (SELECT game_id FROM game_participants WHERE player_id = ?)`, p)
	}
	if len(f.PlayerIDs) >= 2 && f.Relation != "" {
		op := "="
		if f.Relation == "against" {
			op = "!="
		}
		add(`EXISTS (SELECT 1 FROM game_participants a JOIN game_participants b ON b.game_id = a.game_id AND b.side `+op+` a.side
			WHERE a.game_id = games.id AND a.player_id = ? AND b.player_id = ?)`, f.PlayerIDs[0], f.PlayerIDs[1])
	}
	side := `(SELECT side FROM game_participants WHERE game_id = games.id AND player_id = ? LIMIT 1)`
	switch {
	case f.Result == "draw":
		add(`state = 'finished' AND winner IS NULL`)
	case f.Result == "win" && len(f.PlayerIDs) > 0:
		add(`state = 'finished' AND winner = `+side, f.PlayerIDs[0])
	case f.Result == "loss" && len(f.PlayerIDs) > 0:
		add(`state = 'finished' AND winner != `+side, f.PlayerIDs[0])
	}
	solo := `(SELECT SUM(side = 'home') = 1 AND SUM(side = 'away') = 1 FROM game_participants WHERE game_id = games.id)`
	switch f.Kind {
	case "solo":
		add(solo)
	case "team":
		add(`NOT ` + solo)
	}
	if f.Status == "finished" || f.Status == "abandoned" {
		add(`state = ?`, f.Status)
	}
	for _, flag := range f.Flags {
		switch flag {
		case "voided":
			add(`voided`)
		case "edited":
			add(`edits > 0`)
		case "unassigned":
			add(`EXISTS (SELECT 1 FROM game_participants WHERE game_id = games.id AND player_id IS NULL)`)
		case "unknown":
			add(`(EXISTS (SELECT 1 FROM game_participants gp LEFT JOIN players pl ON pl.id = gp.player_id
					WHERE gp.game_id = games.id AND (gp.player_id IS NULL OR pl.is_guest))
				OR NOT EXISTS (SELECT 1 FROM game_participants WHERE game_id = games.id AND side = 'home')
				OR NOT EXISTS (SELECT 1 FROM game_participants WHERE game_id = games.id AND side = 'away'))`)
		case "shutout":
			add(`state = 'finished' AND winner IS NOT NULL AND min(home_goals, away_goals) = 0`)
		case "close":
			add(`state = 'finished' AND abs(home_goals - away_goals) = 1`)
		}
	}
	if !f.From.IsZero() {
		add(`started_at >= ?`, f.From.UnixMilli())
	}
	if !f.To.IsZero() {
		add(`started_at < ?`, f.To.UnixMilli())
	}
	args = append(args, f.Limit)
	rows, err := s.r.QueryContext(ctx, `SELECT `+gameCols+` FROM games WHERE `+strings.Join(where, " AND ")+
		` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	var out []Game
	byID := map[int64]int{}
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		g.Participants = []Participant{}
		byID[g.ID] = len(out)
		out = append(out, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	ids := make([]any, 0, len(out))
	marks := make([]string, 0, len(out))
	for _, g := range out {
		ids = append(ids, g.ID)
		marks = append(marks, "?")
	}
	prow, err := s.r.QueryContext(ctx, `SELECT game_id, side, slot, player_id, coin_uid FROM game_participants
		WHERE game_id IN (`+strings.Join(marks, ",")+`) ORDER BY game_id, side = 'away', slot`, ids...)
	if err != nil {
		return nil, err
	}
	defer prow.Close()
	for prow.Next() {
		var id int64
		var p Participant
		var player sql.NullInt64
		var coin sql.NullString
		if err := prow.Scan(&id, &p.Side, &p.Slot, &player, &coin); err != nil {
			return nil, err
		}
		p.PlayerID, p.CoinUID = int64Ptr(player), strPtr(coin)
		g := &out[byID[id]]
		g.Participants = append(g.Participants, p)
	}
	return out, prow.Err()
}

// EditEntry is one edit in the feed of recent edits.
type EditEntry struct {
	GameID          int64
	Seq             int
	Type            string
	Side            *string
	Slot            *int
	PlayerID        *int64
	TargetSeq       *int
	AuthorAccountID *int64
	Reason          *string
	CreatedAt       time.Time
}

// RecentEdits returns the newest edits (events with source manual) of all games.
func (s *Store) RecentEdits(ctx context.Context, limit int) ([]EditEntry, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT game_id, seq, type, side, slot, player_id, target_seq, author_account_id,
		reason, created_at FROM game_events WHERE source = 'manual' ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EditEntry{}
	for rows.Next() {
		var e EditEntry
		var side, reason sql.NullString
		var slot, player, target, author sql.NullInt64
		var at int64
		if err := rows.Scan(&e.GameID, &e.Seq, &e.Type, &side, &slot, &player, &target, &author, &reason, &at); err != nil {
			return nil, err
		}
		e.Side, e.Slot, e.PlayerID, e.TargetSeq = strPtr(side), intPtr(slot), int64Ptr(player), intPtr(target)
		e.AuthorAccountID, e.Reason, e.CreatedAt = int64Ptr(author), strPtr(reason), time.UnixMilli(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// AccountNames returns the usernames of accounts (deleted accounts are missing).
func (s *Store) AccountNames(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	marks := make([]string, len(ids))
	for i, id := range ids {
		args[i], marks[i] = id, "?"
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id, username FROM accounts WHERE id IN (`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}
