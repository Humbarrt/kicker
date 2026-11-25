package table

import (
	"context"
	"errors"
	"fmt"

	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
)

// Game history and editing finished games (D2, M3 task 7).

// EventView is a line of a game's event log.
type EventView struct {
	Seq       int     `json:"seq"`
	Type      string  `json:"type"`
	Side      *string `json:"side"`
	Slot      *int    `json:"slot"`
	PlayerID  *int64  `json:"player_id"`
	CoinUID   *string `json:"coin_uid"`
	Via       *string `json:"via"`
	TargetSeq *int    `json:"target_seq"`
	Rule      *string `json:"rule"`
	RuleValue *string `json:"rule_value"`
	Source    string  `json:"source"`
	AuthorID  *int64  `json:"author_id"`
	Author    *string `json:"author"` // username; null for the system or a deleted account
	Reason    *string `json:"reason"`
	At        int64   `json:"at"`
	PlayTime  *int64  `json:"play_time_ms"`
	// Reverted: taken back by a `reverted` event in force. Edit: a correction after the game.
	Reverted bool `json:"reverted"`
	Edit     bool `json:"edit"`
}

// GameDetail is a game with its whole event log.
type GameDetail struct {
	Game   GameView    `json:"game"`
	Events []EventView `json:"events"`
}

// GameDetail rebuilds a game and returns it with its event log.
func (t *Table) GameDetail(ctx context.Context, id int64) (GameDetail, error) {
	row, events, err := t.opts.Store.Game(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return GameDetail{}, ErrNoSuchGame
	} else if err != nil {
		return GameDetail{}, err
	}
	evs := eventsFromStore(events)
	g := engine.Replay(headerFromStore(row), evs)
	reverted := engine.Reverted(evs)
	var authors []int64
	for _, e := range events {
		if e.AuthorAccountID != nil {
			authors = append(authors, *e.AuthorAccountID)
		}
	}
	names, err := t.opts.Store.AccountNames(ctx, authors)
	if err != nil {
		return GameDetail{}, err
	}
	out := GameDetail{Game: *gameView(g, false, t.opts.Now()), Events: make([]EventView, len(events))}
	finished := false
	for i, e := range events {
		v := EventView{Seq: e.Seq, Type: e.Type, Side: e.Side, Slot: e.Slot, PlayerID: e.PlayerID, CoinUID: e.CoinUID, Via: e.Via,
			TargetSeq: e.TargetSeq, Rule: e.Rule, RuleValue: e.RuleValue, Source: e.Source, AuthorID: e.AuthorAccountID,
			Reason: e.Reason, At: e.CreatedAt.UnixMilli(), PlayTime: e.PlayTimeMs, Reverted: reverted[e.Seq],
			Edit: finished && e.Source == string(engine.SourceManual)}
		if e.AuthorAccountID != nil {
			if n, ok := names[*e.AuthorAccountID]; ok {
				v.Author = &n
			}
		}
		if e.Type == string(engine.EvFinished) {
			finished = true
		}
		out.Events[i] = v
	}
	return out, nil
}

// EditGame applies an edit to a finished game: one correction event, the game's projections,
// the scoreboard if it still shows the game, and the ratings from this game on (OnGameFinished).
// The caller checks the permissions.
func (t *Table) EditGame(ctx context.Context, id int64, ed engine.Edit) (*engine.Game, error) {
	t.editMu.Lock() // one edit at a time: the next seq is computed here
	defer t.editMu.Unlock()
	st := t.opts.Store
	row, events, err := st.Game(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNoSuchGame
	} else if err != nil {
		return nil, err
	}
	guest, err := st.GuestPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if ed.Kind == engine.EditSetPlayer {
		// Archived players may be put into past games: they did play then.
		if _, err := st.Player(ctx, ed.PlayerID); errors.Is(err, store.ErrNotFound) {
			return nil, ErrInvalidPlayer
		} else if err != nil {
			return nil, err
		}
	}
	evs := eventsFromStore(events)
	header := headerFromStore(row)
	g := engine.Replay(header, evs)
	e, err := engine.MakeEdit(g, evs, ed, guest.ID, t.opts.Now())
	if err != nil {
		return nil, err
	}
	edited := engine.Replay(header, append(evs, e))
	if err := t.Do(ctx, engine.ReplaceGame{Game: edited, Events: []engine.Event{e}}); err != nil {
		return nil, fmt.Errorf("saving the edit: %w", err)
	}
	t.log.Info("game edited", "game", id, "edit", ed.Kind, "seq", e.Seq, "author", ed.Author)
	if t.opts.OnGameFinished != nil {
		t.opts.OnGameFinished(id) // the result may have changed: ratings from this game on
	}
	return edited, nil
}
