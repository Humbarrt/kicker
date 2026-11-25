// Package stats keeps the statistics projections up to date (D2, D9): the ratings now, more stats
// in M3 task 8. It runs beside the table, so a long replay never delays a goal.
package stats

import (
	"context"
	"log/slog"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/ratings"
	"github.com/Humbarrt/kicker/server/internal/store"
)

// TopicStats is published (with a StatsChanged) after the projections changed; clients re-read
// what they show (spec 02 §8 step 4).
const TopicStats = "stats"

// Version is the version of the stats projections (the rating rows and their columns). When the
// stored version is older, everything is replayed once (Outdated).
const Version = 2 // 2: ratings are rounded to 0.01 after every game, so a replay can stop early

// SettingVersion stores the projections' version after a full replay.
const SettingVersion = "stats_version"

// Outdated reports whether the stats were made by an older version and need a full replay.
func Outdated(ctx context.Context, st *store.Store) (bool, error) {
	var v int
	if _, err := st.GetSetting(ctx, SettingVersion, &v); err != nil {
		return false, err
	}
	return v < Version, nil
}

// StatsChanged says from which game on the ratings changed.
type StatsChanged struct {
	FromGame int64 `json:"from_game"`
}

// Options configure a Projector.
type Options struct {
	Store  *store.Store
	Bus    *bus.Bus // optional
	Params ratings.Params
	Logger *slog.Logger
	// Changed is told after every replay (Cache.Changed). Without it the projector announces
	// TopicStats on the bus itself.
	Changed func(StatsChanged)
}

// Projector rates finished games. GameFinished and ReplayFrom only queue work; Run does it, one
// replay at a time, starting from the earliest game queued.
type Projector struct {
	opts Options
	log  *slog.Logger

	mu      sync.Mutex
	pending *store.GamePoint // earliest point to replay from
	until   *store.GamePoint // latest game queued: the replay may stop once past it and converged
	all     bool             // replay everything
	working bool
	wake    chan struct{}
	idle    chan struct{} // closed while nothing is pending (tests)
}

// New creates a projector.
func New(o Options) *Projector {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Params == (ratings.Params{}) {
		o.Params = ratings.Default()
	}
	idle := make(chan struct{})
	close(idle)
	return &Projector{opts: o, log: o.Logger.With("component", "stats"), wake: make(chan struct{}, 1), idle: idle}
}

// GameFinished queues the rating of a game that just finished – or was edited, voided or reopened
// (task 7): the game and every later game are rated again, until the ratings are what they were.
func (p *Projector) GameFinished(ctx context.Context, gameID int64) {
	from, to, ok, err := p.opts.Store.RatingSpan(ctx, gameID)
	if err != nil {
		p.log.Error("rating span", "game", gameID, "err", err)
		p.ReplayAll()
		return
	}
	if ok {
		p.queue(&from, &to, false)
	}
}

// ReplayAll queues a replay of every game.
func (p *Projector) ReplayAll() { p.queue(nil, nil, true) }

func (p *Projector) queue(from, to *store.GamePoint, all bool) {
	p.mu.Lock()
	if p.pending == nil && !p.all && !p.working {
		p.idle = make(chan struct{})
	}
	if all {
		p.all = true
	} else {
		if p.pending == nil || p.pending.After(*from) {
			p.pending = from
		}
		if p.until == nil || to.After(*p.until) {
			p.until = to
		}
	}
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Idle returns a channel that is closed while no work is pending (for tests and shutdown).
func (p *Projector) Idle() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.idle
}

// Run works off the queue until ctx ends.
func (p *Projector) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		}
		p.mu.Lock()
		if p.pending == nil && !p.all {
			p.mu.Unlock()
			continue
		}
		from, until, all := p.pending, p.until, p.all
		p.pending, p.until, p.all, p.working = nil, nil, false, true
		p.mu.Unlock()
		var err error
		if all || from == nil {
			err = p.Replay(ctx, store.GamePoint{})
		} else {
			err = p.replay(ctx, *from, until)
		}
		if err != nil {
			p.log.Error("rating replay failed", "err", err)
		}
		p.mu.Lock()
		p.working = false
		if p.pending == nil && !p.all {
			close(p.idle)
		} else {
			select { // more was queued meanwhile
			case p.wake <- struct{}{}:
			default:
			}
		}
		p.mu.Unlock()
	}
}

// Replay rates every finished game from a point on (zero point = all) and writes what differs
// from the stored rating history.
func (p *Projector) Replay(ctx context.Context, from store.GamePoint) error {
	return p.replay(ctx, from, nil)
}

// replayChunk is how many games a replay reads at once.
const replayChunk = 2000

// Tolerance is how far a replay's ratings may be from the stored ones when it stops early. Elo
// never quite converges after an old edit: the difference spreads over all players and shrinks,
// but rounding to 0.01 keeps a noise of ±0.02 alive forever. Voiding the oldest of 100 000 games
// changed 198 000 of 329 000 rows exactly; down to 0.05 it is 30 000 (spec 04 §4: < 3 s). The
// ratings show as whole numbers, and a full replay (ReplayAll, a new Version) is exact.
const Tolerance = 0.05

