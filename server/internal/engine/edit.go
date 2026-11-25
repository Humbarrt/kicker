package engine

import (
	"errors"
	"fmt"
	"time"
)

// Editing finished games (D2): every edit is one correction event appended to the game's log,
// with the author and the time. Nothing is changed or deleted; a `reverted`
// event takes an edit back. The caller checks permissions, then appends the event and replays
// the game (Replay) and the ratings.

// EditKind is what an edit does.
type EditKind string

const (
	EditAddGoal      EditKind = "add_goal"      // a goal for Side (at PlayTime, default: the end)
	EditVoidGoal     EditKind = "void_goal"     // the goal TargetSeq doesn't count
	EditSetPlayer    EditKind = "set_player"    // PlayerID sits in Side/Slot (replaces whoever sat there)
	EditRemovePlayer EditKind = "remove_player" // nobody sat in Side/Slot
	EditSwapSides    EditKind = "swap_sides"    // home and away were the other way round
	EditVoidGame     EditKind = "void_game"     // the game counts for nothing (games.void)
	EditRevert       EditKind = "revert"        // take back the edit TargetSeq (games.revert)
)

// Edit is a correction of a finished game.
type Edit struct {
	Kind      EditKind
	Side      Side
	Slot      int
	PlayerID  int64
	TargetSeq int
	PlayTime  *time.Duration
	Author    int64
}

// Errors of edits.
var (
	ErrNotFinished = errors.New("only finished games can be edited")
	ErrBadEdit     = fmt.Errorf("%w: edit", ErrInvalidInput)
)

// MakeEdit checks an edit against the game (rebuilt from events) and returns its event. guestID is
// the guest player, who may sit in several slots.
func MakeEdit(g *Game, events []Event, ed Edit, guestID int64, now time.Time) (Event, error) {
	if g.Status != StatusFinished {
		return Event{}, ErrNotFinished
	}
	e := Event{Seq: g.LastSeq + 1, Source: SourceManual, Author: ed.Author, At: now}
	slotOK := ed.Side.Valid() && ed.Slot >= 1 && ed.Slot <= Slots
	switch ed.Kind {
	case EditAddGoal:
		if !ed.Side.Valid() {
			return Event{}, fmt.Errorf("%w: side", ErrBadEdit)
		}
		pt := g.PlayTime(g.EndedAt)
		if ed.PlayTime != nil {
			if *ed.PlayTime < 0 || *ed.PlayTime > pt {
				return Event{}, fmt.Errorf("%w: the goal's time is outside the game", ErrBadEdit)
			}
			pt = *ed.PlayTime
		}
		e.Type, e.Side, e.PlayTime = EvGoal, ed.Side, &pt
	case EditVoidGoal:
		i := g.goalIndex(ed.TargetSeq)
		if i < 0 || g.Goals[i].Voided {
			return Event{}, fmt.Errorf("%w: no such goal, or it doesn't count already", ErrBadEdit)
		}
		e.Type, e.TargetSeq = EvGoalVoided, ed.TargetSeq
	case EditSetPlayer:
		if !slotOK || ed.PlayerID <= 0 {
			return Event{}, fmt.Errorf("%w: slot or player", ErrBadEdit)
		}
		if seat := g.Roster.Side(ed.Side)[ed.Slot-1]; seat.PlayerID == ed.PlayerID {
			return Event{}, fmt.Errorf("%w: the player sits there already", ErrBadEdit)
		}
		if ed.PlayerID != guestID && g.Plays(ed.PlayerID) {
			return Event{}, fmt.Errorf("%w: the player is in this game already", ErrBadEdit)
		}
		e.Type, e.Side, e.Slot, e.PlayerID, e.Via = EvPlayerJoined, ed.Side, ed.Slot, ed.PlayerID, ViaManual
	case EditRemovePlayer:
		if !slotOK || !g.Roster.Side(ed.Side)[ed.Slot-1].Taken() {
			return Event{}, fmt.Errorf("%w: nobody sits there", ErrBadEdit)
		}
		seat := g.Roster.Side(ed.Side)[ed.Slot-1]
		e.Type, e.Side, e.Slot, e.PlayerID, e.CoinUID = EvPlayerLeft, ed.Side, ed.Slot, seat.PlayerID, seat.CoinUID
	case EditSwapSides:
		e.Type = EvSidesSwapped
	case EditVoidGame:
		if g.Voided {
			return Event{}, fmt.Errorf("%w: the game is void already", ErrBadEdit)
		}
		e.Type = EvGameVoided
	case EditRevert:
		target, ok := findEvent(events, ed.TargetSeq)
		reverted := Reverted(events)
		switch {
		case !ok || target.Source != SourceManual || !(isEditType(target.Type) || target.Type == EvReverted):
			return Event{}, fmt.Errorf("%w: only edits can be reverted", ErrBadEdit)
		case reverted[target.Seq]:
			return Event{}, fmt.Errorf("%w: reverted already", ErrBadEdit)
		}
		e.Type, e.TargetSeq = EvReverted, ed.TargetSeq
	default:
		return Event{}, fmt.Errorf("%w: kind %q", ErrBadEdit, ed.Kind)
	}
	return e, nil
}

func (g *Game) goalIndex(seq int) int {
	for i, gl := range g.Goals {
		if gl.Seq == seq {
			return i
		}
	}
	return -1
}

func findEvent(events []Event, seq int) (Event, bool) {
	for _, e := range events {
		if e.Seq == seq {
			return e, true
		}
	}
	return Event{}, false
}

// ReplaceGame puts an edited game into the table state (the scoreboard may still show it) and
// writes its new events. Game is the game rebuilt with Events (Replay).
type ReplaceGame struct {
	Game   *Game
	Events []Event
}

func (ReplaceGame) isInput() {}

func (s *State) replaceGame(in ReplaceGame, r *Result) error {
	g := in.Game
	if g == nil || g.ID == 0 || len(in.Events) == 0 {
		return ErrInvalidInput
	}
	if s.Game != nil && s.Game.ID == g.ID {
		if s.Game.Status != StatusFinished {
			return ErrNotFinished
		}
		s.flush(r)
		s.Game = g.clone()
	}
	if s.Last != nil && s.Last.ID == g.ID {
		s.Last = g.clone()
	}
	r.Writes = append(r.Writes, Write{Game: *g.clone(), Events: in.Events})
	r.Changed = true
	return nil
}
