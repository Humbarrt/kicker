package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/stats"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/table"
	"github.com/Humbarrt/kicker/server/internal/webui"
)

type tableFixture struct {
	token    string // bearer token of an admin
	st       *store.Store
	as       *auth.Service
	srv      *httptest.Server
	tbl      *table.Table
	bus      *bus.Bus
	shutdown func()
}

func newTableFixture(t *testing.T) *tableFixture {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	b := bus.New()
	tbl := table.New(table.Options{Store: st, Bus: b, Config: engine.DefaultConfig()})
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { _ = tbl.Run(ctx); close(stopped) }()
	<-tbl.Ready()
	done := make(chan struct{})
	as := newAuth(t, st)
	token := makeAccount(t, as, "anna", auth.GroupAdmin, auth.StatusApproved)
	r := NewRouter(Options{Auth: as, Table: tbl, Presets: st, Ratings: st, History: st, Stats: stats.Reader{St: st}, Bus: b, Done: done, Heartbeat: 50 * time.Millisecond,
		UI: webui.HandlerFS(fstest.MapFS{})})
	srv := httptest.NewServer(r)
	shutdown := sync.OnceFunc(func() { close(done) })
	t.Cleanup(func() {
		shutdown()
		srv.Close()
		cancel()
		<-stopped
		st.Close()
	})
	return &tableFixture{token: token, st: st, as: as, srv: srv, tbl: tbl, bus: b, shutdown: shutdown}
}

func (f *tableFixture) post(t *testing.T, path, body string) (int, map[string]any) {
	t.Helper()
	return f.do(t, http.MethodPost, path, body)
}

