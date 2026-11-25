package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/table"
	"github.com/Humbarrt/kicker/server/internal/tournaments"
	"github.com/Humbarrt/kicker/server/internal/webui"
)

type tourneyFixture struct {
	srv     *httptest.Server
	st      *store.Store
	tbl     *table.Table
	bus     *bus.Bus
	admin   string
	member  string
	players []int64
}

func newTourneyFixture(t *testing.T) *tourneyFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	b := bus.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var svc *tournaments.Service
	tbl := table.New(table.Options{Store: st, Bus: b, Config: engine.DefaultConfig(), Logger: log,
		OnGameFinished: func(id int64) { svc.GameFinished(id) },
		OnCheckIn:      func(c engine.CheckIn) { svc.CheckIn(c) },
		OnPhase:        func(from, to engine.Phase) { svc.Phase(from, to) }})
	svc = tournaments.New(tournaments.Options{Store: st, Table: tbl, Bus: b, Logger: log})
	go func() { _ = tbl.Run(ctx) }()
	go svc.Run(ctx)
	<-tbl.Ready()
	as := newAuth(t, st)
	f := &tourneyFixture{st: st, tbl: tbl, bus: b,
		admin:  makeAccount(t, as, "anna", auth.GroupAdmin, auth.StatusApproved),
		member: makeAccount(t, as, "ben", auth.GroupMember, auth.StatusApproved)}
	for _, n := range []string{"Mia", "Tom", "Ann", "Bob"} {
		id, err := st.CreatePlayer(ctx, store.Player{Name: n, Color: "#1e3a8a"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.players = append(f.players, id)
	}
	done := make(chan struct{})
	f.srv = httptest.NewServer(NewRouter(Options{Auth: as, Table: tbl, Presets: st, Tournaments: svc, Bus: b, Done: done,
		Heartbeat: 50 * time.Millisecond, UI: webui.HandlerFS(fstest.MapFS{})}))
	t.Cleanup(func() {
		close(done)
		f.srv.Close()
		cancel()
		st.Close()
	})
	return f
}

func (f *tourneyFixture) do(t *testing.T, token, method, path, body string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func (f *tourneyFixture) setup(name string) string {
	var ps []string
	for i, id := range f.players {
		ps = append(ps, fmt.Sprintf(`{"player_id":%d,"team":%d}`, id, i+1))
	}
	return `{"name":"` + name + `","settings":{"team_mode":"fixed","manual_seeds":true,
		"stages":[{"format":"knockout","third_place":true}]},"participants":[` + strings.Join(ps, ",") + `]}`
}

func TestTournamentRoutes(t *testing.T) {
	f := newTourneyFixture(t)
	sub := f.bus.Subscribe(16, tournaments.TopicTournament)
	defer sub.Close()

	// Format options for the setup page, sorted by duration.
	var opts []OptionView
	if code := f.do(t, f.member, "GET", "/api/tournaments/options?teams=4", "", &opts); code != 200 || len(opts) < 2 || opts[0].Name != "knockout" {
		t.Fatalf("options %d %+v", code, opts)
	}
	if code := f.do(t, "", "GET", "/api/tournaments/options?teams=4", "", nil); code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Errorf("options without login: %d", code)
	}

	// Create: fixed teams preview their bracket with an estimate.
	var d TournamentDetail
	if code := f.do(t, f.admin, "POST", "/api/tournaments", f.setup("Friday"), &d); code != http.StatusCreated {
		t.Fatalf("create %d", code)
	}
	id := d.ID
	if !d.Preview || len(d.Stages) != 1 || len(d.Stages[0].Pairings) != 4 || d.Estimate == nil || d.Estimate.Pairings != 4 || !d.Can.Edit {
		t.Fatalf("detail %+v", d)
	}
	var e APIError
	if code := f.do(t, f.admin, "POST", "/api/tournaments", `{"name":"x","settings":{"team_mode":"fixed","stages":[{"format":"knockout"}]},
		"participants":[{"player_id":1}]}`, &e); code != 400 || e.Code != "invalid_setup" {
		t.Errorf("a player without a team: %d %+v", code, e)
	}

	// A member sees it but may only run their own (tournaments.run: own).
	if code := f.do(t, f.member, "GET", fmt.Sprintf("/api/tournaments/%d", id), "", &d); code != 200 || d.Can.Edit || d.Can.Run {
		t.Errorf("member view %d %+v", code, d.Can)
	}
	if code := f.do(t, f.member, "POST", fmt.Sprintf("/api/tournaments/%d/start", id), "", nil); code != 403 {
		t.Errorf("member started another's tournament: %d", code)
	}
	var mine TournamentDetail
	if code := f.do(t, f.member, "POST", "/api/tournaments", f.setup("Ben's cup"), &mine); code != 201 || !mine.Can.Edit || !mine.Can.Run {
		t.Fatalf("member's own: %d %+v", code, mine.Can)
	}

	// Start: the table belongs to the tournament; the ready pairings and the suggestion.
	if code := f.do(t, f.admin, "POST", fmt.Sprintf("/api/tournaments/%d/start", id), "", &d); code != 200 || d.State != "running" {
		t.Fatalf("start %d %+v", code, d.State)
	}
	if len(d.Ready) != 2 || d.Suggested != "s1-W-1-1" || d.Preview {
		t.Errorf("running: ready %v suggested %q", d.Ready, d.Suggested)
	}
	if code := f.do(t, f.member, "POST", fmt.Sprintf("/api/tournaments/%d/start", mine.ID), "", &e); code != 409 || e.Code != "other_running" {
		t.Errorf("second running: %d %+v", code, e)
	}
	if code := f.do(t, f.admin, "POST", "/api/table/start", `{}`, &e); code != 409 || e.Code != "tournament" {
		t.Errorf("free play during the tournament: %d %+v", code, e)
	}
	select {
	case ev := <-sub.C:
		if ev.Data.(tournaments.Changed).ID != id {
			t.Errorf("event %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Error("no tournament event")
	}

	// Play: a ready pairing goes on the table.
	if code := f.do(t, f.admin, "POST", fmt.Sprintf("/api/tournaments/%d/play", id), `{"pairing":"s1-W-1-2"}`, &d); code != 200 || d.Staged != "s1-W-1-2" {
		t.Fatalf("play %d staged %q", code, d.Staged)
	}
	if v := f.tbl.View(); v.Phase != "pregame" {
		t.Errorf("table %s", v.Phase)
	}
	if code := f.do(t, f.admin, "POST", fmt.Sprintf("/api/tournaments/%d/play", id), `{"pairing":"s1-W-2-1"}`, &e); code != 409 || e.Code != "not_ready" {
		t.Errorf("playing the final now: %d %+v", code, e)
	}

	// Pause, continue, abandon, reinstate; deleting needs no games.
	for _, step := range []struct{ path, state string }{{"pause", "paused"}, {"continue", "running"}, {"abandon", "abandoned"}, {"reinstate", "paused"}} {
		if code := f.do(t, f.admin, "POST", fmt.Sprintf("/api/tournaments/%d/%s", id, step.path), "", &d); code != 200 || d.State != step.state {
			t.Errorf("%s: %d %s", step.path, code, d.State)
		}
	}
	if code := f.do(t, f.member, "POST", fmt.Sprintf("/api/tournaments/%d/abandon", mine.ID), "", nil); code != 403 {
		t.Errorf("member abandoned (tournaments.manage): %d", code)
	}
	var list []TournamentSummary
	if code := f.do(t, "", "GET", "/api/tournaments", "", &list); code != 200 || len(list) != 2 || list[0].ID != id {
		t.Errorf("list %d %+v", code, list) // paused before setup
	}
	if code := f.do(t, f.admin, "DELETE", fmt.Sprintf("/api/tournaments/%d", mine.ID), "", nil); code != 204 {
		t.Errorf("delete %d", code)
	}
	if code := f.do(t, f.admin, "GET", fmt.Sprintf("/api/tournaments/%d", mine.ID), "", &e); code != 404 {
		t.Errorf("deleted: %d", code)
	}
	var results []tournaments.PlayerResult
	if code := f.do(t, "", "GET", fmt.Sprintf("/api/players/%d/tournaments", f.players[0]), "", &results); code != 200 ||
		len(results) != 1 || results[0].ID != id || results[0].Place != 0 {
		t.Errorf("player's tournaments %d %+v", code, results) // the deleted one was never started
	}
	if len(d.Events) < 5 || d.Events[0].Kind != "created" {
		t.Errorf("history %+v", d.Events)
	}
}
