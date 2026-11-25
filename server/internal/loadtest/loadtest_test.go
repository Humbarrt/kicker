package loadtest

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/stats"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/table"
)

// A small run keeps the load test working; the budgets are only checked by `kicker loadtest`.
func TestSmallRun(t *testing.T) {
	if testing.Short() {
		t.Skip("generates games")
	}
	var out bytes.Buffer
	dir := t.TempDir()
	rep, err := Run(context.Background(), Options{DataDir: dir, Games: 300, Players: 12, Requests: 3, Clients: 3, Scale: 100, Out: &out})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if rep.Games != 300 || rep.Events < 300*6 || len(rep.Results) < 10 {
		t.Errorf("report: %+v", rep)
	}
	if !strings.Contains(out.String(), "goal → SSE message at 3 clients") {
		t.Errorf("output:\n%s", out.String())
	}
	// A second run reuses the games.
	out.Reset()
	if rep, err := Run(context.Background(), Options{DataDir: dir, Requests: 2, Clients: 2, Scale: 100, Out: &out}); err != nil || rep.Games < 300 {
		t.Errorf("rerun: %v %+v", err, rep)
	}
}

// A replay that stops early (once the ratings are what they were, give or take stats.Tolerance)
// leaves the rating history a full replay gives, give or take that.
func TestEditReplayConverges(t *testing.T) {
	if testing.Short() {
		t.Skip("generates games")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, _, err := generate(ctx, st, Options{Games: 600, Players: 8, Seed: 7}, func(int) {}); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	proj := stats.New(stats.Options{Store: st, Logger: log})
	if err := proj.Replay(ctx, store.GamePoint{}); err != nil {
		t.Fatal(err)
	}
	go proj.Run(ctx)
	tbl := table.New(table.Options{Store: st, Config: engine.DefaultConfig(), Logger: log,
		OnGameFinished: func(id int64) { go proj.GameFinished(ctx, id) }})
	go func() { _ = tbl.Run(ctx) }()
	<-tbl.Ready()

	history := func() []store.RatingRow {
		rows, err := st.RatingRowsIn(ctx, store.GamePoint{}, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	players, _ := st.Players(ctx, true)
	rng := rand.New(rand.NewPCG(1, 2))
	edits := []engine.Edit{
		{Kind: engine.EditVoidGame},
		{Kind: engine.EditAddGoal, Side: engine.Away},
		{Kind: engine.EditSetPlayer, Side: engine.Home, Slot: 1, PlayerID: players[len(players)-1].ID},
		{Kind: engine.EditSwapSides},
		{Kind: engine.EditRemovePlayer, Side: engine.Away, Slot: 1},
	}
	for i, e := range edits {
		before := history()
		e.Author = 1
		var game int64
		for try := 0; ; try++ { // e.g. the player already plays in that game
			game = int64(1 + rng.IntN(500))
			_, err := tbl.EditGame(ctx, game, e)
			if err == nil {
				break
			}
			if try == 20 {
				t.Fatalf("edit %d (%s) of game %d: %v", i, e.Kind, game, err)
			}
		}
		time.Sleep(10 * time.Millisecond)
		<-proj.Idle()
		got := history()
		if err := proj.Replay(ctx, store.GamePoint{}); err != nil {
			t.Fatal(err)
		}
		want := history()
		if len(got) != len(want) {
			t.Fatalf("edit %d (%s): %d rows, a full replay gives %d", i, e.Kind, len(got), len(want))
		}
		changed := 0
		for j := range want {
			close := math.Abs(got[j].After-want[j].After) <= 2*stats.Tolerance && math.Abs(got[j].Before-want[j].Before) <= 2*stats.Tolerance
			if !close || !got[j].SameGame(want[j]) || got[j].GameID != want[j].GameID || got[j].PlayerID != want[j].PlayerID {
				t.Fatalf("edit %d (%s) row %d: %+v, a full replay gives %+v", i, e.Kind, j, got[j], want[j])
			}
			if j < len(before) && !before[j].Same(want[j]) {
				changed++
			}
		}
		t.Logf("edit %d (%s) of game %d: %d of %d rows changed", i, e.Kind, game, changed, len(want))
	}
}