func (f *tableFixture) do(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestTableRoutes(t *testing.T) {
	f := newTableFixture(t)

	resp, err := http.Get(f.srv.URL + "/api/presets")
	if err != nil {
		t.Fatal(err)
	}
	var presets []PresetView
	_ = json.NewDecoder(resp.Body).Decode(&presets)
	resp.Body.Close()
	if len(presets) != 4 || presets[2].Label != "Official" || *presets[2].Rules.MaxScore != 8 {
		t.Fatalf("presets: %+v", presets)
	}

	code, body := f.post(t, "/api/table/undo", "")
	if code != http.StatusConflict || body["code"] != "no_game" {
		t.Errorf("undo while idle: %d %v", code, body)
	}
	code, body = f.post(t, "/api/table/start", `{"preset_id": 999}`)
	if code != http.StatusBadRequest || body["code"] != "invalid_input" {
		t.Errorf("unknown preset: %d %v", code, body)
	}
	code, body = f.post(t, "/api/table/start", `{"rules": {"win_score": 0, "win_by": 1, "time_tie": "draw"}}`)
	if code != http.StatusBadRequest || body["code"] != "invalid_rules" {
		t.Errorf("bad rules: %d %v", code, body)
	}
	code, body = f.post(t, "/api/table/start", `{not json`)
	if code != http.StatusBadRequest {
		t.Errorf("bad json: %d %v", code, body)
	}

	code, body = f.post(t, "/api/table/start", `{"preset_id": 3}`)
	if code != http.StatusOK || body["phase"] != "playing" {
		t.Fatalf("start: %d %v", code, body)
	}
	f.post(t, "/api/table/start", ``)              // "Start new game": the empty game is discarded
	code, body = f.post(t, "/api/table/start", ``) // "Start now", empty body: current (sticky) preset
	if game := body["game"].(map[string]any); code != http.StatusOK || game["preset_id"] != 3.0 {
		t.Errorf("sticky preset: %d %v", code, body)
	}
	code, body = f.post(t, "/api/table/score", `{"side": "away", "delta": 1}`)
	if game := body["game"].(map[string]any); code != http.StatusOK || game["away"] != 1.0 {
		t.Errorf("+1: %d %v", code, body)
	}
	code, body = f.post(t, "/api/table/score", `{"side": "left", "delta": 1}`)
	if code != http.StatusBadRequest {
		t.Errorf("bad side: %d %v", code, body)
	}
	code, body = f.post(t, "/api/table/score", `{"side": "home", "delta": -1}`)
	if code != http.StatusConflict || body["code"] != "nothing_to_undo" {
		t.Errorf("-1 without goal: %d %v", code, body)
	}

	resp, _ = http.Get(f.srv.URL + "/api/table")
	var v table.View
	_ = json.NewDecoder(resp.Body).Decode(&v)
	resp.Body.Close()
	if v.Phase != "playing" || v.Game.Away != 1 || v.Rev == 0 || v.ServerTime == 0 {
		t.Errorf("GET /api/table: %+v", v)
	}
}

func TestRegistrationRoutes(t *testing.T) {
	f := newTableFixture(t)
	ctx := context.Background()
	var ids []int64
	for _, name := range []string{"Mia", "Lena", "Tom"} {
		id, err := f.st.CreatePlayer(ctx, store.Player{Name: name, Color: "#1e3a8a"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	reg := func(side string, id int64) (int, map[string]any) {
		return f.post(t, "/api/table/registrations", fmt.Sprintf(`{"side": %q, "player_id": %d}`, side, id))
	}
	if code, body := reg("home", ids[0]); code != http.StatusOK {
		t.Fatalf("register: %d %v", code, body)
	}
	reg("home", ids[1])
	code, body := reg("home", ids[2])
	if code != http.StatusConflict || body["code"] != "side_full" {
		t.Errorf("third player: %d %v", code, body)
	}
	code, body = reg("away", ids[0]) // Mia moves
	regs := body["registrations"].(map[string]any)
	if code != http.StatusOK || len(regs["home"].([]any)) != 1 || len(regs["away"].([]any)) != 1 {
		t.Errorf("move: %d %v", code, regs)
	}
	if code, body := reg("away", 9999); code != http.StatusBadRequest || body["code"] != "invalid_player" {
		t.Errorf("unknown player: %d %v", code, body)
	}
	archived := true
	_ = f.st.UpdatePlayer(ctx, ids[2], store.PlayerChange{Archived: &archived})
	if code, body := reg("away", ids[2]); code != http.StatusBadRequest || body["code"] != "invalid_player" {
		t.Errorf("archived player: %d %v", code, body)
	}
	if code, body := reg("left", ids[1]); code != http.StatusBadRequest {
		t.Errorf("bad side: %d %v", code, body)
	}

	// The roster is in the game.
	code, body = f.post(t, "/api/table/start", `{"preset_id": 3}`)
	roster := body["game"].(map[string]any)["roster"].(map[string]any)
	if code != http.StatusOK || len(roster["home"].([]any)) != 1 || len(roster["away"].([]any)) != 1 {
		t.Errorf("roster: %d %v", code, roster)
	}
	code, body = f.do(t, http.MethodDelete, "/api/table/registrations/home/2", "")
	if code != http.StatusOK {
		t.Errorf("remove: %d %v", code, body)
	}
	code, body = f.do(t, http.MethodDelete, "/api/table/registrations/home/2", "")
	if code != http.StatusNotFound || body["code"] != "not_registered" {
		t.Errorf("remove again: %d %v", code, body)
	}
	if code, _ := f.do(t, http.MethodDelete, "/api/table/registrations/home/x", ""); code != http.StatusBadRequest {
		t.Errorf("bad slot: %d", code)
	}

	// Anybody can't touch the registrations by default.
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/table/registrations", strings.NewReader(`{"side":"home","player_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Errorf("anonymous: %d", resp.StatusCode)
	}
}

type sseEvent struct{ name, data string }

func readSSE(t *testing.T, sc *bufio.Scanner, ch chan<- sseEvent) {
	var ev sseEvent
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if ev.name != "" {
				ch <- ev
			}
			ev = sseEvent{}
		case strings.HasPrefix(line, ": ping"):
			ch <- sseEvent{name: "ping"}
		case strings.HasPrefix(line, "event: "):
			ev.name = line[7:]
		case strings.HasPrefix(line, "data: "):
			ev.data = line[6:]
		}
	}
	close(ch)
}

func next(t *testing.T, ch <-chan sseEvent, skipPings bool) sseEvent {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("stream ended")
			}
			if skipPings && ev.name == "ping" {
				continue
			}
			return ev
		case <-timeout:
			t.Fatal("no SSE event")
		}
	}
}

