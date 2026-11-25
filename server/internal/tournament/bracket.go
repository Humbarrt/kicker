package tournament

// A bracket is a list of pairings whose sides come from other pairings: the winner or the loser
// of pairing X. Byes run through it on their own: a pairing against a bye is decided at once,
// and the loser of such a pairing is a bye again (spec 09 §6.2, §6.3).

type srcKind int

const (
	fixedSrc srcKind = iota
	winnerOf
	loserOf
)

type src struct {
	kind srcKind
	e    *Entrant // fixedSrc: the team, or a bye
	p    *Pairing // winnerOf / loserOf
}

func fixed(e *Entrant) src  { return src{kind: fixedSrc, e: e} }
func winner(p *Pairing) src { return src{kind: winnerOf, p: p} }
func loser(p *Pairing) src  { return src{kind: loserOf, p: p} }

var bye = &Entrant{Bye: true}

// entrant resolves a source: nil while it isn't known yet.
func (s src) entrant() *Entrant {
	switch s.kind {
	case fixedSrc:
		return s.e
	case winnerOf:
		if s.p.State == ByeState && s.p.Winner == NoSide {
			return bye // bye against bye
		}
		if !decided(s.p) {
			return nil
		}
		return s.p.WinnerEntrant()
	case loserOf:
		if s.p.State == ByeState {
			return bye // nobody lost
		}
		if !decided(s.p) {
			return nil
		}
		return s.p.LoserEntrant()
	}
	return nil
}

func (s src) depth() int {
	if s.kind == fixedSrc {
		return 0
	}
	return s.p.Depth
}

// match adds a bracket pairing fed by two sources.
func (b *builder) match(bracket string, round, pos int, x, y src) *Pairing {
	p := &Pairing{Key: b.key(bracket, round, pos), Bracket: bracket, Round: round, Pos: pos,
		A: x.entrant(), B: y.entrant(), Depth: max(x.depth(), y.depth()) + 1}
	return b.add(p)
}

// bracketOrder returns the seeds in bracket order for a bracket of size n (a power of 2), so that
// 1 and 2 can only meet in the final: 1, 8, 4, 5, 2, 7, 3, 6 for 8.
func seedOrder(n int) []int {
	order := []int{1}
	for len(order) < n {
		next := make([]int, 0, 2*len(order))
		for _, s := range order {
			next = append(next, s, 2*len(order)+1-s)
		}
		order = next
	}
	return order
}

func nextPow2(n int) int {
	p := 1
	for p < n {
		p *= 2
	}
	return p
}

// knockout builds a single or double knockout over the entrants in seed order. Seeds beyond
// the number of entrants are byes, so the top seeds get them (D11).
func (b *builder) knockout(entrants []*Entrant, double bool) {
	size := max(2, nextPow2(len(entrants)))
	var round []*Pairing
	order := seedOrder(size)
	for i := 0; i < size; i += 2 {
		x, y := bye, bye
		if order[i] <= len(entrants) {
			x = entrants[order[i]-1]
		}
		if order[i+1] <= len(entrants) {
			y = entrants[order[i+1]-1]
		}
		round = append(round, b.match("W", 1, i/2+1, fixed(x), fixed(y)))
	}
	winners := [][]*Pairing{round}
	for r := 2; len(round) > 1; r++ {
		var next []*Pairing
		for i := 0; i < len(round); i += 2 {
			next = append(next, b.match("W", r, i/2+1, winner(round[i]), winner(round[i+1])))
		}
		round = next
		winners = append(winners, round)
	}
	final := round[0]
	if !double {
		if b.ss.Stage.ThirdPlace && len(winners) >= 2 {
			semis := winners[len(winners)-2]
			b.match("3rd", 1, 1, loser(semis[0]), loser(semis[1]))
		}
		return
	}

	// Losers bracket: the losers of winners round 1 play each other, then each later winners
	// round drops its losers in against the survivors – in reverse order every other round, so
	// rematches come as late as possible – and the survivors play each other again.
	var lbFinal src
	if len(winners) == 1 {
		lbFinal = loser(winners[0][0]) // 2 teams: the loser of the only game is the losers' champion
	} else {
		var lr []src
		w1 := winners[0]
		r := 1
		var cur []*Pairing
		for i := 0; i < len(w1); i += 2 {
			cur = append(cur, b.match("L", r, i/2+1, loser(w1[i]), loser(w1[i+1])))
		}
		for _, p := range cur {
			lr = append(lr, winner(p))
		}
		for j := 1; j < len(winners); j++ {
			drop := winners[j]
			r++
			cur = nil
			for i := range lr {
				d := drop[i]
				if j%2 == 1 {
					d = drop[len(drop)-1-i]
				}
				cur = append(cur, b.match("L", r, i+1, lr[i], loser(d)))
			}
			lr = lr[:0]
			for _, p := range cur {
				lr = append(lr, winner(p))
			}
			if len(lr) > 1 {
				r++
				cur = nil
				for i := 0; i < len(lr); i += 2 {
					cur = append(cur, b.match("L", r, i/2+1, lr[i], lr[i+1]))
				}
				lr = lr[:0]
				for _, p := range cur {
					lr = append(lr, winner(p))
				}
			}
		}
		lbFinal = lr[0]
	}
	gf := b.match("GF", 1, 1, winner(final), lbFinal)
	// The bracket reset: only if the losers-bracket team won the first grand final (D11).
	reset := &Pairing{Key: b.key("GF", 2, 1), Bracket: "GF", Round: 2, Pos: 1, Depth: gf.Depth + 1}
	if decided(gf) {
		// A is the higher seed, not necessarily the winners-bracket team: compare with it.
		if w := gf.WinnerEntrant(); w != nil && !sameEntrant(w, final.WinnerEntrant()) {
			reset.A, reset.B = gf.A, gf.B
		} else {
			reset.State = Skipped
		}
	}
	if reset.State == Skipped {
		reset.Stage, reset.BestOf = b.stage, b.ss.Stage.BestOf
		b.ss.Pairings = append(b.ss.Pairings, reset)
		return
	}
	b.add(reset)
}

func sameEntrant(x, y *Entrant) bool {
	if x == nil || y == nil {
		return x == y
	}
	if x.Team != 0 || y.Team != 0 {
		return x.Team == y.Team
	}
	if len(x.Players) != len(y.Players) {
		return false
	}
	for i := range x.Players {
		if x.Players[i] != y.Players[i] {
			return false
		}
	}
	return true
}
