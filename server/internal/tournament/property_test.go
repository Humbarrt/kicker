package tournament

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/Humbarrt/kicker/server/internal/engine"
)

// play runs a tournament to the end: random ready pairings (out of order, spec 09 §7.2), random
// winners, best-of games until decided. It returns the final state and the pairings played.
func play(t *testing.T, s Setup, rng *rand.Rand) (*State, []*Pairing) {
	t.Helper()
	results := map[string]Result{}
	var played []*Pairing
	for step := 0; ; step++ {
		st, err := Derive(s, results)
		if err != nil {
			t.Fatal(err)
		}
		if st.Finished {
			final := make([]*Pairing, len(played))
			for i, p := range played {
				final[i] = st.Pairing(p.Key)
			}
			return st, final
		}
		ready := st.ReadyPairings()
		if len(ready) == 0 {
			t.Fatalf("stuck after %d pairings: nothing ready, not finished", len(played))
		}
		if step > 5000 {
			t.Fatal("doesn't end")
		}
		p := ready[rng.IntN(len(ready))]
		var r Result
		need := p.BestOf/2 + 1
		for wa, wb := 0, 0; wa < need && wb < need; {
			w := A
			if rng.IntN(2) == 1 {
				w = B
			}
			g := GameResult{Winner: w, GoalsA: 10, GoalsB: rng.IntN(10)}
			if w == B {
				g.GoalsA, g.GoalsB = g.GoalsB, 10
				wb++
			} else {
				wa++
			}
			r.Games = append(r.Games, g)
		}
		results[p.Key] = r
		played = append(played, p)
	}
}

func teams(n int) []Team {
	out := make([]Team, n)
	for i := range out {
		out[i] = Team{Players: []int64{int64(100 + 2*i), int64(101 + 2*i)}}
	}
	return out
}

func check(t *testing.T, s Setup, st *State, played []*Pairing) {
	t.Helper()
	est := EstimateSetup(s, []engine.Rules{{WinScore: 10, WinBy: 1}}, History{})
	if est.UpTo {
		if len(played) > est.Pairings || len(played) < est.Pairings-1 {
			t.Errorf("played %d pairings, estimate up to %d", len(played), est.Pairings)
		}
	} else if len(played) != est.Pairings {
		t.Errorf("played %d pairings, estimate %d", len(played), est.Pairings)
	}
	if len(st.Placements) < 2 || st.Placements[0].Place != 1 || st.Placements[1].Place != 2 {
		t.Errorf("placements %+v", st.Placements)
	}
	// Every pairing played once: the keys are unique.
	seen := map[string]bool{}
	for _, p := range played {
		if seen[p.Key] {
			t.Errorf("pairing %s played twice", p.Key)
		}
		seen[p.Key] = true
	}
}

func TestFormatsPlayThrough(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for n := 2; n <= 33; n++ {
		for _, third := range []bool{false, true} {
			t.Run(fmt.Sprintf("knockout/%d/third=%v", n, third), func(t *testing.T) {
				s := Setup{Teams: teams(n), Stages: []Stage{{Format: Knockout, ThirdPlace: third}}}
				st, played := play(t, s, rng)
				check(t, s, st, played)
				losses := map[int]int{}
				for _, p := range played {
					losses[p.LoserEntrant().Team]++
				}
				for team, l := range losses {
					if l > 1 && !(third && l == 2) {
						t.Errorf("team %d lost %d times", team, l)
					}
				}
			})
		}
		if n >= 2 {
			t.Run(fmt.Sprintf("double_knockout/%d", n), func(t *testing.T) {
				s := Setup{Teams: teams(n), Stages: []Stage{{Format: DoubleKnockout}}}
				st, played := play(t, s, rng)
				check(t, s, st, played)
				losses := map[int]int{}
				out := map[int]bool{}
				for _, p := range played {
					if out[p.A.Team] || out[p.B.Team] {
						t.Fatalf("pairing %s with a team that was out", p.Key)
					}
					l := p.LoserEntrant().Team
					losses[l]++
					if losses[l] == 2 {
						out[l] = true
					}
				}
				champ := st.Placements[0].Team
				if losses[champ] > 1 {
					t.Errorf("champion %d lost %d times", champ, losses[champ])
				}
				for team, l := range losses {
					if team != champ && l != 2 {
						t.Errorf("team %d out after %d losses", team, l)
					}
				}
			})
		}
	}
	for n := 2; n <= 10; n++ {
		t.Run(fmt.Sprintf("round_robin/%d", n), func(t *testing.T) {
			s := Setup{Teams: teams(n), Stages: []Stage{{Format: RoundRobin, BestOf: 3}}}
			st, played := play(t, s, rng)
			check(t, s, st, played)
			met := map[[2]int]int{}
			for _, p := range played {
				met[teamPair(p.A.Team, p.B.Team)]++
			}
			if len(met) != n*(n-1)/2 {
				t.Errorf("%d different pairs met", len(met))
			}
		})
	}
	for n := 6; n <= 24; n++ {
		for g := 2; g <= n/3; g++ {
			for _, seeding := range []Seeding{Crossed, Reseed} {
				t.Run(fmt.Sprintf("groups/%d/%d/%s", n, g, seeding), func(t *testing.T) {
					s := Setup{Teams: teams(n), Stages: []Stage{
						{Format: Groups, Groups: g, Advance: 2, Seeding: seeding, FillThirds: g == 3},
						{Format: Knockout, ThirdPlace: true}}}
					st, played := play(t, s, rng)
					check(t, s, st, played)
					if seeding == Crossed && g == 2 {
						// The first knockout round never pairs two teams of one group.
						group := map[int]string{}
						for name, es := range st.Stages[0].GroupEntrants {
							for _, e := range es {
								group[e.Team] = name
							}
						}
						for _, p := range st.Stages[1].Pairings {
							if p.Bracket == "W" && p.Round == 1 && p.State == Done && group[p.A.Team] == group[p.B.Team] {
								t.Errorf("%s: %d and %d from group %s", p.Key, p.A.Team, p.B.Team, group[p.A.Team])
							}
						}
					}
				})
			}
		}
	}
	for n := 4; n <= 20; n++ {
		t.Run(fmt.Sprintf("swiss/%d", n), func(t *testing.T) {
			s := Setup{Teams: teams(n), Stages: []Stage{{Format: Swiss, Advance: 4}, {Format: Knockout}}}
			st, played := play(t, s, rng)
			check(t, s, st, played)
			met := map[[2]int]bool{}
			for _, p := range st.Stages[0].Pairings {
				if p.State != Done {
					continue
				}
				k := teamPair(p.A.Team, p.B.Team)
				if met[k] {
					t.Errorf("rematch %v in %s", k, p.Key)
				}
				met[k] = true
			}
		})
	}
	for n := 4; n <= 21; n++ {
		t.Run(fmt.Sprintf("monster_dyp/%d", n), func(t *testing.T) {
			ps := make([]int64, n)
			for i := range ps {
				ps[i] = int64(10 + i)
			}
			s := Setup{Players: ps, Stages: []Stage{{Format: MonsterDYP}}, Seed: int64(n)}
			st, played := play(t, s, rng)
			check(t, s, st, played)
			games := map[int64]int{}
			for _, p := range played {
				for _, id := range append(append([]int64{}, p.A.Players...), p.B.Players...) {
					games[id]++
				}
			}
			lo, hi := 1<<30, 0
			for _, id := range ps {
				lo, hi = min(lo, games[id]), max(hi, games[id])
			}
			if hi-lo > 1 || lo < 4 {
				t.Errorf("games per player %d–%d", lo, hi)
			}
		})
	}
}
