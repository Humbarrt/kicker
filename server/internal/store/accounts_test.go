package store

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAccountConstraints(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	ok := Account{Username: "anna", PasswordHash: "$argon2id$x", GroupID: 1, Status: "approved", CreatedAt: time.Now()}
	id, err := s.CreateAccount(ctx, ok)
	if err != nil {
		t.Fatal(err)
	}
	dup := ok
	dup.Username = "ANNA"
	if _, err := s.CreateAccount(ctx, dup); err == nil {
		// upper case is refused by the CHECK; the same name in lower case by UNIQUE
		t.Error("upper-case username must be refused")
	}
	dup.Username = "anna"
	if _, err := s.CreateAccount(ctx, dup); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate: %v", err)
	}
	for name, a := range map[string]Account{
		"space":    {Username: "an na", PasswordHash: "$argon2id$x", GroupID: 1, Status: "approved"},
		"short":    {Username: "an", PasswordHash: "$argon2id$x", GroupID: 1, Status: "approved"},
		"umlaut":   {Username: "änna", PasswordHash: "$argon2id$x", GroupID: 1, Status: "approved"},
		"bad hash": {Username: "bob", PasswordHash: "plaintext", GroupID: 1, Status: "approved"},

		"status": {Username: "bob", PasswordHash: "$argon2id$x", GroupID: 2, Status: "banned"},
	} {
		if _, err := s.CreateAccount(ctx, a); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	got, err := s.AccountByUsername(ctx, "Anna")
	if err != nil || got.ID != id {
		t.Errorf("case-insensitive lookup: %+v %v", got, err)
	}
	now := time.Now()
	sid, err := s.CreateSession(ctx, Session{AccountID: id, TokenHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Kind: "web", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(ctx, Session{AccountID: id, TokenHash: "short", Kind: "web", CreatedAt: now, LastSeenAt: now, ExpiresAt: now}); err == nil {
		t.Error("token hash must be 64 hex chars")
	}
	if n, _ := s.DeleteExpiredSessions(ctx, now.Add(2*time.Hour)); n != 1 {
		t.Errorf("expired sessions deleted: %d", n)
	}
	_ = sid
	if id2, err := s.ServerID(ctx); err != nil || len(id2) != 36 {
		t.Errorf("server id %q %v", id2, err)
	} else if again, _ := s.ServerID(ctx); again != id2 {
		t.Error("server id must be stable")
	}
}

func TestGroupsAndLastAdmin(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	gs, err := s.Groups(ctx)
	if err != nil || len(gs) != 3 || gs[0].Builtin != "admin" || gs[1].Builtin != "member" || gs[2].Builtin != "anybody" {
		t.Fatalf("built-in groups: %+v %v", gs, err)
	}
	now := time.Now()
	admin, _ := s.CreateAccount(ctx, Account{Username: "anna", PasswordHash: "$argon2id$x", GroupID: 1, Status: "approved", CreatedAt: now})
	mia, _ := s.CreateAccount(ctx, Account{Username: "mia", PasswordHash: "$argon2id$x", GroupID: 2, Status: "approved", CreatedAt: now})

	// the last admin can't be moved, disabled or deleted
	if err := s.SetAccountGroup(ctx, admin, 2); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("move last admin: %v", err)
	}
	if err := s.SetAccountStatus(ctx, admin, "disabled"); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("disable last admin: %v", err)
	}
	if err := s.DeleteAccount(ctx, admin); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("delete last admin: %v", err)
	}
	if err := s.SetAccountGroup(ctx, mia, 3); err == nil {
		t.Error("nobody can be moved into Anybody")
	}
	if err := s.SetAccountGroup(ctx, mia, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccountStatus(ctx, admin, "disabled"); err != nil {
		t.Errorf("with a second admin: %v", err)
	}
	if n, _ := s.CountAdmins(ctx); n != 1 {
		t.Errorf("admins: %d", n)
	}

	// own groups
	crew, err := s.CreateGroup(ctx, "Crew", map[string]int{"view.live": 1, "games.edit_days": -1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateGroup(ctx, "crew", nil); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate name: %v", err)
	}
	name := "Tournament crew"
	if err := s.UpdateGroup(ctx, crew, &name, map[string]int{"view.live": 0, "table.score": 1}); err != nil {
		t.Fatal(err)
	}
	g, _ := s.Group(ctx, crew)
	if g.Name != name || g.Permissions["view.live"] != 0 || g.Permissions["table.score"] != 1 || g.Permissions["games.edit_days"] != -1 {
		t.Errorf("group: %+v", g)
	}
	if _, err := s.CreateGroup(ctx, "Bad", map[string]int{"x": 999}); err == nil {
		t.Error("value out of range must be refused")
	}
	if err := s.UpdateGroup(ctx, 1, nil, map[string]int{"view.live": 0}); err == nil {
		t.Error("admin permissions can't be stored")
	}
	_ = s.SetAccountGroup(ctx, admin, crew) // anna is disabled, so not the last admin
	if err := s.DeleteGroup(ctx, 2, 1); !errors.Is(err, ErrBuiltinGroup) {
		t.Errorf("delete built-in: %v", err)
	}
	if err := s.DeleteGroup(ctx, crew, 3); err == nil {
		t.Error("members can't be moved into Anybody")
	}
	if err := s.DeleteGroup(ctx, crew, 2); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Account(ctx, admin); a.GroupID != 2 {
		t.Errorf("moved to member: %+v", a)
	}
	if _, err := s.Group(ctx, crew); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted: %v", err)
	}
	var n int
	_ = s.r.QueryRow(`SELECT COUNT(*) FROM group_permissions WHERE group_id = ?`, crew).Scan(&n)
	if n != 0 {
		t.Errorf("permissions of a deleted group: %d", n)
	}
	if _, err := s.w.Exec(`DELETE FROM groups WHERE id = 3`); err == nil {
		t.Error("trigger: built-in groups can't be deleted")
	}
}

