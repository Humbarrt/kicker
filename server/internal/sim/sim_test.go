package sim

import (
	"errors"
	"testing"

	"github.com/Humbarrt/kicker/server/internal/modules/virtual"
)

func TestModules(t *testing.T) {
	s := New([]*virtual.Module{virtual.GoalSensorModule("g")})
	ms := s.Modules()
	if len(ms) != 1 || len(ms[0].Instances) != 2 || ms[0].Instances[0].Events[0] != "goal" {
		t.Fatalf("%+v", ms)
	}
	if err := s.Emit("g", "a", "goal"); err != nil {
		t.Error(err)
	}
	for _, c := range [][3]string{{"x", "a", "goal"}, {"g", "c", "goal"}, {"g", "a", "explode"}} {
		if err := s.Emit(c[0], c[1], c[2]); !errors.Is(err, ErrUnknown) {
			t.Errorf("%v: %v", c, err)
		}
	}
}

func TestAcceptors(t *testing.T) {
	acc := virtual.NewCoinAcceptor("c", 2)
	s := New([]*virtual.Module{acc.Module}, acc)
	if err := s.Insert("c", "04A1B2C3"); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("c", "04a1b2c3"); err == nil {
		t.Error("lower-case uid must be refused (the API normalizes)")
	}
	_ = s.Insert("c", "04000002")
	if err := s.Insert("c", "04000003"); !errors.Is(err, virtual.ErrFull) {
		t.Errorf("third coin: %v", err)
	}
	if a := s.Acceptors(); len(a) != 1 || len(a[0].Coins) != 2 || a[0].Cap != 2 {
		t.Errorf("%+v", a)
	}
	_ = s.PressButton("c")
	if a := s.Acceptors(); len(a[0].Coins) != 0 {
		t.Errorf("button empties: %+v", a)
	}
	if err := s.Insert("x", "04A1B2C3"); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown acceptor: %v", err)
	}
	if ms := s.Modules(); len(ms) != 1 || ms[0].Instances[0].Capability != "coin_acceptor" || len(ms[0].Instances[0].Events) != 0 {
		t.Errorf("coin events carry data, so no generic buttons: %+v", ms)
	}
}

// A coin is in one place at a time.
func TestCoinInOneAcceptor(t *testing.T) {
	home, away := virtual.NewCoinAcceptor("h", 2), virtual.NewCoinAcceptor("a", 2)
	s := New([]*virtual.Module{home.Module, away.Module}, home, away)
	if err := s.Insert("h", "04A1B2C3"); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert("a", "04A1B2C3"); !errors.Is(err, virtual.ErrAlreadyIn) {
		t.Errorf("same coin in the other acceptor: %v", err)
	}
	_ = s.PressButton("h")
	if err := s.Insert("a", "04A1B2C3"); err != nil {
		t.Errorf("after the eject it can go elsewhere: %v", err)
	}
}
