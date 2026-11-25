package auth

import (
	"sync"
	"time"
)

// throttle slows down guessing (spec 03 §6): after 5 failures for a key (an IP address or a
// username), every further attempt has to wait 1 s, 2 s, 4 s, … up to 15 min. A success resets it.
type throttle struct {
	mu   sync.Mutex
	m    map[string]*attempts
	free int // failures without delay
	max  time.Duration
}

type attempts struct {
	failures int
	next     time.Time // no attempt before this
	last     time.Time
}

func newThrottle() *throttle {
	return &throttle{m: map[string]*attempts{}, free: 5, max: 15 * time.Minute}
}

// wait returns how long the caller must still wait before trying again (0 = go ahead).
func (t *throttle) wait(now time.Time, keys ...string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	var d time.Duration
	for _, k := range keys {
		if a := t.m[k]; a != nil && a.next.After(now) {
			d = max(d, a.next.Sub(now))
		}
	}
	return d
}

// fail records a failure. It reports true when a key has just reached the delay threshold, which
// is worth an audit log entry ("login failures in bursts").
func (t *throttle) fail(now time.Time, keys ...string) (burst bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gc(now)
	for _, k := range keys {
		a := t.m[k]
		if a == nil {
			a = &attempts{}
			t.m[k] = a
		}
		a.failures++
		a.last = now
		if over := a.failures - t.free; over >= 0 {
			d := t.max
			if over < 20 {
				d = min(time.Second<<over, t.max)
			}
			a.next = now.Add(d)
			burst = burst || over == 0
		}
	}
	return burst
}

func (t *throttle) reset(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range keys {
		delete(t.m, k)
	}
}

// gc forgets keys that have been quiet for a day.
func (t *throttle) gc(now time.Time) {
	if len(t.m) < 1000 {
		return
	}
	for k, a := range t.m {
		if now.Sub(a.last) > 24*time.Hour {
			delete(t.m, k)
		}
	}
}