// A database from before groups (roles) is migrated: admins land in Admin, members in Member,
// sessions survive.
func TestMigrateRolesToGroups(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kicker.db")
	migrateUpTo = 2
	old, err := Open(ctx, path)
	migrateUpTo = 0
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	for _, q := range []string{
		`INSERT INTO accounts (id, username, password_hash, role, status, created_at) VALUES (1, 'anna', '$argon2id$x', 'admin', 'approved', 1)`,
		`INSERT INTO accounts (id, username, password_hash, role, status, created_at) VALUES (2, 'mia', '$argon2id$y', 'member', 'pending', 2)`,
		`INSERT INTO sessions (account_id, token_hash, kind, created_at, last_seen_at, expires_at) VALUES (1, '` + strings.Repeat("a", 64) + `', 'web', 1, 1, ` + strconv.FormatInt(now+3600000, 10) + `)`,
	} {
		if _, err := old.w.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	anna, _ := s.Account(ctx, 1)
	mia, _ := s.Account(ctx, 2)
	if anna.GroupID != 1 || anna.Username != "anna" || mia.GroupID != 2 || mia.Status != "pending" || mia.PasswordHash != "$argon2id$y" {
		t.Errorf("migrated: %+v %+v", anna, mia)
	}
	if _, a, err := s.SessionByTokenHash(ctx, strings.Repeat("a", 64)); err != nil || a.ID != 1 {
		t.Errorf("sessions must survive the rebuild: %v", err)
	}
	var fk int
	_ = s.w.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	if fk != 1 {
		t.Error("foreign keys must be back on after the migration")
	}
	// the foreign key from sessions still points at the rebuilt table
	if err := s.DeleteAccount(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(ctx, Session{AccountID: 2, TokenHash: strings.Repeat("b", 64), Kind: "web"}); err == nil {
		t.Error("a session for a deleted account must violate the foreign key")
	}
}
