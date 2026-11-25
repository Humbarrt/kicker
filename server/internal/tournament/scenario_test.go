package tournament

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Scenario files (testdata/*.txt), one command per line, # comments. Teams are named by their
// seed (team id):
//
//	teams 5
//	stage knockout third_place best_of=3     (also: double_knockout, round_robin double,
//	                                          groups groups=2 advance=2 seeding=reseed fill_thirds,
//	                                          swiss rounds=3 advance=4)
//	expect ready 2-3 4-5                     the ready pairings (any order)
//	expect next 4-5 [after 2-3]              the suggestion (after a pairing that just ended)
//	play 4-5 5 [10:7]                        one game of the pairing of 4 and 5, won by 5
//	walkover 4-5 4                           4 advances without playing
//	expect state s1-GF-2-1 skipped           a pairing's state by key
//	expect standings 1 A 3 1 2               stage 1, group A ("-" = whole stage): ranking by team
//	expect placements 1:3 2:1 3:2 3:4
//	expect finished | expect running
func TestScenarios(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.txt")
	if len(files) == 0 {
		t.Fatal("no scenarios")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) { runScenario(t, f) })
	}
}

func runScenario(t *testing.T, path string) {
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	var setup Setup
	results := map[string]Result{}
	derive := func(line int) *State {
		st, err := Derive(setup, results)
		if err != nil {
			t.Fatalf("line %d: %v", line, err)
		}
		return st
	}
	sc := bufio.NewScanner(fh)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(strings.SplitN(sc.Text(), "#", 2)[0])
		if text == "" {
			continue
		}
		f := strings.Fields(text)
		fail := func(format string, args ...any) {
			t.Helper()
			t.Fatalf("line %d (%s): %s", line, text, fmt.Sprintf(format, args...))
		}
		switch f[0] {
		case "teams":
			n, _ := strconv.Atoi(f[1])
			setup.Teams = teams(n)
		case "stage":
			st := Stage{Format: Format(f[1])}
			for _, o := range f[2:] {
				k, v, _ := strings.Cut(o, "=")
				n, _ := strconv.Atoi(v)
				switch k {
				case "third_place":
					st.ThirdPlace = true
				case "double":
					st.Double = true
				case "fill_thirds":
					st.FillThirds = true
				case "best_of":
					st.BestOf = n
				case "rounds":
					st.Rounds = n
				case "groups":
					st.Groups = n
				case "advance":
					st.Advance = n
				case "seeding":
					st.Seeding = Seeding(v)
				default:
					fail("unknown option %s", k)
				}
			}
			setup.Stages = append(setup.Stages, st)
		case "play", "walkover":
			p := find(derive(line), f[1])
			if p == nil {
				fail("no ready pairing %s", f[1])
			}
			w, _ := strconv.Atoi(f[2])
			side := A
			if p.B.Team == w {
				side = B
			} else if p.A.Team != w {
				fail("%d doesn't play in %s", w, f[1])
			}
			r := results[p.Key]
			if f[0] == "walkover" {
				r.Walkover = side
			} else {
				g := GameResult{Winner: side, GoalsA: 10, GoalsB: 5}
				if len(f) > 3 {
					x, y, _ := strings.Cut(f[3], ":")
					wg, _ := strconv.Atoi(x)
					lg, _ := strconv.Atoi(y)
					g.GoalsA, g.GoalsB = wg, lg
				}
				if side == B {
					g.GoalsA, g.GoalsB = g.GoalsB, g.GoalsA
				}
				r.Games = append(r.Games, g)
			}
			results[p.Key] = r
		case "expect":
			st := derive(line)
			switch f[1] {
			case "ready":
				var got []string
				for _, p := range st.ReadyPairings() {
					got = append(got, name(p))
				}
				want := norm(f[2:])
				sort.Strings(got)
				if strings.Join(got, " ") != strings.Join(want, " ") {
					fail("ready %v", got)
				}
			case "next":
				var after *Pairing
				if len(f) > 4 && f[3] == "after" {
					for _, p := range st.Pairings() {
						if name(p) == norm(f[4:5])[0] && p.State == Done {
							after = p
						}
					}
					if after == nil {
						fail("no finished pairing %s", f[4])
					}
				}
				got := st.Suggest(after)
				if got == nil || name(got) != norm(f[2:3])[0] {
					fail("suggested %v", got)
				}
			case "state":
				p := st.Pairing(f[2])
				if p == nil || string(p.State) != f[3] {
					fail("pairing %s: %+v", f[2], p)
				}
			case "standings":
				stage, _ := strconv.Atoi(f[2])
				group := f[3]
				if group == "-" {
					group = ""
				}
				var got []string
				for _, r := range st.Standings(stage-1, group) {
					got = append(got, strconv.FormatInt(r.ID, 10))
				}
				if strings.Join(got, " ") != strings.Join(f[4:], " ") {
					fail("standings %v", got)
				}
			case "placements":
				var got []string
				for _, p := range st.Placements {
					got = append(got, fmt.Sprintf("%d:%d", p.Place, p.Team))
				}
				if strings.Join(got, " ") != strings.Join(f[2:], " ") {
					fail("placements %v", got)
				}
			case "finished":
				if !st.Finished {
					fail("not finished; ready %v", names(st.ReadyPairings()))
				}
			case "running":
				if st.Finished {
					fail("finished")
				}
			default:
				fail("unknown expectation")
			}
		default:
			t.Fatalf("line %d: unknown command %q", line, f[0])
		}
	}
}

func find(st *State, pair string) *Pairing {
	for _, p := range st.ReadyPairings() {
		if name(p) == norm([]string{pair})[0] {
			return p
		}
	}
	return nil
}

// name is "x-y" with the lower team id first.
func name(p *Pairing) string {
	if p.A == nil || p.B == nil {
		return "?"
	}
	x, y := p.A.Team, p.B.Team
	if y < x {
		x, y = y, x
	}
	return fmt.Sprintf("%d-%d", x, y)
}

func names(ps []*Pairing) []string {
	var out []string
	for _, p := range ps {
		out = append(out, name(p))
	}
	return out
}

func norm(pairs []string) []string {
	out := make([]string, len(pairs))
	for i, s := range pairs {
		x, y, _ := strings.Cut(s, "-")
		a, _ := strconv.Atoi(x)
		b, _ := strconv.Atoi(y)
		if b < a {
			a, b = b, a
		}
		out[i] = fmt.Sprintf("%d-%d", a, b)
	}
	sort.Strings(out)
	return out
}
