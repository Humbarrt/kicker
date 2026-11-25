package tournaments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/tournament"
)

// Changing a running or paused tournament (spec 09 §10–11): replacing a player, a team (or
// Monster DYP player) dropping out, renaming and the rules of later stages, and rebuilding the
// pairings an edit put in conflict.

// Replace puts another player in a participant's place from the next game on.
func (s *Service) Replace(ctx context.Context, id, participant, player int64, author *int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ps, err := s.opts.Store.Tournament(ctx, id)
	if err != nil {
		return err
	}
	if t.State != "running" && t.State != "paused" {
		return store.ErrTournamentState
	}
	pl, err := s.opts.Store.Player(ctx, player)
	if errors.Is(err, store.ErrNotFound) || err == nil && pl.ArchivedAt != nil {
		return fmt.Errorf("%w: no such player", ErrBadSetup)
	} else if err != nil {
		return err
	}
	var old *store.TournamentParticipant
	for i, p := range current(ps) {
		if p.PlayerID == player && !pl.IsGuest {
			return ErrInTournament
		}
		if p.ID == participant {
			old = &current(ps)[i]
		}
	}
	if old == nil {
		return store.ErrNotFound
	}
	details := map[string]any{"old_player": old.PlayerID, "new_player": player}
	if old.TeamNo != nil {
		details["team"] = *old.TeamNo
	}
	if _, err := s.opts.Store.ReplaceParticipant(ctx, id, participant, player, author, details); err != nil {
		return err
	}
	s.publish(id)
	return nil
}

// DropOut takes a team (team number) or, in Monster DYP, a player (place from 1) out of the
// tournament: its open pairings are walkovers or annulled, and it isn't drawn any more (§10).
// Not while it is on the table: that game is finished or abandoned first.
func (s *Service) DropOut(ctx context.Context, id int64, n int, author *int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, t, set, err := s.derive(ctx, id)
	if err != nil {
		return err
	}
	if t.State != "running" && t.State != "paused" {
		return store.ErrTournamentState
	}
	if id == s.active && s.staged != "" {
		if p := st.Pairing(s.staged); p != nil && (involves(p.A, n, set.TeamMode) || involves(p.B, n, set.TeamMode)) {
			return ErrOnTable
		}
	}
	d, err := st.DropFor(n)
	if errors.Is(err, tournament.ErrNoEntrant) || errors.Is(err, tournament.ErrAlreadyDropped) {
		return fmt.Errorf("%w: %v", ErrBadSetup, err)
	} else if err != nil {
		return err
	}
	if set.Drops == nil {
		set.Drops = map[int]tournament.Drop{}
	}
	set.Drops[n] = d
	raw, err := json.Marshal(set)
	if err != nil {
		return err
	}
	_, ps, err := s.opts.Store.Tournament(ctx, id)
	if err != nil {
		return err
	}
	var who []int64
	details := map[string]any{"annul": d.Annul}
	for i, p := range current(ps) {
		if set.TeamMode == Monster && i+1 == n || set.TeamMode != Monster && p.TeamNo != nil && *p.TeamNo == n {
			who = append(who, p.ID)
			details["players"] = append(asInts(details["players"]), p.PlayerID)
		}
	}
	if set.TeamMode == Monster {
		details["slot"] = n
	} else {
		details["team"] = n
	}
	if err := s.opts.Store.DropOut(ctx, id, raw, who, author, details); err != nil {
		return err
	}
	s.log.Info("dropout", "tournament", id, "entrant", n, "annul", d.Annul)
	s.publish(id)
	if id != s.active {
		return nil
	}
	// The walkovers may decide the tournament, or let another pairing start.
	if st, _, _, err = s.derive(ctx, id); err != nil {
		return err
	} else if st.Finished && s.staged == "" {
		return s.finish(ctx, id, st)
	}
	return s.stageNext(ctx, "")
}

func asInts(v any) []int64 {
	x, _ := v.([]int64)
	return x
}

// involves reports whether team (or Monster DYP place) n plays on a side.
func involves(e *tournament.Entrant, n int, mode string) bool {
	if mode == Monster {
		return slices.Contains(e.Slots, n)
	}
	return e.Team == n
}

// Change renames a running or paused tournament and, with rules, sets the game rules of the
// stages that haven't started yet.
func (s *Service) Change(ctx context.Context, id int64, name string, rules *GameRules, presetID *int64, author *int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return fmt.Errorf("%w: a name of 1–80 characters", ErrBadSetup)
	}
	st, t, set, err := s.derive(ctx, id)
	if err != nil {
		return err
	}
	details := map[string]any{}
	if name != t.Name {
		details["name"] = name
	}
	if rules != nil {
		if err := rules.engine().Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrBadSetup, err)
		}
		from := len(st.Stages) // the first stage not started yet
		if from >= len(set.Stages) {
			return fmt.Errorf("%w: every stage has started", ErrBadSetup)
		}
		for i := from; i < len(set.Stages); i++ {
			r := *rules
			set.Stages[i].Rules = &r
		}
		set.PresetID = presetID
		details["rules_from_stage"] = from + 1
	}
	raw, err := json.Marshal(set)
	if err != nil {
		return err
	}
	if err := s.opts.Store.ChangeTournament(ctx, id, name, raw, author, "edited", details); err != nil {
		return err
	}
	s.publish(id)
	return nil
}

// Conflicts lists the pairings whose games were played by other sides than the tournament puts
// there now, after an edit (spec 09 §11). A finished tournament has none: it is frozen.
func (s *Service) Conflicts(ctx context.Context, id int64, st *tournament.State) ([]string, error) {
	t, _, err := s.opts.Store.Tournament(ctx, id)
	if err != nil || t.State == "finished" {
		return nil, err
	}
	raw, err := s.opts.Store.TournamentResults(ctx, id)
	if err != nil {
		return nil, err
	}
	return st.Conflicts(playedSides(raw)), nil
}

func playedSides(raw map[string]store.PairingResult) map[string]string {
	out := map[string]string{}
	for k, r := range raw {
		if len(r.Games) > 0 && r.Sides != "" {
			out[k] = r.Sides
		}
	}
	return out
}

// Rebuild takes the games of the conflicting pairings – and of the ones that depended on them –
// out of the tournament: they stay as normal rated games, and those pairings are played again.
func (s *Service) Rebuild(ctx context.Context, id int64, author *int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == s.active && s.staged != "" {
		return ErrOnTable
	}
	t, ps, err := s.opts.Store.Tournament(ctx, id)
	if err != nil {
		return err
	}
	if t.State != "running" && t.State != "paused" {
		return store.ErrTournamentState
	}
	var set Settings
	if err := json.Unmarshal(t.Settings, &set); err != nil {
		return err
	}
	setup, err := s.setupOf(set, ps)
	if err != nil {
		return err
	}
	raw, err := s.opts.Store.TournamentResults(ctx, id)
	if err != nil {
		return err
	}
	// Take out the conflicts until none are left: without a pairing's result, the ones drawn
	// after it may change too.
	var out []string
	for range 100 {
		st, err := tournament.Derive(setup, Results(raw))
		if err != nil {
			return err
		}
		c := st.Conflicts(playedSides(raw))
		if len(c) == 0 {
			break
		}
		for _, k := range c {
			delete(raw, k)
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		return ErrNoConflicts
	}
	if err := s.opts.Store.UnlinkPairings(ctx, id, out, author, map[string]any{"pairings": out}); err != nil {
		return err
	}
	s.log.Info("tournament rebuilt", "tournament", id, "pairings", out)
	s.publish(id)
	if id == s.active {
		return s.stageNext(ctx, "")
	}
	return nil
}
