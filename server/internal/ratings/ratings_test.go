package ratings

import (
	"math"
	"math/rand/v2"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestKeyFor(t *testing.T) {
	for _, c := range []struct {
		g    Game
		key  Key
		rate bool
	}{
		{Game{Home: []int64{1}, Away: []int64{2}}, Solo, true},
		{Game{Home: []int64{1, 3}, Away: []int64{2, 4}}, Team, true},
		{Game{Home: []int64{1, 3}, Away: []int64{2}}, Team, true}, // 2v1
		{Game{Home: []int64{1}, Away: nil}, "", false},            // empty side
		{Game{Home: []int64{0}, Away: []int64{0, 0}}, "", false},  // guests only
		{Game{Home: []int64{1}, Away: []int64{0}}, Solo, true},    // against the guest
	} {
		key, ok := KeyFor(c.g)
		if key != c.key || ok != c.rate {
			t.Errorf("%+v: %q %v", c.g, key, ok)
		}
	}
}

func TestSoloEqualPlayers(t *testing.T) {
	p := Default()
	tab := map[PlayerKey]Rating{}
	ch := p.Apply(tab, Game{ID: 1, Home: []int64{1}, Away: []int64{2}, Winner: "home"})
	if len(ch) != 2 || !near(ch[0].After, 1016) || !near(ch[1].After, 984) || ch[0].Key != Solo {
		t.Fatalf("equal players, K 32: ±16: %+v", ch)
	}
	if tab[PlayerKey{1, Solo}].Games != 1 || !near(tab[PlayerKey{2, Solo}].Value, 984) {
		t.Errorf("table: %+v", tab)
	}
	// A draw between equal players changes nothing; the stronger one loses points.
	ch = p.Apply(tab, Game{ID: 2, Home: []int64{1}, Away: []int64{2}})
	if !(ch[0].After < ch[0].Before) || !near(ch[0].After-ch[0].Before, -(ch[1].After-ch[1].Before)) {
		t.Errorf("draw: %+v", ch)
	}
}

func TestTeamsAndGuest(t *testing.T) {
	p := Default()
	tab := map[PlayerKey]Rating{{1, Team}: {1100, 5}, {3, Team}: {900, 5}}
	ch := p.Apply(tab, Game{ID: 1, Home: []int64{1, 3}, Away: []int64{2, 0}, Winner: "away"})
	// Both pairs average 1000 (+ the same pair bonus): expected 0.5 each, so ±16, the same for
	// teammates. The guest (0) gets no change and keeps its rating.
	if len(ch) != 3 {
		t.Fatalf("changes: %+v", ch)
	}
	for _, c := range ch {
		want := -16.0
		if c.Player == 2 {
			want = 16
		}
		if !near(c.After-c.Before, want) || c.Key != Team {
			t.Errorf("player %d: %+v", c.Player, c)
		}
	}
	if _, ok := tab[PlayerKey{0, Team}]; ok {
		t.Error("the guest has no rating entry")
	}
	if tab[PlayerKey{2, Team}].Games != 1 || tab[PlayerKey{1, Team}].Games != 6 {
		t.Errorf("games counted: %+v", tab)
	}
	if _, ok := tab[PlayerKey{2, Solo}]; ok {
		t.Error("keys are separate")
	}
}

func TestPairBonus(t *testing.T) {
	p := Default()
	tab := map[PlayerKey]Rating{}
	// 2 against 1, all new: the pair is expected to win, so winning gains less than losing costs.
	ch := p.Apply(tab, Game{ID: 1, Home: []int64{1, 2}, Away: []int64{3}, Winner: "home"})
	gain := ch[0].After - ch[0].Before
	if !(gain > 0 && gain < 16) || ch[0].Key != Team {
		t.Errorf("pair win: %+v", ch)
	}
	if !near(ch[2].After-ch[2].Before, -gain) {
		t.Errorf("zero sum per side: %+v", ch)
	}
}

func TestNotRated(t *testing.T) {
	tab := map[PlayerKey]Rating{}
	if ch := Default().Apply(tab, Game{ID: 1, Home: []int64{1}, Winner: "home"}); ch != nil || len(tab) != 0 {
		t.Errorf("empty side: %+v", ch)
	}
}

// Replaying in pieces gives the same result as one replay: the basis of "replay from an edited
// game".
func TestReplayInPieces(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	var games []Game
	for i := range 500 {
		pick := func() int64 { return int64(r.IntN(8)) } // 0 = guest
		g := Game{ID: int64(i + 1), Home: []int64{pick()}, Away: []int64{pick()}}
		if r.IntN(2) == 0 {
			g.Home = append(g.Home, pick())
		}
		if g.Home[0] == g.Away[0] {
			continue
		}
		g.Winner = []string{"home", "away", ""}[r.IntN(3)]
		games = append(games, g)
	}
	p := Default()
	all := map[PlayerKey]Rating{}
	want := p.Replay(all, games)

	part := map[PlayerKey]Rating{}
	got := p.Replay(part, games[:200])
	got = append(got, p.Replay(part, games[200:])...)
	if len(got) != len(want) {
		t.Fatalf("%d vs %d changes", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("change %d: %+v vs %+v", i, got[i], want[i])
		}
	}
	for k, v := range all {
		if part[k] != v {
			t.Errorf("%+v: %+v vs %+v", k, part[k], v)
		}
	}
}
