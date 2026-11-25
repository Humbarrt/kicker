package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRegistrationsRoundTrip(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mia, err := s.CreatePlayer(ctx, Player{Name: "Mia", Color: "#1e3a8a"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if regs, err := s.Registrations(ctx); err != nil || len(regs) != 0 {
		t.Fatalf("empty: %+v %v", regs, err)
	}
	want := []Registration{
		{Side: "home", Slot: 1, PlayerID: &mia, Sources: []RegSource{{Via: "manual"}, {Via: "coin", Ref: "04A1B2C3", Stale: true}}},
		{Side: "home", Slot: 2, CoinUID: Ptr("04FFFFFF"), Sources: []RegSource{{Via: "coin", Ref: "04FFFFFF"}}},
		{Side: "away", Slot: 2, PlayerID: &mia, Sources: []RegSource{{Via: "corner", Ref: "away_1"}}},
	}
	if err := s.SaveRegistrations(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Registrations(ctx)
	if err != nil || len(got) != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	if *got[0].PlayerID != mia || len(got[0].Sources) != 2 || got[0].Sources[0].Via != "manual" || !got[0].Sources[1].Stale ||
		got[1].PlayerID != nil || *got[1].CoinUID != "04FFFFFF" || got[2].Side != "away" || got[2].Sources[0].Ref != "away_1" {
		t.Errorf("round trip: %+v", got)
	}
	if err := s.DeletePlayer(ctx, mia); !errors.Is(err, ErrInUse) {
		t.Errorf("a registered player can't be deleted: %v", err)
	}
	if err := s.SaveRegistrations(ctx, want[1:2]); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Registrations(ctx); len(got) != 1 {
		t.Errorf("replaced: %+v", got)
	}
	var n int
	_ = s.r.QueryRow(`SELECT COUNT(*) FROM table_registration_sources`).Scan(&n)
	if n != 1 {
		t.Errorf("sources of removed registrations are gone: %d", n)
	}
	if err := s.SaveRegistrations(ctx, []Registration{{Side: "home", Slot: 1, Sources: []RegSource{{Via: "manual"}}}}); err == nil {
		t.Error("neither player nor coin")
	}
}

func TestPlayerEvents(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mia, _ := s.CreatePlayer(ctx, Player{Name: "Mia", Color: "#1e3a8a"}, 0)
	id := newGame(t, s)
	at := time.UnixMilli(1_763_000_012_345)
	_, err := s.AppendEvents(ctx, id, []Event{
		{Type: "player_joined", Side: Ptr("home"), Slot: Ptr(1), PlayerID: &mia, Via: Ptr("coin"), CoinUID: Ptr("04A1B2C3"), Source: "system", CreatedAt: at},
		{Type: "player_joined", Side: Ptr("away"), Slot: Ptr(1), CoinUID: Ptr("04FFFFFF"), Via: Ptr("coin"), Source: "system", CreatedAt: at},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, events, _ := s.Game(ctx, id)
	if *events[1].PlayerID != mia || events[2].PlayerID != nil || *events[2].CoinUID != "04FFFFFF" {
		t.Errorf("events: %+v", events)
	}
	if err := s.DeletePlayer(ctx, mia); !errors.Is(err, ErrInUse) {
		t.Errorf("a player with games can't be deleted: %v", err)
	}
	if _, err := s.AppendEvents(ctx, id, []Event{{Type: "player_joined", PlayerID: Ptr(int64(999)), Source: "system", CreatedAt: at}}, nil); err == nil {
		t.Error("unknown player")
	}
}

// Events written before 0006 keep everything except the (always empty) player column.
func TestMigrateGameEvents(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kicker.db")
	migrateUpTo = 5
	old, err := Open(ctx, path)
	migrateUpTo = 0
	if err != nil {
		t.Fatal(err)
	}
	// Rows as version 5 wrote them (today's code writes columns that came later).
	res, err := old.w.Exec(`INSERT INTO games (win_score, win_by, time_tie, state, started_at) VALUES (10, 2, 'golden_goal', 'active', 1763000000000)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	for _, e := range []string{
		`(?, 1, 'game_started', NULL, 'table', 1763000000000, 0)`,
		`(?, 2, 'goal', 'away', 'sensor', 1763000012345, 12345)`,
	} {
		if _, err := old.w.Exec(`INSERT INTO game_events (game_id, seq, type, side, source, created_at, play_time_ms) VALUES `+e, id); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, events, err := s.Game(ctx, id)
	if err != nil || len(events) != 2 || *events[1].Side != "away" || *events[1].PlayTimeMs != 12345 {
		t.Fatalf("migrated: %+v %v", events, err)
	}
	if _, err := s.w.Exec(`DELETE FROM games WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.r.QueryRow(`SELECT COUNT(*) FROM game_events`).Scan(&n)
	if n != 0 {
		t.Error("cascade still works after the rebuild")
	}
}
