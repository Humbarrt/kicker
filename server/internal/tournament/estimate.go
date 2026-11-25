package tournament

import (
	"math"
	"sort"
	"time"

	"github.com/Humbarrt/kicker/server/internal/engine"
)

// History is what this kicker's finished games tell about game length (spec 09 §4). Zero values
// mean "not enough games yet" and the defaults are used.
type History struct {
	SecondsPerGoal float64 // play time per goal
	LoserShare     float64 // the loser's goals ÷ the winner's
}

// Defaults without history: 22 s per goal (the default first-to-10 game ≈ 6 min), the loser
// scores 0.6 × the winner, 1 min changeover between games (spec 09 §4, §15).
const (
	DefaultSecondsPerGoal = 22.0
	DefaultLoserShare     = 0.6
	Changeover            = time.Minute
	goldenGoalExtra       = 30 * time.Second
)

// GameLength estimates one game under some rules: the expected goals at the history's pace,
// capped by a time limit (plus a golden goal now and then).
func GameLength(r engine.Rules, h History) time.Duration {
	spg, share := h.SecondsPerGoal, h.LoserShare
	if spg <= 0 {
		spg = DefaultSecondsPerGoal
	}
	if share <= 0 || share >= 1 {
		share = DefaultLoserShare
	}
	var goals float64
	if r.WinScore > 0 {
		target := float64(r.WinScore)
		if r.MaxScore > 0 && r.MaxScore < r.WinScore {
			target = float64(r.MaxScore) // the cap ends the game first (D10)
		}
		loser := share * target
		if r.WinBy > 1 && loser > target-float64(r.WinBy) {
			// Close games run on until the lead is there: about one extra exchange per goal of lead.
			extra := 2 * float64(r.WinBy-1)
			target += extra / 2
			loser += extra / 2
		}
		goals = target + loser
		if r.MaxScore > 0 {
			goals = math.Min(goals, float64(2*r.MaxScore-1))
		}
	}
	d := time.Duration(goals * spg * float64(time.Second))
	if r.TimeLimit > 0 && (goals == 0 || r.TimeLimit < d) {
		d = r.TimeLimit + goldenGoalExtra
	}
	return d
}

// expectedGames per pairing: best of 3 ≈ 2.5, best of 5 ≈ 4.
func expectedGames(bestOf int) float64 {
	switch bestOf {
	case 3:
		return 2.5
	case 5:
		return 4
	}
	return 1
}

// Estimate is what the setup shows per format option (D11).
type Estimate struct {
	Pairings        int           // pairings to play (the maximum where it depends on results)
	UpTo            bool          // Pairings is a maximum (double knockout: the bracket reset)
	Games           float64       // expected games
	Duration        time.Duration // expected time
	MinGamesPerTeam int           // pairings every team plays at least
}

// EstimateSetup estimates a whole tournament; rules are the game rules of each stage.
func EstimateSetup(s Setup, rules []engine.Rules, h History) Estimate {
	var e Estimate
	n := len(s.Teams)
	if s.Stages[0].Format == MonsterDYP {
		n = len(s.Players)
	}
	minGames := -1
	for i, st := range s.Stages {
		pairings, upTo, least := stageSize(st, n)
		e.Pairings += pairings
		e.UpTo = e.UpTo || upTo
		bo := st.BestOf
		if bo == 0 {
			bo = 1
		}
		games := float64(pairings) * expectedGames(bo)
		e.Games += games
		r := rules[min(i, len(rules)-1)]
		e.Duration += time.Duration(games * float64(GameLength(r, h)+Changeover))
		if i == 0 {
			minGames = least
		}
		n = qualifierCount(st, n)
	}
	e.MinGamesPerTeam = minGames
	return e
}

// stageSize returns a stage's pairings for n entrants, whether that's a maximum, and the fewest
// pairings any entrant plays in it.
func stageSize(st Stage, n int) (pairings int, upTo bool, least int) {
	switch st.Format {
	case RoundRobin:
		k := 1
		if st.Double {
			k = 2
		}
		return k * n * (n - 1) / 2, false, k * (n - 1)
	case Groups:
		g := max(1, min(st.Groups, n/2))
		k := 1
		if st.Double {
			k = 2
		}
		least = n
		for i := 0; i < g; i++ {
			size := n / g
			if i < n%g {
				size++
			}
			pairings += k * size * (size - 1) / 2
			least = min(least, k*(size-1))
		}
		return pairings, false, least
	case Knockout:
		pairings = n - 1
		if st.ThirdPlace && n >= 4 {
			pairings++
		}
		return pairings, false, 1
	case DoubleKnockout:
		return 2*n - 1, true, 2
	case Swiss:
		r := st.Rounds
		if r <= 0 {
			r = DefaultSwissRounds(n)
		}
		least = r
		if n%2 == 1 {
			least = r - 1 // the round with the bye
		}
		return r * (n / 2), false, least
	case MonsterDYP:
		r := st.Rounds
		if r <= 0 {
			r = DefaultMonsterRounds(n)
		}
		perRound := n / 4
		sitting := r * (n % 4)
		least = r - (sitting+n-1)/n
		return r * perRound, false, least
	}
	return 0, false, 0
}

func qualifierCount(st Stage, n int) int {
	switch st.Format {
	case Groups:
		g := max(1, min(st.Groups, n/2))
		adv := st.Advance
		if adv <= 0 {
			adv = 2
		}
		q := g * adv
		if st.FillThirds {
			q = nextPow2(q)
		}
		return min(q, n)
	case RoundRobin, Swiss:
		return min(st.Advance, n)
	}
	return n
}

// Option is one way to play a tournament for some number of teams, for the setup page.
type Option struct {
	Name     string // "knockout", "groups_knockout", …
	Stages   []Stage
	Estimate Estimate
}

// Options lists the formats that fit n teams (or players, for Monster DYP), sorted by estimated
// duration (D11). rules are the default game rules.
func Options(n int, monster bool, rules engine.Rules, h History) []Option {
	var out []Option
	add := func(name string, stages ...Stage) {
		s := Setup{Stages: stages}
		if monster {
			s.Players = make([]int64, n)
		} else {
			s.Teams = make([]Team, n)
		}
		out = append(out, Option{Name: name, Stages: stages, Estimate: EstimateSetup(s, []engine.Rules{rules}, h)})
	}
	if monster {
		if n >= 4 {
			add("monster_dyp", Stage{Format: MonsterDYP, BestOf: 1})
		}
		return out
	}
	if n < 2 {
		return nil
	}
	add("knockout", Stage{Format: Knockout, BestOf: 1, ThirdPlace: n >= 4})
	if n >= 3 {
		add("double_knockout", Stage{Format: DoubleKnockout, BestOf: 1})
	}
	if n <= 12 {
		add("round_robin", Stage{Format: RoundRobin, BestOf: 1})
	}
	for g := 2; g <= n/3; g++ {
		if n/g > 6 {
			continue // groups of 3–6
		}
		add("groups_knockout", Stage{Format: Groups, Groups: g, Advance: 2, Seeding: Crossed},
			Stage{Format: Knockout, BestOf: 1, ThirdPlace: true})
	}
	if n >= 6 {
		add("swiss", Stage{Format: Swiss})
		add("swiss_final", Stage{Format: Swiss, Advance: 4}, Stage{Format: Knockout, ThirdPlace: true})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Estimate.Duration < out[j].Estimate.Duration })
	return out
}
