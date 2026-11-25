package engine

import (
	"errors"
	"testing"
)

func TestTournamentMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.GuestID = 99
	s := NewState(cfg, t0)
	s, _ = mustStep(t, s, CoinInserted{Side: Home, UID: "AABBCCDD", PlayerID: 5}, t0) // free play: registers
	if len(s.Regs) != 1 {
		t.Fatalf("free play: %+v", s.Regs)
	}

	// On: free-play registrations leave; coins are check-ins and start nothing.
	s, _ = mustStep(t, s, SetTournament{On: true}, t0)
	if len(s.Regs) != 0 || s.Phase != PhaseIdle {
		t.Fatalf("tournament on: %s %+v", s.Phase, s.Regs)
	}
	s, r := mustStep(t, s, CoinInserted{Side: Away, UID: "11223344", PlayerID: 7}, t0)
	if len(s.Regs) != 0 || len(r.CheckIns) != 1 || r.CheckIns[0] != (CheckIn{PlayerID: 7, Side: Away}) {
		t.Errorf("check-in: %+v %+v", s.Regs, r.CheckIns)
	}
	if _, r := mustStep(t, s, CoinInserted{Side: Away, UID: "55667788", PlayerID: 99}, t0); len(r.CheckIns) != 0 {
		t.Errorf("a guest coin checked in: %+v", r.CheckIns)
	}
	if _, _, err := Step(s, StartGame{Rules: Rules{WinScore: 10, WinBy: 1, TimeTie: TimeTieGoldenGoal}}, t0); !errors.Is(err, ErrTournament) {
		t.Errorf("Start now without a pairing: %v", err)
	}

	// Staging a pairing: its players, the countdown, then the game with the pairing's rules.
	rules := Rules{WinScore: 3, WinBy: 1, TimeTie: TimeTieGoldenGoal}
	st := &Staged{Pairing: GamePairing{ID: 42, ASide: Away}, Rules: rules, FirstBall: Away}
	s, _ = mustStep(t, s, StagePairing{Staged: st, Home: []int64{3, 4}, Away: []int64{7, 99}}, t0)
	if s.Phase != PhasePregame || len(s.Regs) != 4 {
		t.Fatalf("staged: %s %+v", s.Phase, s.Regs)
	}
	at := t0.Add(cfg.KickoffDelay)
	s, _ = mustStep(t, s, Tick{}, at)
	g := s.Game
	if s.Phase != PhasePlaying || g.Pairing == nil || *g.Pairing != st.Pairing || g.Rules != rules || g.FirstBall != Away || s.Staged != nil {
		t.Fatalf("kickoff: %s %+v", s.Phase, g)
	}
	if _, _, err := Step(s, StartGame{Rules: rules}, at); !errors.Is(err, ErrTournament) {
		t.Errorf("Start new game during a tournament game: %v", err)
	}
	if _, _, err := Step(s, StagePairing{Staged: st, Home: []int64{1}, Away: []int64{2}}, at); !errors.Is(err, ErrTournament) {
		t.Errorf("staged while playing: %v", err)
	}
	for range 3 {
		s, _ = mustStep(t, s, SensorGoal{Role: "away_goal"}, at) // ball in the away net: point for home
	}
	if s.Phase != PhaseFinished || s.Game.Winner != Home {
		t.Fatalf("game end: %s %+v", s.Phase, s.Game)
	}

	// The next game of the pairing is staged during the finished hold: sides swapped.
	st2 := &Staged{Pairing: GamePairing{ID: 42, ASide: Home}, Rules: rules, FirstBall: Away}
	s, _ = mustStep(t, s, StagePairing{Staged: st2, Home: []int64{7, 99}, Away: []int64{3, 4}}, at)
	if s.Phase != PhasePregame || s.Last == nil || s.Last.Winner != Home || s.Game != nil {
		t.Fatalf("second game staged: %s last %+v", s.Phase, s.Last)
	}
	// Off: the staged pairing leaves the table, free play again.
	s, _ = mustStep(t, s, SetTournament{On: false}, at)
	if s.Phase != PhaseIdle || len(s.Regs) != 0 || s.Staged != nil {
		t.Errorf("off: %s %+v", s.Phase, s.Regs)
	}
	if _, _, err := Step(s, StagePairing{Staged: st}, at); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("staged without a tournament: %v", err)
	}
}
