package engine

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Phase is the table state (spec 02 §3). M1 uses idle, playing and finished.
type Phase string

const (
	PhaseSleeping Phase = "sleeping"
	PhaseIdle     Phase = "idle"
	PhasePregame  Phase = "pregame"
	PhasePlaying  Phase = "playing"
	PhaseFinished Phase = "finished"
)

// BallRule says where the ball goes after a goal (spec 02 §7.1).
type BallRule string

const (
	BallConceding BallRule = "conceding"
	BallScoring   BallRule = "scoring"
	BallAlternate BallRule = "alternate"
)

// Config holds the table settings the engine needs (spec 02 §13).
type Config struct {
	KickoffRequires KickoffRequires
	KickoffDelay    time.Duration
	FirstBall       FirstBallRule
	AfterGoalBall   BallRule
	FinishedHold    time.Duration
	PauseAfter      time.Duration // pause a running game after this long without activity; 0 = never
	SleepAfter      time.Duration // sleep without a running game after this long; 0 = never
	OnWake          OnWake
	Dispenser       bool  // a ball dispenser is adopted and online
	GuestID         int64 // the built-in guest player: may play in several slots at once (§4)
}

// DefaultConfig returns the defaults of spec 02 §13.
func DefaultConfig() Config {
	return Config{KickoffRequires: KickoffBothSides, KickoffDelay: 5 * time.Second, FirstBall: FirstBallRandom,
		AfterGoalBall: BallConceding, FinishedHold: 10 * time.Second,
		PauseAfter: 5 * time.Minute, SleepAfter: 3 * time.Minute, OnWake: OnWakeSmart}
}

// CoinRef is a coin inside an acceptor. PlayerID 0 = unknown (or deactivated) coin: the web
// offers to assign it (spec 02 §4).
type CoinRef struct {
	UID      string
	PlayerID int64
}

// State is the whole table state. Step never modifies the State it is given.
type State struct {
	Config Config
	Phase  Phase
	Game   *Game     // the current game: playing, or finished during the hold
	Last   *Game     // the last finished game, shown while idle
	Since  time.Time // when the phase was entered
	// Acceptors holds the coins inside each side's acceptor, as the modules report them. Not
	// persisted: modules send their state after every (re)connect (spec 01 §6).
	Acceptors map[Side][]CoinRef
	// Regs are the registrations (§4), sorted by side and slot. Saved in table_registrations.
	Regs []Registration
	// Next are the rules of the next game; KickoffAt is the end of the countdown in pregame.
	// NextOnce overrides Next for one game ("Start new game" with other rules).
	Next      NextGame
	NextOnce  *NextGame
	KickoffAt time.Time
	// LastActivity is the last module event, registration change or table action (§9).
	LastActivity time.Time
	// Recent is the most recently paused game, as long as no new game was started since: on_wake
	// may resume it (§9). Resumed: the game on the table was resumed and keeps its own players.
	Recent  *Game
	Resumed bool
	// SmartWake: the table woke up with on_wake smart and Recent is waiting for its players – at
	// least one per side (§9).
	SmartWake bool

	pending []Event // events of Game not yet handed to the caller
	created bool    // Game was created in this step
}

// NewState returns an idle table.
func NewState(cfg Config, now time.Time) State {
	return State{Config: cfg, Phase: PhaseIdle, Since: now, LastActivity: now}
}

// --- inputs -----------------------------------------------------------------------------------

// Input is anything that can change the table. Use the types below.
type Input interface{ isInput() }

// StartGame is "Start now" / "Start new game" (spec 02 §6, §7.4). A running game is paused, or
// discarded if nobody has scored yet (D16).
type StartGame struct {
	Rules     Rules
	PresetID  int64
	FirstBall Side // chosen by the caller (first_ball setting, random needs randomness)
	Author    int64
}

