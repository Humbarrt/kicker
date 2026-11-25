// Package tournaments runs tournaments on the table (spec 09 §8): it starts and pauses them,
// stages the pairings players check in for, swaps sides between the games of a pairing and
// finishes the tournament once the placements are known. The brackets themselves come from the
// pure `tournament` package; this package does the I/O around it.
package tournaments

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/table"
	"github.com/Humbarrt/kicker/server/internal/tournament"
)

// TopicTournament announces tournament changes (state, results, the pairing on the table).
const TopicTournament = "tournament"

// Changed is the TopicTournament payload.
type Changed struct {
	ID int64 `json:"id"`
}

// Team modes (spec 09 §4).
const (
	FixedTeams = "fixed"
	DYP        = "dyp"
	Monster    = "monster"
)

// Settings is what a tournament stores as its settings.
type Settings struct {
	TeamMode    string                `json:"team_mode"` // fixed | dyp | monster
	Stages      []StageSettings       `json:"stages"`
	TieBreaks   []tournament.TieBreak `json:"tie_breaks,omitempty"`
	Seed        int64                 `json:"seed,omitempty"`         // the draw seed, fixed at the start
	ManualSeeds bool                  `json:"manual_seeds,omitempty"` // keep the setup's team order instead of seeding by rating
	// Drops are the teams (team number) or Monster DYP players (place, from 1) that dropped out.
	Drops    map[int]tournament.Drop `json:"drops,omitempty"`
	PresetID *int64                  `json:"preset_id,omitempty"` // the preset the stages' rules were copied from (shown in the setup)
}

// StageSettings is a stage with the rules its games are played with.
type StageSettings struct {
	tournament.Stage
	Rules *GameRules `json:"rules,omitempty"` // nil: the table's current preset, with golden goal
}

// GameRules are a stage's win conditions (D10). Tournaments never end in a draw.
type GameRules struct {
	WinScore   int `json:"win_score"`
	WinBy      int `json:"win_by"`
	MaxScore   int `json:"max_score,omitempty"`
	TimeLimitS int `json:"time_limit_s,omitempty"`
}

func (r GameRules) engine() engine.Rules {
	return engine.Rules{WinScore: r.WinScore, WinBy: r.WinBy, MaxScore: r.MaxScore,
		TimeLimit: time.Duration(r.TimeLimitS) * time.Second, TimeTie: engine.TimeTieGoldenGoal}
}

// Errors of the service.
var (
	ErrOtherRunning = errors.New("another tournament is running")
	ErrNotRunning   = errors.New("the tournament isn't running")
	ErrNotReady     = errors.New("that pairing can't be played now")
	ErrBadSetup     = errors.New("invalid tournament setup")
	ErrOnTable      = errors.New("that team is on the table")
	ErrNoConflicts  = errors.New("nothing to rebuild")
	ErrInTournament = errors.New("that player already takes part")
)

// Options are the service's dependencies.
type Options struct {
	Store  *store.Store
	Table  *table.Table
	Bus    *bus.Bus // optional
	Logger *slog.Logger
	Now    func() time.Time // default time.Now
}

// Service runs the active tournament. Create with New, then Run.
type Service struct {
	opts Options
	log  *slog.Logger
	work chan func(context.Context)

	mu       sync.Mutex
	active   int64               // the running tournament, 0 = none
	checkIns map[int64]time.Time // tournament players who said "I'm here" (spec 09 §8)
	staged   string              // the pairing on the table, waiting for its kickoff or being played
	last     string              // the pairing that ended last (the suggestion skips its teams)
}

// New makes a service; wire its hooks into the table (OnCheckIn, OnPhase, OnGameFinished).
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{opts: o, log: o.Logger.With("component", "tournaments"), work: make(chan func(context.Context), 256),
		checkIns: map[int64]time.Time{}}
}

// Run restores a running tournament after a restart and then handles the table's hooks.
func (s *Service) Run(ctx context.Context) {
	select {
	case <-s.opts.Table.Ready():
	case <-ctx.Done():
		return
	}
	if err := s.restore(ctx); err != nil {
		s.log.Error("restoring the running tournament", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-s.work:
			f(ctx)
		}
	}
}

// queue runs f on the service's goroutine (the table's hooks must not block).
func (s *Service) queue(f func(context.Context)) {
	select {
	case s.work <- f:
	default:
		go func() { s.work <- f }()
	}
}

