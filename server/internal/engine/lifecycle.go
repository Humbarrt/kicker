package engine

import (
	"fmt"
	"time"
)

// Game lifecycle (spec 02 §7.4, §9, D16): manual pause and resume, pause after inactivity, sleep
// and wake, resuming paused games, the queued game, abandoning and reinstating.
//
// Where the games are:
//   - State.Game: the game on the table – running, paused by hand, or finished during the hold.
//   - State.Recent: the most recently paused game that left the table, until a new game starts
//     (on_wake may resume it).
//   - All paused games are in the database (the paused games list); the caller loads one to
//     resume it by hand. Nothing continues by itself after a game ends.

// OnWake says what happens to the most recently paused game when the table wakes (§9).
type OnWake string

const (
	OnWakeSmart   OnWake = "smart"    // resume it once its players are back: at least one per side
	OnWakeResume  OnWake = "resume"   // always resume it
	OnWakeNewGame OnWake = "new_game" // never; it stays in the paused list
)

// PauseGame pauses the game on the table by hand (members). It continues with the next goal or
// Resume.
type PauseGame struct{ Author int64 }

// Resume continues the game paused on the table.
type Resume struct{ Author int64 }

// ResumePaused puts a paused game from the paused list back on the table (D16). The game on the
// table is discarded if it has no goals, otherwise paused (it goes to the paused list). Game is
// the paused game, rebuilt by the caller (Replay).
type ResumePaused struct {
	Game   *Game
	Author int64
}

// Wake wakes the table from the UI.
type Wake struct{ Author int64 }

// AbandonGame gives up a game that has been paused too long (abandon_after_hours, D16). Game is
// rebuilt by the caller.
type AbandonGame struct{ Game *Game }

// ReinstateGame makes an abandoned game paused again (members within their edit window, admins).
type ReinstateGame struct {
	Game   *Game
	Author int64
}

// Sleep and WakeUp send every module to sleep / wake it up (spec 01 §9).
type (
	Sleep  struct{}
	WakeUp struct{}
)

func (PauseGame) isInput()     {}
func (Resume) isInput()        {}
func (ResumePaused) isInput()  {}
func (Wake) isInput()          {}
func (AbandonGame) isInput()   {}
func (ReinstateGame) isInput() {}
func (Sleep) isEffect()        {}
func (WakeUp) isEffect()       {}

// ErrNotPaused: the game is not paused (resume) or not abandoned (reinstate).
var ErrNotPaused = fmt.Errorf("%w: the game is not paused", ErrInvalidInput)

// isActivity tells the inputs that count as activity (§9): module events, registration changes
// and table actions. Activity keeps the table awake and a game from pausing.
func isActivity(in Input) bool {
	switch in.(type) {
	case Tick, SetConfig, SetNextGame, CoinAssigned, AbandonGame, ReinstateGame, ReplaceGame:
		return false
	}
	return true
}

// wakes tells the inputs that wake a sleeping table (§9): a coin, a table action in the UI. An
// acceptor's state sync and its eject report don't, and neither do the goal sensors: they are
// off while the table sleeps.
func wakes(in Input) bool {
	switch in.(type) {
	case CoinInserted, RegisterPlayer, Unregister, StartGame, AdjustScore, UndoGoal,
		PauseGame, Resume, ResumePaused, Wake:
		return true
	}
	return false
}

// wake brings the table back from sleep and applies on_wake to the most recently paused game.
// resume: a coin, a player added by hand or the "Wake" button resume it at once; a
// specific command (start, resume another game) does what it says. smart: it waits for its
// players (smartResume).
func (s *State) wake(in Input, now time.Time, r *Result) {
	s.setPhase(PhaseIdle, now)
	r.Effects = append(r.Effects, WakeUp{})
	r.Changed = true
	g := s.Recent
	if g == nil {
		return
	}
	switch s.Config.OnWake {
	case OnWakeSmart:
		s.SmartWake = true
	case OnWakeResume:
		switch in.(type) {
		case CoinInserted, RegisterPlayer, Wake:
			s.resumeOnTable(g, SourceSystem, 0, now, r)
		}
	}
}

