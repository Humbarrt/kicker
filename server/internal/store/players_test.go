package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPlayerConstraints(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	guest, err := s.GuestPlayer(ctx)
	if err != nil || !guest.IsGuest || guest.Name != "Guest" {
		t.Fatalf("guest: %+v %v", guest, err)
	}
	id, err := s.CreatePlayer(ctx, Player{Name: "Mia", Color: "#2563eb"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePlayer(ctx, Player{Name: "MIA", Color: "#2563eb"}, 0); !errors.Is(err, ErrExists) {
		t.Errorf("name is unique, ignoring case: %v", err)
	}
	for name, p := range map[string]Player{
		"color":     {Name: "A", Color: "blue"},
		"upper hex": {Name: "B", Color: "#2563EB"},
		"spaces":    {Name: " C", Color: "#2563eb"},
		"empty":     {Name: "", Color: "#2563eb"},
	} {
		if _, err := s.CreatePlayer(ctx, p, 0); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	if _, err := s.w.Exec(`DELETE FROM players WHERE is_guest = 1`); err == nil {
		t.Error("trigger: the guest can't be deleted")
	}
	if _, err := s.w.Exec(`INSERT INTO players (name, color, is_guest, created_at) VALUES ('G2', '#000000', 1, 1)`); err == nil {
		t.Error("only one guest")
	}
	if err := s.UpdatePlayer(ctx, guest.ID, PlayerChange{Archived: ptrTo(true)}); err == nil {
		t.Error("the guest can't be archived")
	}
	if err := s.UpdatePlayer(ctx, 999, PlayerChange{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown player: %v", err)
	}

	// accounts: one per player, never the guest
	acc, _ := s.CreateAccount(ctx, Account{Username: "mia", PasswordHash: "$argon2id$x", GroupID: 2, Status: "approved", CreatedAt: time.Now()})
	bob, _ := s.CreateAccount(ctx, Account{Username: "bob", PasswordHash: "$argon2id$x", GroupID: 2, Status: "approved", CreatedAt: time.Now()})
	if err := s.SetAccountPlayer(ctx, acc, id); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccountPlayer(ctx, bob, id); !errors.Is(err, ErrExists) {
		t.Errorf("second account for a player: %v", err)
	}
	if err := s.SetAccountPlayer(ctx, bob, guest.ID); err == nil {
		t.Error("trigger: no account for the guest")
	}
	if p, _ := s.Player(ctx, id); p.AccountID == nil || *p.AccountID != acc {
		t.Errorf("account of the player: %+v", p)
	}
	if err := s.DeletePlayer(ctx, id); !errors.Is(err, ErrInUse) {
		t.Errorf("player with account: %v", err)
	}
	// creating with a link in one go; a second link to the same account fails and rolls back
	if _, err := s.CreatePlayer(ctx, Player{Name: "Bob", Color: "#16a34a"}, bob); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePlayer(ctx, Player{Name: "Bob 2", Color: "#16a34a"}, bob); !errors.Is(err, ErrExists) {
		t.Errorf("account already linked: %v", err)
	}
	if ps, _ := s.Players(ctx, true); len(ps) != 3 {
		t.Errorf("the failed create must roll back: %d players", len(ps))
	}
}

func ptrTo[T any](v T) *T { return &v }