// --- hooks from the table ----------------------------------------------------------------------

// CheckIn is the table's OnCheckIn hook.
func (s *Service) CheckIn(c engine.CheckIn) {
	s.queue(func(ctx context.Context) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.active == 0 {
			return
		}
		s.checkIns[c.PlayerID] = s.opts.Now()
		if err := s.stageNext(ctx, ""); err != nil {
			s.log.Error("staging a pairing", "err", err)
		}
	})
}

// Phase is the table's OnPhase hook: going to sleep forgets the check-ins and, if a tournament
// game was paused by it, pauses the tournament (spec 09 §3).
func (s *Service) Phase(from, to engine.Phase) {
	if to != engine.PhaseSleeping {
		return
	}
	s.queue(func(ctx context.Context) {
		s.mu.Lock()
		defer s.mu.Unlock()
		clear(s.checkIns)
		if s.active == 0 {
			return
		}
		if _, paused, err := s.opts.Store.TournamentPausedGame(ctx, s.active); err != nil || !paused {
			return
		}
		if err := s.pause(ctx, s.active, nil, "sleep"); err != nil {
			s.log.Error("pausing the tournament", "err", err)
		}
	})
}

// GameFinished is called for every finished (or edited) game: a tournament game moves its
// pairing on – the next game of a best-of, or the tournament to its next pairings or its end.
func (s *Service) GameFinished(gameID int64) {
	s.queue(func(ctx context.Context) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.gameFinished(ctx, gameID); err != nil {
			s.log.Error("tournament game", "game", gameID, "err", err)
		}
	})
}

func (s *Service) gameFinished(ctx context.Context, gameID int64) error {
	g, _, err := s.opts.Store.Game(ctx, gameID)
	if err != nil || g.PairingID == nil {
		return err
	}
	tid, key, err := s.opts.Store.TournamentOfPairing(ctx, *g.PairingID)
	if err != nil {
		return err
	}
	s.publish(tid)
	if tid != s.active {
		// An edit of another tournament: its brackets are derived when read; a finished one only
		// changes when one of its games is voided.
		return s.reopenIfVoided(ctx, tid, g)
	}
	st, _, _, err := s.derive(ctx, tid)
	if err != nil {
		return err
	}
	p := st.Pairing(key)
	if key == s.staged {
		s.staged = ""
	}
	if p != nil && p.State == tournament.Ready && g.State == "finished" && g.ID == s.lastGameOf(ctx, *g.PairingID) {
		// Best of n: the next game of the same pairing, the players are still there.
		return s.stage(ctx, tid, st, p)
	}
	if st.Finished {
		return s.finish(ctx, tid, st)
	}
	s.last = key
	return s.stageNext(ctx, "")
}

// finish ends the running tournament: placements stored, results frozen (spec 09 §11).
func (s *Service) finish(ctx context.Context, tid int64, st *tournament.State) error {
	s.log.Info("tournament finished", "tournament", tid)
	raw, err := s.opts.Store.TournamentResults(ctx, tid)
	if err != nil {
		return err
	}
	if err := s.opts.Store.FinishTournament(ctx, tid, st.Placements, raw); err != nil {
		return err
	}
	s.active, s.staged, s.last = 0, "", ""
	clear(s.checkIns)
	s.publish(tid)
	return s.opts.Table.Do(ctx, engine.SetTournament{On: false})
}

// reopenIfVoided reopens a finished tournament when one of the games it was frozen with is
// voided: the pairing has to be played again (spec 09 §11). Other edits don't change it.
func (s *Service) reopenIfVoided(ctx context.Context, tid int64, g store.Game) error {
	if !g.Voided {
		return nil
	}
	t, _, err := s.opts.Store.Tournament(ctx, tid)
	if err != nil || t.State != "finished" {
		return err
	}
	frozen, err := t.FrozenResults()
	if err != nil {
		return err
	}
	for key, r := range frozen {
		for _, fg := range r.Games {
			if fg.GameID == g.ID {
				s.log.Info("tournament reopened: a game was voided", "tournament", tid, "game", g.ID)
				if err := s.opts.Store.SetTournamentState(ctx, tid, "finished", "paused", nil, nil,
					map[string]any{"game": g.ID, "pairing": key}); err != nil {
					return err
				}
				s.publish(tid)
				return nil
			}
		}
	}
	return nil
}