func TestEventStream(t *testing.T) {
	f := newTableFixture(t)
	req, _ := http.NewRequest("GET", f.srv.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+f.token) // an admin also gets "modules" and "console"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	ch := make(chan sseEvent, 32)
	go readSSE(t, bufio.NewScanner(resp.Body), ch)

	first := next(t, ch, true)
	var v table.View
	if err := json.Unmarshal([]byte(first.data), &v); first.name != "table" || err != nil || v.Phase != "idle" {
		t.Fatalf("snapshot: %+v %v", first, err)
	}

	// a goal reaches the stream
	start := time.Now()
	f.post(t, "/api/table/start", ``)
	f.tbl.Feed(engine.SensorGoal{Role: "home_goal", At: time.Now()})
	for {
		ev := next(t, ch, true)
		_ = json.Unmarshal([]byte(ev.data), &v)
		if v.Game != nil && v.Game.Away == 1 {
			break
		}
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("goal took %v to reach the stream", d)
	}

	f.bus.Publish(TopicModules, nil)
	if ev := next(t, ch, true); ev.name != "modules" || ev.data != "{}" {
		t.Errorf("modules event: %+v", ev)
	}
	f.bus.Publish(TopicConsole, ConsoleLine{UID: "G1", Text: "hello"})
	if ev := next(t, ch, true); ev.name != "console" || !strings.Contains(ev.data, `"hello"`) {
		t.Errorf("console event: %+v", ev)
	}
	if ev := next(t, ch, false); ev.name != "ping" {
		t.Errorf("heartbeat: %+v", ev)
	}

	// server shutdown ends the stream
	f.shutdown()
	for range ch {
	}
}

// Anonymous clients get the table, but not the admin topics.
func TestEventStreamAnonymous(t *testing.T) {
	f := newTableFixture(t)
	resp, err := http.Get(f.srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	ch := make(chan sseEvent, 32)
	go readSSE(t, bufio.NewScanner(resp.Body), ch)
	if ev := next(t, ch, true); ev.name != "table" {
		t.Fatalf("snapshot: %+v", ev)
	}
	f.bus.Publish(TopicConsole, ConsoleLine{UID: "G1", Text: "secret"})
	f.bus.Publish(TopicModules, nil)
	f.bus.Publish(table.TopicTable, table.View{Rev: 99})
	if ev := next(t, ch, true); ev.name != "table" || !strings.Contains(ev.data, `"rev":99`) {
		t.Errorf("anonymous got %+v", ev)
	}
}

func TestTableSettingsRoutes(t *testing.T) {
	f := newTableFixture(t)
	code, body := f.do(t, http.MethodGet, "/api/settings/table", "")
	if code != http.StatusOK || body["kickoff_requires"] != "both_sides" || body["current_preset_id"] == nil ||
		body["home"].(map[string]any)["color"] != table.DefaultHomeColor {
		t.Fatalf("defaults: %d %v", code, body)
	}
	code, body = f.do(t, http.MethodPut, "/api/settings/table",
		`{"home": {"name": " Red Team ", "color": "#CC0000"}, "kickoff_requires": "any_player", "kickoff_delay_s": 0, "current_preset_id": 2}`)
	if code != http.StatusOK || body["home"].(map[string]any)["name"] != "Red Team" || body["home"].(map[string]any)["color"] != "#cc0000" ||
		body["kickoff_requires"] != "any_player" || body["first_ball"] != "random" || body["current_preset_id"] != 2.0 {
		t.Fatalf("saved (partial body keeps the rest): %d %v", code, body)
	}
	v := f.tbl.View()
	if v.Sides["home"].Name != "Red Team" || *v.Next.PresetID != 2 || v.Next.Rules.WinScore != 6 {
		t.Errorf("view: %+v %+v", v.Sides, v.Next)
	}
	for _, bad := range []struct{ body, field string }{
		{`{"away": {"name": "x", "color": "red"}}`, "away.color"},
		{`{"home": {"name": "Same", "color": "#000000"}, "away": {"name": "same", "color": "#ffffff"}}`, "away.name"},
		{`{"kickoff_delay_s": 61}`, "kickoff_delay_s"},
		{`{"first_ball": "best"}`, "first_ball"},
		{`{"current_preset_id": 999}`, "current_preset_id"},
	} {
		code, body := f.do(t, http.MethodPut, "/api/settings/table", bad.body)
		if code != http.StatusBadRequest || body["code"] != "invalid_setting" || body["field"] != bad.field {
			t.Errorf("%s: %d %v", bad.body, code, body)
		}
	}

	// With any_player and no delay, one manual registration starts a game at once.
	ctx := context.Background()
	mia, _ := f.st.CreatePlayer(ctx, store.Player{Name: "Mia", Color: "#1e3a8a"}, 0)
	code, body = f.post(t, "/api/table/registrations", fmt.Sprintf(`{"side": "home", "player_id": %d}`, mia))
	if code != http.StatusOK || body["phase"] != "playing" || body["game"].(map[string]any)["preset_id"] != 2.0 {
		t.Errorf("auto kickoff: %d %v", code, body["phase"])
	}

	// Only admins.
	resp, err := http.Get(f.srv.URL + "/api/settings/table")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Errorf("anonymous: %d", resp.StatusCode)
	}
}

