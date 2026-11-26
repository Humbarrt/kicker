package table

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
)

type fixture struct {
	st     *store.Store
	bus    *bus.Bus
	tbl    *Table
	cancel context.CancelFunc
	done   chan error
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func start(t *testing.T, st *store.Store, cfg engine.Config) *fixture {
	t.Helper()
	f := &fixture{st: st, bus: bus.New(), done: make(chan error, 1)}
	f.tbl = New(Options{Store: st, Bus: f.bus, Config: cfg})
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	go func() { f.done <- f.tbl.Run(ctx) }()
	select {
	case <-f.tbl.Ready():
	case err := <-f.done:
		t.Fatal(err)
	}
	t.Cleanup(f.stop)
	return f
}

func (f *fixture) stop() {
	if f.cancel != nil {
		f.cancel()
		<-f.done
		f.cancel = nil
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestGameFlowAndRestart(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	f := start(t, st, engine.DefaultConfig())
	sub := f.bus.Subscribe(64, TopicTable)

	if v := f.tbl.View(); v.Phase != "idle" || v.Game != nil {
		t.Fatalf("fresh table: %+v", v)
	}
	official, _ := st.PresetByLabel(ctx, "Official")
	if err := f.tbl.Start(ctx, StartRequest{PresetID: official.ID, Author: 1}); err != nil {
		t.Fatal(err)
	}
	v := f.tbl.View()
	if v.Phase != "playing" || v.Game == nil || v.Game.ID == 0 || !v.Game.Current || v.Game.Target != 5 || *v.Game.Rules.MaxScore != 8 {
		t.Fatalf("after start: %+v %+v", v, v.Game)
	}
	if m := <-sub.C; m.Data.(View).Phase != "playing" {
		t.Errorf("published: %+v", m)
	}

	// module goals go through Feed
	f.tbl.Feed(engine.SensorGoal{Role: "home_goal", At: time.Now()})
	f.tbl.Feed(engine.SensorGoal{Role: "away_goal", At: time.Now()})
	f.tbl.Feed(engine.SensorGoal{Role: "away_goal", At: time.Now()})
	eventually(t, "three goals", func() bool { g := f.tbl.View().Game; return g.Home == 2 && g.Away == 1 })
	if err := f.tbl.Do(ctx, engine.UndoGoal{Author: 1}); err != nil {
		t.Fatal(err)
	}
	g := f.tbl.View().Game
	if g.Home != 1 || len(g.Goals) != 3 || !g.Goals[2].Voided || g.Goals[0].Side != "away" || g.Goals[0].Source != "sensor" {
		t.Errorf("after undo: %+v", g)
	}
	gameID := g.ID

	// restart: a new table on the same database continues the game
	f.stop()
	f2 := start(t, st, engine.DefaultConfig())
	g2 := f2.tbl.View().Game
	if f2.tbl.View().Phase != "playing" || g2.ID != gameID || g2.Home != 1 || g2.Away != 1 || len(g2.Goals) != 3 {
		t.Fatalf("after restart: %+v", g2)
	}
	for i := 0; i < 4; i++ {
		if err := f2.tbl.Do(ctx, engine.AdjustScore{Side: engine.Home, Delta: 1, Author: 1}); err != nil {
			t.Fatal(err)
		}
	}
	v = f2.tbl.View()
	if v.Phase != "finished" || *v.Game.Winner != "home" || v.Game.EndedAt == nil {
		t.Fatalf("finished: %+v %+v", v, v.Game)
	}

	// stored as finished, with every event in order
	row, events, err := st.Game(ctx, gameID)
	if err != nil || row.State != "finished" || len(events) != 10 || events[9].Type != "finished" || *events[4].TargetSeq != 4 {
		t.Fatalf("stored: %+v %d events %v", row, len(events), err)
	}

	// after another restart the finished game is shown as the last game
	f2.stop()
	f3 := start(t, st, engine.DefaultConfig())
	v = f3.tbl.View()
	if v.Phase != "idle" || v.Game == nil || v.Game.Current || v.Game.ID != gameID || v.Game.Home != 5 {
		t.Fatalf("last game: %+v %+v", v, v.Game)
	}

	// the preset stuck
	p, _ := f3.tbl.CurrentPreset(ctx)
	if p.ID != official.ID {
		t.Errorf("current preset %d, want %d", p.ID, official.ID)
	}
}

func TestCommandErrors(t *testing.T) {
	ctx := context.Background()
	f := start(t, openStore(t), engine.DefaultConfig())
	if err := f.tbl.Do(ctx, engine.UndoGoal{}); !errors.Is(err, engine.ErrNoGame) {
		t.Errorf("undo while idle: %v", err)
	}
	if err := f.tbl.Start(ctx, StartRequest{PresetID: 999}); !errors.Is(err, engine.ErrInvalidInput) {
		t.Errorf("unknown preset: %v", err)
	}
	if err := f.tbl.Start(ctx, StartRequest{Rules: &engine.Rules{WinScore: 0}}); !errors.Is(err, engine.ErrInvalidRules) {
		t.Errorf("bad rules: %v", err)
	}
	if v := f.tbl.View(); v.Phase != "idle" {
		t.Errorf("errors must not change the table: %+v", v)
	}
	// default: the first preset (Long) when nothing is set
	if err := f.tbl.Start(ctx, StartRequest{}); err != nil {
		t.Fatal(err)
	}
	if g := f.tbl.View().Game; g.Rules.WinScore != 10 || g.PresetID == nil {
		t.Errorf("default preset: %+v", g.Rules)
	}
	f.stop()
	if err := f.tbl.Do(ctx, engine.UndoGoal{}); !errors.Is(err, ErrNotRunning) {
		t.Errorf("stopped table: %v", err)
	}
}

// The timer drives the clock and the finished hold without any input.
func TestTimerDrivesTheClock(t *testing.T) {
	ctx := context.Background()
	cfg := engine.DefaultConfig()
	cfg.FinishedHold = 300 * time.Millisecond
	f := start(t, openStore(t), cfg)
	rules := engine.Rules{WinScore: 10, WinBy: 1, TimeLimit: time.Second, TimeTie: engine.TimeTieDraw}
	if err := f.tbl.Start(ctx, StartRequest{Rules: &rules}); err != nil {
		t.Fatal(err)
	}
	if g := f.tbl.View().Game; g.PresetID != nil || *g.Rules.TimeLimitS != 1 || !g.ClockRunning {
		t.Errorf("custom rules: %+v", g)
	}
	eventually(t, "time is up", func() bool { return f.tbl.View().Phase == "finished" })
	if g := f.tbl.View().Game; g.Winner != nil || g.EndReason != "time" || g.ClockRunning {
		t.Errorf("draw: %+v", g)
	}
	eventually(t, "hold ends", func() bool { return f.tbl.View().Phase == "idle" })
	if g := f.tbl.View().Game; g == nil || g.Current {
		t.Errorf("last game while idle: %+v", g)
	}
}

func TestStartNewGameAndPausedGames(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	f := start(t, st, engine.DefaultConfig())
	_ = f.tbl.Start(ctx, StartRequest{})
	first := f.tbl.View().Game.ID
	_ = f.tbl.Start(ctx, StartRequest{}) // "Start new game": discarded (no goals), back to idle
	if _, _, err := st.Game(ctx, first); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("empty game must be discarded: %v", err)
	}
	if v := f.tbl.View(); v.Phase != "idle" {
		t.Errorf("nobody registered: idle, not %s", v.Phase)
	}
	_ = f.tbl.Start(ctx, StartRequest{}) // "Start now"
	f.tbl.Feed(engine.SensorGoal{Role: "home_goal", At: time.Now()})
	eventually(t, "goal", func() bool { g := f.tbl.View().Game; return g != nil && g.Away == 1 })
	second := f.tbl.View().Game.ID
	_ = f.tbl.Start(ctx, StartRequest{})
	if row, _, _ := st.Game(ctx, second); row.State != "paused" {
		t.Errorf("game with goals must be paused, is %q", row.State)
	}
	v := f.tbl.View()
	if len(v.Paused) != 1 || v.Paused[0].ID != second || v.Paused[0].Away != 1 || v.Paused[0].Reason != "new_game" {
		t.Fatalf("paused list: %+v", v.Paused)
	}

	// A third game with a goal; resuming the second one sends it to the paused list.
	_ = f.tbl.Start(ctx, StartRequest{})
	third := f.tbl.View().Game.ID
	f.tbl.Feed(engine.SensorGoal{Role: "away_goal", At: time.Now()})
	eventually(t, "goal", func() bool { return f.tbl.View().Game.Home == 1 })
	if err := f.tbl.ResumeGame(ctx, second, 1); err != nil {
		t.Fatal(err)
	}
	v = f.tbl.View()
	if v.Game.ID != second || v.Game.Status != "active" || len(v.Paused) != 1 || v.Paused[0].ID != third || v.Paused[0].Reason != "switched" {
		t.Errorf("resumed %d, paused: %+v", v.Game.ID, v.Paused)
	}
	if err := f.tbl.ResumeGame(ctx, 9999, 1); !errors.Is(err, ErrNoSuchGame) {
		t.Errorf("unknown game: %v", err)
	}
	f.stop()

	// After a restart the running game continues; the other one is still in the paused list.
	f2 := start(t, st, engine.DefaultConfig())
	v = f2.tbl.View()
	if v.Game == nil || v.Game.ID != second || len(v.Paused) != 1 || v.Paused[0].ID != third {
		t.Errorf("after restart: %+v %+v", v.Game, v.Paused)
	}
}

// Paused games are abandoned after abandon_after_hours and can be reinstated.
func TestAbandonAndReinstate(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	now := time.Now()
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	f := &fixture{st: st, done: make(chan error, 1)}
	f.tbl = New(Options{Store: st, Config: engine.DefaultConfig(), Now: clock})
	runCtx, cancel := context.WithCancel(ctx)
	f.cancel = cancel
	go func() { f.done <- f.tbl.Run(runCtx) }()
	<-f.tbl.Ready()
	t.Cleanup(f.stop)

	_ = f.tbl.Start(ctx, StartRequest{})
	_ = f.tbl.Do(ctx, engine.AdjustScore{Side: engine.Home, Delta: 1})
	id := f.tbl.View().Game.ID
	_ = f.tbl.Start(ctx, StartRequest{}) // paused
	mu.Lock()
	now = now.Add(25 * time.Hour)
	mu.Unlock()
	f.stop()

	// The check runs at start, then every minute.
	f2 := &fixture{st: st, done: make(chan error, 1)}
	f2.tbl = New(Options{Store: st, Config: engine.DefaultConfig(), Now: clock})
	runCtx, cancel = context.WithCancel(ctx)
	f2.cancel = cancel
	go func() { f2.done <- f2.tbl.Run(runCtx) }()
	<-f2.tbl.Ready()
	t.Cleanup(f2.stop)
	if row, _, _ := st.Game(ctx, id); row.State != "abandoned" {
		t.Fatalf("after 25 h: %s", row.State)
	}
	if v := f2.tbl.View(); len(v.Paused) != 0 {
		t.Errorf("not in the paused list any more: %+v", v.Paused)
	}
	list, err := f2.tbl.AbandonedGames(ctx, 10)
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("abandoned list: %+v %v", list, err)
	}
	if err := f2.tbl.ReinstateGame(ctx, id, 1); err != nil {
		t.Fatal(err)
	}
	if v := f2.tbl.View(); len(v.Paused) != 1 || v.Paused[0].ID != id {
		t.Errorf("reinstated: %+v", v.Paused)
	}
}

// GET /api/table and the first SSE message run the clock up to now.
func TestViewClockIsCurrent(t *testing.T) {
	ctx := context.Background()
	f := start(t, openStore(t), engine.DefaultConfig())
	if err := f.tbl.Start(ctx, StartRequest{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	v := f.tbl.View()
	if v.Game.PlayTimeMs < 100 || v.ServerTime < v.Game.StartedAt+100 {
		t.Errorf("play time %d ms at %d (started %d)", v.Game.PlayTimeMs, v.ServerTime, v.Game.StartedAt)
	}
}

func TestRegistrationsPersisted(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	mia, _ := st.CreatePlayer(ctx, store.Player{Name: "Mia", Color: "#1e3a8a"}, 0)
	guest, _ := st.GuestPlayer(ctx)
	var ejected []engine.Side
	var mu sync.Mutex
	f := &fixture{st: st, bus: bus.New(), done: make(chan error, 1)}
	f.tbl = New(Options{Store: st, Config: engine.DefaultConfig(), OnEffect: func(e engine.Effect) {
		if ec, ok := e.(engine.EjectCoins); ok {
			mu.Lock()
			ejected = append(ejected, ec.Side)
			mu.Unlock()
		}
	}})
	runCtx, cancel := context.WithCancel(ctx)
	f.cancel = cancel
	go func() { f.done <- f.tbl.Run(runCtx) }()
	<-f.tbl.Ready()
	t.Cleanup(f.stop)

	f.tbl.Feed(engine.CoinInserted{Side: engine.Home, UID: "04A1B2C3", PlayerID: mia})
	if err := f.tbl.Register(ctx, engine.Away, guest.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := f.tbl.Register(ctx, engine.Away, guest.ID, 1); err != nil { // the guest plays twice
		t.Fatal(err)
	}
	if err := f.tbl.Register(ctx, engine.Away, 9999, 1); !errors.Is(err, ErrInvalidPlayer) {
		t.Errorf("unknown player: %v", err)
	}
	if v := f.tbl.View(); len(v.Registrations["home"]) != 1 || len(v.Registrations["away"]) != 2 || *v.Registrations["home"][0].PlayerID != mia {
		t.Fatalf("view: %+v", v.Registrations)
	}
	f.stop()

	// After a restart the registrations are back; the game gets them as its roster.
	f2 := start(t, st, engine.DefaultConfig())
	if v := f2.tbl.View(); len(v.Registrations["home"]) != 1 || len(v.Registrations["away"]) != 2 ||
		v.Registrations["home"][0].Sources[0].Via != "coin" {
		t.Fatalf("restored: %+v", v.Registrations)
	}
	short, _ := st.PresetByLabel(ctx, "Short")
	if err := f2.tbl.Start(ctx, StartRequest{PresetID: short.ID}); err != nil {
		t.Fatal(err)
	}
	v := f2.tbl.View()
	if len(v.Game.Roster["home"]) != 1 || len(v.Game.Roster["away"]) != 2 || *v.Game.Roster["away"][1].PlayerID != guest.ID {
		t.Errorf("roster: %+v", v.Game.Roster)
	}
	_, events, _ := st.Game(ctx, v.Game.ID)
	if len(events) != 4 || events[1].Type != "player_joined" || *events[1].PlayerID != mia || *events[1].Via != "coin" ||
		*events[1].CoinUID != "04A1B2C3" || *events[2].Slot != 1 || *events[3].Slot != 2 || *events[3].Via != "manual" {
		t.Errorf("roster events: %+v", events)
	}
	f2.stop()

	// The roster is rebuilt from the events after another restart, and the game end ejects coins
	// and ends the one-game registrations.
	f3 := &fixture{st: st, done: make(chan error, 1)}
	f3.tbl = New(Options{Store: st, Config: engine.DefaultConfig(), OnEffect: func(e engine.Effect) {
		if ec, ok := e.(engine.EjectCoins); ok {
			mu.Lock()
			ejected = append(ejected, ec.Side)
			mu.Unlock()
		}
	}})
	runCtx, cancel = context.WithCancel(ctx)
	f3.cancel = cancel
	go func() { f3.done <- f3.tbl.Run(runCtx) }()
	<-f3.tbl.Ready()
	t.Cleanup(f3.stop)
	if v := f3.tbl.View(); len(v.Game.Roster["away"]) != 2 {
		t.Errorf("roster after restart: %+v", v.Game.Roster)
	}
	for range 6 {
		if err := f3.tbl.Do(ctx, engine.AdjustScore{Side: engine.Home, Delta: 1}); err != nil {
			t.Fatal(err)
		}
	}
	v = f3.tbl.View()
	mu.Lock()
	defer mu.Unlock()
	if v.Phase != "finished" || len(v.Registrations["home"])+len(v.Registrations["away"]) != 0 || len(ejected) != 1 || ejected[0] != engine.Home {
		t.Errorf("game end: %s %+v ejected %v", v.Phase, v.Registrations, ejected)
	}
	if regs, _ := st.Registrations(ctx); len(regs) != 0 {
		t.Errorf("saved registrations after the game: %+v", regs)
	}
}

func TestSettingsPersisted(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	f := start(t, st, engine.DefaultConfig())
	if s := f.tbl.Settings(); s != DefaultSettings() {
		t.Errorf("defaults: %+v", s)
	}
	s := DefaultSettings()
	s.Home.Name, s.KickoffRequires, s.KickoffDelayS = "Reds", "any_player", 0
	if err := f.tbl.SetSettings(ctx, s, 0); err != nil {
		t.Fatal(err)
	}
	bad := s
	bad.OnWake = "never"
	if err := f.tbl.SetSettings(ctx, bad, 0); !errors.Is(err, ErrInvalidSettings) {
		t.Errorf("invalid: %v", err)
	}
	f.stop()

	f2 := start(t, st, engine.DefaultConfig())
	if got := f2.tbl.Settings(); got != s {
		t.Errorf("after restart: %+v", got)
	}
	if v := f2.tbl.View(); v.Sides["home"].Name != "Reds" || v.Next == nil || v.Next.Rules.WinScore != 10 {
		t.Errorf("view: %+v %+v", v.Sides, v.Next)
	}
	guest, _ := st.GuestPlayer(ctx)
	if err := f2.tbl.Register(ctx, engine.Home, guest.ID, 0); err != nil {
		t.Fatal(err)
	}
	if v := f2.tbl.View(); v.Phase != "playing" {
		t.Errorf("the restored settings apply: any_player, no delay → %s", v.Phase)
	}
}

// A game paused by hand stays on the table and is not in the paused games list.
func TestPausedByHandNotListed(t *testing.T) {
	ctx := context.Background()
	f := start(t, openStore(t), engine.DefaultConfig())
	_ = f.tbl.Start(ctx, StartRequest{})
	if err := f.tbl.Do(ctx, engine.PauseGame{}); err != nil {
		t.Fatal(err)
	}
	if v := f.tbl.View(); v.Game.Status != "paused" || len(v.Paused) != 0 {
		t.Errorf("paused by hand: %s, list %+v", v.Game.Status, v.Paused)
	}
}

// Games played before the projections existed get their result and roster from their events.
func TestBackfillProjections(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	mia, _ := st.CreatePlayer(ctx, store.Player{Name: "Mia", Color: "#1e3a8a"}, 0)
	f := start(t, st, engine.DefaultConfig())
	_ = f.tbl.Register(ctx, engine.Home, mia, 0)
	short, _ := st.PresetByLabel(ctx, "Short")
	_ = f.tbl.Start(ctx, StartRequest{PresetID: short.ID})
	id := f.tbl.View().Game.ID
	for range 6 {
		_ = f.tbl.Do(ctx, engine.AdjustScore{Side: engine.Away, Delta: 1})
	}
	f.stop()

	// The first run marks the projections as current; pretend they were never written.
	if did, err := BackfillProjections(ctx, st, nil); err != nil || !did {
		t.Fatalf("first run: %v %v", did, err)
	}
	if err := st.SetProjection(ctx, id, store.GameUpdate{State: "finished", EndedAt: store.Ptr(time.Now())}); err != nil {
		t.Fatal(err)
	}
	_ = st.SetSetting(ctx, SettingProjections, 0)
	if did, err := BackfillProjections(ctx, st, nil); err != nil || !did {
		t.Fatalf("backfill: %v %v", did, err)
	}
	row, _, _ := st.Game(ctx, id)
	ps, _ := st.Participants(ctx, id)
	if row.AwayGoals != 6 || row.HomeGoals != 0 || row.Winner == nil || *row.Winner != "away" || row.State != "finished" ||
		len(ps) != 1 || *ps[0].PlayerID != mia {
		t.Errorf("rebuilt: %+v %+v", row, ps)
	}
	if did, _ := BackfillProjections(ctx, st, nil); did {
		t.Error("runs only once per version")
	}
}

// Settings.NextRules: "last" keeps the rules the last game was started with (custom ones too,
// across a restart); "preset" always starts with the current preset.
func TestNextRules(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	f := start(t, st, engine.DefaultConfig())
	short, _ := st.PresetByLabel(ctx, "Short")
	official, _ := st.PresetByLabel(ctx, "Official")
	custom := engine.Rules{WinScore: 4, WinBy: 1, TimeTie: engine.TimeTieGoldenGoal}
	next := func(tbl *Table) *NextView { return tbl.View().Next }

	// "last" (default): custom rules become the next game's rules, and stay after a restart
	if err := f.tbl.Start(ctx, StartRequest{Rules: &custom}); err != nil {
		t.Fatal(err)
	}
	if n := next(f.tbl); n == nil || n.PresetID != nil || n.Rules.WinScore != 4 {
		t.Fatalf("after custom rules: %+v", n)
	}
	f.stop()
	f = start(t, st, engine.DefaultConfig())
	if n := next(f.tbl); n == nil || n.PresetID != nil || n.Rules.WinScore != 4 {
		t.Fatalf("custom rules after a restart: %+v", n)
	}
	if err := f.tbl.Start(ctx, StartRequest{}); err != nil {
		t.Fatal(err)
	}
	if v := f.tbl.View(); v.Game != nil && (v.Game.Rules.WinScore != 4 || v.Game.PresetID != nil) || v.Next.Rules.WinScore != 4 {
		t.Errorf("start without rules: %+v %+v", v.Game, v.Next)
	}
	if err := f.tbl.Start(ctx, StartRequest{PresetID: short.ID}); err != nil {
		t.Fatal(err)
	}
	if n := next(f.tbl); n.PresetID == nil || *n.PresetID != short.ID {
		t.Errorf("after a preset: %+v", n)
	}

	// "preset": the chosen preset, other rules don't change it
	s := f.tbl.Settings()
	s.NextRules = NextRulesPreset
	if err := f.tbl.SetSettings(ctx, s, official.ID); err != nil {
		t.Fatal(err)
	}
	if n := next(f.tbl); n.PresetID == nil || *n.PresetID != official.ID {
		t.Fatalf("fixed preset: %+v", n)
	}
	if err := f.tbl.Start(ctx, StartRequest{Rules: &custom}); err != nil {
		t.Fatal(err)
	}
	if err := f.tbl.Start(ctx, StartRequest{PresetID: short.ID}); err != nil {
		t.Fatal(err)
	}
	if n := next(f.tbl); n.PresetID == nil || *n.PresetID != official.ID {
		t.Errorf("fixed preset after other rules: %+v", n)
	}
	if p, _ := f.tbl.CurrentPreset(ctx); p.ID != official.ID {
		t.Errorf("current preset %d", p.ID)
	}
	f.stop()
	f = start(t, st, engine.DefaultConfig())
	if n := next(f.tbl); n.PresetID == nil || *n.PresetID != official.ID {
		t.Errorf("fixed preset after a restart: %+v", n)
	}

	// back to "last": the last game's rules again (the Short game above)
	s.NextRules = NextRulesLast
	if err := f.tbl.SetSettings(ctx, s, 0); err != nil {
		t.Fatal(err)
	}
	if n := next(f.tbl); n.PresetID == nil || *n.PresetID != short.ID {
		t.Errorf("back to last: %+v", n)
	}
	bad := s
	bad.NextRules = "random"
	if err := f.tbl.SetSettings(ctx, bad, 0); !errors.Is(err, ErrInvalidSettings) {
		t.Errorf("invalid next_rules: %v", err)
	}
}