// lastGameOf returns the id of the pairing's latest game (an edit of an earlier one doesn't
// start another game).
func (s *Service) lastGameOf(ctx context.Context, pairingID int64) int64 {
	tid, key, err := s.opts.Store.TournamentOfPairing(ctx, pairingID)
	if err != nil {
		return 0
	}
	res, err := s.opts.Store.TournamentResults(ctx, tid)
	if err != nil {
		return 0
	}
	gs := res[key].Games
	if len(gs) == 0 {
		return 0
	}
	return gs[len(gs)-1].GameID
}

// --- commands ------------------------------------------------------------------------------------

// Start draws and seeds the teams and lets the tournament run the table (spec 09 §4, §5).
func (s *Service) Start(ctx context.Context, id int64, author *int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if running, err := s.opts.Store.ActiveTournament(ctx); err != nil {
		return err
	} else if running != nil {
		return ErrOtherRunning
	}
	t, ps, err := s.opts.Store.Tournament(ctx, id)
	if err != nil {
		return err
	}
	if t.State != "setup" {
		return store.ErrTournamentState
	}
	var set Settings
	if err := json.Unmarshal(t.Settings, &set); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSetup, err)
	}
	if set.Seed == 0 {
		set.Seed = s.opts.Now().UnixNano()
	}
	ps, err = s.drawAndSeed(ctx, set, ps)
	if err != nil {
		return err
	}
	if _, err := s.setupOf(set, ps); err != nil {
		return err
	}
	raw, _ := json.Marshal(set)
	if err := s.opts.Store.StartTournament(ctx, id, raw, ps, author); err != nil {
		return err
	}
	s.active, s.staged, s.last = id, "", ""
	clear(s.checkIns)
	s.publish(id)
	return s.opts.Table.Do(ctx, engine.SetTournament{On: true})
}

// Pause gives the table back to free play; a game being played pauses (spec 09 §3).
func (s *Service) Pause(ctx context.Context, id int64, author *int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pause(ctx, id, author, "manual")
}

func (s *Service) pause(ctx context.Context, id int64, author *int64, reason string) error {
	if id != s.active {
		return ErrNotRunning
	}
	if v := s.opts.Table.View(); v.Phase == string(engine.PhasePlaying) {
		var a int64
		if author != nil {
			a = *author
		}
		if err := s.opts.Table.Do(ctx, engine.PauseGame{Author: a}); err != nil && !errors.Is(err, engine.ErrNoGame) {
			return err
		}
	}
	if err := s.opts.Store.SetTournamentState(ctx, id, "running", "paused", nil, author, map[string]string{"reason": reason}); err != nil {
		return err
	}
	s.active, s.staged = 0, ""
	clear(s.checkIns)
	s.publish(id)
	return s.opts.Table.Do(ctx, engine.SetTournament{On: false})
}

// Continue lets a paused tournament run the table again and resumes its paused game, if any.
func (s *Service) Continue(ctx context.Context, id int64, author *int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if running, err := s.opts.Store.ActiveTournament(ctx); err != nil {
		return err
	} else if running != nil {
		return ErrOtherRunning
	}
	if err := s.opts.Store.SetTournamentState(ctx, id, "paused", "running", nil, author, nil); err != nil {
		return err
	}
	s.active, s.staged = id, ""
	s.publish(id)
	if err := s.opts.Table.Do(ctx, engine.SetTournament{On: true}); err != nil {
		return err
	}
	// Decided meanwhile (a dropout, or a reopened tournament whose void was taken back).
	if st, _, _, err := s.derive(ctx, id); err != nil {
		return err
	} else if st.Finished {
		return s.finish(ctx, id, st)
	}
	gameID, ok, err := s.opts.Store.TournamentPausedGame(ctx, id)
	if err != nil || !ok {
		return err
	}
	var a int64
	if author != nil {
		a = *author
	}
	return s.opts.Table.ResumeGame(ctx, gameID, a)
}

// Play puts a ready pairing on the table at once, without check-ins (spec 09 §8 "Play").
func (s *Service) Play(ctx context.Context, id int64, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.active {
		return ErrNotRunning
	}
	return s.stageNext(ctx, key)
}

// State derives a tournament (for the API).
func (s *Service) State(ctx context.Context, id int64) (*tournament.State, store.Tournament, Settings, error) {
	return s.derive(ctx, id)
}

