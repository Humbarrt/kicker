// Package tournament is the pure core of tournaments (spec 09): formats, seeding, byes, pairing
// order, standings, placements and the duration estimate. No I/O – like engine/, it is tested
// with scenario files, and the service around it stores setups and results.
//
// The state is never stored: Derive computes it from the setup and the pairing results, so an
// edited game or a rebuilt bracket just means deriving again (spec 09 §13).
package tournament

import (
	"fmt"
	"sort"
	"strconv"
)

// Format is how one stage is played (spec 09 §6).
type Format string

const (
	RoundRobin     Format = "round_robin"
	Groups         Format = "groups" // round robin in groups; the next stage takes the best of each
	Knockout       Format = "knockout"
	DoubleKnockout Format = "double_knockout"
	Swiss          Format = "swiss"
	MonsterDYP     Format = "monster_dyp" // rounds of new random 2-player teams, ranked by player
)

// Seeding is how the qualifiers of a groups stage are placed in the next stage (spec 09 §6.4).
type Seeding string

const (
	Crossed Seeding = "crossed" // A1 vs B2, B1 vs A2, …
	Reseed  Seeding = "reseed"  // by the group results: all winners first, then the runners-up, …
)

// Stage is one format applied to the teams that reach it.
type Stage struct {
	Format     Format  `json:"format"`
	BestOf     int     `json:"best_of,omitempty"`     // games per pairing: 1 (default), 3, 5
	Rounds     int     `json:"rounds,omitempty"`      // Swiss and Monster DYP; 0 = the default
	Double     bool    `json:"double,omitempty"`      // round robin / groups: everyone meets twice
	ThirdPlace bool    `json:"third_place,omitempty"` // knockout: a 3rd place game instead of a shared 3rd
	Groups     int     `json:"groups,omitempty"`      // groups: how many
	Advance    int     `json:"advance,omitempty"`     // how many reach the next stage: per group (groups), in total (round robin, Swiss)
	FillThirds bool    `json:"fill_thirds,omitempty"` // groups: fill the next bracket up to a power of 2 with the best of the next place
	Seeding    Seeding `json:"seeding,omitempty"`     // groups → next stage
}

// Team is a fixed team (1 or 2 players).
type Team struct {
	Players []int64
}

// Setup is everything a tournament is derived from.
type Setup struct {
	Teams     []Team     // in seed order: Teams[0] is seed 1 (team id 1). Empty for Monster DYP
	Players   []int64    // Monster DYP: the players in seed order
	Stages    []Stage    // at least one; only the last may be a knockout
	TieBreaks []TieBreak // nil = DefaultTieBreaks
	Seed      int64      // for the random draws (Monster DYP partners)
	// Drops are the teams (by team id) or, in Monster DYP, the players (by their place in
	// Players, from 1) that dropped out (spec 09 §10).
	Drops map[int]Drop
}

// Drop is when a team or player dropped out and what happens to its pairings (spec 09 §10):
//   - from then on its undecided pairings are walkovers for the opponent (knockouts: also the
//     ones it would have reached; groups and round robin: forfeits);
//   - with Annul (groups, round robin: fewer than half of its pairings played) all its pairings
//     of that stage don't count;
//   - Swiss and Monster DYP don't draw it into rounds after Round;
//   - it doesn't qualify for a later stage and is ranked last in the stage it dropped out of.
//
// A team that dropped out in a later stage keeps its results and qualification from earlier
// ones, so nothing that was already played is drawn again.
type Drop struct {
	Stage int  `json:"stage"`           // index in Stages
	Round int  `json:"round,omitempty"` // Swiss, Monster DYP: the rounds drawn at the time
	Annul bool `json:"annul,omitempty"`
	Seq   int  `json:"seq"` // 1, 2, …: when both sides dropped out, the one that dropped out later goes on
}

// Side is A or B of a pairing. A is the higher seed (spec 09 §7.3).
type Side int

const (
	NoSide Side = iota
	A
	B
)

func (s Side) Other() Side {
	switch s {
	case A:
		return B
	case B:
		return A
	}
	return NoSide
}

// GameResult is one finished game of a pairing.
type GameResult struct {
	Winner         Side
	GoalsA, GoalsB int
}

// Result is what happened in a pairing: its games, or a walkover (spec 09 §10).
type Result struct {
	Games    []GameResult
	Walkover Side // the side that advances without playing
}

// Entrant is one side of a pairing: a team, a Monster DYP pair of players, or a bye.
type Entrant struct {
	Team    int     // team id (= seed in Setup.Teams); 0 for Monster DYP pairs and byes
	Players []int64 // the players
	Bye     bool
	Slots   []int // Monster DYP: the players' places in Setup.Players, from 1
	drop    *Drop // fixed teams and Monster DYP players
}

