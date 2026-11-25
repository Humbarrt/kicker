package engine

import (
	"errors"
	"math/rand/v2"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)

func mustStep(t *testing.T, s State, in Input, now time.Time) (State, Result) {
	t.Helper()
	n, r, err := Step(s, in, now)
	if err != nil {
		t.Fatal(err)
	}
	return n, r
}

func TestStepDoesNotModifyItsInput(t *testing.T) {
	s := NewState(DefaultConfig(), t0)
	s, _ = mustStep(t, s, StartGame{Rules: presets["official"]}, t0)
	s, _ = mustStep(t, s, SensorGoal{Role: "home_goal"}, t0.Add(time.Second))
	before := *s.Game
	goals := len(s.Game.Goals)

	n, _ := mustStep(t, s, SensorGoal{Role: "home_goal"}, t0.Add(2*time.Second))
	n, _ = mustStep(t, n, UndoGoal{}, t0.Add(3*time.Second))
	if s.Game.Away != before.Away || s.Game.LastSeq != before.LastSeq || len(s.Game.Goals) != goals || s.Game.Goals[0].Voided {
		t.Errorf("old state changed: %+v", s.Game)
	}
	if n.Game.Away != 1 || n.Game.LastSeq != 4 {
		t.Errorf("new state: %+v", n.Game)
	}

	// an error returns the given state untouched
	idle := NewState(DefaultConfig(), t0)
	got, r, err := Step(idle, UndoGoal{}, t0)
	if !errors.Is(err, ErrNoGame) || got.Phase != PhaseIdle || len(r.Writes) != 0 {
		t.Errorf("%v %+v %+v", err, got, r)
	}
}

