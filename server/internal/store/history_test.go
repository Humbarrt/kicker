package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestListGamesFilters(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mia, _ := s.CreatePlayer(ctx, Player{Name: "Mia", Color: "#1e3a8a"}, 0)
	tom, _ := s.CreatePlayer(ctx, Player{Name: "Tom", Color: "#1e3a8a"}, 0)
	ben, _ := s.CreatePlayer(ctx, Player{Name: "Ben", Color: "#1e3a8a"}, 0)
	guest, _ := s.GuestPlayer(ctx)
	t0 := time.UnixMilli(1_763_000_000_000)
	n := 0
	game := func(state string, home, away []Participant, hg, ag int) int64 {
		t.Helper()
		n++
		start := t0.Add(time.Duration(n) * time.Hour)
		var ps []Participant
		for i, p := range home {
			p.Side, p.Slot = "home", i+1
			ps = append(ps, p)
		}
		for i, p := range away {
			p.Side, p.Slot = "away", i+1
			ps = append(ps, p)
		}
		id, err := s.CreateGame(ctx, Game{Rules: Rules{WinScore: 6, WinBy: 1, TimeTie: "draw"}, State: "active", StartedAt: start,
			Participants: ps}, []Event{{Type: "game_started", Source: "table", CreatedAt: start}})
		if err != nil {
			t.Fatal(err)
		}
		var winner *string
		if state == "finished" && hg != ag {
			winner = Ptr(map[bool]string{true: "home", false: "away"}[hg > ag])
		}
		end := start.Add(10 * time.Minute)
		if _, err := s.AppendEvents(ctx, id, []Event{{Type: "finished", Source: "system", CreatedAt: end}},
			&GameUpdate{State: state, EndedAt: &end, HomeGoals: hg, AwayGoals: ag, Winner: winner, Participants: ps}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	p := func(id int64) Participant { return Participant{PlayerID: Ptr(id)} }
	coin := Participant{CoinUID: Ptr("04A1")}
	g1 := game("finished", []Participant{p(mia)}, []Participant{p(tom)}, 6, 0)               // solo, shutout, Mia wins
	g2 := game("finished", []Participant{p(mia), p(ben)}, []Participant{p(tom)}, 5, 6)       // team, close, Mia loses
	g3 := game("finished", []Participant{p(mia)}, []Participant{p(ben), p(guest.ID)}, 5, 5)  // draw, with the guest
	g4 := game("abandoned", []Participant{p(tom)}, []Participant{coin}, 2, 1)                // abandoned, unknown coin
	g5 := game("finished", []Participant{p(tom), p(ben)}, []Participant{p(mia), coin}, 6, 2) // Mia loses, unknown coin
	g6 := game("finished", nil, nil, 3, 1)                                                   // nobody on either side
	if _, err := s.w.ExecContext(ctx, `UPDATE games SET voided = 1, edits = 1 WHERE id = ?`, g5); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		f    GameFilter
		want []int64
	}{
		{"all", GameFilter{}, []int64{g6, g5, g4, g3, g2, g1}},
		{"mia", GameFilter{PlayerIDs: []int64{mia}}, []int64{g5, g3, g2, g1}},
		{"mia and ben", GameFilter{PlayerIDs: []int64{mia, ben}}, []int64{g5, g3, g2}},
		{"mia with ben", GameFilter{PlayerIDs: []int64{mia, ben}, Relation: "together"}, []int64{g2}},
		{"mia against ben", GameFilter{PlayerIDs: []int64{mia, ben}, Relation: "against"}, []int64{g5, g3}},
		{"mia won", GameFilter{PlayerIDs: []int64{mia}, Result: "win"}, []int64{g1}},
		{"mia lost", GameFilter{PlayerIDs: []int64{mia}, Result: "loss"}, []int64{g5, g2}},
		{"draws", GameFilter{Result: "draw"}, []int64{g3}},
		{"won without a player", GameFilter{Result: "win"}, []int64{g6, g5, g4, g3, g2, g1}},
		{"solo", GameFilter{Kind: "solo"}, []int64{g4, g1}},
		{"team (a game without players is neither)", GameFilter{Kind: "team"}, []int64{g5, g3, g2}},
		{"abandoned", GameFilter{Status: "abandoned"}, []int64{g4}},
		{"voided", GameFilter{Flags: []string{"voided"}}, []int64{g5}},
		{"edited", GameFilter{Flags: []string{"edited"}}, []int64{g5}},
		{"unassigned", GameFilter{Flags: []string{"unassigned"}}, []int64{g5, g4}},
		{"unknown: guest, unknown coins, empty sides", GameFilter{Flags: []string{"unknown"}}, []int64{g6, g5, g4, g3}},
		{"shutout", GameFilter{Flags: []string{"shutout"}}, []int64{g1}},
		{"close", GameFilter{Flags: []string{"close"}}, []int64{g2}},
		{"close and unassigned", GameFilter{Flags: []string{"close", "unassigned"}}, nil},
		{"from g2 to g4", GameFilter{From: t0.Add(2 * time.Hour), To: t0.Add(4 * time.Hour)}, []int64{g3, g2}},
		{"page", GameFilter{BeforeID: g3, Limit: 1}, []int64{g2}},
	}
	for _, c := range cases {
		list, err := s.ListGames(ctx, c.f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var got []int64
		for _, g := range list {
			got = append(got, g.ID)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
