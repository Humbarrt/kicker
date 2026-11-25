package engine

import (
	"errors"
	"testing"
	"time"
)

// finishedGame plays 7 against 3, home wins 5:2 (official rules), and returns its event log.
func finishedGame(t *testing.T) (Game, []Event) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.GuestID = 1
	s := NewState(cfg, t0)
	var log []Event
	var header Game
	step := func(in Input, at time.Time) {
		var r Result
		var err error
		s, r, err = Step(s, in, at)
		if err != nil {
			t.Fatalf("%T: %v", in, err)
		}
		for _, w := range r.Writes {
			if w.Create {
				header = w.Game
				header.ID = 1
			}
			log = append(log, w.Events...)
		}
		if s.Game != nil && s.Game.ID == 0 {
			s.Game.ID = 1
		}
	}
	step(RegisterPlayer{Side: Home, PlayerID: 7}, t0)
	step(RegisterPlayer{Side: Away, PlayerID: 3}, t0)
	step(StartGame{Rules: presets["official"]}, t0)
	at := t0
	for _, side := range []Side{Home, Away, Home, Home, Away, Home, Home} {
		at = at.Add(10 * time.Second)
		step(AdjustScore{Side: side, Delta: 1}, at)
	}
	if s.Game.Status != StatusFinished || s.Game.Home != 5 || s.Game.Away != 2 {
		t.Fatalf("setup: %+v", s.Game)
	}
	return header, log
}

func edit(t *testing.T, header Game, log []Event, ed Edit) ([]Event, *Game, error) {
	t.Helper()
	g := Replay(header, log)
	e, err := MakeEdit(g, log, ed, 1, t0.Add(time.Hour))
	if err != nil {
		return log, g, err
	}
	log = append(append([]Event{}, log...), e)
	return log, Replay(header, log), nil
}

func mustEdit(t *testing.T, header Game, log []Event, ed Edit) ([]Event, *Game) {
	t.Helper()
	log, g, err := edit(t, header, log, ed)
	if err != nil {
		t.Fatalf("%+v: %v", ed, err)
	}
	return log, g
}

func TestEditGoalsAndWinner(t *testing.T) {
	header, log := finishedGame(t)
	g := Replay(header, log)
	// Void two home goals and add three away goals: the result turns around.
	var homeGoals []int
	for _, gl := range g.Goals {
		if gl.Side == Home {
			homeGoals = append(homeGoals, gl.Seq)
		}
	}
	log, g = mustEdit(t, header, log, Edit{Kind: EditVoidGoal, TargetSeq: homeGoals[0], Author: 2})
	log, g = mustEdit(t, header, log, Edit{Kind: EditVoidGoal, TargetSeq: homeGoals[1], Author: 2})
	if g.Home != 3 || g.Winner != Home || g.Edits != 2 {
		t.Errorf("after voiding: %d:%d %q %d", g.Home, g.Away, g.Winner, g.Edits)
	}
	if _, _, err := edit(t, header, log, Edit{Kind: EditVoidGoal, TargetSeq: homeGoals[0]}); !errors.Is(err, ErrBadEdit) {
		t.Errorf("void twice: %v", err)
	}
	pt := 15 * time.Second
	log, g = mustEdit(t, header, log, Edit{Kind: EditAddGoal, Side: Away, PlayTime: &pt})
	log, g = mustEdit(t, header, log, Edit{Kind: EditAddGoal, Side: Away})
	if g.Home != 3 || g.Away != 4 || g.Winner != Away || g.Status != StatusFinished {
		t.Errorf("after adding: %d:%d %q", g.Home, g.Away, g.Winner)
	}
	last := g.Goals[len(g.Goals)-1]
	if last.PlayTime != g.PlayTime(g.EndedAt) || last.Source != SourceManual {
		t.Errorf("added goal at the end: %+v", last)
	}
	tooLate := time.Hour
	if _, _, err := edit(t, header, log, Edit{Kind: EditAddGoal, Side: Home, PlayTime: &tooLate}); !errors.Is(err, ErrBadEdit) {
		t.Errorf("goal after the end: %v", err)
	}
	// A draw has no winner.
	_, g = mustEdit(t, header, log, Edit{Kind: EditVoidGoal, TargetSeq: last.Seq})
	if g.Home != 3 || g.Away != 3 || g.Winner != "" {
		t.Errorf("draw: %d:%d %q", g.Home, g.Away, g.Winner)
	}
}

