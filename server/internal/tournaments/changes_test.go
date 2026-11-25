package tournaments

import (
	"errors"
	"testing"

	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/tournament"
)

func (r *rig) tstate(id int64) string {
	tr, _, err := r.st.Tournament(r.ctx, id)
	if err != nil {
		r.t.Fatal(err)
	}
	return tr.State
}

func (r *rig) edit(game int64, ed engine.Edit) {
	r.t.Helper()
	if _, err := r.tbl.EditGame(r.ctx, game, ed); err != nil {
		r.t.Fatal(err)
	}
}

// A finished tournament is frozen: a goal edit doesn't change its winner; voiding a game of it
// reopens it, and the pairing is played again (spec 09 §11).
func TestFrozenAndReopened(t *testing.T) {
	r := newRig(t)
	id := r.tournament(`{"format":"knockout","rules":{"win_score":2,"win_by":1}}`, "Mia", "Tom")
	if err := r.svc.Start(r.ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Play(r.ctx, id, "s1-W-1-1"); err != nil {
		t.Fatal(err)
	}
	game := r.playing("Mia", "Tom").ID
	r.win(engine.Home, 2)
	r.eventually("finished", func() bool { return r.tstate(id) == "finished" })

	for range 3 { // Tom "wins" afterwards by hand
		r.edit(game, engine.Edit{Kind: engine.EditAddGoal, Side: engine.Away})
	}
	r.eventually("edit seen", func() bool {
		g, _, _ := r.st.Game(r.ctx, game)
		return g.Winner != nil && *g.Winner == "away"
	})
	if st := r.state(id); !st.Finished || st.Placements[0].Team != 1 {
		t.Fatalf("a goal edit changed the finished tournament: %+v", st.Placements)
	}

	r.edit(game, engine.Edit{Kind: engine.EditVoidGame})
	r.eventually("reopened", func() bool { return r.tstate(id) == "paused" })
	if p := r.state(id).Pairing("s1-W-1-1"); p.State != tournament.Ready {
		t.Fatalf("final after the void: %s", p.State)
	}
	if err := r.svc.Continue(r.ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Play(r.ctx, id, "s1-W-1-1"); err != nil {
		t.Fatal(err)
	}
	r.playing("Mia", "Tom")
	r.win(engine.Away, 2)
	r.eventually("finished again", func() bool { return r.tstate(id) == "finished" })
	if st := r.state(id); st.Placements[0].Team != 2 {
		t.Errorf("placements %+v", st.Placements)
	}
}

// Replacing a player and teams dropping out (spec 09 §10).
func TestReplaceAndDropOut(t *testing.T) {
	r := newRig(t)
	eva, err := r.st.CreatePlayer(r.ctx, store.Player{Name: "Eva", Color: "#1e3a8a"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	id := r.tournament(`{"format":"knockout","rules":{"win_score":2,"win_by":1}}`, "Mia", "Tom", "Ann", "Ben")
	if err := r.svc.Start(r.ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	_, ps, _ := r.st.Tournament(r.ctx, id)
	tom := ps[1].ID
	if err := r.svc.Replace(r.ctx, id, tom, r.players["Mia"], nil); !errors.Is(err, ErrInTournament) {
		t.Errorf("Mia twice: %v", err)
	}
	if err := r.svc.Replace(r.ctx, id, tom, eva, nil); err != nil {
		t.Fatal(err)
	}
	if got := r.state(id).Setup.Teams[1].Players[0]; got != eva {
		t.Errorf("team 2 is %d, not Eva", got)
	}
	if err := r.svc.Replace(r.ctx, id, tom, r.players["Tom"], nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("replacing a replaced player: %v", err)
	}

	// Ben (team 4) can't drop out while his pairing is on the table.
	if err := r.svc.Play(r.ctx, id, "s1-W-1-1"); err != nil {
		t.Fatal(err)
	}
	r.playing("Mia", "Ben")
	if err := r.svc.DropOut(r.ctx, id, 4, nil); !errors.Is(err, ErrOnTable) {
		t.Errorf("dropout on the table: %v", err)
	}
	r.win(engine.Home, 2)
	r.eventually("semi-final 1", func() bool { return r.state(id).Pairing("s1-W-1-1").State == tournament.Done })

	// Team 2 (Eva) drops out: Ann reaches the final without playing.
	if err := r.svc.DropOut(r.ctx, id, 2, nil); err != nil {
		t.Fatal(err)
	}
	st := r.state(id)
	if p := st.Pairing("s1-W-1-2"); p.State != tournament.Done || !p.Walkover || p.WinnerEntrant().Team != 3 {
		t.Fatalf("semi-final 2: %+v", p)
	}
	if err := r.svc.DropOut(r.ctx, id, 2, nil); !errors.Is(err, ErrBadSetup) {
		t.Errorf("dropping out twice: %v", err)
	}
	if f := st.Pairing("s1-W-2-1"); f.State != tournament.Ready {
		t.Errorf("final: %s", f.State)
	}
}

// An edit that changes who won a pairing after its follow-up was played: a conflict, and the
// rebuild plays the follow-up again (spec 09 §11).
func TestConflictAndRebuild(t *testing.T) {
	r := newRig(t)
	id := r.tournament(`{"format":"knockout","third_place":true,"rules":{"win_score":2,"win_by":1}}`, "Mia", "Tom", "Ann", "Ben")
	if err := r.svc.Start(r.ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	play := func(key, home, away string, winner engine.Side) int64 {
		t.Helper()
		if err := r.svc.Play(r.ctx, id, key); err != nil {
			t.Fatal(err)
		}
		g := r.playing(home, away).ID
		r.win(winner, 2)
		r.eventually(key, func() bool { return r.state(id).Pairing(key).State == tournament.Done })
		return g
	}
	semi := play("s1-W-1-1", "Mia", "Ben", engine.Home)
	play("s1-W-1-2", "Tom", "Ann", engine.Home)
	play("s1-W-2-1", "Mia", "Tom", engine.Home)
	if c, _ := r.svc.Conflicts(r.ctx, id, r.state(id)); len(c) != 0 {
		t.Fatalf("conflicts before the edit: %v", c)
	}
	if err := r.svc.Rebuild(r.ctx, id, nil); !errors.Is(err, ErrNoConflicts) {
		t.Errorf("rebuild without conflicts: %v", err)
	}

	for range 3 { // Ben won the semi-final after all
		r.edit(semi, engine.Edit{Kind: engine.EditAddGoal, Side: engine.Away})
	}
	r.eventually("conflict", func() bool {
		c, _ := r.svc.Conflicts(r.ctx, id, r.state(id))
		return len(c) == 1 && c[0] == "s1-W-2-1"
	})
	if err := r.svc.Rebuild(r.ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	st := r.state(id)
	if c, _ := r.svc.Conflicts(r.ctx, id, st); len(c) != 0 {
		t.Errorf("conflicts after the rebuild: %v", c)
	}
	f := st.Pairing("s1-W-2-1")
	if f.State != tournament.Ready || f.Sides() != "t2-t4" {
		t.Errorf("final after the rebuild: %s %s", f.State, f.Sides())
	}
	// The final's game stays a normal game.
	if g, _, _ := r.st.Game(r.ctx, semi+2); g.PairingID != nil {
		t.Errorf("the old final is still linked")
	}
}
