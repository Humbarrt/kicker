package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/modules"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateAndPragmas(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sub", "kicker.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.SchemaVersion(ctx)
	if ms, _ := loadMigrations(); err != nil || v != len(ms) || v < 2 {
		t.Fatalf("schema version %d %v", v, err)
	}
	var mode string
	var fk int
	_ = s.w.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode)
	_ = s.w.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk)
	if mode != "wal" || fk != 1 {
		t.Errorf("journal_mode=%s foreign_keys=%d, want wal / 1", mode, fk)
	}
	if _, err := s.r.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('x', '1')`); err == nil {
		t.Error("the read pool must be read-only")
	}
	s.Close()

	// reopening applies nothing twice
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	var n int
	_ = s2.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM rule_presets`).Scan(&n)
	if n != 4 {
		t.Errorf("built-in presets inserted %d times? got %d rows", n/4, n)
	}
}

func TestPresets(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	ps, err := s.Presets(ctx)
	if err != nil || len(ps) != 4 {
		t.Fatalf("presets: %v %v", ps, err)
	}
	official, err := s.PresetByLabel(ctx, "Official")
	if err != nil || official.Rules.WinScore != 5 || official.Rules.WinBy != 2 || *official.Rules.MaxScore != 8 || official.Rules.TimeLimitS != nil || !official.Builtin {
		t.Errorf("Official: %+v %v", official, err)
	}
	timed, _ := s.PresetByLabel(ctx, "Timed")
	if timed.Rules.MaxScore != nil || *timed.Rules.TimeLimitS != 300 || timed.Rules.TimeTie != "golden_goal" {
		t.Errorf("Timed: %+v", timed.Rules)
	}
	if _, err := s.Preset(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing preset: %v", err)
	}
}

func TestSettings(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	var id int64
	if ok, err := s.GetSetting(ctx, SettingCurrentPreset, &id); ok || err != nil {
		t.Fatalf("unset setting: %v %v", ok, err)
	}
	if err := s.SetSetting(ctx, SettingCurrentPreset, int64(3)); err != nil {
		t.Fatal(err)
	}
	_ = s.SetSetting(ctx, SettingCurrentPreset, int64(2))
	if ok, err := s.GetSetting(ctx, SettingCurrentPreset, &id); !ok || err != nil || id != 2 {
		t.Errorf("got %d %v %v", id, ok, err)
	}
}

