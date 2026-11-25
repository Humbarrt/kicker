// Package loadtest is `kicker loadtest` (spec 04 §4): it generates games through the real engine
// into a database, then measures the budgets of spec 04 against the real HTTP API, the table
// actor, the rating projector and the SSE hub.
package loadtest

import (
	"bufio"
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing/fstest"
	"time"

	"github.com/Humbarrt/kicker/server/internal/api"
	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/onboarding"
	"github.com/Humbarrt/kicker/server/internal/players"
	"github.com/Humbarrt/kicker/server/internal/stats"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/table"
	"github.com/Humbarrt/kicker/server/internal/webui"
)

// Options configure a run.
type Options struct {
	DataDir  string  // where the database goes; reused if it already has games (unless Fresh)
	Fresh    bool    // delete an existing database first
	Games    int     // games to generate (default 100 000)
	Players  int     // players (default 40)
	Requests int     // requests per measured endpoint (default 200)
	Clients  int     // SSE clients (default 50)
	Scale    float64 // multiplies every time budget (default 1 = the Raspberry Pi 4 budgets)
	Seed     uint64
	Out      io.Writer
	Log      io.Writer // the server's log (default: none)
}

// Budgets of spec 04 §4, for the Raspberry Pi 4.
const (
	BudgetStats  = 150 * time.Millisecond // stats pages and player profiles, p95
	BudgetCold   = 500 * time.Millisecond // a stats view computed afresh (the first request after a game)
	BudgetReplay = 3 * time.Second        // rating replay after editing the oldest game
	BudgetSSE    = 50 * time.Millisecond  // goal → SSE message at the clients, p95
	BudgetGoal   = 10 * time.Millisecond  // goal → saved and effects out (dispenser release), p95
)

// Result is one measured line of the report.
type Result struct {
	Name   string
	P50    time.Duration
	P95    time.Duration
	Max    time.Duration
	N      int
	Budget time.Duration // 0 = only reported
	OK     bool
}

// Report is the outcome of a run.
type Report struct {
	Games, Events int
	Results       []Result
}

// Failed lists the results over budget.
func (r Report) Failed() []Result {
	var out []Result
	for _, x := range r.Results {
		if !x.OK {
			out = append(out, x)
		}
	}
	return out
}

func (o *Options) defaults() {
	if o.Games <= 0 {
		o.Games = 100_000
	}
	if o.Players <= 0 {
		o.Players = 40
	}
	if o.Requests <= 0 {
		o.Requests = 200
	}
	if o.Clients <= 0 {
		o.Clients = 50
	}
	if o.Scale <= 0 {
		o.Scale = 1
	}
	if o.Seed == 0 {
		o.Seed = 42
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
}

func percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	i := int(math.Ceil(p*float64(len(s)))) - 1
	return s[max(0, min(i, len(s)-1))]
}

func result(name string, ds []time.Duration, budget time.Duration) Result {
	r := Result{Name: name, N: len(ds), P50: percentile(ds, 0.5), P95: percentile(ds, 0.95), Max: percentile(ds, 1), Budget: budget}
	r.OK = budget == 0 || r.P95 <= budget
	return r
}

