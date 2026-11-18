package engine

import "testing"

func TestWinner(t *testing.T) {
	long := Rules{WinScore: 10, WinBy: 2, MaxScore: 15}
	short := Rules{WinScore: 6, WinBy: 1, MaxScore: 6}
	official := Rules{WinScore: 5, WinBy: 2, MaxScore: 8}
	noCap := Rules{WinScore: 10, WinBy: 2}

	tests := []struct {
		name       string
		rules      Rules
		home, away int
		want       Side
	}{
		{"long: not yet", long, 9, 8, ""},
		{"long: clean win", long, 10, 3, Home},
		{"long: needs lead", long, 10, 9, ""},
		{"long: lead reached", long, 9, 11, Away},
		{"long: cap at 15", long, 15, 14, Home},
		{"short: first to 6", short, 6, 5, Home},
		{"official: 5:4 goes on", official, 5, 4, ""},
		{"official: 6:4 wins", official, 6, 4, Home},
		{"official: cap at 8", official, 7, 8, Away},
		{"no cap: 14:13 goes on", noCap, 14, 13, ""},
		{"no cap: 16:14 wins", noCap, 16, 14, Home},
		{"start", long, 0, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rules.Winner(tt.home, tt.away); got != tt.want {
				t.Errorf("Winner(%d, %d) = %q, want %q (target %d)",
					tt.home, tt.away, got, tt.want, tt.rules.ScoreTarget(tt.home, tt.away))
			}
		})
	}
}