func TestInvalidInput(t *testing.T) {
	s := NewState(DefaultConfig(), t0)
	s, _ = mustStep(t, s, StartGame{Rules: presets["long"]}, t0)
	for name, in := range map[string]Input{
		"bad side":       AdjustScore{Side: "middle", Delta: 1},
		"delta 2":        AdjustScore{Side: Home, Delta: 2},
		"bad rules":      StartGame{Rules: Rules{WinScore: 0, WinBy: 1, TimeTie: TimeTieDraw}},
		"bad first ball": StartGame{Rules: presets["long"], FirstBall: "left"},
		"unknown input":  nil,
		"max below win":  StartGame{Rules: Rules{WinScore: 10, WinBy: 2, MaxScore: 5, TimeTie: TimeTieDraw}},
		"half seconds":   StartGame{Rules: Rules{WinScore: 10, WinBy: 2, TimeLimit: 1500 * time.Millisecond, TimeTie: TimeTieDraw}},
		"unknown tie":    StartGame{Rules: Rules{WinScore: 10, WinBy: 2}},
	} {
		if _, _, err := Step(s, in, t0); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestEventsCarryPlayTimeAndAuthor(t *testing.T) {
	s := NewState(DefaultConfig(), t0)
	s, r := mustStep(t, s, StartGame{Rules: presets["long"], Author: 7}, t0)
	w := r.Writes[0]
	if !w.Create || w.Events[0].Author != 7 || w.Events[0].Source != SourceTable || *w.Events[0].PlayTime != 0 {
		t.Errorf("start: %+v", w)
	}
	s.Game.ID = 1
	_, r = mustStep(t, s, SensorGoal{Role: "away_goal", At: t0.Add(1500 * time.Millisecond)}, t0.Add(2*time.Second))
	e := r.Writes[0].Events[0]
	if r.Writes[0].Create || r.Writes[0].Game.ID != 1 || e.Seq != 2 || e.Side != Home || e.Source != SourceSensor ||
		*e.PlayTime != 1500*time.Millisecond || !e.At.Equal(t0.Add(1500*time.Millisecond)) {
		t.Errorf("goal: %+v %+v", r.Writes[0], e)
	}
}

func TestNextDeadline(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PauseAfter, cfg.SleepAfter = 0, 0 // inactivity deadlines: 13-lifecycle
	s := NewState(cfg, t0)
	if !s.NextDeadline().IsZero() {
		t.Error("idle has no deadline")
	}
	s, _ = mustStep(t, s, StartGame{Rules: presets["long"]}, t0)
	if !s.NextDeadline().IsZero() {
		t.Error("untimed game has no deadline")
	}
	s, _ = mustStep(t, s, StartGame{Rules: presets["timed"]}, t0.Add(time.Minute)) // parks the first game
	s, _ = mustStep(t, s, StartGame{Rules: presets["timed"]}, t0.Add(time.Minute))
	s, _ = mustStep(t, s, SensorGoal{Role: "home_goal"}, t0.Add(2*time.Minute))
	if got := s.NextDeadline(); !got.Equal(t0.Add(6 * time.Minute)) {
		t.Errorf("timed: %v", got)
	}
	s, _ = mustStep(t, s, Tick{}, t0.Add(6*time.Minute))
	if s.Phase != PhaseFinished || !s.NextDeadline().Equal(t0.Add(6*time.Minute+10*time.Second)) {
		t.Errorf("finished hold: %s %v", s.Phase, s.NextDeadline())
	}
}

// Whatever happens live, replaying the persisted events gives the same game. This is what makes
// restart recovery trustworthy.
func TestReplayMatchesLiveState(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	ruleSets := []Rules{presets["long"], presets["official"], presets["timed"],
		{WinScore: 3, WinBy: 1, TimeLimit: 20 * time.Second, TimeTie: TimeTieDraw}}
	for run := 0; run < 300; run++ {
		cfg := DefaultConfig()
		cfg.Dispenser = rng.IntN(2) == 0
		cfg.AfterGoalBall = []BallRule{BallConceding, BallScoring, BallAlternate}[rng.IntN(3)]
		now := t0
		s := NewState(cfg, now)
		var log []Event
		var header Game
		record := func(r Result) {
			for _, w := range r.Writes {
				log = append(log, w.Events...)
				header = w.Game
			}
		}
		s, r0 := mustStep(t, s, StartGame{Rules: ruleSets[rng.IntN(len(ruleSets))], FirstBall: Away}, now)
		s.Game.ID = 1
		record(r0)

		for i := 0; i < 60 && s.Phase == PhasePlaying; i++ {
			now = now.Add(time.Duration(rng.IntN(3000)) * time.Millisecond)
			var in Input
			switch rng.IntN(6) {
			case 0, 1:
				in = SensorGoal{Role: "home_goal", At: now.Add(-time.Duration(rng.IntN(500)) * time.Millisecond)}
			case 2, 3:
				in = SensorGoal{Role: "away_goal"}
			case 4:
				in = AdjustScore{Side: []Side{Home, Away}[rng.IntN(2)], Delta: []int{1, -1}[rng.IntN(2)]}
			case 5:
				in = UndoGoal{}
			}
			n, r, err := Step(s, in, now)
			if err != nil {
				continue
			}
			s = n
			record(r)
			n, r, _ = Step(s, Tick{}, now)
			s = n
			record(r)
		}
		for i, e := range log {
			if e.Seq != i+1 {
				t.Fatalf("run %d: seq %d at %d", run, e.Seq, i)
			}
		}
		live := s.Game
		re := Replay(Game{ID: 1, PresetID: header.PresetID, Rules: header.Rules, NextBallSide: header.NextBallSide}, log)
		if re.Home != live.Home || re.Away != live.Away || re.Status != live.Status || re.Winner != live.Winner ||
			re.EndReason != live.EndReason || re.LastSeq != live.LastSeq || re.GoldenGoal != live.GoldenGoal ||
			re.PlayTime(now) != live.PlayTime(now) || header.NextBallSide != live.NextBallSide {
			t.Fatalf("run %d: replay %+v\nlive %+v", run, re, live)
		}
		// a game only ends when it is decided
		if live.Status == StatusFinished && live.EndReason == ReasonScore && live.Rules.Winner(live.Home, live.Away) != live.Winner {
			t.Fatalf("run %d: finished %d:%d with winner %s", run, live.Home, live.Away, live.Winner)
		}
	}
}

func TestRestore(t *testing.T) {
	var log []Event
	s := NewState(DefaultConfig(), t0)
	s, r := mustStep(t, s, StartGame{Rules: presets["long"]}, t0)
	log = append(log, r.Writes[0].Events...)
	s.Game.ID = 1
	s, r = mustStep(t, s, SensorGoal{Role: "home_goal"}, t0.Add(time.Second))
	log = append(log, r.Writes[0].Events...)

	got := Restore(DefaultConfig(), Restored{Active: Replay(r.Writes[0].Game, log)}, t0.Add(time.Hour))
	if got.Phase != PhasePlaying || got.Game.Away != 1 || got.Game.ID != 1 {
		t.Errorf("active game: %+v", got)
	}

	_, r = mustStep(t, s, StartGame{Rules: presets["long"]}, t0.Add(2*time.Second))
	paused := r.Writes[0]
	if paused.Game.Status != StatusPaused || paused.Events[0].Type != EvPaused || paused.Events[0].Reason != ReasonNewGame {
		t.Fatalf("%+v", paused)
	}
	log = append(log, paused.Events...)
	pg := Replay(paused.Game, log)
	got = Restore(DefaultConfig(), Restored{Active: pg, Recent: pg}, t0.Add(time.Hour))
	if got.Phase != PhaseIdle || got.Game != nil || got.Recent == nil || got.Recent.Away != 1 {
		t.Errorf("a paused game is only resumed on request: %+v", got)
	}
	if got := Restore(DefaultConfig(), Restored{}, t0); got.Phase != PhaseIdle {
		t.Errorf("no game: %+v", got)
	}
}

func TestAcceptors(t *testing.T) {
	s := NewState(DefaultConfig(), t0)
	s, r := mustStep(t, s, CoinInserted{Side: Home, UID: "04A1B2C3", PlayerID: 7}, t0)
	if !r.Changed || len(s.Acceptors[Home]) != 1 || s.Acceptors[Home][0].PlayerID != 7 {
		t.Fatalf("insert: %+v", s.Acceptors)
	}
	before := s
	s, _ = mustStep(t, s, CoinInserted{Side: Home, UID: "04FFFFFF"}, t0)
	s, _ = mustStep(t, s, CoinInserted{Side: Home, UID: "04FFFFFF"}, t0) // repeated report
	if len(s.Acceptors[Home]) != 2 || s.Acceptors[Home][1].PlayerID != 0 || len(before.Acceptors[Home]) != 1 {
		t.Fatalf("unknown coin, no duplicates, old state untouched: %+v / %+v", s.Acceptors, before.Acceptors)
	}
	s, _ = mustStep(t, s, CoinAssigned{UID: "04FFFFFF", PlayerID: 9}, t0)
	if s.Acceptors[Home][1].PlayerID != 9 {
		t.Errorf("assigned: %+v", s.Acceptors)
	}
	s, _ = mustStep(t, s, CoinsHeld{Side: Away, Coins: []CoinRef{{UID: "04000001", PlayerID: 3}}}, t0)
	s, _ = mustStep(t, s, CoinsEjected{Side: Home, UIDs: []string{"04A1B2C3"}}, t0)
	if len(s.Acceptors[Home]) != 0 || len(s.Acceptors[Away]) != 1 {
		t.Errorf("eject empties the side completely: %+v", s.Acceptors)
	}
	if _, _, err := Step(s, CoinInserted{Side: "left", UID: "04000001"}, t0); err == nil {
		t.Error("bad side")
	}
}
