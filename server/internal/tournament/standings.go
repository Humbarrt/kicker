package tournament

import "sort"

// TieBreak orders competitors with the same points (spec 09 §9).
type TieBreak string

const (
	HeadToHead TieBreak = "head_to_head" // points in the pairings between the tied ones
	GameDiff   TieBreak = "game_diff"    // games won − lost (best of n)
	GoalDiff   TieBreak = "goal_diff"
	GoalsFor   TieBreak = "goals_for"
	Buchholz   TieBreak = "buchholz" // sum of the opponents' points
	SeedOrder  TieBreak = "seed"     // always applied last
)

// DefaultTieBreaks is the order of spec 09 §9.
var DefaultTieBreaks = []TieBreak{HeadToHead, GameDiff, GoalDiff, GoalsFor, Buchholz, SeedOrder}

// Row is one competitor in a ranking: a team, or a player (Monster DYP).
type Row struct {
	Entrant      *Entrant
	ID           int64 // team id, or player id
	Seed         int   // place in the stage's entrant order, from 1
	Played       int   // pairings, incl. byes and walkovers
	Won, Lost    int
	Points       int
	GamesWon     int
	GamesLost    int
	GoalsFor     int
	GoalsAgainst int
	Buchholz     int
	Dropped      bool // dropped out: ranked last, doesn't qualify
	opponents    []int64
	h2h          int // within a tie
}

// PerGame is the points per pairing played (groups rank by it, D11).
func (r Row) PerGame() float64 {
	if r.Played == 0 {
		return 0
	}
	return float64(r.Points) / float64(r.Played)
}

// rank ranks the competitors of some pairings. byPlayer ranks the players of each side
// separately (Monster DYP); perGame ranks by points per pairing played.
func rank(entrants []*Entrant, pairings []*Pairing, byPlayer, perGame bool, tbs []TieBreak, stage int) []Row {
	rows := map[int64]*Row{}
	var order []int64
	for i, e := range entrants {
		for _, id := range ids(e, byPlayer) {
			if _, ok := rows[id]; !ok {
				rows[id] = &Row{Entrant: e, ID: id, Seed: i + 1}
				order = append(order, id)
			}
		}
	}
	type meeting struct {
		x, y   int64 // competitors on opposite sides
		winner int64 // 0: not decided by play
	}
	var meetings []meeting
	for _, p := range pairings {
		if p.State != Done && !(p.State == ByeState && p.Winner != NoSide) {
			continue
		}
		w, l := p.WinnerEntrant(), p.LoserEntrant()
		if p.State == ByeState { // a Swiss bye counts as a win in the standings (D11)
			for _, id := range ids(w, byPlayer) {
				if r := rows[id]; r != nil {
					r.Played++
					r.Won++
					r.Points++
				}
			}
			continue
		}
		wg, lg := p.WinsA, p.WinsB
		wGoals, lGoals := p.GoalsA, p.GoalsB
		if p.Winner == B {
			wg, lg, wGoals, lGoals = lg, wg, lGoals, wGoals
		}
		for _, side := range []struct {
			e      *Entrant
			won    bool
			gw, gl int
			gf, ga int
			other  *Entrant
		}{{w, true, wg, lg, wGoals, lGoals, l}, {l, false, lg, wg, lGoals, wGoals, w}} {
			for _, id := range ids(side.e, byPlayer) {
				r := rows[id]
				if r == nil {
					continue
				}
				r.Played++
				if side.won {
					r.Won++
					r.Points++
				} else {
					r.Lost++
				}
				r.GamesWon += side.gw
				r.GamesLost += side.gl
				r.GoalsFor += side.gf
				r.GoalsAgainst += side.ga
				r.opponents = append(r.opponents, ids(side.other, byPlayer)...)
			}
		}
		for _, x := range ids(w, byPlayer) {
			for _, y := range ids(l, byPlayer) {
				meetings = append(meetings, meeting{x, y, x})
			}
		}
	}
	for _, r := range rows {
		for _, o := range r.opponents {
			if or := rows[o]; or != nil {
				r.Buchholz += or.Points
			}
		}
	}
	list := make([]*Row, 0, len(order))
	for _, id := range order {
		list = append(list, rows[id])
	}
	primary := func(r *Row) float64 {
		if perGame {
			return r.PerGame()
		}
		return float64(r.Points)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if a, b := primary(list[i]), primary(list[j]); a != b {
			return a > b
		}
		return list[i].Seed < list[j].Seed
	})
	// Ties: head to head is counted among the tied ones only.
	for i := 0; i < len(list); {
		j := i + 1
		for j < len(list) && primary(list[j]) == primary(list[i]) {
			j++
		}
		if j-i > 1 {
			tied := map[int64]*Row{}
			for _, r := range list[i:j] {
				r.h2h = 0
				tied[r.ID] = r
			}
			for _, m := range meetings {
				if tied[m.x] != nil && tied[m.y] != nil {
					tied[m.winner].h2h++
				}
			}
			block := list[i:j]
			sort.SliceStable(block, func(a, b int) bool { return before(block[a], block[b], tbs) })
		}
		i = j
	}
	// Who dropped out of this stage (or before) is ranked last (spec 09 §10).
	out := make([]Row, 0, len(list))
	var dropped []Row
	for _, r := range list {
		if r.Entrant.Dropped(stage) {
			r.Dropped = true
			dropped = append(dropped, *r)
			continue
		}
		out = append(out, *r)
	}
	return append(out, dropped...)
}

func before(a, b *Row, tbs []TieBreak) bool {
	for _, tb := range tbs {
		var x, y int
		switch tb {
		case HeadToHead:
			x, y = a.h2h, b.h2h
		case GameDiff:
			x, y = a.GamesWon-a.GamesLost, b.GamesWon-b.GamesLost
		case GoalDiff:
			x, y = a.GoalsFor-a.GoalsAgainst, b.GoalsFor-b.GoalsAgainst
		case GoalsFor:
			x, y = a.GoalsFor, b.GoalsFor
		case Buchholz:
			x, y = a.Buchholz, b.Buchholz
		case SeedOrder:
			return a.Seed < b.Seed
		}
		if x != y {
			return x > y
		}
	}
	return a.Seed < b.Seed
}

// ids returns who a side counts for: its team, or each of its players.
func ids(e *Entrant, byPlayer bool) []int64 {
	if e == nil || e.Bye {
		return nil
	}
	if byPlayer {
		return e.Players
	}
	return []int64{int64(e.Team)}
}

// Standings ranks one stage – or one group of a groups stage ("" = the whole stage). Knockout
// stages have no standings (nil).
func (s *State) Standings(stage int, group string) []Row {
	if stage < 0 || stage >= len(s.Stages) {
		return nil
	}
	ss := s.Stages[stage]
	f := ss.Stage.Format
	if f == Knockout || f == DoubleKnockout {
		return nil
	}
	var ps []*Pairing
	for _, p := range ss.Pairings {
		if group == "" || p.Bracket == group {
			ps = append(ps, p)
		}
	}
	return rank(s.stageEntrants(stage, group), ps, f == MonsterDYP, f == Groups, s.Setup.TieBreaks, stage)
}