// smartResume resumes the most recently paused game once, after a smart wake, at least one of
// its players is registered again for each side it had players on (by coin, phone or by hand).
// Before that the normal kickoff flow goes on; if a new game starts first, the paused game stays
// in the list (§9).
func (s *State) smartResume(now time.Time, r *Result) bool {
	g := s.Recent
	if !s.SmartWake || g == nil || (s.Phase != PhaseIdle && s.Phase != PhasePregame) {
		return false
	}
	back := map[int64]bool{}
	for _, reg := range s.Regs {
		if !reg.Stale() {
			back[reg.PlayerID] = true
		}
	}
	for _, side := range []Side{Home, Away} {
		var players, present int
		for _, st := range g.Roster.Side(side) {
			if s.isParticipant(g, st.PlayerID) {
				players++
				if back[st.PlayerID] {
					present++
				}
			}
		}
		if players > 0 && present == 0 {
			return false
		}
	}
	s.resumeOnTable(g, SourceSystem, 0, now, r)
	return true
}

// isParticipant: a real player (not the guest, not an unknown coin) of the game.
func (s *State) isParticipant(g *Game, playerID int64) bool {
	return playerID != 0 && playerID != s.Config.GuestID && g.Plays(playerID)
}

// goSleep sends the table to sleep: coins out, registrations end (only phones lying on a corner
// stay, M4), modules asleep (§9).
func (s *State) goSleep(now time.Time, r *Result) {
	s.releasePlayers(r)
	s.SmartWake = false
	s.setPhase(PhaseSleeping, now)
	s.KickoffAt = time.Time{}
	r.Effects = append(r.Effects, Sleep{})
	r.Changed = true
}

// parkGame takes the (paused) game off the table into the paused list.
func (s *State) parkGame(r *Result) {
	s.flush(r)
	s.Recent, s.Game, s.Resumed = s.Game, nil, false
}

// clearRef forgets a game that is no longer paused.
func (s *State) clearRef(id int64) {
	if s.Recent != nil && s.Recent.ID == id {
		s.Recent = nil
	}
}

func (s *State) pauseCmd(in PauseGame, now time.Time, r *Result) error {
	s.checkClock(now, r)
	if !s.playing() {
		return ErrNoGame
	}
	s.emit(Event{Type: EvPaused, Reason: ReasonManual, Source: SourceTable, Author: in.Author, At: now})
	return nil
}

func (s *State) resumeCmd(in Resume, now time.Time, r *Result) error {
	if s.Phase != PhasePlaying || s.Game.Status != StatusPaused {
		return ErrNotPaused
	}
	s.emit(Event{Type: EvResumed, Source: SourceTable, Author: in.Author, At: now})
	return nil
}

// resumeIfPaused continues a game paused by hand at the next goal (§7.4).
func (s *State) resumeIfPaused(src Source, author int64, at time.Time) {
	if s.Phase == PhasePlaying && s.Game.Status == StatusPaused {
		s.emit(Event{Type: EvResumed, Source: src, Author: author, At: at})
	}
}

func (s *State) resumePausedCmd(in ResumePaused, now time.Time, r *Result) error {
	g := in.Game
	if g == nil || g.ID == 0 || g.Status != StatusPaused {
		return ErrNotPaused
	}
	if s.Game != nil && s.Game.ID == g.ID {
		if s.Phase == PhasePlaying {
			return s.resumeCmd(Resume{Author: in.Author}, now, r)
		}
		return ErrNotPaused
	}
	s.resumeOnTable(g.clone(), SourceTable, in.Author, now, r)
	return nil
}

// resumeOnTable puts a paused game on the table (D16): the game there is discarded (no goals) or
// paused into the paused list. The resumed game keeps its own players; the registrations wait for
// the game after it.
func (s *State) resumeOnTable(g *Game, src Source, author int64, now time.Time, r *Result) {
	switch s.Phase {
	case PhasePlaying:
		old := s.Game
		if len(old.Goals) == 0 {
			r.Writes = append(r.Writes, Write{Game: *old.clone(), Delete: true})
		} else {
			if old.Status == StatusActive {
				s.emit(Event{Type: EvPaused, Reason: ReasonSwitched, Source: src, Author: author, At: now})
			}
			s.flush(r)
		}
	case PhaseFinished:
		s.flush(r)
		s.Last = s.Game
	}
	s.clearRef(g.ID)
	s.SmartWake = false
	s.Game, s.Resumed, s.KickoffAt = g, true, time.Time{}
	s.LastActivity = now
	s.setPhase(PhasePlaying, now)
	s.emit(Event{Type: EvResumed, Source: src, Author: author, At: now})
	r.Changed = true
}