func TestEditPlayersAndSides(t *testing.T) {
	header, log := finishedGame(t)
	log, g := mustEdit(t, header, log, Edit{Kind: EditSetPlayer, Side: Home, Slot: 2, PlayerID: 9})
	if g.Roster.Home[1].PlayerID != 9 || g.Roster.Home[1].Via != ViaManual {
		t.Errorf("added: %+v", g.Roster)
	}
	log, g = mustEdit(t, header, log, Edit{Kind: EditSetPlayer, Side: Away, Slot: 1, PlayerID: 4}) // 4 instead of 3
	if g.Roster.Away[0].PlayerID != 4 || g.Plays(3) {
		t.Errorf("replaced: %+v", g.Roster)
	}
	if _, _, err := edit(t, header, log, Edit{Kind: EditSetPlayer, Side: Away, Slot: 2, PlayerID: 7}); !errors.Is(err, ErrBadEdit) {
		t.Errorf("a player can't play twice: %v", err)
	}
	log, _ = mustEdit(t, header, log, Edit{Kind: EditSetPlayer, Side: Away, Slot: 2, PlayerID: 1})
	log, _ = mustEdit(t, header, log, Edit{Kind: EditSetPlayer, Side: Home, Slot: 2, PlayerID: 1}) // the guest may
	log, g = mustEdit(t, header, log, Edit{Kind: EditRemovePlayer, Side: Home, Slot: 2})
	if g.Roster.Home[1].Taken() {
		t.Errorf("removed: %+v", g.Roster)
	}
	if _, _, err := edit(t, header, log, Edit{Kind: EditRemovePlayer, Side: Home, Slot: 2}); !errors.Is(err, ErrBadEdit) {
		t.Errorf("remove nobody: %v", err)
	}
	_, g = mustEdit(t, header, log, Edit{Kind: EditSwapSides})
	if g.Home != 2 || g.Away != 5 || g.Winner != Away || g.Roster.Away[0].PlayerID != 7 || g.Roster.Home[0].PlayerID != 4 ||
		g.Goals[0].Side != Away {
		t.Errorf("swapped: %d:%d %q %+v", g.Home, g.Away, g.Winner, g.Roster)
	}
}

func TestEditVoidAndRevert(t *testing.T) {
	header, log := finishedGame(t)
	log, g := mustEdit(t, header, log, Edit{Kind: EditVoidGame})
	voidSeq := g.LastSeq
	if !g.Voided {
		t.Fatal("voided")
	}
	if _, _, err := edit(t, header, log, Edit{Kind: EditVoidGame}); !errors.Is(err, ErrBadEdit) {
		t.Errorf("void twice: %v", err)
	}
	log, g = mustEdit(t, header, log, Edit{Kind: EditRevert, TargetSeq: voidSeq})
	revertSeq := g.LastSeq
	if g.Voided || g.Edits != 0 {
		t.Errorf("reverted: %+v", g)
	}
	if _, _, err := edit(t, header, log, Edit{Kind: EditRevert, TargetSeq: voidSeq}); !errors.Is(err, ErrBadEdit) {
		t.Errorf("revert twice: %v", err)
	}
	_, g = mustEdit(t, header, log, Edit{Kind: EditRevert, TargetSeq: revertSeq}) // revert the revert
	if !g.Voided {
		t.Error("the void counts again")
	}
	if _, _, err := edit(t, header, log, Edit{Kind: EditRevert, TargetSeq: 2}); !errors.Is(err, ErrBadEdit) {
		t.Errorf("only edits can be reverted, not the game's own events: %v", err)
	}
}

func TestEditOnlyFinished(t *testing.T) {
	g := &Game{Status: StatusActive}
	if _, err := MakeEdit(g, nil, Edit{Kind: EditSwapSides}, 1, t0); !errors.Is(err, ErrNotFinished) {
		t.Errorf("running game: %v", err)
	}
	header, log := finishedGame(t)
	if _, _, err := edit(t, header, log, Edit{Kind: "rename"}); !errors.Is(err, ErrBadEdit) {
		t.Errorf("unknown kind: %v", err)
	}
}
