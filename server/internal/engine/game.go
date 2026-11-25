package engine

import (
	"slices"
	"time"
)

// EventType is the type column of game_events (spec 02 §11).
type EventType string

const (
	EvGameStarted  EventType = "game_started"
	EvFinished     EventType = "finished"
	EvPaused       EventType = "paused"
	EvResumed      EventType = "resumed"
	EvGoal         EventType = "goal"
	EvGoalVoided   EventType = "goal_voided"
	EvBallReleased EventType = "ball_released"
	EvClockExpired EventType = "clock_expired"
	EvGoldenGoal   EventType = "golden_goal"

	EvAbandoned    EventType = "abandoned"
	EvReinstated   EventType = "reinstated"
	EvSidesSwapped EventType = "sides_swapped"
	EvGameVoided   EventType = "game_voided"
	EvReverted     EventType = "reverted" // takes back the edit TargetSeq (or a revert: the edit counts again)

	EvPlayerJoined   EventType = "player_joined"
	EvPlayerLeft     EventType = "player_left"
	EvPlayerAssigned EventType = "player_assigned"
)

// Source says where an event came from (spec 02 §11).
type Source string

const (
	SourceSensor Source = "sensor" // a module
	SourceTable  Source = "table"  // a live correction on the scoreboard page
	SourceManual Source = "manual" // an edit after the game (M3)
	SourceSystem Source = "system" // the engine itself
)

// End and pause reasons.
const (
	ReasonScore      = "score"
	ReasonTime       = "time"
	ReasonGoldenGoal = "golden_goal"
	ReasonManual     = "manual"
	ReasonTimeout    = "timeout"
	ReasonNewGame    = "new_game"
	ReasonSwitched   = "switched" // another paused game was resumed (D16)
)

// Event is one row of a game's event log. Zero values mean NULL, except where a pointer is used
// because zero is a valid value.
type Event struct {
	Seq       int
	Type      EventType
	Side      Side
	Slot      int    // player_*: 1 or 2
	PlayerID  int64  // player_*: 0 = unknown coin
	CoinUID   string // player_*: the coin, if it was one
	Via       Via    // player_joined
	TargetSeq int
	Value     *int
	Source    Source
	Author    int64 // account id of the member, 0 = none
	Reason    string
	At        time.Time
	PlayTime  *time.Duration
}

// GameStatus is the state column of games.
type GameStatus string

const (
	StatusActive    GameStatus = "active"
	StatusPaused    GameStatus = "paused"
	StatusFinished  GameStatus = "finished"
	StatusAbandoned GameStatus = "abandoned"
)

// Goal is a goal of the current game, voided or not.
type Goal struct {
	Seq      int
	Side     Side // scoring side
	Source   Source
	At       time.Time
	PlayTime time.Duration
	Voided   bool
}

// Game is a game rebuilt from its event log. Everything except the header (ID, PresetID, Rules,
// NextBallSide) is derived from events by Apply, so live play and restart recovery share one path.
type Game struct {
	ID           int64 // 0 until the caller has created the row
	PresetID     int64 // 0 = custom rules
	Rules        Rules
	NextBallSide Side

	FirstBall    Side // the side of the first ball (side of game_started)
	Status       GameStatus
	StartedAt    time.Time
	EndedAt      time.Time
	LastSeq      int
	Goals        []Goal
	Home, Away   int
	Winner       Side // "" while running, or for a draw
	EndReason    string
	ClockExpired bool
	GoldenGoal   bool
	Roster       Roster
	PausedAt     time.Time // while paused or abandoned: when it was paused
	PauseReason  string
	Voided       bool         // game_voided: the game stays but counts for nothing
	Edits        int          // edits of the finished game in force (not reverted)
	Pairing      *GamePairing // a tournament game: its pairing (spec 09)

	played       time.Duration // play time before runningSince
	runningSince time.Time     // zero = clock stopped
}

// Score returns the score of one side.
func (g *Game) Score(s Side) int {
	if s == Home {
		return g.Home
	}
	return g.Away
}

// PlayTime is the game clock: time since kickoff without pauses.
func (g *Game) PlayTime(now time.Time) time.Duration {
	d := g.played
	if !g.runningSince.IsZero() && now.After(g.runningSince) {
		d += now.Sub(g.runningSince)
	}
	return d
}

// ClockRunning reports whether the game clock is running.
func (g *Game) ClockRunning() bool { return !g.runningSince.IsZero() }

// LastGoal returns the index of the last goal that still counts, optionally only of one side.
func (g *Game) lastGoal(side Side) int {
	for i := len(g.Goals) - 1; i >= 0; i-- {
		if !g.Goals[i].Voided && (side == "" || g.Goals[i].Side == side) {
			return i
		}
	}
	return -1
}

