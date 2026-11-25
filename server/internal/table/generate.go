package table

import (
	"errors"
	"time"

	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
)

// Generated is a game played through the engine without a table: for the load test (spec 04 §4),
// which needs many realistic games fast and writes them with store.ImportGames.
type Generated struct {
	Home, Away []int64 // players; guests are the guest player's id
	Rules      engine.Rules
	PresetID   int64
	Start      time.Time
	// NextGoal says who scores the next goal (engine.Home / engine.Away) and how long after the
	// previous one (or the kickoff).
	NextGoal func() (engine.Side, time.Duration)
}

// ErrNoResult is returned when a generated game doesn't end within 200 goals.
var ErrNoResult = errors.New("the game did not end")

// Play runs the game through engine.Step – registrations, "Start now", one sensor goal after the
// other – and returns the finished game as the table would have stored it.
func (gen Generated) Play(cfg engine.Config) (store.ImportGame, error) {
	cfg.Dispenser = false
	now := gen.Start
	s := engine.NewState(cfg, now)
	var last *engine.Game
	var events []engine.Event
	step := func(in engine.Input) error {
		next, res, err := engine.Step(s, in, now)
		if err != nil {
			return err
		}
		for _, w := range res.Writes {
			g := w.Game
			last = &g
			events = append(events, w.Events...)
		}
		s = next
		return nil
	}
	for _, id := range gen.Home {
		if err := step(engine.RegisterPlayer{Side: engine.Home, PlayerID: id}); err != nil {
			return store.ImportGame{}, err
		}
	}
	for _, id := range gen.Away {
		if err := step(engine.RegisterPlayer{Side: engine.Away, PlayerID: id}); err != nil {
			return store.ImportGame{}, err
		}
	}
	if err := step(engine.StartGame{Rules: gen.Rules, PresetID: gen.PresetID, FirstBall: engine.Home}); err != nil {
		return store.ImportGame{}, err
	}
	for range 200 {
		side, after := gen.NextGoal()
		now = now.Add(after)
		role := "home_goal" // ball in the home net: a point for away
		if side == engine.Home {
			role = "away_goal"
		}
		if err := step(engine.SensorGoal{Role: role, At: now}); err != nil {
			return store.ImportGame{}, err
		}
		if last != nil && last.Status == engine.StatusFinished {
			return store.ImportGame{Game: gameToStore(*last), Events: eventsToStore(events)}, nil
		}
	}
	return store.ImportGame{}, ErrNoResult
}
