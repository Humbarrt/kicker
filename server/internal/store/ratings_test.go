package store

import (
	"context"
	"testing"
	"time"
)

func TestGameProjectionAndRatings(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mia, _ := s.CreatePlayer(ctx, Player{Name: "Mia", Color: "#1e3a8a"}, 0)
	tom, _ := s.CreatePlayer(ctx, Player{Name: "Tom", Color: "#1e3a8a"}, 0)
	t0 := time.UnixMilli(1_763_000_000_000)
	finish := func(start time.Time, winner string, ps ...Participant) int64 {
		id, err := s.CreateGame(ctx, Game{Rules: Rules{WinScore: 5, WinBy: 1, TimeTie: "golden_goal"}, State: "active", StartedAt: start,
			Participants: ps}, []Event{{Type: "game_started", Source: "table", CreatedAt: start}})
		if err != nil {
			t.Fatal(err)
		}
		end := start.Add(time.Minute)
		if _, err := s.AppendEvents(ctx, id, []Event{{Type: "finished", Side: &winner, Source: "system", CreatedAt: end}},
			&GameUpdate{State: "finished", EndedAt: &end, HomeGoals: 5, AwayGoals: 3, Winner: &winner, Participants: ps}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	home := Participant{Side: "home", Slot: 1, PlayerID: &mia}
	away := Participant{Side: "away", Slot: 1, PlayerID: &tom}
	unknown := Participant{Side: "away", Slot: 2, CoinUID: Ptr("04A1B2C3")}
	g1 := finish(t0, "home", home, away)
	g2 := finish(t0.Add(time.Hour), "away", home, away, unknown)

	row, _, _ := s.Game(ctx, g1)
	if row.HomeGoals != 5 || row.AwayGoals != 3 || *row.Winner != "home" {
		t.Errorf("result columns: %+v", row)
	}
	ps, _ := s.Participants(ctx, g2)
	if len(ps) != 3 || ps[2].PlayerID != nil || *ps[2].CoinUID != "04A1B2C3" {
		t.Errorf("participants: %+v", ps)
	}

	games, err := s.RatedGames(ctx, GamePoint{}, true, 100)
	if err != nil || len(games) != 2 || games[0].ID != g1 || len(games[1].Participants) != 3 {
		t.Fatalf("rated games: %+v %v", games, err)
	}
	p2, to, ok, _ := s.RatingSpan(ctx, g2)
	if !ok || p2.ID != g2 || to != p2 {
		t.Fatalf("span: %+v %+v", p2, to)
	}
	if games, _ := s.RatedGames(ctx, p2, true, 100); len(games) != 1 || games[0].ID != g2 {
		t.Errorf("from g2: %+v", games)
	}
	if games, _ := s.RatedGames(ctx, GamePoint{}, true, 1); len(games) != 1 || games[0].ID != g1 || len(games[0].Participants) != 2 {
		t.Errorf("limit: %+v", games)
	}
	if games, _ := s.RatedGames(ctx, p2, false, 100); len(games) != 0 {
		t.Errorf("after g2: %+v", games)
	}

	e1, e2 := t0.Add(time.Minute), t0.Add(time.Hour+time.Minute)
	rr := func(g, p int64, key string, before, after float64, ended time.Time, result string, gf, ga int) RatingRow {
		return RatingRow{GameID: g, PlayerID: p, Key: key, Before: before, After: after, EndedAt: ended, Result: result,
			GoalsFor: gf, GoalsAgainst: ga}
	}
	rows := []RatingRow{
		rr(g1, mia, "solo", 1000, 1016, e1, "win", 5, 3), rr(g1, tom, "solo", 1000, 984, e1, "loss", 3, 5),
		rr(g2, mia, "team", 1000, 990, e2, "loss", 5, 3), rr(g2, tom, "team", 1000, 1010, e2, "win", 3, 5),
	}
	if err := s.ChangeRatings(ctx, nil, rows, nil); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.PlayerRatings(ctx, mia); len(r) != 2 || r[0].Key != "solo" || r[0].Rating != 1016 || r[1].Rating != 990 || r[0].Games != 1 ||
		r[0].Wins != 1 || r[1].Losses != 1 || r[1].GoalsFor != 5 {
		t.Errorf("mia: %+v", r)
	}
	if res, _ := s.PlayerResults(ctx, mia); len(res) != 2 || res[0].GameID != g1 || res[1].Result != "loss" || !res[1].EndedAt.Equal(e2) {
		t.Errorf("mia's results: %+v", res)
	}
	if res, _ := s.LastResults(ctx, mia, "", 1); len(res) != 1 || res[0].GameID != g2 {
		t.Errorf("last results: %+v", res)
	}
	if res, _ := s.LastResults(ctx, mia, "solo", 5); len(res) != 1 || res[0].GameID != g1 {
		t.Errorf("last solo results: %+v", res)
	}
	if ch, _ := s.RatingChangesSince(ctx, e2); len(ch) != 2 || ch[0].PlayerID != mia || ch[0].Games != 1 || ch[0].Change != -10 {
		t.Errorf("changes since g2: %+v", ch)
	}
	if rel, _ := s.PlayerRelations(ctx, mia, 0, must(s.PlayerResults(ctx, mia))); len(rel) != 1 || rel[0].PlayerID != tom || rel[0].Teammate || rel[0].Wins != 1 || rel[0].Losses != 1 {
		t.Errorf("relations (the unknown coin doesn't count): %+v", rel)
	}
	if tot, _ := s.GamesSince(ctx, t0); tot.Games != 2 || tot.Goals != 16 {
		t.Errorf("totals: %+v", tot)
	}
	if id, ok, _ := s.BiggestWin(ctx, t0); !ok || id != g2 {
		t.Errorf("biggest win (the later of equal ones): %d %v", id, ok)
	}
	if lb, _ := s.Leaderboard(ctx, "team", 10); len(lb) != 2 || lb[0].PlayerID != tom {
		t.Errorf("leaderboard: %+v", lb)
	}
	before, _ := s.RatingsBefore(ctx, p2)
	if len(before) != 2 || before[0].Key != "solo" || before[0].Rating != 1016 || before[1].Rating != 984 {
		t.Errorf("before g2: %+v", before)
	}
	if in, _ := s.RatingRowsIn(ctx, p2, true, nil); len(in) != 2 || in[0].GameID != g2 {
		t.Errorf("rows from g2: %+v", in)
	}
	if in, _ := s.RatingRowsIn(ctx, GamePoint{}, true, &GamePoint{EndedAt: e1, ID: g1}); len(in) != 2 || in[1].GameID != g1 {
		t.Errorf("rows up to g1: %+v", in)
	}
	// Changing g2 keeps g1's rows.
	if err := s.ChangeRatings(ctx, []RatingRow{rr(g2, mia, "team", 1000, 1020, e2, "win", 5, 3)}, nil,
		[]RatingRow{rr(g2, tom, "team", 1000, 1010, e2, "win", 3, 5)}); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GameRatings(ctx, g1); len(r) != 2 {
		t.Errorf("g1 kept: %+v", r)
	}
	if r, _ := s.PlayerRatings(ctx, tom); len(r) != 1 || r[0].Key != "solo" {
		t.Errorf("tom's team rating is gone: %+v", r)
	}
	if r, _ := s.GameRatings(ctx, g2); len(r) != 1 || r[0].After != 1020 {
		t.Errorf("g2: %+v", r)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