// Run generates (if needed) and measures.
func Run(ctx context.Context, o Options) (Report, error) {
	gin.SetMode(gin.ReleaseMode) // no route list on the console
	o.defaults()
	var rep Report
	if o.Log == nil {
		o.Log = io.Discard
	}
	log := slog.New(slog.NewTextHandler(o.Log, nil))
	say := func(format string, args ...any) { fmt.Fprintf(o.Out, format+"\n", args...) }

	if err := os.MkdirAll(o.DataDir, 0o750); err != nil {
		return rep, err
	}
	dbPath := filepath.Join(o.DataDir, "kicker.db")
	if o.Fresh {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return rep, err
	}
	defer st.Close()
	as, err := auth.New(ctx, auth.Options{Store: st, Logger: log})
	if err != nil {
		return rep, err
	}
	if err := onboarding.CompleteFromConsole(ctx, st); err != nil {
		return rep, err
	}
	ids, err := st.AllGameIDs(ctx)
	if err != nil {
		return rep, err
	}
	if len(ids) == 0 {
		t0 := time.Now()
		n, events, err := generate(ctx, st, o, func(done int) {
			if done%10_000 == 0 {
				say("  … %d games", done)
			}
		})
		if err != nil {
			return rep, err
		}
		say("generated %d games with %d events through the engine in %s", n, events, time.Since(t0).Round(time.Millisecond))
		rep.Games, rep.Events = n, events
	} else {
		say("using the %d games in %s (--fresh to generate new ones)", len(ids), dbPath)
		rep.Games = len(ids)
	}

	// Ratings: the full replay (what a start after the migration or `ReplayAll` does).
	proj := stats.New(stats.Options{Store: st, Logger: log})
	t0 := time.Now()
	if err := proj.Replay(ctx, store.GamePoint{}); err != nil {
		return rep, err
	}
	rep.Results = append(rep.Results, result("full rating replay (all games)", []time.Duration{time.Since(t0)}, 0))

	// The server: the real table actor, router and SSE hub over HTTP.
	b := bus.New()
	cache := stats.NewCache(stats.Reader{St: st}, b, log)
	proj = stats.New(stats.Options{Store: st, Bus: b, Logger: log, Changed: cache.Changed})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go proj.Run(runCtx)
	go cache.Run(runCtx)
	t0 = time.Now()
	<-cache.Idle()
	rep.Results = append(rep.Results, result("start-up: every stats view computed", []time.Duration{time.Since(t0)}, 0))
	tbl := table.New(table.Options{Store: st, Bus: b, Config: engine.DefaultConfig(), Logger: log,
		OnGameFinished: func(id int64) { go proj.GameFinished(runCtx, id) }})
	tableDone := make(chan error, 1)
	go func() { tableDone <- tbl.Run(runCtx) }()
	select {
	case <-tbl.Ready():
	case err := <-tableDone:
		return rep, fmt.Errorf("table: %w", err)
	}
	ob, err := onboarding.New(ctx, onboarding.Options{Store: st, Auth: as})
	if err != nil {
		return rep, err
	}
	ps, err := players.New(players.Options{Store: st, Auth: as, MediaDir: filepath.Join(o.DataDir, "media"), Logger: log})
	if err != nil {
		return rep, err
	}
	done := make(chan struct{})
	defer close(done)
	router := api.NewRouter(api.Options{ServerID: "loadtest", DataDir: o.DataDir, Auth: as, Onboarding: ob, Players: ps, Table: tbl,
		Ratings: st, History: st, Stats: stats.Reader{St: st, Cache: cache}, Presets: st, Bus: b, Done: done,
		UI: webui.HandlerFS(fstest.MapFS{})})
	srv := httptest.NewServer(router)
	defer srv.Close()

	// 1. Stats pages and profiles: as served (cached until the next game), and computed afresh,
	// as the first request after a game gets them.
	res, err := measureHTTP(ctx, srv.URL, st, o)
	if err != nil {
		return rep, err
	}
	rep.Results = append(rep.Results, res...)
	res, err = measureCold(ctx, st, o)
	if err != nil {
		return rep, err
	}
	rep.Results = append(rep.Results, res...)

	// 2. Voiding the oldest game rates every later game again – until the ratings are what they
	// were; taking the void back does the same. Both leave the games as they were for the next run.
	first, err := oldestGame(ctx, st)
	if err != nil {
		return rep, err
	}
	edit := func(label string, ed engine.Edit) error {
		<-proj.Idle()
		t0 := time.Now()
		if _, err := tbl.EditGame(ctx, first, ed); err != nil {
			return fmt.Errorf("editing game %d: %w", first, err)
		}
		time.Sleep(5 * time.Millisecond) // the projector picks the edit up asynchronously
		<-proj.Idle()
		rep.Results = append(rep.Results, result(label+" → ratings replayed", []time.Duration{time.Since(t0) - 5*time.Millisecond},
			scale(BudgetReplay, o.Scale)))
		<-cache.Idle() // the background refresh: the requests meanwhile are served the previous version
		rep.Results = append(rep.Results, result(label+" → every stats view refreshed", []time.Duration{time.Since(t0) - 5*time.Millisecond}, 0))
		return nil
	}
	if err := edit("void the oldest game", engine.Edit{Kind: engine.EditVoidGame}); err != nil {
		return rep, err
	}
	_, events, err := st.Game(ctx, first)
	if err != nil {
		return rep, err
	}
	if err := edit("take the void back", engine.Edit{Kind: engine.EditRevert,
		TargetSeq: events[len(events)-1].Seq}); err != nil {
		return rep, err
	}

	// 3. Goals with SSE clients attached.
	res, err = measureLive(ctx, srv.URL, tbl, st, o)
	if err != nil {
		return rep, err
	}
	rep.Results = append(rep.Results, res...)

	cancel()
	<-tableDone
	print(o.Out, rep, o.Scale)
	return rep, nil
}

