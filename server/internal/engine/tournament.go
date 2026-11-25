package engine

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Tournament mode (spec 09 §8). While a tournament runs, it owns the table: it puts the pairing
// to play on the table (StagePairing) with its players, rules and first ball. Coins don't
// register anyone; a coin of a tournament player is a check-in ("I'm here"), reported to the
// caller, who decides which pairing to stage. Free play doesn't start.

// GamePairing links a game to a tournament pairing: the pairing row and where team A plays.
type GamePairing struct {
	ID    int64
	ASide Side
}

// Staged is the pairing waiting for its kickoff.
type Staged struct {
	Pairing   GamePairing
	Rules     Rules
	PresetID  int64
	FirstBall Side
}

// CheckIn is a tournament player saying "I'm here" with a coin (Result.CheckIns).
type CheckIn struct {
	PlayerID int64
	Side     Side
}

// SetTournament turns tournament mode on or off. Off also takes a staged pairing off the table.
type SetTournament struct{ On bool }

// StagePairing puts a pairing on the table: its players replace the registrations and the
// kickoff countdown runs (spec 09 §8). Staged nil takes a staged pairing off again.
type StagePairing struct {
	Staged     *Staged
	Home, Away []int64 // player ids; the guest may appear more than once
}

func (SetTournament) isInput() {}
func (StagePairing) isInput()  {}

// ErrTournament is returned for what tournament mode doesn't allow.
var ErrTournament = errors.New("not possible while a tournament runs the table")

func (s *State) setTournament(in SetTournament, now time.Time, r *Result) error {
	if s.Tournament == in.On {
		return nil
	}
	s.Tournament = in.On
	r.Changed = true
	if s.Phase == PhasePlaying || s.Phase == PhaseFinished {
		s.Staged = nil
		return nil // a game on the table plays on; the change applies from the next kickoff
	}
	if s.Staged != nil || in.On {
		// Off: the staged pairing's players leave. On: free-play registrations leave.
		s.Staged = nil
		s.clearRegs(r)
	}
	s.updateKickoff(now, true, r)
	return nil
}

func (s *State) stagePairing(in StagePairing, now time.Time, r *Result) error {
	if !s.Tournament {
		return fmt.Errorf("%w: no tournament runs the table", ErrInvalidInput)
	}
	if s.Phase == PhasePlaying {
		return fmt.Errorf("%w: a game is being played", ErrTournament)
	}
	if in.Staged == nil {
		if s.Staged == nil {
			return nil
		}
		s.Staged = nil
		s.clearRegs(r)
		s.updateKickoff(now, true, r)
		return nil
	}
	st := *in.Staged
	if err := st.Rules.Validate(); err != nil {
		return err
	}
	if !st.Pairing.ASide.Valid() || !st.FirstBall.Valid() || st.Pairing.ID == 0 {
		return fmt.Errorf("%w: pairing %+v", ErrInvalidInput, st)
	}
	if len(in.Home) == 0 || len(in.Away) == 0 || len(in.Home) > 2 || len(in.Away) > 2 {
		return fmt.Errorf("%w: 1 or 2 players per side", ErrInvalidInput)
	}
	if s.Phase == PhaseSleeping {
		s.wake(in, now, r)
	}
	if s.Phase == PhaseFinished {
		s.flush(r)
		s.Last = s.Game
		s.Game = nil
		s.setPhase(PhaseIdle, now)
	}
	s.clearRegs(r)
	for _, side := range []struct {
		side    Side
		players []int64
	}{{Home, in.Home}, {Away, in.Away}} {
		for i, id := range side.players {
			s.Regs = append(s.Regs, Registration{Side: side.side, Slot: i + 1, PlayerID: id,
				Sources: []RegSource{{Via: ViaManual}}})
		}
	}
	s.Staged = &st
	s.LastActivity = now
	r.Registrations, r.Changed = true, true
	s.updateKickoff(now, true, r)
	return nil
}

// clearRegs removes every registration (not during a game: callers check).
func (s *State) clearRegs(r *Result) {
	if len(s.Regs) == 0 {
		return
	}
	s.Regs = nil
	r.Registrations, r.Changed = true, true
}

// checkIn reports a tournament player's coin instead of registering it.
func (s *State) checkIn(side Side, c CoinRef, r *Result) {
	if c.PlayerID == 0 || c.PlayerID == s.Config.GuestID {
		return // unknown coins and guest coins name nobody
	}
	if !slices.ContainsFunc(r.CheckIns, func(x CheckIn) bool { return x.PlayerID == c.PlayerID }) {
		r.CheckIns = append(r.CheckIns, CheckIn{PlayerID: c.PlayerID, Side: side})
	}
}
