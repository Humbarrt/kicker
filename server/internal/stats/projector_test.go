package stats

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/table"
)

// Games played through the real table are rated by the projector; a replay from an older game
// gives the same ratings as rating everything again.
func TestProjectorWithTable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b := bus.New()
	sub := b.Subscribe(16, TopicStats)
	defer sub.Close()
	p := New(Options{Store: st, Bus: b})
	go p.Run(ctx)

	var tbl *table.Table
	tbl = table.New(table.Options{Store: st, Config: engine.DefaultConfig(),
		OnGameFinished: func(id int64) { go p.GameFinished(ctx, id) }})
	go func() { _ = tbl.Run(ctx) }()
	<-tbl.Ready()

	var ids []int64
	for _, n := range []string{"Mia", "Tom", "Lena"} {
		id, _ := st.CreatePlayer(ctx, store.Player{Name: n, Color: "#1e3a8a"}, 0)
		ids = append(ids, id)
	}
	mia, tom, lena := ids[0], ids[1], ids[2]
	guest, _ := st.GuestPlayer(ctx)
	short, _ := st.PresetByLabel(ctx, "Short") // first to 6

	play := func(home, away []int64, winner engine.Side) int64 {
		t.Helper()
		for _, id := range home {
			if err := tbl.Register(ctx, engine.Home, id, 1); err != nil {
				t.Fatal(err)
			}
		}
		for _, id := range away {
			if err := tbl.Register(ctx, engine.Away, id, 1); err != nil {
				t.Fatal(err)
			}
		}
		if err := tbl.Start(ctx, table.StartRequest{PresetID: short.ID}); err != nil {
			t.Fatal(err)
		}
		gid := tbl.View().Game.ID
		for range 6 {
			if err := tbl.Do(ctx, engine.AdjustScore{Side: winner, Delta: 1}); err != nil {
				t.Fatal(err)
			}
		}
		if err := tbl.Do(ctx, engine.StartGame{Rules: engine.Rules{WinScore: 1, WinBy: 1, TimeTie: engine.TimeTieGoldenGoal}}); err != nil {
			t.Fatal(err) // "Start" during the hold kicks off a game …
		}
		if err := tbl.Do(ctx, engine.StartGame{Rules: engine.Rules{WinScore: 1, WinBy: 1, TimeTie: engine.TimeTieGoldenGoal}}); err != nil {
			t.Fatal(err) // … "Start new game" discards it (no goals), the table is idle again
		}
		<-p.Idle()
		return gid
	}
	wait := func() {
		t.Helper()
		select {
		case <-sub.C:
		case <-time.After(5 * time.Second):
			t.Fatal("no stats_changed")
		}
		<-p.Idle()
	}

	g1 := play([]int64{mia}, []int64{tom}, engine.Home)
	wait()
	r, _ := st.GameRatings(ctx, g1)
	if len(r) != 2 || r[0].Key != "solo" || math.Abs(r[0].After-r[0].Before) != 16 {
		t.Fatalf("game 1: %+v", r)
	}
	g2 := play([]int64{mia, lena}, []int64{tom, guest.ID}, engine.Away)
	wait()
	r, _ = st.GameRatings(ctx, g2)
	if len(r) != 3 || r[0].Key != "team" { // the guest has no rating
		t.Fatalf("game 2: %+v", r)
	}
	g3 := play([]int64{lena}, []int64{tom}, engine.Away)
	wait()

	snapshot := func() map[int64][]store.PlayerRating {
		out := map[int64][]store.PlayerRating{}
		for _, id := range ids {
			out[id], _ = st.PlayerRatings(ctx, id)
		}
		return out
	}
	want := snapshot()
	if want[tom][0].Games != 2 || want[lena][0].Games != 1 || want[lena][1].Games != 1 {
		t.Errorf("ratings: %+v", want)
	}

	// Replay from game 2 (as after an edit), and everything: same result.
	p.GameFinished(ctx, g2)
	wait()
	p.ReplayAll()
	wait()
	got := snapshot()
	for _, id := range ids {
		if len(got[id]) != len(want[id]) {
			t.Fatalf("player %d: %+v vs %+v", id, got[id], want[id])
		}
		for i := range want[id] {
			if math.Abs(got[id][i].Rating-want[id][i].Rating) > 1e-9 || got[id][i].Games != want[id][i].Games {
				t.Errorf("player %d: %+v vs %+v", id, got[id][i], want[id][i])
			}
		}
	}
	// Editing a game (task 7) rates it and every later game again; a voided game counts for nothing.
	if _, err := tbl.EditGame(ctx, g1, engine.Edit{Kind: engine.EditVoidGame, Author: 1}); err != nil {
		t.Fatal(err)
	}
	wait()
	if r, _ := st.GameRatings(ctx, g1); len(r) != 0 {
		t.Errorf("voided game still rated: %+v", r)
	}
	if r, _ := st.PlayerRatings(ctx, mia); len(r) != 1 || r[0].Key != "team" {
		t.Errorf("mia has no solo games left: %+v", r)
	}
	if r, _ := st.PlayerRatings(ctx, tom); r[0].Key != "solo" || r[0].Games != 1 {
		t.Errorf("tom's solo games: %+v", r)
	}
	// Swapping sides of game 3 moves players and score together: Tom still won.
	before, _ := st.PlayerRatings(ctx, lena)
	if _, err := tbl.EditGame(ctx, g3, engine.Edit{Kind: engine.EditSwapSides, Author: 1}); err != nil {
		t.Fatal(err)
	}
	wait()
	after, _ := st.PlayerRatings(ctx, lena)
	if after[0] != before[0] {
		t.Errorf("lena: %+v → %+v", before, after)
	}
	// Six goals for Lena (now away) make it 6:6 – a draw is half a win, so she gains.
	for range 6 {
		if _, err := tbl.EditGame(ctx, g3, engine.Edit{Kind: engine.EditAddGoal, Side: engine.Away, Author: 1}); err != nil {
			t.Fatal(err)
		}
		wait() // one at a time: edits in a row may be rated together, with fewer stats events
	}
	after, _ = st.PlayerRatings(ctx, lena)
	if g, _, _ := st.Game(ctx, g3); g.HomeGoals != 6 || g.AwayGoals != 6 || g.Winner != nil || !(after[0].Rating > before[0].Rating) {
		t.Errorf("draw: %+v, lena %+v → %+v", g, before, after)
	}
}