func scale(d time.Duration, s float64) time.Duration { return time.Duration(float64(d) * s) }

func oldestGame(ctx context.Context, st *store.Store) (int64, error) {
	ids, err := st.AllGameIDs(ctx)
	if err != nil {
		return 0, err
	}
	for _, id := range ids { // ascending; the first finished one
		g, _, err := st.Game(ctx, id)
		if err != nil {
			return 0, err
		}
		if g.State == "finished" && !g.Voided {
			return id, nil
		}
	}
	return 0, fmt.Errorf("no finished game")
}

// generate creates the players and plays o.Games games through the engine.
func generate(ctx context.Context, st *store.Store, o Options, progress func(int)) (games, events int, err error) {
	rng := rand.New(rand.NewPCG(o.Seed, o.Seed^0x9e3779b97f4a7c15))
	guest, err := st.GuestPlayer(ctx)
	if err != nil {
		return 0, 0, err
	}
	official, err := st.PresetByLabel(ctx, "Official")
	if err != nil {
		return 0, 0, err
	}
	rules := engine.Rules{WinScore: official.Rules.WinScore, WinBy: official.Rules.WinBy, TimeTie: engine.TimeTie(official.Rules.TimeTie)}
	if official.Rules.MaxScore != nil {
		rules.MaxScore = *official.Rules.MaxScore
	}
	var ids []int64
	skill := map[int64]float64{guest.ID: 1}
	for i := range o.Players {
		id, err := st.CreatePlayer(ctx, store.Player{Name: fmt.Sprintf("Player %02d", i+1), Color: players.Palette[i%len(players.Palette)],
			CreatedAt: time.Now()}, 0)
		if err != nil {
			return 0, 0, err
		}
		ids = append(ids, id)
		skill[id] = math.Exp(rng.NormFloat64() * 0.4) // strengths differ, so ratings spread
	}
	// Some players play much more than others.
	weight := func() int64 {
		for {
			i := int(math.Abs(rng.NormFloat64()) * float64(len(ids)) / 2.5)
			if i < len(ids) {
				return ids[i]
			}
		}
	}
	cfg := engine.DefaultConfig()
	cfg.GuestID = guest.ID
	// Spread the games over the last two years, about one every few minutes in office hours.
	start := time.Now().Add(-time.Duration(o.Games) * 7 * time.Minute)
	const batch = 1000
	var buf []store.ImportGame
	for g := range o.Games {
		n := 4
		if rng.Float64() < 0.3 {
			n = 2
		}
		var picked []int64
		for len(picked) < n {
			p := weight()
			if rng.Float64() < 0.03 {
				p = guest.ID // a guest now and then
			}
			if p == guest.ID || !slices.Contains(picked, p) {
				picked = append(picked, p)
			}
		}
		home, away := picked[:n/2], picked[n/2:]
		strength := func(side []int64) float64 {
			var s float64
			for _, p := range side {
				s += skill[p]
			}
			return s
		}
		sh, sa := strength(home), strength(away)
		gen := table.Generated{Home: home, Away: away, Rules: rules, PresetID: official.ID,
			Start: start.Add(time.Duration(g) * 7 * time.Minute),
			NextGoal: func() (engine.Side, time.Duration) {
				after := time.Duration(10+rng.IntN(50)) * time.Second
				if rng.Float64() < sh/(sh+sa) {
					return engine.Home, after
				}
				return engine.Away, after
			}}
		ig, err := gen.Play(cfg)
		if err != nil {
			return games, events, fmt.Errorf("game %d: %w", g, err)
		}
		buf = append(buf, ig)
		events += len(ig.Events)
		if len(buf) == batch || g == o.Games-1 {
			if err := st.ImportGames(ctx, buf); err != nil {
				return games, events, err
			}
			games += len(buf)
			buf = buf[:0]
			progress(games)
		}
	}
	if err := st.SetSetting(ctx, table.SettingProjections, table.ProjectionsVersion); err != nil {
		return games, events, err
	}
	return games, events, nil
}

