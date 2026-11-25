// Package ratings is the Elo rating as a pure function over the ordered game log (D9): no I/O,
// so a full replay, a replay from an edited game and the load test all run the same code.
//
// Rules (D9):
//   - Two rating keys: solo (1 against 1) and team (every other format: 2v2, 2v1).
//   - The expected result is computed per team: team strength = average of its players' ratings,
//     plus PairBonus for a pair (in the team key), so a pair is expected to beat a single player
//     of the same rating.
//   - Teammates get the same change: K × (result − expected).
//   - The guest (and unknown coins) plays with a fixed rating that never changes.
//   - Not rated: games with an empty side, games without any real player, tournament forfeits
//     and byes (M6). Draws count as half a win.
package ratings

import "math"

// Key is a rating key.
type Key string

const (
	Solo Key = "solo"
	Team Key = "team"
)

// Params are the constants of the rating. K-factor and goal difference are still open details
// of D9, to be tuned with real data.
type Params struct {
	Start     float64 // rating of a player's first game in a key
	Guest     float64 // the guest's fixed rating
	K         float64
	PairBonus float64 // added to a pair's strength in the team key
}

// Default are the parameters until there is real data to tune them.
func Default() Params { return Params{Start: 1000, Guest: 1000, K: 32, PairBonus: 100} }

// Game is a finished game for the rating. Player id 0 = the guest or an unknown coin.
type Game struct {
	ID         int64
	Home, Away []int64
	Winner     string // "home", "away", "" = draw
}

// Rating is a player's rating in one key.
type Rating struct {
	Value float64
	Games int
}

// PlayerKey identifies a rating.
type PlayerKey struct {
	Player int64
	Key    Key
}

// Change is the rating change of one player in one game.
type Change struct {
	GameID, Player int64
	Key            Key
	Before, After  float64
}

// KeyFor returns the rating key of a game, and false if the game is not rated.
func KeyFor(g Game) (Key, bool) {
	if len(g.Home) == 0 || len(g.Away) == 0 {
		return "", false // an empty side
	}
	real := false
	for _, p := range append(append([]int64{}, g.Home...), g.Away...) {
		real = real || p != 0
	}
	if !real {
		return "", false
	}
	if len(g.Home) == 1 && len(g.Away) == 1 {
		return Solo, true
	}
	return Team, true
}

// Apply rates one game: it updates t and returns the changes of the real players.
func (p Params) Apply(t map[PlayerKey]Rating, g Game) []Change {
	key, ok := KeyFor(g)
	if !ok {
		return nil
	}
	rating := func(player int64) float64 {
		if player == 0 {
			return p.Guest
		}
		if r, ok := t[PlayerKey{player, key}]; ok {
			return r.Value
		}
		return p.Start
	}
	strength := func(side []int64) float64 {
		var sum float64
		for _, pl := range side {
			sum += rating(pl)
		}
		s := sum / float64(len(side))
		if key == Team && len(side) == 2 {
			s += p.PairBonus
		}
		return s
	}
	home, away := strength(g.Home), strength(g.Away)
	expected := 1 / (1 + math.Pow(10, (away-home)/400)) // for home
	result := 0.5
	switch g.Winner {
	case "home":
		result = 1
	case "away":
		result = 0
	}
	delta := p.K * (result - expected)

	var out []Change
	seen := map[int64]bool{}
	apply := func(side []int64, d float64) {
		for _, pl := range side {
			if pl == 0 || seen[pl] {
				continue
			}
			seen[pl] = true
			before := rating(pl)
			after := before + d
			r := t[PlayerKey{pl, key}]
			t[PlayerKey{pl, key}] = Rating{Value: after, Games: r.Games + 1}
			out = append(out, Change{GameID: g.ID, Player: pl, Key: key, Before: before, After: after})
		}
	}
	apply(g.Home, delta)
	apply(g.Away, -delta)
	return out
}

// Replay rates games in order, starting from the ratings in t (which it updates).
func (p Params) Replay(t map[PlayerKey]Rating, games []Game) []Change {
	var out []Change
	for _, g := range games {
		out = append(out, p.Apply(t, g)...)
	}
	return out
}
