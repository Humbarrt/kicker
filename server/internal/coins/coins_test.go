package coins

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/store"
)

func TestNormalizeUID(t *testing.T) {
	for in, want := range map[string]string{
		"04a1b2c3":             "04A1B2C3",
		"04:A1:B2:C3:D4:E5:F6": "04A1B2C3D4E5F6",
		" 04 a1 b2 c3 ":        "04A1B2C3",
		"0102030405060708090A": "0102030405060708090A",
	} {
		if got, err := NormalizeUID(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "04A1B2", "04A1B2C3D4", "04A1B2CG", "zz"} {
		if _, err := NormalizeUID(bad); !errors.Is(err, ErrInvalidUID) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCoins(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	as, _ := auth.New(ctx, auth.Options{Store: st})
	var changes [][2]any
	s := New(Options{Store: st, Auth: as, OnChange: func(uid string, pid int64) { changes = append(changes, [2]any{uid, pid}) }})

	mia, _ := st.CreatePlayer(ctx, store.Player{Name: "Mia", Color: "#2563eb"}, 0)
	max, _ := st.CreatePlayer(ctx, store.Player{Name: "Max", Color: "#16a34a"}, 0)
	guest, _ := st.GuestPlayer(ctx)
	acc := auth.Account{ID: 1, Status: auth.StatusApproved, Group: auth.GroupRef{ID: as.BuiltinGroup(auth.GroupMember), Builtin: auth.GroupMember}}
	member := Actor{AccountID: 1, Perms: as.PermsOf(&acc)} // Member has coins.manage by default
	anybody := Actor{Perms: as.PermsOf(nil)}

	if _, err := s.Assign(ctx, anybody, "04A1B2C3", mia, ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("anybody assigns: %v", err)
	}
	c, err := s.Assign(ctx, member, "04:a1:b2:c3", mia, " blue one ")
	if err != nil || c.UID != "04A1B2C3" || c.PlayerID != mia || c.Label != "blue one" || !c.Active {
		t.Fatalf("assign: %+v %v", c, err)
	}
	if s.Resolve(ctx, "04A1B2C3") != mia || s.Resolve(ctx, "04FFFFFF") != 0 {
		t.Error("resolve")
	}
	if c2, _ := st.Coin(ctx, "04A1B2C3"); c2.LastSeenAt == nil || time.Since(*c2.LastSeenAt) > time.Minute {
		t.Errorf("last seen: %+v", c2)
	}
	if _, err := s.Assign(ctx, member, "04A1B2C3", max, "blue one"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Assign(ctx, member, "04000001", guest.ID, "guest coin"); err != nil {
		t.Errorf("guest coins: %v", err)
	}
	if _, err := s.Assign(ctx, member, "04000002", 999, ""); !errors.Is(err, ErrPlayer) {
		t.Errorf("unknown player: %v", err)
	}
	_ = st.UpdatePlayer(ctx, mia, store.PlayerChange{Archived: ptr(true)})
	if _, err := s.Assign(ctx, member, "04000002", mia, ""); !errors.Is(err, ErrPlayer) {
		t.Errorf("archived player: %v", err)
	}

	// deactivated coins count as unknown; activating restores them
	if _, err := s.SetActive(ctx, member, "04A1B2C3", false); err != nil {
		t.Fatal(err)
	}
	if s.Resolve(ctx, "04A1B2C3") != 0 {
		t.Error("deactivated coin must be unknown")
	}
	_, _ = s.SetActive(ctx, member, "04A1B2C3", true)
	if s.Resolve(ctx, "04A1B2C3") != max {
		t.Error("activated again")
	}

	// lists: coins.manage sees all; others only their own player's coins
	if l, err := s.List(ctx, member, 0); err != nil || len(l) != 2 {
		t.Errorf("all: %v %v", l, err)
	}
	noManage := Actor{AccountID: 2, PlayerID: max, Perms: as.PermsOf(nil)}
	if l, err := s.List(ctx, noManage, max); err != nil || len(l) != 1 {
		t.Errorf("own: %v %v", l, err)
	}
	if _, err := s.List(ctx, noManage, guest.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("others' coins: %v", err)
	}

	if err := s.Delete(ctx, member, "04000001"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, member, "04000001"); !errors.Is(err, ErrNotFound) {
		t.Errorf("twice: %v", err)
	}
	// the table hears about every change
	want := []int64{mia, max, guest.ID, 0, max, 0}
	if len(changes) != len(want) {
		t.Fatalf("changes: %v", changes)
	}
	for i, w := range want {
		if changes[i][1] != w {
			t.Errorf("change %d: %v, want player %d", i, changes[i], w)
		}
	}
	log, _ := as.AuditLog(ctx, 20)
	if len(log) != 6 {
		t.Errorf("audit entries: %d", len(log))
	}
	// a player with coins can't be deleted
	if err := st.DeletePlayer(ctx, max); !errors.Is(err, store.ErrInUse) {
		t.Errorf("player with coins: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