// measureHTTP times the stats pages: overview, leaderboards, profiles, the game list (plain and
// filtered) and single games.
// measureCold computes the stats views without the cache: what the first request after a game
// waits for. Profiles of the players with the most games are the slowest.
func measureCold(ctx context.Context, st *store.Store, o Options) ([]Result, error) {
	r := stats.Reader{St: st}
	lb, err := r.Leaderboard(ctx, "team")
	if err != nil {
		return nil, err
	}
	busiest, most := int64(0), -1
	for _, x := range lb.Rows {
		if x.Record.Games > most {
			busiest, most = x.PlayerID, x.Record.Games
		}
	}
	n := max(5, o.Requests/10)
	cases := []struct {
		name string
		f    func() error
	}{
		{"overview, computed (in the background)", func() error { _, err := r.Overview(ctx); return err }},
		{"leaderboard (team), computed", func() error { _, err := r.Leaderboard(ctx, "team"); return err }},
		{fmt.Sprintf("profile (%d games), computed", most), func() error {
			_, err := r.Profile(ctx, busiest)
			return err
		}},
	}
	var out []Result
	for _, c := range cases {
		var ds []time.Duration
		for range n {
			t0 := time.Now()
			if err := c.f(); err != nil {
				return nil, err
			}
			ds = append(ds, time.Since(t0))
		}
		out = append(out, result(c.name, ds, scale(BudgetCold, o.Scale)))
	}
	return out, nil
}

func measureHTTP(ctx context.Context, base string, st *store.Store, o Options) ([]Result, error) {
	rng := rand.New(rand.NewPCG(o.Seed, 7))
	ps, err := st.Players(ctx, false)
	if err != nil {
		return nil, err
	}
	var pids []int64
	for _, p := range ps {
		if !p.IsGuest {
			pids = append(pids, p.ID)
		}
	}
	ids, err := st.AllGameIDs(ctx)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	get := func(path string) (time.Duration, error) {
		t0 := time.Now()
		resp, err := client.Get(base + path)
		if err != nil {
			return 0, err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("GET %s: %d", path, resp.StatusCode)
		}
		return time.Since(t0), nil
	}
	pick := func() int64 { return pids[rng.IntN(len(pids))] }
	cases := []struct {
		name string
		path func() string
	}{
		{"GET /api/overview", func() string { return "/api/overview" }},
		{"GET /api/leaderboard (team)", func() string { return "/api/leaderboard?key=team" }},
		{"GET /api/leaderboard (1 against 1)", func() string { return "/api/leaderboard?key=solo" }},
		{"GET /api/players/:id/stats", func() string { return fmt.Sprintf("/api/players/%d/stats", pick()) }},
		{"GET /api/games (first page)", func() string { return "/api/games?limit=30" }},
		{"GET /api/games (two players, against)", func() string {
			return fmt.Sprintf("/api/games?player=%d,%d&relation=against&limit=30", pids[0], pids[1])
		}},
		{"GET /api/games (player, won, team, close)", func() string {
			return fmt.Sprintf("/api/games?player=%d&result=win&kind=team&flags=close&limit=30", pick())
		}},
		{"GET /api/games/:id", func() string { return fmt.Sprintf("/api/games/%d", ids[rng.IntN(len(ids))]) }},
	}
	var out []Result
	for _, c := range cases {
		if _, err := get(c.path()); err != nil { // warm up once
			return nil, err
		}
		var ds []time.Duration
		for range o.Requests {
			d, err := get(c.path())
			if err != nil {
				return nil, err
			}
			ds = append(ds, d)
		}
		out = append(out, result(c.name, ds, scale(BudgetStats, o.Scale)))
	}
	return out, nil
}