func TestPauseResumeRoutes(t *testing.T) {
	f := newTableFixture(t)
	code, body := f.post(t, "/api/table/pause", "")
	if code != http.StatusConflict || body["code"] != "no_game" {
		t.Errorf("pause without game: %d %v", code, body)
	}
	f.post(t, "/api/table/start", `{"preset_id": 3}`)
	code, body = f.post(t, "/api/table/pause", "")
	if code != http.StatusOK || body["game"].(map[string]any)["status"] != "paused" || body["phase"] != "playing" {
		t.Errorf("pause: %d %v", code, body)
	}
	code, body = f.post(t, "/api/table/resume", "")
	if code != http.StatusOK || body["game"].(map[string]any)["status"] != "active" {
		t.Errorf("resume: %d %v", code, body)
	}
	if code, body = f.post(t, "/api/table/resume", ""); code != http.StatusConflict || body["code"] != "not_paused" {
		t.Errorf("resume again: %d %v", code, body)
	}
	if code, _ = f.post(t, "/api/table/wake", ""); code != http.StatusOK {
		t.Errorf("wake while awake: %d", code)
	}

	// A goal, then "Start new game": the game goes to the paused list and can be resumed.
	f.post(t, "/api/table/score", `{"side": "home", "delta": 1}`)
	id := f.tbl.View().Game.ID
	_, body = f.post(t, "/api/table/start", "")
	paused := body["paused"].([]any)
	if body["phase"] != "idle" || len(paused) != 1 || paused[0].(map[string]any)["id"] != float64(id) {
		t.Fatalf("paused list: %v %v", body["phase"], paused)
	}
	code, body = f.post(t, fmt.Sprintf("/api/games/%d/resume", id), "")
	if code != http.StatusOK || body["game"].(map[string]any)["id"] != float64(id) || body["phase"] != "playing" {
		t.Errorf("resume from list: %d %v", code, body)
	}
	if code, body = f.post(t, "/api/games/999/resume", ""); code != http.StatusNotFound || body["code"] != "not_found" {
		t.Errorf("unknown game: %d %v", code, body)
	}
	if code, _ = f.post(t, "/api/games/x/resume", ""); code != http.StatusBadRequest {
		t.Errorf("bad id: %d", code)
	}
	if code, body = f.post(t, fmt.Sprintf("/api/games/%d/reinstate", id), ""); code != http.StatusBadRequest {
		t.Errorf("reinstate a running game: %d %v", code, body)
	}
	resp, err := http.Get(f.srv.URL + "/api/games/abandoned")
	if err != nil {
		t.Fatal(err)
	}
	var list []any
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || list == nil || len(list) != 0 {
		t.Errorf("abandoned list: %d %v", resp.StatusCode, list)
	}
}

func TestMayEdit(t *testing.T) {
	now := time.Now()
	mia := int64(7)
	g := &engine.Game{StartedAt: now.Add(-10 * 24 * time.Hour), PausedAt: now.Add(-3 * 24 * time.Hour)}
	g.Roster.Home[0] = engine.Seat{PlayerID: mia}
	member := auth.Perms{Values: map[string]int{auth.GamesEdit: auth.ScopeOwn, auth.GamesEditDays: 7}}
	acc := &auth.Account{PlayerID: &mia}
	eight := int64(8)
	other := &auth.Account{PlayerID: &eight}
	for _, c := range []struct {
		name string
		p    auth.Perms
		a    *auth.Account
		want bool
	}{
		{"own game, within 7 days", member, acc, true},
		{"someone else's game", member, other, false},
		{"no player", member, &auth.Account{}, false},
		{"all games", auth.Perms{Values: map[string]int{auth.GamesEdit: auth.ScopeAll, auth.GamesEditDays: 7}}, other, true},
		{"window too short", auth.Perms{Values: map[string]int{auth.GamesEdit: auth.ScopeAll, auth.GamesEditDays: 2}}, other, false},
		{"unlimited", auth.Perms{Values: map[string]int{auth.GamesEdit: auth.ScopeAll, auth.GamesEditDays: auth.Unlimited}}, other, true},
		{"no edit permission", auth.Perms{Values: map[string]int{auth.GamesEditDays: 7}}, acc, false},
		{"admin", auth.Perms{Admin: true}, nil, true},
	} {
		if got := mayEdit(c.p, c.a, g, now); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}
