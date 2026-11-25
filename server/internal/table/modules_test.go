package table

import (
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/modules"
	"github.com/Humbarrt/kicker/server/internal/protocol"
)

func TestInputFor(t *testing.T) {
	resolve := func(uid string) int64 {
		if uid == "04A1B2C3" {
			return 7
		}
		return 0
	}
	at := time.Now()
	cases := []struct {
		ev   modules.CapabilityEvent
		want engine.Input
	}{
		{modules.CapabilityEvent{Adopted: true, Capability: "goal_sensor", Role: "home_goal", Name: "goal", At: at},
			engine.SensorGoal{Role: "home_goal", At: at}},
		{modules.CapabilityEvent{Adopted: true, Capability: "coin_acceptor", Role: "away", Name: "inserted", Data: &protocol.CoinInserted{UID: "04A1B2C3"}},
			engine.CoinInserted{Side: engine.Away, UID: "04A1B2C3", PlayerID: 7}},
		{modules.CapabilityEvent{Adopted: true, Capability: "coin_acceptor", Role: "home", Name: "ejected", Data: &protocol.CoinEjected{By: "button", UIDs: []string{"X"}}},
			engine.CoinsEjected{Side: engine.Home, UIDs: []string{"X"}}},
	}
	for _, c := range cases {
		got, ok := InputFor(c.ev, resolve)
		if !ok {
			t.Errorf("%+v: not used", c.ev)
			continue
		}
		switch w := c.want.(type) {
		case engine.CoinsEjected:
			g := got.(engine.CoinsEjected)
			if g.Side != w.Side || len(g.UIDs) != 1 {
				t.Errorf("got %+v", got)
			}
		default:
			if got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		}
	}
	held, _ := InputFor(modules.CapabilityEvent{Adopted: true, Capability: "coin_acceptor", Role: "home", Name: "held",
		Data: &protocol.CoinHeld{UIDs: []string{"04A1B2C3", "04FFFFFF"}}}, resolve)
	if h := held.(engine.CoinsHeld); len(h.Coins) != 2 || h.Coins[0].PlayerID != 7 || h.Coins[1].PlayerID != 0 {
		t.Errorf("held: %+v", held)
	}
	for _, ev := range []modules.CapabilityEvent{
		{Adopted: false, Capability: "goal_sensor", Role: "home_goal", Name: "goal"},
		{Adopted: true, Capability: "goal_sensor", Role: "", Name: "goal"},
		{Adopted: true, Capability: "coin_acceptor", Role: "middle", Data: &protocol.CoinInserted{UID: "04A1B2C3"}},
		{Adopted: true, Capability: "acme.test/horn", Role: "x", Name: "honk"},
	} {
		if _, ok := InputFor(ev, resolve); ok {
			t.Errorf("%+v must be ignored", ev)
		}
	}
}