func TestAdoptionsRoundTrip(t *testing.T) {
	s := open(t)
	a := s.Adoptions()
	at := time.UnixMilli(1_763_000_000_000)
	ad := modules.Adoption{
		UID: "E6614104033F0F35", Name: "Goal sensors", AdoptedAt: at,
		Roles:  map[string]string{"a": "home_goal", "b": "away_goal"},
		Caps:   map[string]string{"a": "goal_sensor", "b": "goal_sensor", "x": "acme.test/horn"},
		Config: map[string]json.RawMessage{"a": json.RawMessage(`{"lockout_ms":1500}`), "": json.RawMessage(`{"debug":true}`)},
	}
	if err := a.SaveAdoption(ad); err != nil {
		t.Fatal(err)
	}
	got, err := a.Adoptions()
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	g := got[0]
	if g.Name != "Goal sensors" || !g.AdoptedAt.Equal(at) || g.Roles["b"] != "away_goal" || g.Caps["x"] != "acme.test/horn" ||
		len(g.Roles) != 2 || string(g.Config["a"]) != `{"lockout_ms":1500}` || string(g.Config[""]) != `{"debug":true}` {
		t.Errorf("round trip: %+v", g)
	}

	// saving again replaces roles; adopted_at stays
	ad.Roles = map[string]string{"a": "home_goal"}
	ad.Name = "Renamed"
	_ = a.SaveAdoption(ad)
	got, _ = a.Adoptions()
	if got[0].Name != "Renamed" || len(got[0].Roles) != 1 || !got[0].AdoptedAt.Equal(at) {
		t.Errorf("update: %+v", got[0])
	}

	if err := a.DeleteAdoption(ad.UID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.r.QueryRow(`SELECT COUNT(*) FROM module_capabilities`).Scan(&n)
	if got, _ := a.Adoptions(); len(got) != 0 || n != 0 {
		t.Errorf("delete must cascade: %d adoptions, %d capability rows", len(got), n)
	}
}

func TestExclusiveRolesInDatabase(t *testing.T) {
	s := open(t)
	a := s.Adoptions()
	g1 := modules.Adoption{UID: "G1", Name: "g1", Roles: map[string]string{"a": "home_goal"}, Caps: map[string]string{"a": "goal_sensor"}}
	g2 := modules.Adoption{UID: "G2", Name: "g2", Roles: map[string]string{"a": "home_goal"}, Caps: map[string]string{"a": "goal_sensor"}}
	if err := a.SaveAdoption(g1); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveAdoption(g2); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("second home_goal must violate the unique index, got %v", err)
	}
	if got, _ := a.Adoptions(); len(got) != 1 {
		t.Errorf("failed save must roll back completely, got %d adoptions", len(got))
	}
	d1 := modules.Adoption{UID: "D1", Name: "d1", Roles: map[string]string{"s": "home"}, Caps: map[string]string{"s": "display"}}
	d2 := modules.Adoption{UID: "D2", Name: "d2", Roles: map[string]string{"s": "home"}, Caps: map[string]string{"s": "display"}}
	if err := a.SaveAdoption(d1); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveAdoption(d2); err != nil {
		t.Errorf("display mirrors must be allowed: %v", err)
	}
}