// Key identifies an entrant across derivations, even after a player was replaced: "t3" (team
// 3), "p2+7" (Monster DYP players 2 and 7), "bye".
func (e *Entrant) Key() string {
	switch {
	case e == nil:
		return ""
	case e.Bye:
		return "bye"
	case len(e.Slots) > 0:
		k := "p"
		for i, s := range e.Slots {
			if i > 0 {
				k += "+"
			}
			k += strconv.Itoa(s)
		}
		return k
	}
	return "t" + strconv.Itoa(e.Team)
}

// Dropped reports whether the entrant had dropped out by the given stage.
func (e *Entrant) Dropped(stage int) bool { return e != nil && e.drop != nil && e.drop.Stage <= stage }

// Sides identifies who a pairing is between ("t1-t4"): a pairing that was played by other sides
// than it has now is a conflict (spec 09 §11).
func (p *Pairing) Sides() string {
	if p.A == nil || p.B == nil {
		return ""
	}
	return p.A.Key() + "-" + p.B.Key()
}

// Conflicts lists the pairings whose games were played by other sides than the tournament puts
// there now (played maps a pairing key to the sides it was played by), in order.
func (s *State) Conflicts(played map[string]string) []string {
	var out []string
	for _, p := range s.Pairings() {
		if want, ok := played[p.Key]; ok && want != "" && want != p.Sides() {
			out = append(out, p.Key)
		}
		delete(played, p.Key)
	}
	// Played pairings the tournament doesn't have at all any more.
	var rest []string
	for k, v := range played {
		if v != "" {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// PairingState is where a pairing stands.
type PairingState string

const (
	Waiting  PairingState = "waiting" // a side isn't known yet
	Ready    PairingState = "ready"   // both sides known, not decided
	Done     PairingState = "done"    // decided by its games or a walkover
	ByeState PairingState = "bye"     // one side is a bye: the other advances without playing
	Skipped  PairingState = "skipped" // not needed (the bracket reset of a double knockout)
)

// Pairing is one row of a bracket or table (spec 09 §7.1).
type Pairing struct {
	Key      string // stable id: results are stored by it
	Stage    int    // index in Setup.Stages
	Round    int    // within its bracket / group / stage, from 1
	Pos      int    // within the round, from 1
	Bracket  string // "W"/"L"/"GF"/"3rd" (knockouts), the group name ("A", …), "" otherwise
	Depth    int    // how far into the stage: the order of play suggestion uses it
	A, B     *Entrant
	State    PairingState
	WinsA    int
	WinsB    int
	GoalsA   int
	GoalsB   int
	Winner   Side
	BestOf   int
	Walkover bool
}

// WinnerEntrant and LoserEntrant return the sides of a decided pairing (nil otherwise).
func (p *Pairing) WinnerEntrant() *Entrant { return p.side(p.Winner) }
func (p *Pairing) LoserEntrant() *Entrant  { return p.side(p.Winner.Other()) }

func (p *Pairing) side(s Side) *Entrant {
	switch s {
	case A:
		return p.A
	case B:
		return p.B
	}
	return nil
}

// Placement is a final place: 1, 2 or 3 (3 may be shared).
type Placement struct {
	Place   int
	Team    int
	Players []int64
}

// StageState is a derived stage.
type StageState struct {
	Stage    Stage
	Pairings []*Pairing
	Done     bool
	Started  bool
	Entrants []*Entrant // who plays in this stage, in seed order
	Groups   []string   // groups stage: the group names …
	// … and who is in each group, in seed order
	GroupEntrants map[string][]*Entrant
	SatOut        [][]int64 // Monster DYP: who sat out each round
}

// State is a derived tournament.
type State struct {
	Setup      Setup
	Stages     []*StageState
	Finished   bool
	Placements []Placement
}

// Pairings returns every pairing of every started stage, in stage and creation order.
func (s *State) Pairings() []*Pairing {
	var out []*Pairing
	for _, st := range s.Stages {
		out = append(out, st.Pairings...)
	}
	return out
}

// Pairing finds a pairing by key.
func (s *State) Pairing(key string) *Pairing {
	for _, p := range s.Pairings() {
		if p.Key == key {
			return p
		}
	}
	return nil
}

// ReadyPairings are the pairings that can be played now – in any order (spec 09 §7.2).
func (s *State) ReadyPairings() []*Pairing {
	var out []*Pairing
	for _, p := range s.Pairings() {
		if p.State == Ready {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].before(out[j]) })
	return out
}

func (p *Pairing) before(q *Pairing) bool {
	if p.Stage != q.Stage {
		return p.Stage < q.Stage
	}
	if p.Depth != q.Depth {
		return p.Depth < q.Depth
	}
	if p.Bracket != q.Bracket {
		return bracketOrder(p.Bracket) < bracketOrder(q.Bracket)
	}
	if p.Round != q.Round {
		return p.Round < q.Round
	}
	return p.Pos < q.Pos
}

func bracketOrder(b string) string {
	switch b {
	case "W":
		return "0"
	case "L":
		return "1"
	case "3rd":
		return "2"
	case "GF":
		return "3"
	}
	return "4" + b
}

// Suggest returns the pairing to play next: the first ready one, skipping pairings with a team
// or player that just played where another ready pairing exists (spec 09 §7.2). nil = none ready.
func (s *State) Suggest(justPlayed *Pairing) *Pairing {
	ready := s.ReadyPairings()
	if len(ready) == 0 {
		return nil
	}
	if justPlayed != nil {
		for _, p := range ready {
			if !p.shares(justPlayed) {
				return p
			}
		}
	}
	return ready[0]
}

func (p *Pairing) shares(q *Pairing) bool {
	for _, a := range []*Entrant{p.A, p.B} {
		for _, b := range []*Entrant{q.A, q.B} {
			if a == nil || b == nil || a.Bye || b.Bye {
				continue
			}
			if a.Team != 0 && a.Team == b.Team {
				return true
			}
			for _, x := range a.Players {
				for _, y := range b.Players {
					if x == y {
						return true
					}
				}
			}
		}
	}
	return false
}

// Validate checks a setup before the start.
func (s Setup) Validate() error {
	if len(s.Stages) == 0 {
		return fmt.Errorf("no stage")
	}
	monster := s.Stages[0].Format == MonsterDYP
	switch {
	case monster && len(s.Players) < 4:
		return fmt.Errorf("Monster DYP needs at least 4 players")
	case !monster && len(s.Teams) < 2:
		return fmt.Errorf("at least 2 teams")
	}
	for i, st := range s.Stages {
		if st.BestOf != 0 && st.BestOf != 1 && st.BestOf != 3 && st.BestOf != 5 {
			return fmt.Errorf("stage %d: best of %d", i+1, st.BestOf)
		}
		last := i == len(s.Stages)-1
		switch st.Format {
		case Knockout, DoubleKnockout:
			if !last {
				return fmt.Errorf("stage %d: a knockout must be the last stage", i+1)
			}
		case MonsterDYP:
			if i != 0 || !last {
				return fmt.Errorf("Monster DYP is a tournament of its own")
			}
		case Groups:
			if last {
				return fmt.Errorf("stage %d: groups need a following stage", i+1)
			}
		case RoundRobin, Swiss:
		default:
			return fmt.Errorf("stage %d: unknown format %q", i+1, st.Format)
		}
		if !last && st.Format != Groups && st.Advance < 2 {
			return fmt.Errorf("stage %d: say how many advance", i+1)
		}
	}
	return nil
}

// Derive computes the tournament from its setup and the results so far, by pairing key.
func Derive(setup Setup, results map[string]Result) (*State, error) {
	if err := setup.Validate(); err != nil {
		return nil, err
	}
	if setup.TieBreaks == nil {
		setup.TieBreaks = DefaultTieBreaks
	}
	st := &State{Setup: setup}
	var entrants []*Entrant
	drop := func(n int) *Drop {
		if d, ok := setup.Drops[n]; ok {
			return &d
		}
		return nil
	}
	if setup.Stages[0].Format == MonsterDYP {
		for i, id := range setup.Players {
			entrants = append(entrants, &Entrant{Players: []int64{id}, Slots: []int{i + 1}, drop: drop(i + 1)})
		}
	} else {
		for i, t := range setup.Teams {
			entrants = append(entrants, &Entrant{Team: i + 1, Players: t.Players, drop: drop(i + 1)})
		}
	}
	for i, stage := range setup.Stages {
		if stage.BestOf == 0 {
			stage.BestOf = 1
		}
		ss := &StageState{Stage: stage, Started: true}
		st.Stages = append(st.Stages, ss)
		b := &builder{setup: setup, stage: i, ss: ss, results: results}
		switch stage.Format {
		case RoundRobin:
			b.roundRobin(entrants, "")
		case Groups:
			b.groups(entrants)
		case Knockout:
			b.knockout(entrants, false)
		case DoubleKnockout:
			b.knockout(entrants, true)
		case Swiss:
			b.swiss(entrants)
		case MonsterDYP:
			b.monster(entrants)
		}
		ss.Done = b.complete()
		if !ss.Done {
			return st, nil
		}
		if i+1 < len(setup.Stages) {
			entrants = st.qualifiers(i)
		}
	}
	st.Finished = true
	st.Placements = st.placements()
	return st, nil
}

// builder adds the pairings of one stage and applies their results.
type builder struct {
	setup   Setup
	stage   int
	ss      *StageState
	results map[string]Result
}

func (b *builder) key(parts ...any) string {
	k := fmt.Sprintf("s%d", b.stage+1)
	for _, p := range parts {
		k += fmt.Sprintf("-%v", p)
	}
	return k
}

// add creates a pairing with both sides possibly unknown (nil) and applies its result.
func (b *builder) add(p *Pairing) *Pairing {
	p.Stage = b.stage
	p.BestOf = b.ss.Stage.BestOf
	b.ss.Pairings = append(b.ss.Pairings, p)
	b.resolve(p)
	return p
}

// resolve sets a pairing's state from its sides and its result.
func (b *builder) resolve(p *Pairing) {
	p.State, p.Winner, p.WinsA, p.WinsB, p.GoalsA, p.GoalsB, p.Walkover = Waiting, NoSide, 0, 0, 0, 0, false
	switch {
	case p.A == nil || p.B == nil:
		return
	case p.A.Bye && p.B.Bye:
		p.State = ByeState // nobody: the bye goes on
		return
	case p.A.Bye || p.B.Bye:
		p.State, p.Winner = ByeState, A
		if p.A.Bye {
			p.Winner = B
		}
		return
	}
	// The higher seed is A (spec 09 §7.3).
	if p.A.Team != 0 && p.B.Team != 0 && p.B.Team < p.A.Team {
		p.A, p.B = p.B, p.A
	}
	p.State = Ready
	if b.annulled(p.A) || b.annulled(p.B) {
		p.State = Skipped // doesn't count (spec 09 §10)
		return
	}
	r, ok := b.results[p.Key]
	if !ok {
		// A side that dropped out loses by walkover. If both did, the one that dropped out later
		// goes on (and loses its next pairing): what was decided before stays decided.
		da, db := b.dropped(p.A), b.dropped(p.B)
		switch {
		case da > 0 && (db == 0 || da < db):
			p.State, p.Winner, p.Walkover = Done, B, true
		case db > 0:
			p.State, p.Winner, p.Walkover = Done, A, true
		}
		return
	}
	if r.Walkover != NoSide {
		p.State, p.Winner, p.Walkover = Done, r.Walkover, true
		return
	}
	need := p.BestOf/2 + 1
	for _, g := range r.Games {
		if p.WinsA >= need || p.WinsB >= need {
			break // a game after the pairing was decided doesn't count
		}
		p.GoalsA += g.GoalsA
		p.GoalsB += g.GoalsB
		switch g.Winner {
		case A:
			p.WinsA++
		case B:
			p.WinsB++
		}
	}
	switch {
	case p.WinsA >= need:
		p.State, p.Winner = Done, A
	case p.WinsB >= need:
		p.State, p.Winner = Done, B
	}
}

// dropped tells whether a side (or, in Monster DYP, one of its players) has dropped out: the
// Seq of the first dropout, 0 if none.
func (b *builder) dropped(e *Entrant) int {
	if e.Dropped(b.stage) {
		return max(e.drop.Seq, 1)
	}
	seq := 0
	for _, s := range e.Slots {
		if d, ok := b.setup.Drops[s]; ok && d.Stage <= b.stage && (seq == 0 || max(d.Seq, 1) < seq) {
			seq = max(d.Seq, 1)
		}
	}
	return seq
}

// annulled reports whether a side's pairings of this stage don't count.
func (b *builder) annulled(e *Entrant) bool {
	return e.drop != nil && e.drop.Annul && e.drop.Stage == b.stage
}

// active leaves out who dropped out of this stage before round r was drawn (Swiss, Monster DYP).
func (b *builder) active(es []*Entrant, r int) []*Entrant {
	var out []*Entrant
	for _, e := range es {
		if d := e.drop; d != nil && (d.Stage < b.stage || d.Stage == b.stage && r > d.Round) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// complete reports whether every pairing of the stage is decided.
func (b *builder) complete() bool {
	for _, p := range b.ss.Pairings {
		if p.State == Waiting || p.State == Ready {
			return false
		}
	}
	return len(b.ss.Pairings) > 0
}

// decided reports whether a pairing is over (played, bye or skipped).
func decided(p *Pairing) bool { return p.State == Done || p.State == ByeState || p.State == Skipped }
