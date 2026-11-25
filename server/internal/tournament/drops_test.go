package tournament

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// TestDropoutsAndReplacements plays every format with teams dropping out and players being
// replaced at random moments (spec 09 §10). After every change nothing that was played moves:
// no conflicts, played pairings keep their result. Every tournament still ends, no dropped team
// is ever ready to play, and none gets a place.
func TestDropoutsAndReplacements(t *testing.T) {
	setups := map[string]func(n int) Setup{
		"knockout": func(n int) Setup {
			return Setup{Teams: teams(n), Stages: []Stage{{Format: Knockout, ThirdPlace: true}}}
		},
		"double_knockout": func(n int) Setup { return Setup{Teams: teams(n), Stages: []Stage{{Format: DoubleKnockout}}} },
		"round_robin":     func(n int) Setup { return Setup{Teams: teams(n), Stages: []Stage{{Format: RoundRobin, BestOf: 3}}} },
		"groups": func(n int) Setup {
			return Setup{Teams: teams(n), Stages: []Stage{{Format: Groups, Groups: max(2, n/4), Advance: 2, FillThirds: true}, {Format: Knockout, ThirdPlace: true}}}
		},
		"swiss": func(n int) Setup {
			return Setup{Teams: teams(n), Stages: []Stage{{Format: Swiss, Advance: 4}, {Format: Knockout}}}
		},
		"monster_dyp": func(n int) Setup {
			ps := make([]int64, n)
			for i := range ps {
				ps[i] = int64(10 + i)
			}
			return Setup{Players: ps, Stages: []Stage{{Format: MonsterDYP}}, Seed: int64(n)}
		},
	}
	for name, mk := range setups {
		for n := 5; n <= 17; n += 3 {
			for run := 0; run < 8; run++ {
				t.Run(fmt.Sprintf("%s/%d/%d", name, n, run), func(t *testing.T) {
					rng := rand.New(rand.NewPCG(uint64(n), uint64(run*31+len(name))))
					playWithChanges(t, mk(n), rng)
				})
			}
		}
	}
}

func playWithChanges(t *testing.T, s Setup, rng *rand.Rand) {
	t.Helper()
	monster := s.Stages[0].Format == MonsterDYP
	entrants := len(s.Teams)
	if monster {
		entrants = len(s.Players)
	}
	results := map[string]Result{}
	sides := map[string]string{} // pairing → the sides it was played by
	before := map[string]Side{}  // pairing → its winner when played
	nextID := int64(1000)
	derive := func() *State {
		st, err := Derive(s, results)
		if err != nil {
			t.Fatal(err)
		}
		if c := st.Conflicts(copyMap(sides)); len(c) > 0 {
			for _, k := range c {
				t.Logf("%s played by %s, now %s", k, sides[k], st.Pairing(k).Sides())
			}
			t.Fatalf("conflicts %v (drops %v)", c, s.Drops)
		}
		for key, w := range before {
			p := st.Pairing(key)
			if p == nil {
				t.Fatalf("played pairing %s vanished", key)
			}
			if p.State == Skipped && s.Drops != nil {
				continue // annulled
			}
			if p.State != Done || p.Winner != w {
				t.Fatalf("played pairing %s now %s/%v, was won by %v", key, p.State, p.Winner, w)
			}
		}
		return st
	}
	for step := 0; ; step++ {
		st := derive()
		if st.Finished {
			for _, pl := range st.Placements {
				for n, d := range s.Drops {
					_ = d
					if !monster && pl.Team == n {
						t.Errorf("team %d dropped out but placed %d", n, pl.Place)
					}
					if monster && len(pl.Players) == 1 && pl.Players[0] == s.Players[n-1] {
						t.Errorf("player %d dropped out but placed %d", n, pl.Place)
					}
				}
			}
			return
		}
		if step > 5000 {
			t.Fatal("doesn't end")
		}
		// Now and then a dropout (keeping at least 2 teams / 4 players) or a replacement.
		switch x := rng.IntN(20); {
		case x == 0 && len(s.Drops) < entrants-(map[bool]int{true: 4, false: 2})[monster]:
			n := 1 + rng.IntN(entrants)
			d, err := st.DropFor(n)
			if err == nil {
				if s.Drops == nil {
					s.Drops = map[int]Drop{}
				}
				s.Drops[n] = d
				continue
			}
		case x == 1:
			nextID++
			if monster {
				s.Players[rng.IntN(len(s.Players))] = nextID
			} else {
				tm := &s.Teams[rng.IntN(len(s.Teams))]
				tm.Players = append([]int64(nil), tm.Players...)
				tm.Players[rng.IntN(len(tm.Players))] = nextID
			}
			continue
		}
		ready := st.ReadyPairings()
		if len(ready) == 0 {
			t.Fatalf("stuck: nothing ready, not finished (drops %v)", s.Drops)
		}
		for _, p := range ready {
			if p.A.Dropped(p.Stage) || p.B.Dropped(p.Stage) {
				t.Fatalf("%s is ready with a team that dropped out", p.Key)
			}
			for _, sl := range append(append([]int{}, p.A.Slots...), p.B.Slots...) {
				if _, ok := s.Drops[sl]; ok {
					t.Fatalf("%s is ready with a player who dropped out", p.Key)
				}
			}
		}
		p := ready[rng.IntN(len(ready))]
		var r Result
		need := p.BestOf/2 + 1
		for wa, wb := 0, 0; wa < need && wb < need; {
			if rng.IntN(2) == 0 {
				r.Games = append(r.Games, GameResult{Winner: A, GoalsA: 5, GoalsB: rng.IntN(5)})
				wa++
			} else {
				r.Games = append(r.Games, GameResult{Winner: B, GoalsA: rng.IntN(5), GoalsB: 5})
				wb++
			}
		}
		results[p.Key] = r
		sides[p.Key] = p.Sides()
		if wa := countWins(r, A); wa >= need {
			before[p.Key] = A
		} else {
			before[p.Key] = B
		}
	}
}

