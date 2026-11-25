package tournaments

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/table"
	"github.com/Humbarrt/kicker/server/internal/tournament"
)

type rig struct {
	t       *testing.T
	ctx     context.Context
	st      *store.Store
	tbl     *table.Table
	svc     *Service
	players map[string]int64
}

func newRig(t *testing.T) *rig {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := engine.DefaultConfig()
	cfg.KickoffDelay = 20 * time.Millisecond
	cfg.FinishedHold = 300 * time.Millisecond
	r := &rig{t: t, ctx: ctx, st: st, players: map[string]int64{}}
	r.tbl = table.New(table.Options{Store: st, Config: cfg, Logger: log,
		OnGameFinished: func(id int64) { r.svc.GameFinished(id) },
		OnCheckIn:      func(c engine.CheckIn) { r.svc.CheckIn(c) },
		OnPhase:        func(from, to engine.Phase) { r.svc.Phase(from, to) }})
	r.svc = New(Options{Store: st, Table: r.tbl, Logger: log})
	go func() { _ = r.tbl.Run(ctx) }()
	go r.svc.Run(ctx)
	<-r.tbl.Ready()
	for _, n := range []string{"Mia", "Tom", "Ann", "Ben"} {
		id, err := st.CreatePlayer(ctx, store.Player{Name: n, Color: "#1e3a8a"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		r.players[n] = id
	}
	return r
}

// tournament creates a tournament of 1-player teams in the given order (manual seeds).
func (r *rig) tournament(stage string, names ...string) int64 {
	var ps []store.TournamentParticipant
	for i, n := range names {
		ps = append(ps, store.TournamentParticipant{PlayerID: r.players[n], TeamNo: store.Ptr(i + 1), Seed: i + 1})
	}
	settings := `{"team_mode":"fixed","manual_seeds":true,"stages":[` + stage + `]}`
	id, err := r.st.CreateTournament(r.ctx, store.Tournament{Name: "Test", Settings: json.RawMessage(settings)}, ps, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

func (r *rig) eventually(what string, ok func() bool) {
	r.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			v := r.tbl.View()
			gs := ""
			if v.Game != nil {
				gs = fmt.Sprintf("game %d %s %d:%d", v.Game.ID, v.Game.Status, v.Game.Home, v.Game.Away)
			}
			r.t.Fatalf("timed out waiting for %s (table %s, %s)", what, v.Phase, gs)
		}
	}
}

func (r *rig) checkIn(name string, side engine.Side) {
	r.t.Helper()
	uid := map[string]string{"Mia": "AAAAAAA1", "Tom": "AAAAAAA2", "Ann": "AAAAAAA3", "Ben": "AAAAAAA4"}[name]
	if err := r.tbl.Do(r.ctx, engine.CoinInserted{Side: side, UID: uid, PlayerID: r.players[name]}); err != nil {
		r.t.Fatal(err)
	}
}

// playing waits for a game of the given pairing and returns it.
func (r *rig) playing(home, away string) *table.GameView {
	r.t.Helper()
	r.eventually(home+" vs "+away, func() bool {
		v := r.tbl.View()
		if v.Phase != string(engine.PhasePlaying) || v.Game == nil {
			return false
		}
		h, a := v.Game.Roster["home"], v.Game.Roster["away"]
		return len(h) == 1 && len(a) == 1 && h[0].PlayerID != nil && *h[0].PlayerID == r.players[home] &&
			a[0].PlayerID != nil && *a[0].PlayerID == r.players[away]
	})
	return r.tbl.View().Game
}

// win scores goals for one side until the game is over.
func (r *rig) win(side engine.Side, goals int) {
	r.t.Helper()
	role := "away_goal" // ball in the away net: a point for home
	if side == engine.Away {
		role = "home_goal"
	}
	id := r.tbl.View().Game.ID
	for range goals {
		if err := r.tbl.Do(r.ctx, engine.SensorGoal{Role: role}); err != nil {
			r.t.Fatal(err)
		}
	}
	// The game itself, not the table: the next game of a best-of follows within milliseconds.
	r.eventually("game end", func() bool {
		g, _, err := r.st.Game(r.ctx, id)
		return err == nil && g.State == "finished"
	})
}

func (r *rig) state(id int64) *tournament.State {
	st, _, _, err := r.svc.State(r.ctx, id)
	if err != nil {
		r.t.Fatal(err)
	}
	return st
}

// A 4-team knockout from check-ins to placements (spec 09 §8).
func TestKnockoutOnTheTable(t *testing.T) {
	r := newRig(t)
	id := r.tournament(`{"format":"knockout","third_place":true,"rules":{"win_score":2,"win_by":1}}`, "Mia", "Tom", "Ann", "Ben")
	if err := r.svc.Start(r.ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Start(r.ctx, id, nil); err == nil {
		t.Error("started twice")
	}
	// One player of each team: the pairing goes on the table. Mia is seed 1 – team A, at home.
	r.checkIn("Mia", engine.Home)
	if r.tbl.View().Phase != string(engine.PhaseIdle) {
		t.Fatalf("one check-in started something: %s", r.tbl.View().Phase)
	}
	r.checkIn("Ben", engine.Home) // the side of the coin doesn't matter
	g := r.playing("Mia", "Ben")
	if g.PairingID == 0 || g.PairingASide != "home" || g.Rules.WinScore != 2 || *g.FirstBall != "home" {
		t.Errorf("game 1: %+v", g)
	}
	r.win(engine.Home, 2)
	r.eventually("Mia advances", func() bool { return r.state(id).Pairing("s1-W-1-1").State == tournament.Done })

	// Out of order: 2–3 is the other semi-final; "Play" starts it without check-ins.
	if err := r.svc.Play(r.ctx, id, "s1-W-1-2"); err != nil {
		t.Fatal(err)
	}
	r.playing("Tom", "Ann")
	r.win(engine.Away, 2) // Ann wins
	r.eventually("semi-finals done", func() bool { return len(r.state(id).ReadyPairings()) == 2 })

	// Final (Mia–Ann) and 3rd place (Tom–Ben): whichever checks in first.
	r.checkIn("Tom", engine.Home)
	r.checkIn("Ben", engine.Away)
	r.playing("Tom", "Ben")
	r.win(engine.Away, 2) // Ben 3rd
	r.checkIn("Ann", engine.Home)
	r.checkIn("Mia", engine.Away)
	r.playing("Mia", "Ann")
	r.win(engine.Away, 2) // Ann wins the final

	r.eventually("finished", func() bool {
		tr, _, _ := r.st.Tournament(r.ctx, id)
		return tr.State == "finished"
	})
	st := r.state(id)
	if len(st.Placements) != 3 || st.Placements[0].Team != 3 || st.Placements[1].Team != 1 || st.Placements[2].Team != 4 {
		t.Errorf("placements %+v", st.Placements)
	}
	// The table is free again: a coin registers its player.
	r.eventually("free play", func() bool { return r.tbl.View().Phase != string(engine.PhaseFinished) })
	r.checkIn("Tom", engine.Home)
	r.eventually("registration", func() bool { return len(r.tbl.View().Registrations["home"]) == 1 })
}

// Best of 3: sides swap every game, the loser of a game gets the next first ball, and the next
// game starts by itself (spec 09 §7.3, §8). Pausing hands the table back; continuing resumes.
func TestBestOfAndPause(t *testing.T) {
	r := newRig(t)
	id := r.tournament(`{"format":"knockout","best_of":3,"rules":{"win_score":2,"win_by":1}}`, "Mia", "Tom")
	if err := r.svc.Start(r.ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	r.checkIn("Mia", engine.Home)
	r.checkIn("Tom", engine.Away)
	r.playing("Mia", "Tom")
	r.win(engine.Away, 2) // Tom wins game 1
	// Game 2: sides swapped – Mia (team A) away; Mia lost, so her side gets the ball.
	g := r.playing("Tom", "Mia")
	if g.PairingASide != "away" || *g.FirstBall != "away" {
		t.Errorf("game 2: a side %s, first ball %v", g.PairingASide, *g.FirstBall)
	}
	if err := r.tbl.Do(r.ctx, engine.SensorGoal{Role: "home_goal"}); err != nil { // 0:1 for Mia
		t.Fatal(err)
	}

	// Pause: the game pauses, the table is free.
	if err := r.svc.Pause(r.ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	if tr, _, _ := r.st.Tournament(r.ctx, id); tr.State != "paused" {
		t.Errorf("state %s", tr.State)
	}
	if err := r.svc.Play(r.ctx, id, "s1-W-1-1"); err == nil {
		t.Error("played while paused")
	}
	// Continue: the paused game comes back with its score.
	if err := r.svc.Continue(r.ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	g = r.playing("Tom", "Mia")
	if g.Away != 1 || g.Status != "active" {
		t.Errorf("resumed: %+v", g)
	}
	r.win(engine.Away, 1) // Mia 2:0 → 1:1 in games
	g = r.playing("Mia", "Tom")
	if *g.FirstBall != "away" { // Tom lost game 2: his side (away) gets the ball
		t.Errorf("game 3 first ball %s", *g.FirstBall)
	}
	// The table going to sleep during a game pauses the tournament.
	if err := r.tbl.Do(r.ctx, engine.PauseGame{}); err != nil {
		t.Fatal(err)
	}
	r.svc.Phase(engine.PhasePlaying, engine.PhaseSleeping)
	r.eventually("paused by sleep", func() bool {
		tr, _, _ := r.st.Tournament(r.ctx, id)
		return tr.State == "paused"
	})
	evs, _ := r.st.TournamentEvents(r.ctx, id)
	if last := evs[len(evs)-1]; last.Kind != "paused" || string(last.Details) != `{"reason":"sleep"}` {
		t.Errorf("event %+v", last)
	}
}