// Staged returns the pairing on the table of the running tournament ("" = none).
func (s *Service) Staged() (int64, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active, s.staged
}

// CheckedIn returns the players who checked in, for the scoreboard.
func (s *Service) CheckedIn() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, 0, len(s.checkIns))
	for id := range s.checkIns {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// --- inside ----------------------------------------------------------------------------------

func (s *Service) restore(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.opts.Store.ActiveTournament(ctx)
	if err != nil || t == nil {
		return err
	}
	s.active = t.ID
	// A tournament game still on the table (not the last finished one the idle table shows).
	if g := s.opts.Table.View().Game; g != nil && g.Current && g.Status != "finished" && g.PairingID != 0 {
		_, s.staged, _ = s.opts.Store.TournamentOfPairing(ctx, g.PairingID)
	}
	s.log.Info("tournament running", "tournament", t.ID, "name", t.Name)
	return s.opts.Table.Do(ctx, engine.SetTournament{On: true})
}

func (s *Service) publish(id int64) {
	if s.opts.Bus != nil {
		s.opts.Bus.Publish(TopicTournament, Changed{ID: id})
	}
}

// derive loads a tournament and computes its brackets.
func (s *Service) derive(ctx context.Context, id int64) (*tournament.State, store.Tournament, Settings, error) {
	t, ps, err := s.opts.Store.Tournament(ctx, id)
	if err != nil {
		return nil, t, Settings{}, err
	}
	var set Settings
	if err := json.Unmarshal(t.Settings, &set); err != nil {
		return nil, t, set, fmt.Errorf("%w: %v", ErrBadSetup, err)
	}
	setup, err := s.setupOf(set, ps)
	if err != nil {
		return nil, t, set, err
	}
	raw, err := s.results(ctx, t)
	if err != nil {
		return nil, t, set, err
	}
	st, err := tournament.Derive(setup, Results(raw))
	return st, t, set, err
}

// results are a tournament's pairing results: as frozen at the finish, otherwise the games now.
func (s *Service) results(ctx context.Context, t store.Tournament) (map[string]store.PairingResult, error) {
	if t.State == "finished" && len(t.Frozen) > 0 {
		return t.FrozenResults()
	}
	return s.opts.Store.TournamentResults(ctx, t.ID)
}

// setupOf turns the stored participants into the core's setup: teams by team number, players
// by seed.
func (s *Service) setupOf(set Settings, ps []store.TournamentParticipant) (tournament.Setup, error) {
	setup := tournament.Setup{TieBreaks: set.TieBreaks, Seed: set.Seed, Drops: set.Drops}
	for _, st := range set.Stages {
		setup.Stages = append(setup.Stages, st.Stage)
	}
	ps = current(ps)
	if set.TeamMode == Monster {
		for _, p := range ps {
			setup.Players = append(setup.Players, p.PlayerID)
		}
	} else {
		byTeam := map[int][]int64{}
		var nos []int
		for _, p := range ps {
			if p.TeamNo == nil {
				return setup, fmt.Errorf("%w: a player without a team", ErrBadSetup)
			}
			if _, ok := byTeam[*p.TeamNo]; !ok {
				nos = append(nos, *p.TeamNo)
			}
			byTeam[*p.TeamNo] = append(byTeam[*p.TeamNo], p.PlayerID)
		}
		slices.Sort(nos)
		for _, no := range nos {
			if len(byTeam[no]) > 2 {
				return setup, fmt.Errorf("%w: team %d has %d players", ErrBadSetup, no, len(byTeam[no]))
			}
			setup.Teams = append(setup.Teams, tournament.Team{Players: byTeam[no]})
		}
	}
	if err := setup.Validate(); err != nil {
		return setup, fmt.Errorf("%w: %v", ErrBadSetup, err)
	}
	return setup, nil
}

// current leaves out replaced participants: the ones playing now, in seed order.
func current(ps []store.TournamentParticipant) []store.TournamentParticipant {
	out := make([]store.TournamentParticipant, 0, len(ps))
	for _, p := range ps {
		if p.ReplacedBy == nil {
			out = append(out, p)
		}
	}
	return out
}

// drawAndSeed makes the final teams: DYP draws them; fixed and drawn teams are seeded by rating
// unless the setup's order is kept (spec 09 §5). The result has team numbers 1..n = seeds.
func (s *Service) drawAndSeed(ctx context.Context, set Settings, ps []store.TournamentParticipant) ([]store.TournamentParticipant, error) {
	rng := rand.New(rand.NewPCG(uint64(set.Seed), 0x6b69636b))
	guest, err := s.opts.Store.GuestPlayer(ctx)
	if err != nil {
		return nil, err
	}
	rating := func(player int64, key string) float64 {
		if player == guest.ID {
			return 1000 // the guest's fixed rating (D9)
		}
		rs, err := s.opts.Store.PlayerRatings(ctx, player)
		if err == nil {
			for _, r := range rs {
				if r.Key == key {
					return r.Rating
				}
			}
		}
		return 1000
	}
	if set.TeamMode == Monster {
		out := slices.Clone(ps)
		if !set.ManualSeeds {
			// Players by their best rating; ties by draw.
			type pr struct {
				p store.TournamentParticipant
				r float64
				x uint64
			}
			list := make([]pr, len(out))
			for i, p := range out {
				list[i] = pr{p, max(rating(p.PlayerID, "solo"), rating(p.PlayerID, "team")), rng.Uint64()}
			}
			slices.SortStableFunc(list, func(a, b pr) int {
				return cmp.Or(cmp.Compare(b.r, a.r), cmp.Compare(a.x, b.x))
			})
			for i := range list {
				out[i] = list[i].p
			}
		}
		for i := range out {
			out[i].Seed, out[i].TeamNo = i+1, nil
		}
		return out, nil
	}
	// Teams: fixed by team number, or drawn (DYP): pairs of the shuffled players, one solo team
	// if the number is odd (D11).
	var teams [][]store.TournamentParticipant
	if set.TeamMode == DYP {
		shuffled := slices.Clone(ps)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		for i := 0; i < len(shuffled); i += 2 {
			teams = append(teams, shuffled[i:min(i+2, len(shuffled))])
		}
	} else {
		byNo := map[int][]store.TournamentParticipant{}
		var nos []int
		for _, p := range ps {
			if p.TeamNo == nil {
				return nil, fmt.Errorf("%w: a player without a team", ErrBadSetup)
			}
			if _, ok := byNo[*p.TeamNo]; !ok {
				nos = append(nos, *p.TeamNo)
			}
			byNo[*p.TeamNo] = append(byNo[*p.TeamNo], p)
		}
		slices.Sort(nos)
		for _, no := range nos {
			teams = append(teams, byNo[no])
		}
	}
	if !set.ManualSeeds {
		type tr struct {
			t []store.TournamentParticipant
			r float64
			x uint64
		}
		list := make([]tr, len(teams))
		for i, t := range teams {
			key := "team"
			if len(t) == 1 {
				key = "solo"
			}
			var sum float64
			for _, p := range t {
				sum += rating(p.PlayerID, key)
			}
			list[i] = tr{t, sum / float64(len(t)), rng.Uint64()}
		}
		slices.SortStableFunc(list, func(a, b tr) int { return cmp.Or(cmp.Compare(b.r, a.r), cmp.Compare(a.x, b.x)) })
		for i := range list {
			teams[i] = list[i].t
		}
	}
	var out []store.TournamentParticipant
	for i, t := range teams {
		for _, p := range t {
			p.TeamNo = store.Ptr(i + 1)
			p.Seed = len(out) + 1
			out = append(out, p)
		}
	}
	return out, nil
}

// stageNext puts the next pairing on the table: key if given ("Play"), otherwise the first ready
// pairing – in the suggested order – with a checked-in player on each team (spec 09 §8).
func (s *Service) stageNext(ctx context.Context, key string) error {
	if s.active == 0 {
		return ErrNotRunning
	}
	v := s.opts.Table.View()
	busy := v.Phase == string(engine.PhasePlaying) || v.Phase == string(engine.PhasePregame) && s.staged != ""
	if busy && key == "" {
		return nil // the next pairing waits for the table
	}
	st, _, _, err := s.derive(ctx, s.active)
	if err != nil {
		return err
	}
	if key != "" {
		p := st.Pairing(key)
		if p == nil || p.State != tournament.Ready {
			return ErrNotReady
		}
		if v.Phase == string(engine.PhasePlaying) {
			return fmt.Errorf("%w: a game is being played", ErrNotReady)
		}
		return s.stage(ctx, s.active, st, p)
	}
	var last *tournament.Pairing
	if s.last != "" {
		last = st.Pairing(s.last)
	}
	ready := st.ReadyPairings()
	if sug := st.Suggest(last); sug != nil { // the suggestion first, then the rest in order
		ready = append([]*tournament.Pairing{sug}, slices.DeleteFunc(ready, func(p *tournament.Pairing) bool { return p == sug })...)
	}
	for _, p := range ready {
		if s.present(p.A) && s.present(p.B) {
			return s.stage(ctx, s.active, st, p)
		}
	}
	return nil
}

func (s *Service) present(e *tournament.Entrant) bool {
	for _, id := range e.Players {
		if _, ok := s.checkIns[id]; ok {
			return true
		}
	}
	return false
}

// stage puts one game of a pairing on the table: team A at home in odd games, away in even ones;
// the first ball to the higher seed in game 1, then to the loser of the previous game (spec 09 §7.3).
func (s *Service) stage(ctx context.Context, tid int64, st *tournament.State, p *tournament.Pairing) error {
	_, _, set, err := s.derive(ctx, tid)
	if err != nil {
		return err
	}
	rules, presetID, err := s.rulesOf(ctx, set, p.Stage)
	if err != nil {
		return err
	}
	pid, err := s.opts.Store.PairingID(ctx, tid, p.Key, p.Sides())
	if err != nil {
		return err
	}
	played := p.WinsA + p.WinsB
	aSide := engine.Home
	if played%2 == 1 {
		aSide = engine.Away
	}
	first := aSide // game 1: the higher seed
	if played > 0 {
		raw, err := s.opts.Store.TournamentResults(ctx, tid)
		if err != nil {
			return err
		}
		if gs := Results(raw)[p.Key].Games; len(gs) > 0 {
			if gs[len(gs)-1].Winner == tournament.A {
				first = aSide.Other() // B lost: B gets the ball
			}
		}
	}
	home, away := p.A.Players, p.B.Players
	if aSide == engine.Away {
		home, away = away, home
	}
	staged := &engine.Staged{Pairing: engine.GamePairing{ID: pid, ASide: aSide}, Rules: rules, PresetID: presetID, FirstBall: first}
	if err := s.opts.Table.Do(ctx, engine.StagePairing{Staged: staged, Home: home, Away: away}); err != nil {
		return err
	}
	for _, e := range []*tournament.Entrant{p.A, p.B} {
		for _, id := range e.Players {
			delete(s.checkIns, id)
		}
	}
	s.staged = p.Key
	s.log.Info("pairing on the table", "tournament", tid, "pairing", p.Key, "game", played+1)
	s.publish(tid)
	return nil
}

// rulesOf returns a stage's game rules: its own, or the table's current preset with golden goal.
func (s *Service) rulesOf(ctx context.Context, set Settings, stage int) (engine.Rules, int64, error) {
	if stage < len(set.Stages) && set.Stages[stage].Rules != nil {
		r := set.Stages[stage].Rules.engine()
		return r, 0, r.Validate()
	}
	p, err := s.opts.Table.CurrentPreset(ctx)
	if err != nil {
		return engine.Rules{}, 0, err
	}
	r := GameRules{WinScore: p.Rules.WinScore, WinBy: p.Rules.WinBy}
	if p.Rules.MaxScore != nil {
		r.MaxScore = *p.Rules.MaxScore
	}
	if p.Rules.TimeLimitS != nil {
		r.TimeLimitS = *p.Rules.TimeLimitS
	}
	return r.engine(), p.ID, nil
}

// Results converts the stored pairing results into the core's.
func Results(raw map[string]store.PairingResult) map[string]tournament.Result {
	out := make(map[string]tournament.Result, len(raw))
	for key, r := range raw {
		var res tournament.Result
		switch r.Walkover {
		case "a":
			res.Walkover = tournament.A
		case "b":
			res.Walkover = tournament.B
		}
		for _, g := range r.Games {
			gr := tournament.GameResult{GoalsA: g.HomeGoals, GoalsB: g.AwayGoals}
			if g.ASide == "away" {
				gr.GoalsA, gr.GoalsB = g.AwayGoals, g.HomeGoals
			}
			if g.Winner != nil {
				if *g.Winner == g.ASide {
					gr.Winner = tournament.A
				} else {
					gr.Winner = tournament.B
				}
			}
			res.Games = append(res.Games, gr)
		}
		out[key] = res
	}
	return out
}
