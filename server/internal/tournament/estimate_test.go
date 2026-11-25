package tournament

import (
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/engine"
)

func TestGameLength(t *testing.T) {
	for _, c := range []struct {
		r    engine.Rules
		h    History
		want time.Duration
	}{
		{engine.Rules{WinScore: 10, WinBy: 1}, History{}, 352 * time.Second},                             // 16 goals × 22 s ≈ 6 min
		{engine.Rules{WinScore: 10, WinBy: 1, TimeLimit: 5 * time.Minute}, History{}, 330 * time.Second}, // the limit + golden goal
		{engine.Rules{WinScore: 6, WinBy: 1}, History{SecondsPerGoal: 30, LoserShare: 0.5}, 270 * time.Second},
		{engine.Rules{WinScore: 5, WinBy: 2}, History{LoserShare: 0.9}, time.Duration(11.5 * 22 * float64(time.Second))}, // runs on for the lead
		{engine.Rules{WinScore: 10, WinBy: 1, MaxScore: 6}, History{}, time.Duration(9.6 * 22 * float64(time.Second))},
		{engine.Rules{TimeLimit: 4 * time.Minute}, History{}, 270 * time.Second},
	} {
		if got := GameLength(c.r, c.h); got.Round(time.Second) != c.want.Round(time.Second) {
			t.Errorf("%+v %+v: %v, want %v", c.r, c.h, got, c.want)
		}
	}
}

func TestOptions(t *testing.T) {
	opts := Options(10, false, engine.Rules{WinScore: 10, WinBy: 1}, History{})
	if len(opts) < 5 || opts[0].Name != "knockout" {
		t.Fatalf("%+v", opts)
	}
	for i := 1; i < len(opts); i++ {
		if opts[i].Estimate.Duration < opts[i-1].Estimate.Duration {
			t.Errorf("not sorted by duration: %s before %s", opts[i-1].Name, opts[i].Name)
		}
	}
	if m := Options(9, true, engine.Rules{WinScore: 10, WinBy: 1}, History{}); len(m) != 1 || m[0].Estimate.MinGamesPerTeam < 4 {
		t.Errorf("Monster DYP: %+v", m)
	}
}
