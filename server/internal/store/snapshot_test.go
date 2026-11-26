package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// An existing database is handed to BeforeMigrate before it is migrated; a new one isn't.
func TestBeforeMigrate(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kicker.db")
	called := 0
	hook := OpenOptions{BeforeMigrate: func(_ context.Context, s *Store, from, to int) error {
		called++
		if from != Schema()-1 || to != Schema() {
			t.Errorf("from %d to %d", from, to)
		}
		return s.Snapshot(ctx, filepath.Join(filepath.Dir(path), "before.db"))
	}}
	migrateUpTo = Schema() - 1
	old, err := OpenWith(ctx, path, hook)
	migrateUpTo = 0
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	if called != 0 {
		t.Fatal("a new database was backed up")
	}
	s, err := OpenWith(ctx, path, hook)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if called != 1 {
		t.Fatalf("hook called %d times", called)
	}
	if v, err := CheckFile(ctx, filepath.Join(filepath.Dir(path), "before.db")); err != nil || v != Schema()-1 {
		t.Errorf("snapshot before the migration: schema %d, %v", v, err)
	}
	// Up to date: no hook.
	s.Close()
	if s, err = OpenWith(ctx, path, hook); err != nil || called != 1 {
		t.Fatalf("reopen: %v, %d calls", err, called)
	}
}

// A snapshot has no sessions; a newer schema is refused by CheckFile.
func TestSnapshotAndCheck(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, filepath.Join(dir, "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defer s.w.Exec(`PRAGMA foreign_keys = ON`)
	for _, q := range []string{
		`PRAGMA foreign_keys = OFF`, // a session without its account is enough here
		`INSERT INTO sessions (account_id, token_hash, kind, created_at, last_seen_at, expires_at) VALUES (1, '` + strings.Repeat("a", 64) + `', 'web', 1, 1, 9999999999999)`,
	} {
		if _, err := s.w.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	snap := filepath.Join(dir, "snap.db")
	if err := s.Snapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	if v, err := CheckFile(ctx, snap); err != nil || v != Schema() {
		t.Fatalf("check: %d %v", v, err)
	}
	c, err := Open(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	c.r.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	if n != 0 {
		t.Errorf("snapshot: %d sessions", n)
	}
	c.w.Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (999, 'future', 1)`)
	c.Close()
	if _, err := CheckFile(ctx, snap); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("newer schema: %v", err)
	}
	if _, err := CheckFile(ctx, filepath.Join(dir, "missing.db")); err == nil {
		t.Error("a missing file passed")
	}
}
