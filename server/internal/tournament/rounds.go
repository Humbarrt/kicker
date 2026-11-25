package tournament

import (
	"math/rand/v2"
	"sort"
)

// roundRobin: everyone meets everyone (twice with Stage.Double), in rounds by the circle method,
// so nobody plays twice in a row where it can be avoided (spec 09 §6.1).
func (b *builder) roundRobin(entrants []*Entrant, group string) {
	if group == "" {
		b.ss.Entrants = entrants
	}
	n := len(entrants)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	if n%2 == 1 {
		idx = append(idx, -1) // the one paired with -1 sits the round out
	}
	m := len(idx)
	cycles := 1
	if b.ss.Stage.Double {
		cycles = 2
	}
	round := 0
	for c := 0; c < cycles; c++ {
		ring := append([]int(nil), idx...)
		for r := 0; r < m-1; r++ {
			round++
			pos := 0
			for i := 0; i < m/2; i++ {
				x, y := ring[i], ring[m-1-i]
				if x < 0 || y < 0 {
					continue
				}
				if c == 1 {
					x, y = y, x
				}
				pos++
				key := b.key("r", round, pos)
				if group != "" {
					key = b.key(group, round, pos)
				}
				b.add(&Pairing{Key: key, Bracket: group, Round: round, Pos: pos, Depth: round,
					A: entrants[x], B: entrants[y]})
			}
			// Rotate everyone but the first.
			last := ring[m-1]
			copy(ring[2:], ring[1:m-1])
			ring[1] = last
		}
	}
}

// groupNames are A, B, C, …
func groupName(i int) string { return string(rune('A' + i)) }

// groups draws the entrants into groups in snake order by seed (1 → A, 2 → B, 3 → C, 4 → C, …)
// and plays a round robin in each (spec 09 §6.4).
func (b *builder) groups(entrants []*Entrant) {
	b.ss.Entrants = entrants
	g := b.ss.Stage.Groups
	if g < 1 {
		g = 1
	}
	g = min(g, len(entrants)/2)
	members := make([][]*Entrant, g)
	for i, e := range entrants {
		k := i % g
		if (i/g)%2 == 1 {
			k = g - 1 - k
		}
		members[k] = append(members[k], e)
	}
	b.ss.GroupEntrants = map[string][]*Entrant{}
	for i, m := range members {
		name := groupName(i)
		b.ss.Groups = append(b.ss.Groups, name)
		b.ss.GroupEntrants[name] = m
		b.roundRobin(m, name)
	}
}

// DefaultSwissRounds is ⌈log₂ teams⌉ (spec 09 §6.5).
func DefaultSwissRounds(teams int) int {
	r := 0
	for 1<<r < teams {
		r++
	}
	return max(r, 1)
}

// swiss plays rounds in which teams with equal points meet, without rematches; an odd team out
// gets a bye that counts as a win (spec 09 §6.5). A round is drawn once the previous one is over.
func (b *builder) swiss(entrants []*Entrant) {
	b.ss.Entrants = entrants
	rounds := b.ss.Stage.Rounds
	if rounds <= 0 {
		rounds = DefaultSwissRounds(len(entrants))
	}
	played := map[[2]int]bool{}
	hadBye := map[int]bool{}
	var done []*Pairing
	for r := 1; r <= rounds; r++ {
		var order []*Entrant
		if r == 1 {
			order = entrants
		} else {
			for _, row := range rank(entrants, done, false, false, []TieBreak{Buchholz, GoalDiff, GoalsFor, SeedOrder}) {
				order = append(order, row.Entrant)
			}
		}
		var byeTeam *Entrant
		if len(order)%2 == 1 {
			for i := len(order) - 1; i >= 0; i-- { // the lowest-ranked team without a bye yet
				if !hadBye[order[i].Team] {
					byeTeam = order[i]
					break
				}
			}
			if byeTeam == nil {
				byeTeam = order[len(order)-1]
			}
			rest := make([]*Entrant, 0, len(order)-1)
			for _, e := range order {
				if e != byeTeam {
					rest = append(rest, e)
				}
			}
			order = rest
		}
		var pairs [][2]*Entrant
		if r == 1 { // top half against bottom half
			h := len(order) / 2
			for i := 0; i < h; i++ {
				pairs = append(pairs, [2]*Entrant{order[i], order[i+h]})
			}
		} else {
			pairs = swissPairs(order, played)
		}
		var round []*Pairing
		for i, pr := range pairs {
			p := b.add(&Pairing{Key: b.key("r", r, i+1), Round: r, Pos: i + 1, Depth: r, A: pr[0], B: pr[1]})
			played[teamPair(pr[0].Team, pr[1].Team)] = true
			round = append(round, p)
		}
		if byeTeam != nil {
			hadBye[byeTeam.Team] = true
			round = append(round, b.add(&Pairing{Key: b.key("r", r, "bye"), Round: r, Pos: len(pairs) + 1,
				Depth: r, A: byeTeam, B: bye}))
		}
		for _, p := range round {
			if !decided(p) {
				return // the next round is drawn when this one is over
			}
		}
		done = append(done, round...)
	}
}

