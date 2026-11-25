package tournament

import "sort"

func (s *State) stageEntrants(stage int, group string) []*Entrant {
	ss := s.Stages[stage]
	if group != "" {
		return ss.GroupEntrants[group]
	}
	return ss.Entrants
}

// qualifiers returns who reaches the stage after a finished one, in seed order for it.
func (s *State) qualifiers(stage int) []*Entrant {
	ss := s.Stages[stage]
	adv := ss.Stage.Advance
	if ss.Stage.Format != Groups {
		var out []*Entrant
		for _, r := range s.Standings(stage, "") {
			if len(out) == adv {
				break
			}
			out = append(out, r.Entrant)
		}
		return out
	}
	if adv <= 0 {
		adv = 2
	}
	// places[k] = the (k+1)-th placed of each group, in group order.
	var places [][]Row
	for _, g := range ss.Groups {
		for k, r := range s.Standings(stage, g) {
			for len(places) <= k {
				places = append(places, nil)
			}
			places[k] = append(places[k], r)
		}
	}
	var q []Row
	for k := 0; k < adv && k < len(places); k++ {
		if ss.Stage.Seeding == Reseed {
			q = append(q, byResult(places[k])...)
			continue
		}
		q = append(q, crossed(places[k], k)...)
	}
	if ss.Stage.FillThirds && adv < len(places) {
		extra := nextPow2(len(q)) - len(q)
		best := byResult(places[adv])
		q = append(q, best[:min(extra, len(best))]...)
	}
	out := make([]*Entrant, len(q))
	for i, r := range q {
		out[i] = r.Entrant
	}
	return out
}

// crossed orders the k-th placed teams so that, in a bracket seeded 1, 2, … from the group
// winners on, nobody meets a team from their own group in the first knockout round: the
// runners-up come in the group order shifted by one, reversed (A1–B2, B1–A2 for two groups).
func crossed(place []Row, k int) []Row {
	if k%2 == 0 || len(place) < 2 {
		return place
	}
	g := len(place)
	out := make([]Row, g)
	for j := 1; j <= g; j++ {
		opp := g + 1 - j // the group whose winner this seed meets
		out[j-1] = place[opp%g]
	}
	return out
}

// byResult orders teams from different groups by their points per game, goal difference, goals.
func byResult(rows []Row) []Row {
	out := append([]Row(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.PerGame() != b.PerGame() {
			return a.PerGame() > b.PerGame()
		}
		if d1, d2 := a.GoalsFor-a.GoalsAgainst, b.GoalsFor-b.GoalsAgainst; d1 != d2 {
			return d1 > d2
		}
		if a.GoalsFor != b.GoalsFor {
			return a.GoalsFor > b.GoalsFor
		}
		return a.Seed < b.Seed
	})
	return out
}

// placements of a finished tournament: from the last stage (spec 09 §9).
func (s *State) placements() []Placement {
	last := len(s.Stages) - 1
	ss := s.Stages[last]
	place := func(n int, e *Entrant) Placement {
		return Placement{Place: n, Team: e.Team, Players: e.Players}
	}
	var out []Placement
	find := func(bracket string, round int) *Pairing {
		var best *Pairing
		for _, p := range ss.Pairings {
			if p.Bracket == bracket && (round == 0 && (best == nil || p.Round > best.Round) || p.Round == round) {
				best = p
			}
		}
		return best
	}
	real := func(e *Entrant) bool { return e != nil && !e.Bye }
	switch ss.Stage.Format {
	case Knockout:
		final := find("W", 0)
		out = append(out, place(1, final.WinnerEntrant()))
		if real(final.LoserEntrant()) {
			out = append(out, place(2, final.LoserEntrant()))
		}
		if third := find("3rd", 1); third != nil {
			if real(third.WinnerEntrant()) {
				out = append(out, place(3, third.WinnerEntrant()))
			}
		} else if final.Round >= 2 {
			for _, p := range ss.Pairings {
				if p.Bracket == "W" && p.Round == final.Round-1 && real(p.LoserEntrant()) {
					out = append(out, place(3, p.LoserEntrant())) // shared 3rd
				}
			}
		}
	case DoubleKnockout:
		gf := find("GF", 1)
		if reset := find("GF", 2); reset != nil && reset.State == Done {
			gf = reset
		}
		out = append(out, place(1, gf.WinnerEntrant()))
		if real(gf.LoserEntrant()) {
			out = append(out, place(2, gf.LoserEntrant()))
		}
		if lf := find("L", 0); lf != nil && real(lf.LoserEntrant()) {
			out = append(out, place(3, lf.LoserEntrant()))
		}
	default:
		for i, r := range s.Standings(last, "") {
			if i == 3 {
				break
			}
			if ss.Stage.Format == MonsterDYP {
				out = append(out, Placement{Place: i + 1, Players: []int64{r.ID}})
				continue
			}
			out = append(out, place(i+1, r.Entrant))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Place != out[j].Place {
			return out[i].Place < out[j].Place
		}
		return out[i].Team < out[j].Team
	})
	return out
}