func newGame(t *testing.T, s *Store) int64 {
	t.Helper()
	ctx := context.Background()
	p, _ := s.PresetByLabel(ctx, "Official")
	start := time.UnixMilli(1_763_000_000_000)
	id, err := s.CreateGame(ctx, Game{PresetID: &p.ID, Rules: p.Rules, State: "active", StartedAt: start}, []Event{
		{Type: "game_started", Source: "table", CreatedAt: start},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestGameEventLog(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id := newGame(t, s)
	at := time.UnixMilli(1_763_000_012_345)

	got, err := s.AppendEvents(ctx, id, []Event{
		{Type: "goal", Side: Ptr("away"), Source: "sensor", CreatedAt: at, PlayTimeMs: Ptr(int64(12345))},
		{Type: "goal", Side: Ptr("home"), Source: "sensor", CreatedAt: at.Add(time.Second), PlayTimeMs: Ptr(int64(13345))},
	}, nil)
	if err != nil || len(got) != 2 || got[0].Seq != 2 || got[1].Seq != 3 || got[0].ID == 0 {
		t.Fatalf("append: %+v %v", got, err)
	}
	_, err = s.AppendEvents(ctx, id, []Event{
		{Type: "goal_voided", TargetSeq: Ptr(3), Source: "table", AuthorAccountID: Ptr(int64(1)), Reason: Ptr("ball bounced out"), CreatedAt: at.Add(2 * time.Second)},
		{Type: "finished", Side: Ptr("away"), Reason: Ptr("score"), Source: "system", CreatedAt: at.Add(3 * time.Second)},
	}, &GameUpdate{State: "finished", EndedAt: Ptr(at.Add(3 * time.Second)), NextBallSide: Ptr("home")})
	if err != nil {
		t.Fatal(err)
	}

	g, events, err := s.Game(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if g.State != "finished" || g.EndedAt == nil || *g.NextBallSide != "home" || g.Rules.WinScore != 5 || *g.Rules.MaxScore != 8 {
		t.Errorf("game: %+v", g)
	}
	if len(events) != 5 || events[1].Type != "goal" || *events[1].Side != "away" || *events[1].PlayTimeMs != 12345 ||
		!events[1].CreatedAt.Equal(at) || *events[3].TargetSeq != 3 || *events[3].Reason != "ball bounced out" || events[4].Slot != nil {
		t.Errorf("events: %+v", events)
	}
	if open, _ := s.OpenGames(ctx); len(open) != 0 {
		t.Errorf("finished game must not be open: %+v", open)
	}
}

func TestCheckConstraints(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id := newGame(t, s)
	now := time.Now()
	bad := map[string]Event{
		"unknown type":    {Type: "own_goal", Source: "sensor", CreatedAt: now},
		"bad side":        {Type: "goal", Side: Ptr("middle"), Source: "sensor", CreatedAt: now},
		"bad slot":        {Type: "player_joined", Slot: Ptr(3), Source: "system", CreatedAt: now},
		"bad source":      {Type: "goal", Source: "magic", CreatedAt: now},
		"negative time":   {Type: "goal", Source: "sensor", CreatedAt: now, PlayTimeMs: Ptr(int64(-1))},
		"bad rule":        {Type: "rules_changed", Rule: Ptr("speed"), Source: "table", CreatedAt: now},
		"reason too long": {Type: "goal_voided", Reason: Ptr(strings.Repeat("x", 201)), Source: "table", CreatedAt: now},
	}
	for name, e := range bad {
		if _, err := s.AppendEvents(ctx, id, []Event{e}, nil); err == nil {
			t.Errorf("%s: must be rejected by a CHECK constraint", name)
		}
	}
	if _, err := s.AppendEvents(ctx, 9999, []Event{{Type: "goal", Source: "sensor", CreatedAt: now}}, nil); err == nil {
		t.Error("event for a missing game must violate the foreign key")
	}
	if _, err := s.CreateGame(ctx, Game{Rules: Rules{WinScore: 0, WinBy: 1, TimeTie: "draw"}, State: "active", StartedAt: now}, nil); err == nil {
		t.Error("win_score 0 must be rejected")
	}
	if _, _, err := s.Game(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, events, _ := s.Game(ctx, id); len(events) != 1 {
		t.Errorf("rejected events must leave no trace, got %d events", len(events))
	}
}

func TestOpenGamesAndDiscard(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	a := newGame(t, s)
	b := newGame(t, s)
	_, _ = s.AppendEvents(ctx, a, []Event{{Type: "paused", Reason: Ptr("timeout"), Source: "system", CreatedAt: time.Now()}},
		&GameUpdate{State: "paused"})
	open, err := s.OpenGames(ctx)
	if err != nil || len(open) != 2 {
		t.Fatalf("open games: %+v %v", open, err)
	}
	if err := s.DeleteGame(ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Game(ctx, b); !errors.Is(err, ErrNotFound) {
		t.Errorf("discarded game: %v", err)
	}
	var n int
	_ = s.r.QueryRow(`SELECT COUNT(*) FROM game_events WHERE game_id = ?`, b).Scan(&n)
	if n != 0 {
		t.Errorf("events of a discarded game must be deleted, %d left", n)
	}
	if err := s.DeleteGame(ctx, b); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
}

// The single writer serializes concurrent appends: sequence numbers stay gap-free and unique.
func TestConcurrentWritersAndReaders(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id := newGame(t, s)
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, err := s.AppendEvents(ctx, id, []Event{{Type: "goal", Side: Ptr("home"), Source: "sensor", CreatedAt: time.Now()}}, nil); err != nil {
					errs <- err
				}
			}
		}()
		go func() {
			defer wg.Done()
			if _, _, err := s.Game(ctx, id); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	_, events, _ := s.Game(ctx, id)
	if len(events) != 101 {
		t.Fatalf("got %d events, want 101", len(events))
	}
	for i, e := range events {
		if e.Seq != i+1 {
			t.Fatalf("seq gap at %d: %d", i, e.Seq)
		}
	}
}

// The host works on top of the database store, end to end.
func TestModuleHostOnDatabase(t *testing.T) {
	s := open(t)
	if _, err := modules.New(modules.Options{Store: s.Adoptions()}); err != nil {
		t.Fatal(err)
	}
}