// measureLive attaches SSE clients, plays goals on the table and times goal → saved and
// goal → SSE message at every client.
func measureLive(ctx context.Context, base string, tbl *table.Table, st *store.Store, o Options) ([]Result, error) {
	type client struct {
		mu   sync.Mutex
		seen map[uint64]time.Time // table view rev → arrival
	}
	clients := make([]*client, o.Clients)
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var ready sync.WaitGroup
	for i := range clients {
		c := &client{seen: map[uint64]time.Time{}}
		clients[i] = c
		req, _ := http.NewRequestWithContext(lctx, http.MethodGet, base+"/api/events", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		ready.Add(1)
		go func() {
			defer resp.Body.Close()
			sc := bufio.NewScanner(resp.Body)
			sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
			event, first := "", true
			for sc.Scan() {
				line := sc.Text()
				switch {
				case strings.HasPrefix(line, "event: "):
					event = line[7:]
				case strings.HasPrefix(line, "data: ") && event == "table":
					at := time.Now()
					if i := strings.Index(line, `"rev":`); i >= 0 {
						var rev uint64
						_, _ = fmt.Sscan(line[i+6:], &rev)
						c.mu.Lock()
						c.seen[rev] = at
						c.mu.Unlock()
					}
					if first {
						first = false
						ready.Done()
					}
				}
			}
		}()
	}
	ready.Wait()

	// A game nobody can win in 30 goals. The table may be asleep after a restart (goals don't wake
	// it); a game of an earlier run is paused and goes to the paused list.
	if err := tbl.Do(ctx, engine.Wake{}); err != nil {
		return nil, err
	}
	if tbl.View().Phase == string(engine.PhasePlaying) {
		if err := tbl.Do(ctx, engine.PauseGame{}); err != nil {
			return nil, err
		}
	}
	{
		ps, err := st.Players(ctx, false)
		if err != nil {
			return nil, err
		}
		var real []int64
		for _, p := range ps {
			if !p.IsGuest {
				real = append(real, p.ID)
			}
		}
		if err := tbl.Do(ctx, engine.RegisterPlayer{Side: engine.Home, PlayerID: real[0]}); err != nil {
			return nil, err
		}
		if err := tbl.Do(ctx, engine.RegisterPlayer{Side: engine.Away, PlayerID: real[1]}); err != nil {
			return nil, err
		}
		if err := tbl.Start(ctx, table.StartRequest{Rules: &engine.Rules{WinScore: 99, WinBy: 1, TimeTie: engine.TimeTieGoldenGoal}}); err != nil {
			return nil, err
		}
	}
	for deadline := time.Now().Add(15 * time.Second); tbl.View().Phase != string(engine.PhasePlaying); { // kickoff countdown
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the table is %s, not playing", tbl.View().Phase)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var goal, sse []time.Duration
	last := tbl.View().Rev
	for i := range 30 {
		role := []string{"home_goal", "away_goal"}[i%2]
		t0 := time.Now()
		if err := tbl.Do(ctx, engine.SensorGoal{Role: role}); err != nil {
			return nil, err
		}
		goal = append(goal, time.Since(t0))
		rev := tbl.View().Rev
		if rev <= last {
			return nil, fmt.Errorf("goal %d didn't change the table", i+1)
		}
		last = rev
		deadline := time.Now().Add(2 * time.Second)
		for _, c := range clients {
			for {
				c.mu.Lock()
				at, ok := c.seen[rev]
				c.mu.Unlock()
				if ok {
					sse = append(sse, at.Sub(t0))
					break
				}
				if time.Now().After(deadline) {
					return nil, fmt.Errorf("an SSE client missed goal %d", i+1)
				}
				time.Sleep(time.Millisecond)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return []Result{
		result(fmt.Sprintf("goal → saved, effects out (%d SSE clients)", o.Clients), goal, scale(BudgetGoal, o.Scale)),
		result(fmt.Sprintf("goal → SSE message at %d clients", o.Clients), sse, scale(BudgetSSE, o.Scale)),
	}, nil
}

func print(w io.Writer, rep Report, s float64) {
	fmt.Fprintf(w, "\n%-46s %9s %9s %9s %10s\n", "measurement", "p50", "p95", "max", "budget")
	for _, r := range rep.Results {
		budget, mark := "–", ""
		if r.Budget > 0 {
			budget = r.Budget.Round(time.Millisecond).String()
			mark = "ok"
			if !r.OK {
				mark = "OVER"
			}
		}
		fmt.Fprintf(w, "%-46s %9s %9s %9s %10s  %s\n", r.Name, round(r.P50), round(r.P95), round(r.Max), budget, mark)
	}
	if s != 1 {
		fmt.Fprintf(w, "(budgets scaled by %.2f)\n", s)
	}
}

func round(d time.Duration) string {
	if d < 10*time.Millisecond {
		return d.Round(10 * time.Microsecond).String()
	}
	return d.Round(100 * time.Microsecond).String()
}
