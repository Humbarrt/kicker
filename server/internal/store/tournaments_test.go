package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTournaments(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mia, _ := s.CreatePlayer(ctx, Player{Name: "Mia", Color: "#1e3a8a"}, 0)
	tom, _ := s.CreatePlayer(ctx, Player{Name: "Tom", Color: "#1e3a8a"}, 0)
	guest, _ := s.GuestPlayer(ctx)

	// Setup: two 1-player teams and the guest twice (guests may take part several times).
	ps := []TournamentParticipant{{PlayerID: mia, TeamNo: Ptr(1), Seed: 1}, {PlayerID: tom, TeamNo: Ptr(2), Seed: 2},
		{PlayerID: guest.ID, Seed: 3}, {PlayerID: guest.ID, Seed: 4}}
	id, err := s.CreateTournament(ctx, Tournament{Name: "Friday", Settings: json.RawMessage(`{"stages":[{"format":"knockout"}]}`)}, ps, nil)
	if err != nil {
		t.Fatal(err)
	}
	tr, got, err := s.Tournament(ctx, id)
	if err != nil || tr.State != "setup" || len(got) != 4 || *got[1].TeamNo != 2 || string(tr.Settings) != `{"stages":[{"format":"knockout"}]}` {
		t.Fatalf("%+v %+v %v", tr, got, err)
	}
	tr.Name = "Friday cup"
	if err := s.UpdateTournamentSetup(ctx, tr, ps[:2], nil); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.ActiveTournament(ctx); a != nil {
		t.Errorf("active before the start: %+v", a)
	}

	// Lifecycle (spec 09 §3): only the allowed moves.
	if err := s.SetTournamentState(ctx, id, "setup", "finished", nil, nil, nil); !errors.Is(err, ErrTournamentState) {
		t.Errorf("setup → finished: %v", err)
	}
	if err := s.SetTournamentState(ctx, id, "setup", "running", nil, nil, map[string]int{"seed": 7}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTournamentState(ctx, id, "setup", "running", nil, nil, nil); !errors.Is(err, ErrTournamentState) {
		t.Errorf("started twice: %v", err)
	}
	if err := s.SetTournamentState(ctx, 999, "setup", "running", nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if err := s.UpdateTournamentSetup(ctx, tr, ps[:2], nil); !errors.Is(err, ErrTournamentState) {
		t.Errorf("setup changed while running: %v", err)
	}
	if a, _ := s.ActiveTournament(ctx); a == nil || a.ID != id || a.StartedAt == nil || a.Name != "Friday cup" {
		t.Errorf("active: %+v", a)
	}

	// A pairing with a game: team A played away; a voided game and a running one don't count.
	pid, err := s.PairingID(ctx, id, "s1-W-1-1", "t1-t2")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := s.PairingID(ctx, id, "s1-W-1-1", "t1-t2"); again != pid {
		t.Errorf("a second row: %d, %d", pid, again)
	}
	end := time.Now()
	game := func(state string, winner string, voided bool) int64 {
		g := Game{Rules: Rules{WinScore: 10, WinBy: 1, TimeTie: "golden_goal"}, PairingID: &pid, PairingASide: Ptr("away"), State: state,
			StartedAt: end, HomeGoals: 4, AwayGoals: 10}
		if state == "finished" {
			g.EndedAt, g.Winner = &end, &winner
		}
		gid, err := s.CreateGame(ctx, g, nil)
		if err != nil {
			t.Fatal(err)
		}
		if voided {
			if _, err := s.w.ExecContext(ctx, `UPDATE games SET voided = 1 WHERE id = ?`, gid); err != nil {
				t.Fatal(err)
			}
		}
		return gid
	}
	g1 := game("finished", "away", false)
	game("finished", "home", true)
	game("active", "", false)
	if g, _, err := s.Game(ctx, g1); err != nil || g.PairingASide == nil || *g.PairingASide != "away" {
		t.Errorf("game: %+v %v", g, err)
	}
	if err := s.SetWalkover(ctx, id, "s1-W-1-2", "b", nil); err != nil {
		t.Fatal(err)
	}
	res, err := s.TournamentResults(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	r := res["s1-W-1-1"]
	if r.ID != pid || len(r.Games) != 1 || r.Games[0].GameID != g1 || r.Games[0].ASide != "away" || *r.Games[0].Winner != "away" {
		t.Errorf("pairing: %+v", r)
	}
	if res["s1-W-1-2"].Walkover != "b" {
		t.Errorf("walkover: %+v", res)
	}
	if tid, key, err := s.TournamentOfPairing(ctx, pid); err != nil || tid != id || key != "s1-W-1-1" {
		t.Errorf("of pairing: %d %s %v", tid, key, err)
	}
	if err := s.DeleteTournament(ctx, id); !errors.Is(err, ErrInUse) {
		t.Errorf("deleted with games: %v", err)
	}

	// The history.
	if err := s.SetTournamentState(ctx, id, "running", "finished", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.TournamentEvents(ctx, id)
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	if want := "created edited started walkover finished"; strings.Join(kinds, " ") != want {
		t.Errorf("events %v", kinds)
	}
	if string(evs[2].Details) != `{"seed":7}` {
		t.Errorf("details %s", evs[2].Details)
	}

	// Deleting a tournament without games.
	id2, _ := s.CreateTournament(ctx, Tournament{Name: "Empty"}, nil, nil)
	if all, _ := s.Tournaments(ctx); len(all) != 2 || all[0].ID != id2 {
		t.Errorf("list: %+v", all) // setup before finished
	}
	if err := s.DeleteTournament(ctx, id2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Tournament(ctx, id2); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
}