// SensorGoal is a goal event from an adopted goal sensor (spec 01 goal_sensor@1).
type SensorGoal struct {
	Role string    // home_goal = ball in the home net = point for away
	At   time.Time // server time of the event (module clock corrected); zero = now
}

// AdjustScore is +1 / −1 on the scoreboard page (spec 02 §7.3).
type AdjustScore struct {
	Side   Side
	Delta  int // +1 or −1
	Author int64
}

// UndoGoal voids the last goal that still counts (spec 02 §7.3).
type UndoGoal struct{ Author int64 }

// Tick lets time pass: game clock and finished hold. See State.NextDeadline.
type Tick struct{}

// CoinInserted is a coin pushed into a side's acceptor (coin_acceptor@1 "inserted"); the caller
// has looked the coin up (PlayerID 0 = unknown).
type CoinInserted struct {
	Side     Side
	UID      string
	PlayerID int64
}

// CoinsHeld is an acceptor's state sync: exactly these coins are inside.
type CoinsHeld struct {
	Side  Side
	Coins []CoinRef
}

// CoinsEjected: the acceptor gave these coins back, on the server's command or because a
// player pressed its button.
type CoinsEjected struct {
	Side     Side
	UIDs     []string
	ByButton bool
}

// CoinAssigned: a coin was assigned to a player, reassigned or deactivated (PlayerID 0).
type CoinAssigned struct {
	UID      string
	PlayerID int64
}

func (StartGame) isInput()    {}
func (SensorGoal) isInput()   {}
func (AdjustScore) isInput()  {}
func (UndoGoal) isInput()     {}
func (Tick) isInput()         {}
func (CoinInserted) isInput() {}
func (CoinsHeld) isInput()    {}
func (CoinsEjected) isInput() {}
func (CoinAssigned) isInput() {}

// --- outputs ----------------------------------------------------------------------------------

// Write is what the caller must persist for one game, in order, before running the effects.
type Write struct {
	Game   Game    // snapshot after the events; its ID is 0 if Create is set
	Create bool    // insert the game row first (with Events)
	Delete bool    // discard the game and all its events (D16)
	Events []Event // to append, Seq already set
}

// Effect is something the caller does after persisting: module commands, notifications.
type Effect interface{ isEffect() }

// ReleaseBall asks the dispenser for balls.
type ReleaseBall struct {
	Side  Side
	Count int
}

// EjectCoins asks a side's coin acceptor to give all coins back.
type EjectCoins struct{ Side Side }

func (ReleaseBall) isEffect() {}
func (EjectCoins) isEffect()  {}

// Result is the outcome of one Step.
type Result struct {
	Writes  []Write
	Effects []Effect
	Changed bool // the visible state changed: publish it
	// Registrations changed: save State.Regs (after the writes).
	Registrations bool
}

// Errors for API commands. Sensor input is never an error; it is ignored if it doesn't apply.
var (
	ErrNoGame        = errors.New("no game is being played")
	ErrNothingToUndo = errors.New("no goal to take back")
	ErrInvalidRules  = errors.New("invalid rules")
	ErrInvalidInput  = errors.New("invalid input")
)

