package tournament

import "errors"

// Errors of DropFor.
var (
	ErrNoEntrant      = errors.New("no such team or player")
	ErrAlreadyDropped = errors.New("already dropped out")
	ErrFinished       = errors.New("the tournament is finished")
)

// DropFor works out what a dropout now means for team n (fixed teams, DYP) or player n (Monster
// DYP, the place in Setup.Players): the stage it drops out of, the rounds drawn so far and, in
// groups and round robin, whether its pairings are annulled (fewer than half played) or the
// rest become forfeits (spec 09 §10). The result goes into Setup.Drops.
func (s *State) DropFor(n int) (Drop, error) {
	if s.Finished {
		return Drop{}, ErrFinished
	}
	if _, ok := s.Setup.Drops[n]; ok {
		return Drop{}, ErrAlreadyDropped
	}
	monster := len(s.Setup.Stages) > 0 && s.Setup.Stages[0].Format == MonsterDYP
	if n < 1 || monster && n > len(s.Setup.Players) || !monster && n > len(s.Setup.Teams) {
		return Drop{}, ErrNoEntrant
	}
	stage := len(s.Stages) - 1 // the stage being played: the last one derived
	if stage < 0 {
		return Drop{}, ErrNoEntrant
	}
	ss := s.Stages[stage]
	d := Drop{Stage: stage, Seq: 1}
	for _, x := range s.Setup.Drops {
		d.Seq = max(d.Seq, x.Seq+1)
	}
	in := func(e *Entrant) bool {
		if e == nil {
			return false
		}
		if monster {
			for _, x := range e.Slots {
				if x == n {
					return true
				}
			}
			return false
		}
		return e.Team == n
	}
	total, played := 0, 0
	for _, p := range ss.Pairings {
		d.Round = max(d.Round, p.Round)
		if !in(p.A) && !in(p.B) || p.State == ByeState {
			continue
		}
		total++
		if p.State == Done && !p.Walkover {
			played++
		}
	}
	if f := ss.Stage.Format; f == Groups || f == RoundRobin {
		d.Round = 0
		d.Annul = 2*played < total
	} else if f != Swiss && f != MonsterDYP {
		d.Round = 0
	}
	return d, nil
}