// otherGame appends an event to a game that is not on the table.
func (s *State) otherGame(g *Game, e Event, r *Result) {
	g = g.clone()
	e.Seq = g.LastSeq + 1
	if e.PlayTime == nil {
		pt := g.PlayTime(e.At)
		e.PlayTime = &pt
	}
	g.Apply(e)
	r.Writes = append(r.Writes, Write{Game: *g, Events: []Event{e}})
	r.Changed = true
}

func (s *State) abandonCmd(in AbandonGame, now time.Time, r *Result) error {
	g := in.Game
	if g == nil || g.ID == 0 || g.Status != StatusPaused || (s.Game != nil && s.Game.ID == g.ID) {
		return ErrNotPaused
	}
	s.clearRef(g.ID)
	s.otherGame(g, Event{Type: EvAbandoned, Source: SourceSystem, At: now}, r)
	return nil
}

func (s *State) reinstateCmd(in ReinstateGame, now time.Time, r *Result) error {
	g := in.Game
	if g == nil || g.ID == 0 || g.Status != StatusAbandoned {
		return fmt.Errorf("%w: the game is not abandoned", ErrInvalidInput)
	}
	s.otherGame(g, Event{Type: EvReinstated, Source: SourceManual, Author: in.Author, At: now}, r)
	return nil
}

// inactivity pauses a running game or sends the table to sleep (§9). Called by tick.
func (s *State) inactivity(now time.Time, r *Result) {
	switch s.Phase {
	case PhasePlaying:
		g := s.Game
		switch {
		case g.Status == StatusActive && s.Config.PauseAfter > 0 && !now.Before(s.LastActivity.Add(s.Config.PauseAfter)):
			// Backdated to the last activity, so the idle minutes don't count as play time.
			at := s.LastActivity
			if at.Before(g.StartedAt) {
				at = g.StartedAt
			}
			s.emit(Event{Type: EvPaused, Reason: ReasonTimeout, Source: SourceSystem, At: at})
			s.parkGame(r)
			s.goSleep(now, r)
		case g.Status == StatusPaused && s.sleepDue(now):
			s.parkGame(r)
			s.goSleep(now, r)
		}
	case PhaseIdle, PhasePregame:
		if s.sleepDue(now) {
			s.goSleep(now, r)
		}
	}
}

func (s *State) sleepDue(now time.Time) bool {
	return s.Config.SleepAfter > 0 && !now.Before(s.LastActivity.Add(s.Config.SleepAfter))
}

// inactivityDeadline is when inactivity changes the table next; zero = never.
func (s *State) inactivityDeadline() time.Time {
	switch s.Phase {
	case PhasePlaying:
		if s.Game.Status == StatusActive {
			if s.Config.PauseAfter > 0 {
				return s.LastActivity.Add(s.Config.PauseAfter)
			}
		} else if s.Config.SleepAfter > 0 {
			return s.LastActivity.Add(s.Config.SleepAfter)
		}
	case PhaseIdle, PhasePregame:
		if s.Config.SleepAfter > 0 {
			return s.LastActivity.Add(s.Config.SleepAfter)
		}
	}
	return time.Time{}
}

// earliest returns the earlier of two deadlines; zero = none.
func earliest(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero() || a.Before(b):
		return a
	}
	return b
}

// Restored is what the caller loads after a restart (spec 02 §2).
type Restored struct {
	Active *Game // the game that was running (status active); paused-by-hand games are in the list
	Recent *Game // the most recently paused game
	Last   *Game // the last finished game, shown while idle
	Regs   []Registration
}

// Restore rebuilds the table after a server restart. Call Step with a Tick right after, so
// expired clocks are handled.
func Restore(cfg Config, d Restored, now time.Time) State {
	s := NewState(cfg, now)
	for _, r := range d.Regs {
		s.Regs = append(s.Regs, r.clone())
	}
	s.sortRegs()
	if d.Active != nil && d.Active.Status == StatusActive {
		s.Phase, s.Game = PhasePlaying, d.Active.clone()
	}
	// Only if no game was started after it was paused (§9 smart wake).
	if g := d.Recent; g != nil && g.Status == StatusPaused &&
		(s.Game == nil || !s.Game.StartedAt.After(g.PausedAt)) && (d.Last == nil || !d.Last.StartedAt.After(g.PausedAt)) {
		s.Recent = g.clone()
	}
	if d.Last != nil {
		s.Last = d.Last.clone()
	}
	return s
}

func cloneGame(g *Game) *Game {
	if g == nil {
		return nil
	}
	return g.clone()
}