// Apply folds one event into the game.
func (g *Game) Apply(e Event) {
	g.LastSeq = e.Seq
	edit := g.Status == StatusFinished && isEditType(e.Type)
	switch e.Type {
	case EvGameStarted:
		g.FirstBall = e.Side
		g.Status = StatusActive
		g.StartedAt = e.At
		g.played = 0
		g.runningSince = e.At
	case EvGoal:
		gl := Goal{Seq: e.Seq, Side: e.Side, Source: e.Source, At: e.At}
		if e.PlayTime != nil {
			gl.PlayTime = *e.PlayTime
		}
		g.Goals = append(g.Goals, gl)
		g.recount()
	case EvGoalVoided:
		for i := range g.Goals {
			if g.Goals[i].Seq == e.TargetSeq {
				g.Goals[i].Voided = true
			}
		}
		g.recount()
	case EvPlayerJoined:
		if e.Side.Valid() && e.Slot >= 1 && e.Slot <= Slots {
			g.Roster.Side(e.Side)[e.Slot-1] = Seat{PlayerID: e.PlayerID, CoinUID: e.CoinUID, Via: e.Via}
		}
	case EvPlayerLeft:
		if e.Side.Valid() && e.Slot >= 1 && e.Slot <= Slots {
			g.Roster.Side(e.Side)[e.Slot-1] = Seat{}
		}
	case EvPlayerAssigned:
		if e.Side.Valid() && e.Slot >= 1 && e.Slot <= Slots {
			g.Roster.Side(e.Side)[e.Slot-1].PlayerID = e.PlayerID
		}
	case EvClockExpired:
		g.ClockExpired = true
	case EvGoldenGoal:
		g.GoldenGoal = true
	case EvPaused:
		g.stopClock(e.At)
		g.Status = StatusPaused
		g.PausedAt, g.PauseReason = e.At, e.Reason
	case EvResumed:
		g.Status = StatusActive
		g.runningSince = e.At
		g.PausedAt, g.PauseReason = time.Time{}, ""
	case EvAbandoned:
		g.Status = StatusAbandoned
	case EvReinstated:
		g.Status = StatusPaused
	case EvFinished:
		g.stopClock(e.At)
		g.Status = StatusFinished
		g.Winner = e.Side
		g.EndReason = e.Reason
		g.EndedAt = e.At
	case EvSidesSwapped:
		g.Home, g.Away = g.Away, g.Home
		g.Roster.Home, g.Roster.Away = g.Roster.Away, g.Roster.Home
		for i := range g.Goals {
			g.Goals[i].Side = swapped(g.Goals[i].Side)
		}
		g.Winner, g.FirstBall, g.NextBallSide = swapped(g.Winner), swapped(g.FirstBall), swapped(g.NextBallSide)
	case EvGameVoided:
		g.Voided = true
	}
	if edit {
		// An edited result: whoever has more goals wins (D2), however the game ended.
		g.Edits++
		g.Winner = ""
		switch {
		case g.Home > g.Away:
			g.Winner = Home
		case g.Away > g.Home:
			g.Winner = Away
		}
	}
}

// swapped is the other side; "" (a draw, no side) stays "".
func swapped(s Side) Side {
	if !s.Valid() {
		return s
	}
	return s.Other()
}

// isEditType: the event types that correct a finished game (D2).
func isEditType(t EventType) bool {
	switch t {
	case EvGoal, EvGoalVoided, EvPlayerJoined, EvPlayerLeft, EvPlayerAssigned, EvSidesSwapped, EvGameVoided:
		return true
	}
	return false
}

// Reverted returns the seqs of the events taken back by `reverted` events that are themselves in
// force. A revert can be reverted, which brings its target back.
func Reverted(events []Event) map[int]bool {
	out := map[int]bool{}
	for i := len(events) - 1; i >= 0; i-- { // targets always come before their revert
		e := events[i]
		if e.Type == EvReverted && !out[e.Seq] {
			out[e.TargetSeq] = true
		}
	}
	return out
}

func (g *Game) stopClock(at time.Time) {
	if !g.runningSince.IsZero() {
		g.played = g.PlayTime(at)
		g.runningSince = time.Time{}
	}
}

func (g *Game) recount() {
	g.Home, g.Away = 0, 0
	for _, gl := range g.Goals {
		if gl.Voided {
			continue
		}
		if gl.Side == Home {
			g.Home++
		} else {
			g.Away++
		}
	}
}

func (g *Game) clone() *Game {
	c := *g
	c.Goals = slices.Clone(g.Goals)
	return &c
}

// Replay rebuilds a game from its header and its stored events.
func Replay(header Game, events []Event) *Game {
	g := &Game{ID: header.ID, PresetID: header.PresetID, Rules: header.Rules, NextBallSide: header.NextBallSide, Pairing: header.Pairing}
	reverted := Reverted(events)
	for _, e := range events {
		if reverted[e.Seq] || e.Type == EvReverted {
			g.LastSeq = e.Seq
			continue
		}
		g.Apply(e)
	}
	return g
}

// Other returns the opposite side.
func (s Side) Other() Side {
	if s == Home {
		return Away
	}
	return Home
}

// Valid reports whether s is home or away.
func (s Side) Valid() bool { return s == Home || s == Away }

// Plays reports whether a player is in the roster.
func (g *Game) Plays(playerID int64) bool {
	for _, side := range []Side{Home, Away} {
		for _, st := range g.Roster.Side(side) {
			if st.PlayerID == playerID {
				return true
			}
		}
	}
	return false
}

// Goals that count.
func (g *Game) scored() bool { return g.Home+g.Away > 0 }