// Step is the engine: the next state, what to persist and what to do (spec 02 §2).
// On error the given state is returned unchanged.
func Step(s State, in Input, now time.Time) (State, Result, error) {
	n := s.clone()
	var r Result
	var err error
	if isActivity(in) {
		n.LastActivity = now
	}
	if n.Phase == PhaseSleeping && wakes(in) {
		n.wake(in, now, &r)
	}
	switch in := in.(type) {
	case PauseGame:
		err = n.pauseCmd(in, now, &r)
	case Resume:
		err = n.resumeCmd(in, now, &r)
	case ResumePaused:
		err = n.resumePausedCmd(in, now, &r)
	case Wake:
		// Waking (above) is all it does.
	case AbandonGame:
		err = n.abandonCmd(in, now, &r)
	case ReinstateGame:
		err = n.reinstateCmd(in, now, &r)
	case ReplaceGame:
		err = n.replaceGame(in, &r)
	case StartGame:
		err = n.start(in, SourceTable, now, &r)
	case SetConfig:
		err = n.setConfig(in, now, &r)
	case SetNextGame:
		err = n.setNextGame(in, &r)
	case SensorGoal:
		n.sensorGoal(in, now, &r)
	case AdjustScore:
		err = n.adjust(in, now, &r)
	case UndoGoal:
		err = n.undo(in, now, &r)
	case Tick:
		n.tick(now, &r)
	case CoinInserted, CoinsHeld, CoinsEjected, CoinAssigned:
		err = n.coins(in, now, &r)
	case RegisterPlayer:
		err = n.registerCmd(in, now, &r)
	case Unregister:
		err = n.unregisterCmd(in, now, &r)
	default:
		err = fmt.Errorf("%w: %T", ErrInvalidInput, in)
	}
	if err != nil {
		return s, Result{}, err
	}
	n.flush(&r)
	return n, r, nil
}

// NextDeadline is when the state changes by itself (clock runs out, finished hold ends);
// zero = never. The caller sends a Tick then.
func (s State) NextDeadline() time.Time {
	return earliest(s.gameDeadline(), s.inactivityDeadline())
}

func (s State) gameDeadline() time.Time {
	switch s.Phase {
	case PhasePlaying:
		g := s.Game
		if g.Status == StatusActive && g.Rules.TimeLimit > 0 && !g.ClockExpired && g.ClockRunning() {
			return g.runningSince.Add(g.Rules.TimeLimit - g.played)
		}
	case PhaseFinished:
		return s.Since.Add(s.Config.FinishedHold)
	case PhasePregame:
		return s.KickoffAt
	}
	return time.Time{}
}

func (s State) clone() State {
	if s.Game != nil {
		s.Game = s.Game.clone()
	}
	acc := make(map[Side][]CoinRef, 2)
	for side, coins := range s.Acceptors {
		acc[side] = append([]CoinRef(nil), coins...)
	}
	s.Acceptors = acc
	s.Recent = cloneGame(s.Recent)
	if s.NextOnce != nil {
		next := *s.NextOnce
		s.NextOnce = &next
	}
	regs := make([]Registration, len(s.Regs))
	for i, r := range s.Regs {
		regs[i] = r.clone()
	}
	s.Regs = regs
	s.pending, s.created = nil, false
	return s
}

func (s *State) setPhase(p Phase, at time.Time) {
	s.Phase, s.Since = p, at
}

// emit appends an event to the current game.
func (s *State) emit(e Event) {
	g := s.Game
	e.Seq = g.LastSeq + 1
	if e.PlayTime == nil && e.Type != EvGameStarted {
		pt := g.PlayTime(e.At)
		e.PlayTime = &pt
	}
	g.Apply(e)
	s.pending = append(s.pending, e)
}

// flush hands the pending events of the current game to the caller.
func (s *State) flush(r *Result) {
	if len(s.pending) == 0 {
		return
	}
	r.Writes = append(r.Writes, Write{Game: *s.Game.clone(), Create: s.created, Events: s.pending})
	r.Changed = true
	s.pending, s.created = nil, false
}

// --- handlers ---------------------------------------------------------------------------------