func teamPair(x, y int) [2]int {
	if y < x {
		x, y = y, x
	}
	return [2]int{x, y}
}

// swissPairs pairs teams in ranking order, each with the best-ranked one it hasn't met yet,
// backtracking where that leaves someone without a partner; rematches only if nothing else works.
func swissPairs(order []*Entrant, played map[[2]int]bool) [][2]*Entrant {
	used := make([]bool, len(order))
	var out [][2]*Entrant
	var try func() bool
	try = func() bool {
		i := 0
		for i < len(order) && used[i] {
			i++
		}
		if i == len(order) {
			return true
		}
		used[i] = true
		for j := i + 1; j < len(order); j++ {
			if used[j] || played[teamPair(order[i].Team, order[j].Team)] {
				continue
			}
			used[j] = true
			out = append(out, [2]*Entrant{order[i], order[j]})
			if try() {
				return true
			}
			out = out[:len(out)-1]
			used[j] = false
		}
		used[i] = false
		return false
	}
	if try() {
		return out
	}
	// Everyone has met everyone: pair in order.
	out = out[:0]
	for i := 0; i+1 < len(order); i += 2 {
		out = append(out, [2]*Entrant{order[i], order[i+1]})
	}
	return out
}

// DefaultMonsterRounds gives every player about 5 games (spec 09 §6.6).
func DefaultMonsterRounds(players int) int {
	playing := players - players%4
	if playing <= 0 {
		return 1
	}
	return (5*players + playing - 1) / playing
}

// monster plays rounds of new random 2-player teams (spec 09 §6.6): players that don't fit into
// a match of 2 against 2 sit out in rotation; partners and opponents repeat as rarely as the
// draw allows. Ranked by player.
func (b *builder) monster(players []*Entrant) {
	b.ss.Entrants = players
	rounds := b.ss.Stage.Rounds
	if rounds <= 0 {
		rounds = DefaultMonsterRounds(len(players))
	}
	partners := map[[2]int64]int{}
	opponents := map[[2]int64]int{}
	satOut := map[int64]int{}
	var done []*Pairing
	for r := 1; r <= rounds; r++ {
		// Who sits out: fewest rounds out so far, then the lowest-ranked.
		ranking := rank(players, done, true, false, b.setup.TieBreaks)
		sit := len(players) % 4
		var out []int64
		if sit > 0 {
			cands := make([]Row, len(ranking))
			for i := range ranking { // lowest-ranked first, then fewest rounds out (stable)
				cands[i] = ranking[len(ranking)-1-i]
			}
			sort.SliceStable(cands, func(i, j int) bool { return satOut[cands[i].ID] < satOut[cands[j].ID] })
			for _, c := range cands[:sit] {
				out = append(out, c.ID)
				satOut[c.ID]++
			}
		}
		var in []int64
		for _, e := range players {
			if !contains(out, e.Players[0]) {
				in = append(in, e.Players[0])
			}
		}
		best := monsterDraw(in, partners, opponents, rand.New(rand.NewPCG(uint64(b.setup.Seed), uint64(r))))
		var round []*Pairing
		for i := 0; i+3 < len(best); i += 4 {
			x := &Entrant{Players: []int64{best[i], best[i+1]}}
			y := &Entrant{Players: []int64{best[i+2], best[i+3]}}
			partners[pair64(best[i], best[i+1])]++
			partners[pair64(best[i+2], best[i+3])]++
			for _, p := range x.Players {
				for _, q := range y.Players {
					opponents[pair64(p, q)]++
				}
			}
			round = append(round, b.add(&Pairing{Key: b.key("r", r, i/4+1), Round: r, Pos: i/4 + 1, Depth: r, A: x, B: y}))
		}
		b.ss.SatOut = append(b.ss.SatOut, out)
		for _, p := range round {
			if !decided(p) {
				return
			}
		}
		done = append(done, round...)
	}
}

// monsterDraw shuffles the players 200 times and keeps the order with the fewest repeated
// partners (weighted 10) and opponents; consecutive fours are one match.
func monsterDraw(in []int64, partners, opponents map[[2]int64]int, rng *rand.Rand) []int64 {
	best, bestCost := append([]int64(nil), in...), -1
	cur := append([]int64(nil), in...)
	for t := 0; t < 200; t++ {
		rng.Shuffle(len(cur), func(i, j int) { cur[i], cur[j] = cur[j], cur[i] })
		cost := 0
		for i := 0; i+3 < len(cur); i += 4 {
			cost += 10 * (partners[pair64(cur[i], cur[i+1])] + partners[pair64(cur[i+2], cur[i+3])])
			for _, p := range cur[i : i+2] {
				for _, q := range cur[i+2 : i+4] {
					cost += opponents[pair64(p, q)]
				}
			}
		}
		if bestCost < 0 || cost < bestCost {
			bestCost = cost
			copy(best, cur)
			if cost == 0 {
				break
			}
		}
	}
	return best
}

func pair64(x, y int64) [2]int64 {
	if y < x {
		x, y = y, x
	}
	return [2]int64{x, y}
}

func contains(xs []int64, x int64) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
