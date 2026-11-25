package tournaments

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/tournament"
)

// Setup is a tournament as the setup page edits it (spec 09 §4).
type Setup struct {
	Name         string        `json:"name"`
	Settings     Settings      `json:"settings"`
	Participants []Participant `json:"participants"`
}

// Participant is a player in the setup: fixed teams give every player a team number (players
// with the same number play together); DYP and Monster DYP leave it out.
type Participant struct {
	PlayerID int64 `json:"player_id"`
	Team     int   `json:"team,omitempty"`
}

// Create stores a new tournament in setup; the author owns it.
func (s *Service) Create(ctx context.Context, in Setup, author *int64) (int64, error) {
	t, ps, err := s.check(ctx, in)
	if err != nil {
		return 0, err
	}
	t.OwnerAccountID = author
	return s.opts.Store.CreateTournament(ctx, t, ps, author)
}

// Update changes a tournament that hasn't started.
func (s *Service) Update(ctx context.Context, id int64, in Setup, author *int64) error {
	t, ps, err := s.check(ctx, in)
	if err != nil {
		return err
	}
	t.ID = id
	if err := s.opts.Store.UpdateTournamentSetup(ctx, t, ps, author); err != nil {
		return err
	}
	s.publish(id)
	return nil
}

// check validates a setup and turns it into store rows (seeds in the given order).
func (s *Service) check(ctx context.Context, in Setup) (store.Tournament, []store.TournamentParticipant, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		return store.Tournament{}, nil, fmt.Errorf("%w: a name of 1–80 characters", ErrBadSetup)
	}
	set := in.Settings
	switch set.TeamMode {
	case FixedTeams, DYP, Monster:
	default:
		return store.Tournament{}, nil, fmt.Errorf("%w: team mode %q", ErrBadSetup, set.TeamMode)
	}
	guest, err := s.opts.Store.GuestPlayer(ctx)
	if err != nil {
		return store.Tournament{}, nil, err
	}
	seen := map[int64]bool{}
	var ps []store.TournamentParticipant
	for i, p := range in.Participants {
		if seen[p.PlayerID] && p.PlayerID != guest.ID {
			return store.Tournament{}, nil, fmt.Errorf("%w: a player is in twice", ErrBadSetup)
		}
		seen[p.PlayerID] = true
		row := store.TournamentParticipant{PlayerID: p.PlayerID, Seed: i + 1}
		if set.TeamMode == FixedTeams {
			if p.Team < 1 {
				return store.Tournament{}, nil, fmt.Errorf("%w: every player needs a team", ErrBadSetup)
			}
			row.TeamNo = store.Ptr(p.Team)
		}
		ps = append(ps, row)
	}
	for i, st := range set.Stages {
		if st.Rules != nil {
			if err := st.Rules.engine().Validate(); err != nil {
				return store.Tournament{}, nil, fmt.Errorf("%w: stage %d: %v", ErrBadSetup, i+1, err)
			}
		}
	}
	// The stages must fit the teams: check them with stand-in teams (DYP draws at the start).
	stand := ps
	if set.TeamMode == DYP {
		stand = nil
		for i, p := range ps {
			p.TeamNo = store.Ptr(i/2 + 1)
			stand = append(stand, p)
		}
	}
	if len(ps) > 0 { // an empty setup can be saved and filled later
		if _, err := s.setupOf(set, stand); err != nil {
			return store.Tournament{}, nil, err
		}
	}
	raw, err := json.Marshal(set)
	if err != nil {
		return store.Tournament{}, nil, err
	}
	return store.Tournament{Name: in.Name, Settings: raw}, ps, nil
}

// Delete removes a tournament without played games.
func (s *Service) Delete(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == s.active {
		return ErrOtherRunning
	}
	if err := s.opts.Store.DeleteTournament(ctx, id); err != nil {
		return err
	}
	s.publish(id)
	return nil
}

// Abandon stops a tournament for good (spec 09 §3): played games stay valid; a running one is
// paused first, so its game on the table pauses too.
func (s *Service) Abandon(ctx context.Context, id int64, author *int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == s.active {
		if err := s.pause(ctx, id, author, "abandon"); err != nil {
			return err
		}
	}
	if err := s.opts.Store.SetTournamentState(ctx, id, "paused", "abandoned", nil, author, nil); err != nil {
		return err
	}
	s.publish(id)
	return nil
}

// Reinstate brings an abandoned tournament back as paused.
func (s *Service) Reinstate(ctx context.Context, id int64, author *int64) error {
	if err := s.opts.Store.SetTournamentState(ctx, id, "abandoned", "paused", nil, author, nil); err != nil {
		return err
	}
	s.publish(id)
	return nil
}

