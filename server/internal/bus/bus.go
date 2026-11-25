// Package bus is the in-process event bus: the table, the module host and later the stats
// publish on it; the SSE hub subscribes.
package bus

import "sync"

// Message is one published value.
type Message struct {
	Topic string
	Data  any
}

// Bus delivers messages to subscribers without ever blocking the publisher.
type Bus struct {
	mu   sync.Mutex
	subs map[*Sub]struct{}
}

// Sub is a subscription. C is closed when the subscription ends: by Close, or because the
// subscriber fell behind (its buffer was full). A subscriber that falls behind must re-read the
// current state, e.g. an SSE client reconnects and gets a fresh snapshot.
type Sub struct {
	C      <-chan Message
	c      chan Message
	topics map[string]bool
	bus    *Bus
}

// New returns an empty bus.
func New() *Bus { return &Bus{subs: map[*Sub]struct{}{}} }

// Subscribe returns a subscription with a buffer of n messages for the given topics
// (none = all topics).
func (b *Bus) Subscribe(n int, topics ...string) *Sub {
	c := make(chan Message, n)
	s := &Sub{C: c, c: c, bus: b}
	if len(topics) > 0 {
		s.topics = map[string]bool{}
		for _, t := range topics {
			s.topics[t] = true
		}
	}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Publish sends a message to every matching subscriber.
func (b *Bus) Publish(topic string, data any) {
	m := Message{Topic: topic, Data: data}
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		if s.topics != nil && !s.topics[topic] {
			continue
		}
		select {
		case s.c <- m:
		default:
			delete(b.subs, s)
			close(s.c)
		}
	}
}

// Close ends the subscription. It is safe to call more than once.
func (s *Sub) Close() {
	b := s.bus
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[s]; ok {
		delete(b.subs, s)
		close(s.c)
	}
}

// Len returns the number of subscribers (for tests and diagnostics).
func (b *Bus) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
