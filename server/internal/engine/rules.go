// Package engine is the pure game engine (spec 02). It must not import store, api or modules.
package engine

import "time"

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
