package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/table"
	"github.com/Humbarrt/kicker/server/internal/tournament"
	"github.com/Humbarrt/kicker/server/internal/tournaments"
)

// Tournaments (M6 task 7): a large double knockout and a large Monster DYP played to the end on
// the real table – every pairing put on it with "Play", its goals scored by the sensor – while
// the tournament page is requested over HTTP after every pairing.

// BudgetTournament is the tournament page as served (derived from setup + results), p95; and the
// time from a pairing's last goal until the page shows it decided.
const BudgetTournament = 150 * time.Millisecond

// abandonLeftovers abandons a tournament an interrupted run left running or paused: it owns the
// table (free play is refused) and would be restored at the next start.
func abandonLeftovers(ctx context.Context, svc *tournaments.Service) error {
	ts, err := svc.Tournaments(ctx)
	if err != nil {
		return err
	}
	for _, t := range ts {
		if t.State == "running" || t.State == "paused" {
			if err := svc.Abandon(ctx, t.ID, nil); err != nil {
				return fmt.Errorf("abandoning tournament %d of an earlier run: %w", t.ID, err)
			}
		}
	}
	return nil
}

// tournamentPlayers returns n players for the tournaments, creating the missing ones.
func tournamentPlayers(ctx context.Context, st *store.Store, n int) ([]int64, error) {
	ps, err := st.Players(ctx, true)
	if err != nil {
		return nil, err
	}
	byName := map[string]int64{}
	for _, p := range ps {
		byName[p.Name] = p.ID
	}
	out := make([]int64, n)
	for i := range out {
		name := fmt.Sprintf("Tournament %03d", i+1)
		id, ok := byName[name]
		if !ok {
			if id, err = st.CreatePlayer(ctx, store.Player{Name: name, Color: "#57534e"}, 0); err != nil {
				return nil, err
			}
		}
		out[i] = id
	}
	return out, nil
}

func measureTournaments(ctx context.Context, base string, tbl *table.Table, st *store.Store, svc *tournaments.Service, o Options) ([]Result, error) {
	// The goal measurement's game is still on: home wins it.
	for i := 0; tbl.View().Phase == string(engine.PhasePlaying); i++ {
		if i > 200 {
			return nil, fmt.Errorf("the table's game doesn't end")
		}
		if err := tbl.Do(ctx, engine.SensorGoal{Role: "away_goal"}); err != nil {
			return nil, err
		}
	}
	// No countdown, no pause after a game: thousands of seconds otherwise.
	cfg := engine.DefaultConfig() // as the load test's table was made; not saved
	cfg.KickoffDelay, cfg.FinishedHold = 0, 0
	if err := tbl.Do(ctx, engine.SetConfig{Config: cfg}); err != nil {
		return nil, err
	}
	n := max(o.TournamentTeams, o.MonsterPlayers)
	pids, err := tournamentPlayers(ctx, st, n)
	if err != nil {
		return nil, err
	}
	var out []Result
	for _, run := range []struct {
		label    string
		mode     string
		stage    string
		entrants int
	}{
		{fmt.Sprintf("%d-team double knockout", o.TournamentTeams), tournaments.FixedTeams, `{"format":"double_knockout"}`, o.TournamentTeams},
		{fmt.Sprintf("%d-player Monster DYP", o.MonsterPlayers), tournaments.Monster, `{"format":"monster_dyp"}`, o.MonsterPlayers},
	} {
		res, err := playTournament(ctx, base, tbl, st, svc, o, run.label, run.mode, run.stage, pids[:run.entrants])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", run.label, err)
		}
		out = append(out, res...)
	}
	return out, nil
}

