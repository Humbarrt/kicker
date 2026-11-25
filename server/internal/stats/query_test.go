package stats

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/store"
)

func TestStatsQueries(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var ids []int64
	for _, n := range []string{"Mia", "Tom", "Lena", "Ben"} {
		id, _ := st.CreatePlayer(ctx, store.Player{Name: n, Color: "#1e3a8a"}, 0)
		ids = append(ids, id)
	}
	mia, tom, lena, ben := ids[0], ids[1], ids[2], ids[3]
	t0 := time.UnixMilli(1_763_000_000_000)
	n := 0
	play := func(home, away []int64, hg, ag int) int64 {
		t.Helper()
		var ps []store.Participant
		for i, id := range home {
			ps = append(ps, store.Participant{Side: "home", Slot: i + 1, PlayerID: store.Ptr(id)})
		}
		for i, id := range away {
			ps = append(ps, store.Participant{Side: "away", Slot: i + 1, PlayerID: store.Ptr(id)})
		}
		n++
		start := t0.Add(time.Duration(n) * time.Hour)
		gid, err := st.CreateGame(ctx, store.Game{Rules: store.Rules{WinScore: 6, WinBy: 1, TimeTie: "draw"}, State: "active",
			StartedAt: start, Participants: ps}, []store.Event{{Type: "game_started", Source: "table", CreatedAt: start}})
		if err != nil {
			t.Fatal(err)
		}
		var winner *string
		switch {
		case hg > ag:
			winner = store.Ptr("home")
		case ag > hg:
			winner = store.Ptr("away")
		}
		end := start.Add(10 * time.Minute)
		if _, err := st.AppendEvents(ctx, gid, []store.Event{{Type: "finished", Side: winner, Source: "system", CreatedAt: end}},
			&store.GameUpdate{State: "finished", EndedAt: &end, HomeGoals: hg, AwayGoals: ag, Winner: winner, Participants: ps}); err != nil {
			t.Fatal(err)
		}
		return gid
	}
	one := func(id int64) []int64 { return []int64{id} }
	play(one(mia), one(tom), 6, 2)
	play(one(mia), one(tom), 6, 4)
	g3 := play(one(mia), one(tom), 6, 0) // the biggest win
	play([]int64{mia, lena}, []int64{tom, ben}, 5, 6)
	play([]int64{mia, lena}, []int64{tom, ben}, 6, 3)
	play([]int64{mia, lena}, []int64{tom, ben}, 6, 4)
	play([]int64{mia, ben}, []int64{tom, lena}, 5, 5) // a draw
	play([]int64{mia, lena}, []int64{tom, ben}, 6, 1)

	if old, _ := Outdated(ctx, st); !old {
		t.Error("a new database needs a full replay")
	}
	if err := New(Options{Store: st}).Replay(ctx, store.GamePoint{}); err != nil {
		t.Fatal(err)
	}
	if old, _ := Outdated(ctx, st); old {
		t.Error("after a full replay the stats are current")
	}
	r := Reader{St: st, Now: func() time.Time { return t0.Add(12 * time.Hour) }}

	p, err := r.Profile(ctx, mia)
	if err != nil {
		t.Fatal(err)
	}
	if p.Record != (RecordView{Games: 8, Wins: 6, Losses: 1, Draws: 1, GoalsFor: 46, GoalsAgainst: 25}) {
		t.Errorf("record: %+v", p.Record)
	}
	if p.Streak != 1 || p.BestStreak != 3 {
		t.Errorf("streaks: %d, best %d", p.Streak, p.BestStreak)
	}
	if !slices.Equal(p.Form, []string{"win", "draw", "win", "win", "loss", "win", "win", "win"}) {
		t.Errorf("form: %v", p.Form)
	}
	if len(p.Keys) != 2 || p.Keys[0].Key != "solo" || p.Keys[0].Record.Games != 3 || p.Keys[1].Record.Games != 5 ||
		len(p.Keys[0].History) != 3 || p.Keys[0].Start != 1000 || p.Keys[0].Peak != p.Keys[0].Rating {
		t.Errorf("keys: %+v", p.Keys)
	}
	if p.Keys[1].Rank != 1 || p.Keys[1].Ranked != 4 || p.Keys[0].Rank != 0 || p.Keys[0].Ranked != 0 {
		t.Errorf("ranks: team %d of %d, solo %d of %d", p.Keys[1].Rank, p.Keys[1].Ranked, p.Keys[0].Rank, p.Keys[0].Ranked)
	}
	if p.BestTeammate == nil || p.BestTeammate.PlayerID != lena || p.BestTeammate.Record.Wins != 3 {
		t.Errorf("best teammate: %+v", p.BestTeammate)
	}
	// Ben beat her 1 of 4 times as an opponent, Tom 1 of 8: Ben is the worst enemy, Tom the favourite victim.
	if p.WorstEnemy == nil || p.WorstEnemy.PlayerID != ben || p.FavoriteVictim == nil || p.FavoriteVictim.PlayerID != tom {
		t.Errorf("enemy %+v, victim %+v", p.WorstEnemy, p.FavoriteVictim)
	}
	if len(p.Opponents) != 3 || p.Opponents[0].PlayerID != tom || p.Opponents[0].Record.Games != 8 {
		t.Errorf("opponents: %+v", p.Opponents)
	}
	if pts := thin(make([]HistoryPoint, 1000), 300); len(pts) != 300 {
		t.Errorf("thin: %d", len(pts))
	}

	lb, err := r.Leaderboard(ctx, "team")
	if err != nil {
		t.Fatal(err)
	}
	if len(lb.Rows) != 4 || lb.Rows[0].Rank != 1 || lb.Rows[3].Rank != 4 || lb.Rows[0].Record.Games != 5 || len(lb.Rows[0].Form) != LeaderForm {
		t.Errorf("team leaderboard: %+v", lb.Rows)
	}
	if lb.Rows[0].Rating < lb.Rows[1].Rating || lb.Rows[0].Change == 0 {
		t.Errorf("order and change: %+v", lb.Rows)
	}
	solo, _ := r.Leaderboard(ctx, "solo")
	if len(solo.Rows) != 2 || solo.Rows[0].Rank != 0 { // 3 games each: not ranked yet
		t.Errorf("solo leaderboard: %+v", solo.Rows)
	}

	o, err := r.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.Games != 8 || o.Goals != 71 || len(o.Top) != 1 || o.Top[0].Key != "team" || len(o.Top[0].Rows) != 3 {
		t.Errorf("overview: %+v", o)
	}
	kinds := map[string]Highlight{}
	for _, h := range o.Highlights {
		kinds[h.Kind] = h
	}
	if _, ok := kinds["streak"]; ok {
		t.Error("nobody is on a streak of 3")
	}
	if h := kinds["biggest_win"]; h.GameID == nil || *h.GameID != g3 || h.Value != 6 || !slices.Equal(h.PlayerIDs, []int64{mia}) {
		t.Errorf("biggest win: %+v", h)
	}
	if h := kinds["most_games"]; h.Value != 8 {
		t.Errorf("most games: %+v", h)
	}
	if h := kinds["climber"]; len(h.PlayerIDs) != 1 || h.PlayerIDs[0] != mia {
		t.Errorf("climber: %+v", h)
	}

	// Outside the window (30 days) there is nothing to tell.
	later := Reader{St: st, Now: func() time.Time { return t0.Add(40 * 24 * time.Hour) }}
	if o, _ := later.Overview(ctx); o.Games != 0 || len(o.Highlights) != 0 || len(o.Top) != 1 {
		t.Errorf("a month later: %+v", o)
	}
}