// start kicks off a game: "Start now" by a member (source table) or the end of the countdown
// (source system).
func (s *State) start(in StartGame, src Source, now time.Time, r *Result) error {
	if err := in.Rules.Validate(); err != nil {
		return err
	}
	if in.FirstBall != "" && !in.FirstBall.Valid() {
		return fmt.Errorf("%w: first ball side %q", ErrInvalidInput, in.FirstBall)
	}
	if s.Phase == PhasePlaying {
		// "Start new game" (§7.4): the running game is paused – or discarded without goals – and
		// the table goes to the kickoff countdown with the current registrations.
		old := s.Game
		if len(old.Goals) == 0 {
			r.Writes = append(r.Writes, Write{Game: *old.clone(), Delete: true})
			s.Game, s.Resumed = nil, false
		} else {
			if old.Status == StatusActive {
				s.emit(Event{Type: EvPaused, Reason: ReasonNewGame, Source: SourceTable, Author: in.Author, At: now})
			}
			s.parkGame(r)
		}
		s.NextOnce = &NextGame{Rules: in.Rules, PresetID: in.PresetID}
		s.setPhase(PhaseIdle, now)
		r.Changed = true
		s.updateKickoff(now, false, r)
		return nil
	}
	if s.Phase == PhaseFinished {
		s.flush(r)
		s.Last = s.Game
	}
	s.NextOnce, s.Resumed = nil, false
	s.Recent, s.SmartWake = nil, false // a new game: the paused one stays in the list, by hand only

	first := in.FirstBall
	if first == "" {
		first = s.firstBall(now)
	}
	s.KickoffAt = time.Time{}
	// Players who left are not in the next game (§4); the others are its roster (§6).
	if slices.ContainsFunc(s.Regs, Registration.Stale) {
		s.dropStale()
		r.Registrations = true
	}
	s.Game = &Game{PresetID: in.PresetID, Rules: in.Rules, NextBallSide: first}
	s.created = true
	s.LastActivity = now // a fresh game doesn't pause right away
	s.setPhase(PhasePlaying, now)
	zero := time.Duration(0)
	s.emit(Event{Type: EvGameStarted, Side: first, Source: src, Author: in.Author, At: now, PlayTime: &zero})
	for _, reg := range s.Regs {
		s.emit(joinEvent(reg, Event{Source: SourceSystem, At: now, PlayTime: &zero}))
	}
	s.release(first, now, r)
	r.Changed = true
	return nil
}

// playing reports whether goals count right now.
func (s *State) playing() bool {
	return s.Phase == PhasePlaying && s.Game.Status == StatusActive
}

func (s *State) sensorGoal(in SensorGoal, now time.Time, r *Result) {
	var scorer Side
	switch in.Role {
	case "home_goal":
		scorer = Away
	case "away_goal":
		scorer = Home
	default:
		return
	}
	at := in.At
	if at.IsZero() || at.After(now) {
		at = now
	}
	// A game paused by hand continues with the next goal, which counts (§7.4).
	s.resumeIfPaused(SourceSensor, 0, at)
	// The clock may have run out between the goal and its processing.
	s.checkClock(at, r)
	if !s.playing() || at.Before(s.Game.StartedAt) {
		return // not a goal: the ball only went back into the dispenser (spec 02 §3)
	}
	s.goal(scorer, SourceSensor, 0, at, r)
}

func (s *State) adjust(in AdjustScore, now time.Time, r *Result) error {
	if !in.Side.Valid() || (in.Delta != 1 && in.Delta != -1) {
		return fmt.Errorf("%w: adjust %q by %d", ErrInvalidInput, in.Side, in.Delta)
	}
	if in.Delta == 1 {
		s.resumeIfPaused(SourceTable, in.Author, now)
	}
	s.checkClock(now, r)
	if in.Delta == 1 {
		if !s.playing() {
			return ErrNoGame
		}
		s.goal(in.Side, SourceTable, in.Author, now, r)
		return nil
	}
	if !s.onTable() { // corrections also while paused by hand
		return ErrNoGame
	}
	return s.void(s.Game.lastGoal(in.Side), in.Author, now, r)
}

// onTable reports whether a running game is on the table, active or paused by hand.
func (s *State) onTable() bool {
	return s.Phase == PhasePlaying && (s.Game.Status == StatusActive || s.Game.Status == StatusPaused)
}