func countWins(r Result, s Side) int {
	n := 0
	for _, g := range r.Games {
		if g.Winner == s {
			n++
		}
	}
	return n
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// TestDropRules checks the three kinds of dropout on small examples.
func TestDropRules(t *testing.T) {
	win := func(w Side) Result {
		if w == A {
			return Result{Games: []GameResult{{Winner: A, GoalsA: 5, GoalsB: 1}}}
		}
		return Result{Games: []GameResult{{Winner: B, GoalsA: 1, GoalsB: 5}}}
	}
	t.Run("knockout walkovers", func(t *testing.T) {
		s := Setup{Teams: teams(4), Stages: []Stage{{Format: Knockout}}}
		st, _ := Derive(s, nil)
		d, err := st.DropFor(1)
		if err != nil {
			t.Fatal(err)
		}
		s.Drops = map[int]Drop{1: d}
		st, _ = Derive(s, nil)
		// Seed 1 meets seed 4 in round 1: 4 goes through without playing.
		var p *Pairing
		for _, x := range st.Stages[0].Pairings {
			if x.Round == 1 && (x.A.Team == 1 || x.B.Team == 1) {
				p = x
			}
		}
		if p == nil || p.State != Done || !p.Walkover || p.WinnerEntrant().Team != 4 {
			t.Fatalf("pairing of the dropped team: %+v", p)
		}
		if _, err := st.DropFor(1); err != ErrAlreadyDropped {
			t.Errorf("second drop: %v", err)
		}
	})
	t.Run("round robin annul and forfeit", func(t *testing.T) {
		s := Setup{Teams: teams(4), Stages: []Stage{{Format: RoundRobin}}}
		st, _ := Derive(s, nil)
		results := map[string]Result{}
		// Team 4 plays one of its three pairings (and loses): fewer than half → annulled.
		for _, p := range st.Stages[0].Pairings {
			if p.A.Team == 4 || p.B.Team == 4 {
				if p.A.Team == 4 {
					results[p.Key] = win(B)
				} else {
					results[p.Key] = win(A)
				}
				break
			}
		}
		st, _ = Derive(s, results)
		d, _ := st.DropFor(4)
		if !d.Annul {
			t.Fatalf("1 of 3 played: %+v", d)
		}
		s.Drops = map[int]Drop{4: d}
		st, _ = Derive(s, results)
		rows := st.Standings(0, "")
		if last := rows[len(rows)-1]; last.Entrant.Team != 4 || !last.Dropped {
			t.Errorf("dropped team not last: %+v", last)
		}
		for _, r := range rows {
			if r.Played != 0 {
				t.Errorf("team %d has %d pairings counted after the annulment", r.ID, r.Played)
			}
		}
		// Two of three played: the last one is a forfeit.
		s.Drops = nil
		n := 0
		for _, p := range st.Stages[0].Pairings {
			if (p.A.Team == 4 || p.B.Team == 4) && n < 2 {
				results[p.Key] = win(A)
				n++
			}
		}
		st, _ = Derive(s, results)
		d, _ = st.DropFor(4)
		if d.Annul {
			t.Fatalf("2 of 3 played: %+v", d)
		}
		s.Drops = map[int]Drop{4: d}
		st, _ = Derive(s, results)
		for _, p := range st.Stages[0].Pairings {
			if (p.A.Team == 4 || p.B.Team == 4) && p.State != Done {
				t.Errorf("%s: %s", p.Key, p.State)
			}
		}
	})
	t.Run("groups: the next best qualifies", func(t *testing.T) {
		s := Setup{Teams: teams(6), Stages: []Stage{{Format: Groups, Groups: 2, Advance: 2}, {Format: Knockout}}}
		s.Drops = map[int]Drop{1: {Stage: 0, Annul: true}}
		st, _ := Derive(s, nil)
		results := map[string]Result{}
		for !st.Stages[0].Done {
			for _, p := range st.ReadyPairings() {
				results[p.Key] = win(A)
			}
			st, _ = Derive(s, results)
		}
		for _, e := range st.Stages[1].Entrants {
			if e.Team == 1 {
				t.Fatal("the dropped team qualified")
			}
		}
		if len(st.Stages[1].Entrants) != 4 {
			t.Errorf("%d qualified", len(st.Stages[1].Entrants))
		}
	})
}