// playTournament creates, starts and plays one tournament to its end.
func playTournament(ctx context.Context, base string, tbl *table.Table, st *store.Store, svc *tournaments.Service,
	o Options, label, mode, stage string, pids []int64) ([]Result, error) {
	rng := rand.New(rand.NewPCG(o.Seed, uint64(len(pids))))
	var ps []store.TournamentParticipant
	for i, id := range pids {
		p := store.TournamentParticipant{PlayerID: id, Seed: i + 1}
		if mode == tournaments.FixedTeams {
			p.TeamNo = store.Ptr(i + 1)
		}
		ps = append(ps, p)
	}
	// Short games (first to 3) and golden goal: the measured part is the tournament, not the game.
	var set map[string]any
	if err := json.Unmarshal([]byte(`{"team_mode":"`+mode+`","manual_seeds":true,"stages":[`+stage+`]}`), &set); err != nil {
		return nil, err
	}
	set["stages"].([]any)[0].(map[string]any)["rules"] = map[string]int{"win_score": 3, "win_by": 1}
	raw, _ := json.Marshal(set)
	id, err := st.CreateTournament(ctx, store.Tournament{Name: "Load test: " + label, Settings: raw}, ps, nil)
	if err != nil {
		return nil, err
	}
	if err := svc.Start(ctx, id, nil); err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	page := func() (time.Duration, error) {
		t0 := time.Now()
		resp, err := client.Get(fmt.Sprintf("%s/api/tournaments/%d", base, id))
		if err != nil {
			return 0, err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("tournament page: %d", resp.StatusCode)
		}
		return time.Since(t0), nil
	}

	var pages, decided []time.Duration
	t0 := time.Now()
	pairings := 0
	for {
		st8, err := svc.Tournaments(ctx)
		if err != nil {
			return nil, err
		}
		if stateOf(st8, id) == "finished" {
			break
		}
		state, _, _, err := svc.State(ctx, id)
		if err != nil {
			return nil, err
		}
		ready := state.ReadyPairings()
		if len(ready) == 0 {
			return nil, fmt.Errorf("stuck after %d pairings: nothing ready, not finished", pairings)
		}
		p := ready[rng.IntN(len(ready))] // in any order (spec 09 §7.2)
		if err := waitPhase(tbl, engine.PhaseIdle, engine.PhaseFinished); err != nil {
			return nil, err
		}
		if err := svc.Play(ctx, id, p.Key); err != nil {
			return nil, fmt.Errorf("playing %s: %w", p.Key, err)
		}
		if err := waitPhase(tbl, engine.PhasePlaying); err != nil {
			return nil, err
		}
		// The loser scores 0–2, then the winner 3.
		win, lose := "away_goal", "home_goal" // a ball in the away net is a point for home
		if rng.IntN(2) == 1 {
			win, lose = lose, win
		}
		for range rng.IntN(3) {
			if err := tbl.Do(ctx, engine.SensorGoal{Role: lose}); err != nil {
				return nil, err
			}
		}
		for range 2 {
			if err := tbl.Do(ctx, engine.SensorGoal{Role: win}); err != nil {
				return nil, err
			}
		}
		last := time.Now()
		if err := tbl.Do(ctx, engine.SensorGoal{Role: win}); err != nil {
			return nil, err
		}
		// Until the tournament has the result (the service hears of the game asynchronously).
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
			s, _, _, err := svc.State(ctx, id)
			if err != nil {
				return nil, err
			}
			if q := s.Pairing(p.Key); q == nil || q.State == tournament.Done {
				break
			}
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("pairing %s not decided after its game", p.Key)
			}
		}
		decided = append(decided, time.Since(last))
		d, err := page()
		if err != nil {
			return nil, err
		}
		pages = append(pages, d)
		pairings++
	}
	total := time.Since(t0)
	return []Result{
		result(fmt.Sprintf("%s: tournament page, %d times", label, len(pages)), pages, scale(BudgetTournament, o.Scale)),
		result(fmt.Sprintf("%s: last goal → pairing decided", label), decided, scale(BudgetTournament, o.Scale)),
		result(fmt.Sprintf("%s: %d pairings played to the end", label, pairings), []time.Duration{total}, 0),
	}, nil
}

func stateOf(ts []store.Tournament, id int64) string {
	for _, t := range ts {
		if t.ID == id {
			return t.State
		}
	}
	return ""
}

func waitPhase(tbl *table.Table, phases ...engine.Phase) error {
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
		v := tbl.View().Phase
		for _, p := range phases {
			if v == string(p) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the table is %s, not %v", v, phases)
		}
	}
}
