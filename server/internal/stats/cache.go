package stats

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Humbarrt/kicker/server/internal/bus"
)

// RefreshEvery is how often the cache refreshes even without a new game: the overview's 30-day
// window moves on, a player gets archived.
const RefreshEvery = time.Hour

// Cache serves the stats views (profiles, leaderboards, the overview) from memory and recomputes
// them in the background, like a scheduled task: a finished or edited game queues a refresh, the
// requests meanwhile get the previous version at once, and clients hear about the new one over
// SSE (TopicStats) when it is ready. How long a computation takes (a regular with 13 000 games:
// 200 ms on x86, more on the Pi) is then decoupled from the requests (spec 04 §4, decision B).
// Only the very first request for a view, before the start-up refresh got to it, waits.
type Cache struct {
	r   Reader // without a cache
	bus *bus.Bus
	log *slog.Logger

	mu      sync.Mutex
	m       map[string]*cacheEntry
	pending *StatsChanged // a change waiting for the refresh
	wake    chan struct{}
	idle    chan struct{} // closed while no refresh is pending or running
	working bool
}

type cacheEntry struct {
	compute func(context.Context) (any, error)
	busy    sync.Mutex // one computation at a time
	v       any        // guarded by Cache.mu
	ok      bool
}

// NewCache makes a cache over a store reader; b (optional) gets the TopicStats announcements.
func NewCache(r Reader, b *bus.Bus, log *slog.Logger) *Cache {
	if log == nil {
		log = slog.Default()
	}
	r.Cache = nil
	c := &Cache{r: r, bus: b, log: log.With("component", "stats-cache"), m: map[string]*cacheEntry{},
		wake: make(chan struct{}, 1), idle: make(chan struct{}), pending: &StatsChanged{}}
	c.wake <- struct{}{} // the start-up refresh, announced like any other
	return c
}

// Changed queues a refresh after the ratings changed (Options.Changed of the Projector).
func (c *Cache) Changed(sc StatsChanged) {
	c.mu.Lock()
	if c.pending == nil {
		if !c.working {
			c.idle = make(chan struct{})
		}
		c.pending = &sc
	} else if sc.FromGame != 0 && (c.pending.FromGame == 0 || sc.FromGame < c.pending.FromGame) {
		c.pending.FromGame = sc.FromGame
	}
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Idle returns a channel that is closed once no refresh is pending or running.
func (c *Cache) Idle() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.idle
}

// Run computes every view once, then refreshes them after each change and every RefreshEvery.
func (c *Cache) Run(ctx context.Context) {
	tick := time.NewTicker(RefreshEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
		case <-tick.C:
			c.refresh(ctx, nil)
			continue
		}
		c.mu.Lock()
		sc := c.pending
		if sc == nil { // a wake-up for a change already handled
			c.mu.Unlock()
			continue
		}
		c.pending, c.working = nil, true
		c.mu.Unlock()
		c.refresh(ctx, sc)
		c.mu.Lock()
		c.working = false
		if c.pending == nil {
			close(c.idle)
		}
		c.mu.Unlock()
	}
}

// refresh recomputes the overview and the leaderboards, then every profile, announcing each part
// when it is done (sc nil: a periodic refresh, announced once at the end).
func (c *Cache) refresh(ctx context.Context, sc *StatsChanged) {
	start := time.Now()
	c.prepare(ctx)
	announce := func() {
		if sc != nil && c.bus != nil {
			c.bus.Publish(TopicStats, *sc)
		}
	}
	c.mu.Lock()
	var quick, profiles []string
	for k := range c.m {
		if len(k) > 8 && k[:8] == "profile/" {
			profiles = append(profiles, k)
		} else {
			quick = append(quick, k)
		}
	}
	c.mu.Unlock()
	for _, k := range quick {
		c.recompute(ctx, k)
	}
	announce() // the scoreboard's overview doesn't wait for the profiles
	for _, k := range profiles {
		c.recompute(ctx, k)
	}
	if len(profiles) > 0 {
		announce()
	}
	c.log.Info("stats refreshed", "views", len(quick)+len(profiles), "took", time.Since(start).Round(time.Millisecond))
}

// prepare registers the views worth having ready: the overview, both leaderboards and the
// profile of every player who isn't archived.
func (c *Cache) prepare(ctx context.Context) {
	r := c.r
	c.entry("overview", func(ctx context.Context) (any, error) { return r.overview(ctx) })
	for _, key := range []string{"solo", "team"} {
		c.entry("leaderboard/"+key, func(ctx context.Context) (any, error) { return r.leaderboard(ctx, key) })
	}
	players, err := r.St.Players(ctx, false)
	if err != nil {
		c.log.Error("players", "err", err)
		return
	}
	for _, p := range players {
		if !p.IsGuest {
			id := p.ID
			c.entry(fmt.Sprintf("profile/%d", id), func(ctx context.Context) (any, error) { return r.profile(ctx, id) })
		}
	}
}

func (c *Cache) entry(key string, compute func(context.Context) (any, error)) *cacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.m[key]
	if e == nil {
		e = &cacheEntry{compute: compute}
		c.m[key] = e
	}
	return e
}

// recompute computes one view again; on an error the previous version stays.
func (c *Cache) recompute(ctx context.Context, key string) {
	c.mu.Lock()
	e := c.m[key]
	c.mu.Unlock()
	e.busy.Lock()
	defer e.busy.Unlock()
	v, err := e.compute(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.log.Error("refresh", "view", key, "err", err)
		}
		return
	}
	c.mu.Lock()
	e.v, e.ok = v, true
	c.mu.Unlock()
}

// cached returns a view from the cache, computing it now only if it was never computed.
func cached[T any](r Reader, key string, compute func(context.Context) (T, error)) func(context.Context) (T, error) {
	return func(ctx context.Context) (T, error) {
		c := r.Cache
		if c == nil {
			return compute(ctx)
		}
		e := c.entry(key, func(ctx context.Context) (any, error) { return compute(ctx) })
		c.mu.Lock()
		v, ok := e.v, e.ok
		c.mu.Unlock()
		if ok {
			return v.(T), nil
		}
		e.busy.Lock() // someone may be computing it right now
		defer e.busy.Unlock()
		c.mu.Lock()
		v, ok = e.v, e.ok
		c.mu.Unlock()
		if ok {
			return v.(T), nil
		}
		x, err := compute(ctx)
		if err != nil {
			var zero T
			return zero, err
		}
		c.mu.Lock()
		e.v, e.ok = x, true
		c.mu.Unlock()
		return x, nil
	}
}
