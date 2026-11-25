package table

import (
	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/modules"
	"github.com/Humbarrt/kicker/server/internal/protocol"
)

// ResolveCoin looks a coin up: its player, 0 = unknown or deactivated.
type ResolveCoin func(uid string) int64

// InputFor turns an event of an adopted module into an engine input; ok is false for events the
// table doesn't use (or modules that aren't adopted, or have no role).
func InputFor(ev modules.CapabilityEvent, resolve ResolveCoin) (in engine.Input, ok bool) {
	if !ev.Adopted || ev.Role == "" {
		return nil, false
	}
	switch ev.Capability {
	case "goal_sensor":
		if ev.Name == "goal" {
			return engine.SensorGoal{Role: ev.Role, At: ev.At}, true
		}
	case "coin_acceptor":
		side := engine.Side(ev.Role)
		if !side.Valid() {
			return nil, false
		}
		switch d := ev.Data.(type) {
		case *protocol.CoinInserted:
			return engine.CoinInserted{Side: side, UID: d.UID, PlayerID: resolve(d.UID)}, true
		case *protocol.CoinHeld:
			coins := make([]engine.CoinRef, len(d.UIDs))
			for i, uid := range d.UIDs {
				coins[i] = engine.CoinRef{UID: uid, PlayerID: resolve(uid)}
			}
			return engine.CoinsHeld{Side: side, Coins: coins}, true
		case *protocol.CoinEjected:
			return engine.CoinsEjected{Side: side, UIDs: d.UIDs, ByButton: d.By == "button"}, true
		}
	}
	return nil, false
}