// replay rates the games from a point on and writes the rows that differ. With until set it stops
// once past that game and every rating is within Tolerance of the stored one: the later games
// can't differ by more then. Ratings are rounded to 0.01 after every game, as stored.
func (p *Projector) replay(ctx context.Context, from store.GamePoint, until *store.GamePoint) error {
	start := time.Now()
	st := p.opts.Store
	tab := map[ratings.PlayerKey]ratings.Rating{} // the replay's ratings
	stored := map[ratings.PlayerKey]float64{}     // the stored ratings at the same point
	if from != (store.GamePoint{}) {
		prev, err := st.RatingsBefore(ctx, from)
		if err != nil {
			return err
		}
		for _, r := range prev {
			k := ratings.PlayerKey{Player: r.PlayerID, Key: ratings.Key(r.Key)}
			tab[k], stored[k] = ratings.Rating{Value: r.Rating}, r.Rating
		}
	}
	dirty := map[ratings.PlayerKey]bool{}
	check := func(k ratings.PlayerKey) {
		t, tok := tab[k]
		v, sok := stored[k]
		if tok != sok || math.Abs(t.Value-v) > Tolerance {
			dirty[k] = true
		} else {
			delete(dirty, k)
		}
	}
	guest, err := st.GuestPlayer(ctx)
	if err != nil {
		return err
	}
	type rowKey struct {
		player int64
		key    string
	}
	var update, insert, del []store.RatingRow
	var first int64
	games, rated := 0, 0
	cursor, inclusive := from, true
replay:
	for {
		batch, err := st.RatedGames(ctx, cursor, inclusive, replayChunk)
		if err != nil {
			return err
		}
		var upTo *store.GamePoint
		if len(batch) == replayChunk {
			last := batch[len(batch)-1]
			upTo = &store.GamePoint{EndedAt: last.EndedAt, ID: last.ID}
		}
		old, err := st.RatingRowsIn(ctx, cursor, inclusive, upTo)
		if err != nil {
			return err
		}
		// Walk the games and the stored rows together, in rating order. Stored rows of a game that
		// isn't rated any more (voided, reopened, moved) are deleted.
		i, j := 0, 0
		for i < len(batch) || j < len(old) {
			var pt store.GamePoint
			var g *store.RatedGame
			if i < len(batch) {
				g = &batch[i]
				pt = store.GamePoint{EndedAt: g.EndedAt, ID: g.ID}
			}
			if j < len(old) {
				op := store.GamePoint{EndedAt: old[j].EndedAt, ID: old[j].GameID}
				if g == nil || pt.After(op) {
					g, pt = nil, op
				}
			}
			was := map[rowKey]store.RatingRow{}
			for ; j < len(old) && old[j].GameID == pt.ID && old[j].EndedAt.Equal(pt.EndedAt); j++ {
				r := old[j]
				was[rowKey{r.PlayerID, r.Key}] = r
				k := ratings.PlayerKey{Player: r.PlayerID, Key: ratings.Key(r.Key)}
				stored[k] = r.After
			}
			if g != nil {
				i++
				games++
				if first == 0 {
					first = g.ID
				}
				for _, row := range p.rate(tab, *g, guest.ID) {
					rated++
					k := rowKey{row.PlayerID, row.Key}
					if w, ok := was[k]; !ok {
						insert = append(insert, row)
					} else if !w.SameGame(row) {
						del, insert = append(del, w), append(insert, row)
					} else if !w.Same(row) {
						update = append(update, row)
					}
					delete(was, k)
					check(ratings.PlayerKey{Player: row.PlayerID, Key: ratings.Key(row.Key)})
				}
			}
			for k, r := range was {
				del = append(del, r)
				check(ratings.PlayerKey{Player: k.player, Key: ratings.Key(k.key)})
			}
			if until != nil && !until.After(pt) && len(dirty) == 0 {
				break replay
			}
			cursor, inclusive = pt, false
		}
		if upTo == nil {
			break
		}
	}
	if err := st.ChangeRatings(ctx, update, insert, del); err != nil {
		return err
	}
	if from == (store.GamePoint{}) {
		if err := st.SetSetting(ctx, SettingVersion, Version); err != nil {
			return err
		}
	}
	p.log.Info("ratings updated", "games", games, "rated", rated, "changed", len(update)+len(insert)+len(del),
		"took", time.Since(start).Round(time.Millisecond))
	if p.opts.Changed != nil {
		p.opts.Changed(StatsChanged{FromGame: first})
	} else if p.opts.Bus != nil {
		p.opts.Bus.Publish(TopicStats, StatsChanged{FromGame: first})
	}
	return nil
}

// rate applies one game to the ratings and returns its rows, rounded as stored.
func (p *Projector) rate(tab map[ratings.PlayerKey]ratings.Rating, g store.RatedGame, guestID int64) []store.RatingRow {
	rg := RatingGame(g, guestID)
	var rows []store.RatingRow
	for _, c := range p.opts.Params.Apply(tab, rg) {
		after := store.RoundRating(c.After)
		tab[ratings.PlayerKey{Player: c.Player, Key: c.Key}] = ratings.Rating{Value: after}
		row := store.RatingRow{GameID: c.GameID, PlayerID: c.Player, Key: string(c.Key), Before: store.RoundRating(c.Before),
			After: after, EndedAt: g.EndedAt, Result: "draw", GoalsFor: g.HomeGoals, GoalsAgainst: g.AwayGoals}
		side := "home"
		if slices.Contains(rg.Away, c.Player) {
			side = "away"
			row.GoalsFor, row.GoalsAgainst = g.AwayGoals, g.HomeGoals
		}
		if g.Winner != nil {
			row.Result = "loss"
			if *g.Winner == side {
				row.Result = "win"
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// RatingGame turns a stored game into the rating's view: the guest and unknown coins are 0.
func RatingGame(g store.RatedGame, guestID int64) ratings.Game {
	out := ratings.Game{ID: g.ID}
	if g.Winner != nil {
		out.Winner = *g.Winner
	}
	for _, p := range g.Participants {
		id := int64(0)
		if p.PlayerID != nil && *p.PlayerID != guestID {
			id = *p.PlayerID
		}
		if p.Side == "home" {
			out.Home = append(out.Home, id)
		} else {
			out.Away = append(out.Away, id)
		}
	}
	return out
}