func (s *State) undo(in UndoGoal, now time.Time, r *Result) error {
	s.checkClock(now, r)
	if !s.onTable() {
		return ErrNoGame
	}
	return s.void(s.Game.lastGoal(""), in.Author, now, r)
}

func (s *State) void(i int, author int64, now time.Time, r *Result) error {
	if i < 0 {
		return ErrNothingToUndo
	}
	gl := s.Game.Goals[i]
	s.emit(Event{Type: EvGoalVoided, Side: gl.Side, TargetSeq: gl.Seq, Source: SourceTable, Author: author, At: now})
	// Taking a goal back can decide a game: 10:9 → 10:8 with win_by 2.
	s.checkWin(now, r)
	return nil
}

func (s *State) goal(scorer Side, src Source, author int64, at time.Time, r *Result) {
	s.emit(Event{Type: EvGoal, Side: scorer, Source: src, Author: author, At: at})
	if s.checkWin(at, r) {
		return
	}
	g := s.Game
	switch s.Config.AfterGoalBall {
	case BallScoring:
		g.NextBallSide = scorer
	case BallAlternate:
		g.NextBallSide = g.NextBallSide.Other()
	default:
		g.NextBallSide = scorer.Other()
	}
	s.release(g.NextBallSide, at, r)
}

// checkWin finishes the game if it is decided (D10, spec 02 §7.2).
func (s *State) checkWin(at time.Time, r *Result) bool {
	g := s.Game
	var w Side
	reason := ReasonScore
	if g.GoldenGoal {
		w, reason = leader(g), ReasonGoldenGoal
	} else {
		w = g.Rules.Winner(g.Home, g.Away)
	}
	if w == "" {
		return false
	}
	s.finish(w, reason, at, r)
	return true
}

// checkClock handles the end of the time limit (spec 02 §7.2).
func (s *State) checkClock(now time.Time, r *Result) {
	if !s.playing() {
		return
	}
	d := s.gameDeadline() // not NextDeadline: that includes the inactivity timers
	if d.IsZero() || now.Before(d) {
		return
	}
	g := s.Game
	s.emit(Event{Type: EvClockExpired, Source: SourceSystem, At: d})
	if w := leader(g); w != "" {
		s.finish(w, ReasonTime, d, r)
	} else if g.Rules.TimeTie == TimeTieDraw {
		s.finish("", ReasonTime, d, r)
	} else {
		s.emit(Event{Type: EvGoldenGoal, Source: SourceSystem, At: d})
	}
}

func (s *State) finish(winner Side, reason string, at time.Time, r *Result) {
	s.emit(Event{Type: EvFinished, Side: winner, Reason: reason, Source: SourceSystem, At: at})
	s.setPhase(PhaseFinished, at)
	if !s.Resumed { // a resumed game's registrations are for the game after it (D16)
		s.releasePlayers(r)
	}
}

func (s *State) release(side Side, at time.Time, r *Result) {
	if !s.Config.Dispenser {
		return
	}
	one := 1
	s.emit(Event{Type: EvBallReleased, Side: side, Value: &one, Source: SourceSystem, At: at})
	r.Effects = append(r.Effects, ReleaseBall{Side: side, Count: 1})
}

func (s *State) tick(now time.Time, r *Result) {
	switch s.Phase {
	case PhasePlaying:
		s.checkClock(now, r)
	case PhaseFinished:
		if !now.Before(s.Since.Add(s.Config.FinishedHold)) {
			s.Last, s.Game, s.Resumed = s.Game, nil, false
			s.setPhase(PhaseIdle, now)
			r.Changed = true
			s.updateKickoff(now, false, r) // e.g. phones still on the corners (§8 step 5)
		}
	case PhaseIdle, PhasePregame:
		s.updateKickoff(now, false, r)
	}
	s.inactivity(now, r)
}

func leader(g *Game) Side {
	switch {
	case g.Home > g.Away:
		return Home
	case g.Away > g.Home:
		return Away
	}
	return ""
}