// Options lists the formats for some number of teams (or players, Monster DYP), sorted by the
// estimated duration with this table's pace (spec 09 §4).
// With rules nil the estimates use the table's current preset.
func (s *Service) Options(ctx context.Context, n int, monster bool, gr *GameRules) ([]tournament.Option, error) {
	var set Settings
	if gr != nil {
		set.Stages = []StageSettings{{Rules: gr}}
	}
	rules, _, err := s.rulesOf(ctx, set, 0)
	if err != nil {
		return nil, err
	}
	h, err := s.pace(ctx)
	if err != nil {
		return nil, err
	}
	return tournament.Options(n, monster, rules, h), nil
}

// Estimate estimates a setup's duration.
func (s *Service) Estimate(ctx context.Context, set Settings, setup tournament.Setup) (tournament.Estimate, error) {
	h, err := s.pace(ctx)
	if err != nil {
		return tournament.Estimate{}, err
	}
	var rules []engine.Rules
	for i := range set.Stages {
		r, _, err := s.rulesOf(ctx, set, i)
		if err != nil {
			return tournament.Estimate{}, err
		}
		rules = append(rules, r)
	}
	if len(rules) == 0 {
		return tournament.Estimate{}, nil
	}
	return tournament.EstimateSetup(setup, rules, h), nil
}

// pace is this table's history, once it has 10 games (spec 09 §4).
func (s *Service) pace(ctx context.Context) (tournament.History, error) {
	spg, share, n, err := s.opts.Store.GamePace(ctx)
	if err != nil || n < 10 {
		return tournament.History{}, err
	}
	return tournament.History{SecondsPerGoal: spg, LoserShare: share}, nil
}

// Tournaments lists all tournaments.
func (s *Service) Tournaments(ctx context.Context) ([]store.Tournament, error) {
	return s.opts.Store.Tournaments(ctx)
}

// Get returns a tournament with its participants and history.
func (s *Service) Get(ctx context.Context, id int64) (store.Tournament, []store.TournamentParticipant, []store.TournamentEvent, error) {
	t, ps, err := s.opts.Store.Tournament(ctx, id)
	if err != nil {
		return t, nil, nil, err
	}
	evs, err := s.opts.Store.TournamentEvents(ctx, id)
	return t, ps, evs, err
}

// SetupOf exposes the core setup of stored participants (for previews and estimates).
func (s *Service) SetupOf(set Settings, ps []store.TournamentParticipant) (tournament.Setup, error) {
	return s.setupOf(set, ps)
}

// PairingGames returns the stored results of a tournament's pairings, with their games.
// A finished tournament's are the ones it was frozen with.
func (s *Service) PairingGames(ctx context.Context, id int64) (map[string]store.PairingResult, error) {
	t, _, err := s.opts.Store.Tournament(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.results(ctx, t)
}

// LastPairing is the pairing of the running tournament that ended last ("" = none).
func (s *Service) LastPairing() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// PlayerResult is a tournament on a player's profile: their place (0 = none, or not finished).
type PlayerResult struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	State   string `json:"state"`
	EndedAt *int64 `json:"ended_at"`
	Place   int    `json:"place"`
	// Replaced: somebody took the player's place; Dropped: their team dropped out (spec 09 §10).
	Replaced bool `json:"replaced,omitempty"`
	Dropped  bool `json:"dropped,omitempty"`
}

// PlayerResults lists the tournaments a player took part in (spec 09 §12: profile).
func (s *Service) PlayerResults(ctx context.Context, playerID int64) ([]PlayerResult, error) {
	ts, err := s.opts.Store.PlayerTournaments(ctx, playerID)
	if err != nil {
		return nil, err
	}
	out := make([]PlayerResult, 0, len(ts))
	for _, t := range ts {
		r := PlayerResult{ID: t.ID, Name: t.Name, State: t.State, Replaced: t.Replaced, Dropped: t.Dropped}
		if t.EndedAt != nil {
			r.EndedAt = store.Ptr(t.EndedAt.UnixMilli())
		}
		var places []tournament.Placement
		if len(t.Placements) > 0 && json.Unmarshal(t.Placements, &places) == nil {
			for _, p := range places {
				for _, id := range p.Players {
					if id == playerID && (r.Place == 0 || p.Place < r.Place) {
						r.Place = p.Place
					}
				}
			}
		}
		out = append(out, r)
	}
	return out, nil
}
