package engine

import (
	"fmt"
	"time"
)

// Kickoff (spec 02 §6): once enough players are registered the table enters pregame and counts
// down; every roster change restarts the countdown; at zero the game starts by itself.

// KickoffRequires says when a kickoff countdown starts (setting kickoff_requires).
type KickoffRequires string

const (
	KickoffBothSides KickoffRequires = "both_sides" // at least one player on each side
	KickoffAnyPlayer KickoffRequires = "any_player" // at least one player anywhere
)

// FirstBallRule says which side gets the first ball (setting first_ball).
type FirstBallRule string

const (
	FirstBallRandom        FirstBallRule = "random"
	FirstBallLowerRating   FirstBallRule = "lower_rating" // ratings arrive in M3 task 6; random until then
	FirstBallLoserPrevious FirstBallRule = "loser_previous"
	FirstBallAlternate     FirstBallRule = "alternate"
	FirstBallHome          FirstBallRule = "home"
	FirstBallAway          FirstBallRule = "away"
)

// NextGame are the rules the next game starts with: the current preset, or a member's adjusted
// copy (spec 02 §6, §7.3).
type NextGame struct {
	Rules    Rules
	PresetID int64 // 0 = custom rules
}

// SetConfig changes the table settings. GuestID is kept if it is 0.
type SetConfig struct{ Config Config }

// SetNextGame changes the rules of the next game.
type SetNextGame struct{ Next NextGame }

func (SetConfig) isInput()   {}
func (SetNextGame) isInput() {}

func (s *State) setConfig(in SetConfig, now time.Time, r *Result) error {
	c := in.Config
	if err := c.Validate(); err != nil {
		return err
	}
	if c.GuestID == 0 {
		c.GuestID = s.Config.GuestID
	}
	s.Config = c
	r.Changed = true
	s.updateKickoff(now, false, r)
	return nil
}

func (s *State) setNextGame(in SetNextGame, r *Result) error {
	if err := in.Next.Rules.Validate(); err != nil {
		return err
	}
	s.Next = in.Next
	r.Changed = true
	return nil
}

// Validate checks the settings the engine uses.
func (c Config) Validate() error {
	switch c.KickoffRequires {
	case KickoffBothSides, KickoffAnyPlayer:
	default:
		return fmt.Errorf("%w: kickoff_requires %q", ErrInvalidInput, c.KickoffRequires)
	}
	switch c.FirstBall {
	case FirstBallRandom, FirstBallLowerRating, FirstBallLoserPrevious, FirstBallAlternate, FirstBallHome, FirstBallAway:
	default:
		return fmt.Errorf("%w: first_ball %q", ErrInvalidInput, c.FirstBall)
	}
	switch c.AfterGoalBall {
	case BallConceding, BallScoring, BallAlternate:
	default:
		return fmt.Errorf("%w: after_goal_ball %q", ErrInvalidInput, c.AfterGoalBall)
	}
	switch c.OnWake {
	case OnWakeSmart, OnWakeResume, OnWakeNewGame:
	default:
		return fmt.Errorf("%w: on_wake %q", ErrInvalidInput, c.OnWake)
	}
	if c.PauseAfter < 0 || c.SleepAfter < 0 {
		return fmt.Errorf("%w: negative inactivity time", ErrInvalidInput)
	}
	if c.KickoffDelay < 0 || c.KickoffDelay > time.Minute || c.FinishedHold < 0 || c.FinishedHold > 5*time.Minute {
		return fmt.Errorf("%w: kickoff delay or finished hold out of range", ErrInvalidInput)
	}
	return nil
}

// kickoffReady reports whether enough players are registered (§6). Players who have left don't
// count: they are not in the next game.
func (s *State) kickoffReady() bool {
	n := map[Side]int{}
	for _, r := range s.Regs {
		if !r.Stale() {
			n[r.Side]++
		}
	}
	if s.Config.KickoffRequires == KickoffAnyPlayer {
		return n[Home]+n[Away] > 0
	}
	return n[Home] > 0 && n[Away] > 0
}

// updateKickoff moves between idle and pregame and starts the game when the countdown is over.
// rosterChanged restarts a running countdown, so a group can finish registering.
func (s *State) updateKickoff(now time.Time, rosterChanged bool, r *Result) {
	if s.Phase != PhaseIdle && s.Phase != PhasePregame {
		return
	}
	next := s.Next
	if s.NextOnce != nil {
		next = *s.NextOnce
	}
	if s.Tournament { // only a staged pairing kicks off (spec 09 §8)
		if s.Staged == nil {
			next = NextGame{}
		} else {
			next = NextGame{Rules: s.Staged.Rules, PresetID: s.Staged.PresetID}
		}
	}
	ready := s.kickoffReady() && next.Rules.Validate() == nil
	switch {
	case ready && s.Phase == PhaseIdle:
		s.setPhase(PhasePregame, now)
		s.KickoffAt = now.Add(s.Config.KickoffDelay)
		r.Changed = true
	case ready && rosterChanged:
		s.KickoffAt = now.Add(s.Config.KickoffDelay)
		r.Changed = true
	case !ready && s.Phase == PhasePregame:
		s.setPhase(PhaseIdle, now)
		s.KickoffAt = time.Time{}
		r.Changed = true
	}
	if s.Phase == PhasePregame && !now.Before(s.KickoffAt) {
		// Can't fail: the rules were validated above.
		_ = s.start(StartGame{Rules: next.Rules, PresetID: next.PresetID}, SourceSystem, now, r)
	}
}

// firstBall picks the side of the first ball by the first_ball rule. Called after the previous
// game has become s.Last.
func (s *State) firstBall(now time.Time) Side {
	switch s.Config.FirstBall {
	case FirstBallHome:
		return Home
	case FirstBallAway:
		return Away
	case FirstBallLoserPrevious:
		if s.Last != nil && s.Last.Winner.Valid() {
			return s.Last.Winner.Other()
		}
	case FirstBallAlternate:
		if s.Last != nil && s.Last.FirstBall.Valid() {
			return s.Last.FirstBall.Other()
		}
	}
	// random, lower_rating (until ratings exist), and the fallbacks above.
	return coinToss(now)
}

// coinToss is a fair, deterministic coin from the kickoff time, so Step stays a pure function.
func coinToss(now time.Time) Side {
	x := uint64(now.UnixNano()) + 0x9e3779b97f4a7c15 // splitmix64
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	x ^= x >> 31
	if x&1 == 0 {
		return Home
	}
	return Away
}
