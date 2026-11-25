package stats

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/store"
)

// The cache computes the views at start-up, serves them without computing, and recomputes them
// in the background after a change, announcing it on the bus.
func TestCache(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mia, _ := st.CreatePlayer(ctx, store.Player{Name: "Mia", Color: "#1e3a8a"}, 0)
	b := bus.New()
	sub := b.Subscribe(8, TopicStats)
	defer sub.Close()
	c := NewCache(Reader{St: st}, b, nil)
	r := Reader{St: st, Cache: c}
	go c.Run(ctx)
	<-sub.C // the start-up refresh: overview and leaderboards …
	<-sub.C // … then the profiles
	waitIdle(t, c)
	c.mu.Lock()
	e := c.m[fmt.Sprintf("profile/%d", mia)]
	n := len(c.m)
	c.mu.Unlock()
	if e == nil || !e.ok || n != 4 {
		t.Fatalf("start-up refresh: %d views, Mia's profile %+v", n, e)
	}
	if _, err := r.Profile(ctx, mia); err != nil {
		t.Fatal(err)
	}

	// A view: computed by the first request, then served; a change recomputes it in the background.
	calls := 0
	view := cached(r, "test", func(context.Context) (int, error) { calls++; return calls, nil })
	if v, _ := view(ctx); v != 1 {
		t.Fatalf("first: %d", v)
	}
	if v, _ := view(ctx); v != 1 || calls != 1 {
		t.Errorf("served: %d after %d computations", v, calls)
	}
	c.Changed(StatsChanged{FromGame: 7})
	c.Changed(StatsChanged{FromGame: 3}) // merged: the earliest game
	if sc := (<-sub.C).Data.(StatsChanged); sc.FromGame != 3 {
		t.Errorf("announced %+v", sc)
	}
	waitIdle(t, c)
	if v, _ := view(ctx); v != 2 || calls != 2 {
		t.Errorf("after the refresh: %d after %d computations", v, calls)
	}

	// Without a cache every call computes.
	if v, _ := cached(Reader{}, "test", func(context.Context) (int, error) { calls++; return calls, nil })(ctx); v != 3 {
		t.Errorf("no cache: %d", v)
	}
}

func waitIdle(t *testing.T, c *Cache) {
	t.Helper()
	select {
	case <-c.Idle():
	case <-time.After(5 * time.Second):
		t.Fatal("refresh didn't finish")
	}
}
