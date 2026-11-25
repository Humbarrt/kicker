// Package engine is the pure game engine (spec 02). It must not import store, api or modules.
package engine

import (
	"fmt"
	"time"
)

// TimeTie decides what happens when the time limit runs out with a tied score (D10).
type TimeTie string

const (
	TimeTieGoldenGoal TimeTie = "golden_goal"
	TimeTieDraw       TimeTie = "draw"
)

// Rules are the win conditions of one game. They are copied from a preset when the game starts (D10).
type Rules struct {
	WinScore  int           // first to N
	WinBy     int           // required lead
	MaxScore  int           // hard cap; 0 = no cap
	TimeLimit time.Duration // 0 = no time limit
	TimeTie   TimeTie
}

// ScoreTarget returns the score a side needs to win, given the current scores.
// Same formula as v1: min(max(win_score, lower_score + win_by), max_score).
func (r Rules) ScoreTarget(home, away int) int {
	lower := min(home, away)
	target := max(r.WinScore, lower+r.WinBy)
	if r.MaxScore > 0 {
		target = min(target, r.MaxScore)
	}
	return target
}

// Side is one of the two sides of the table (D17).
type Side string

const (
	Home Side = "home"
	Away Side = "away"
)

// Winner returns the winning side once the score target is reached, or "" while the game goes on.
func (r Rules) Winner(home, away int) Side {
	target := r.ScoreTarget(home, away)
	switch {
	case home >= target && home > away:
		return Home
	case away >= target && away > home:
		return Away
	default:
		return ""
	}
}

// Validate checks the rules against the limits of the games table.
func (r Rules) Validate() error {
	switch {
	case r.WinScore < 1 || r.WinScore > 99:
		return fmt.Errorf("%w: win score must be 1–99", ErrInvalidRules)
	case r.WinBy < 1 || r.WinBy > 10:
		return fmt.Errorf("%w: win by must be 1–10", ErrInvalidRules)
	case r.MaxScore != 0 && (r.MaxScore < r.WinScore || r.MaxScore > 99):
		return fmt.Errorf("%w: max score must be empty or between win score and 99", ErrInvalidRules)
	case r.TimeLimit < 0 || r.TimeLimit > 24*time.Hour || r.TimeLimit%time.Second != 0:
		return fmt.Errorf("%w: time limit must be whole seconds, at most 24 h", ErrInvalidRules)
	case r.TimeTie != TimeTieGoldenGoal && r.TimeTie != TimeTieDraw:
		return fmt.Errorf("%w: time tie must be golden_goal or draw", ErrInvalidRules)
	}
	return nil
}
