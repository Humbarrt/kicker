package bus

import (
	"sync"
	"testing"
)

func TestTopicsAndOrder(t *testing.T) {
	b := New()
	all := b.Subscribe(10)
	table := b.Subscribe(10, "table")
	b.Publish("table", 1)
	b.Publish("modules", 2)
	b.Publish("table", 3)
	if m := <-all.C; m.Topic != "table" || m.Data != 1 {
		t.Errorf("%+v", m)
	}
	if m := <-all.C; m.Topic != "modules" {
		t.Errorf("%+v", m)
	}
	if m := <-table.C; m.Data != 1 {
		t.Errorf("%+v", m)
	}
	if m := <-table.C; m.Data != 3 {
		t.Errorf("filtered sub got %+v", m)
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	b := New()
	slow := b.Subscribe(2)
	fast := b.Subscribe(10)
	for i := 0; i < 5; i++ {
		b.Publish("x", i) // never blocks
	}
	n := 0
	for range slow.C {
		n++
	}
	if n != 2 {
		t.Errorf("slow subscriber got %d messages before being closed, want 2", n)
	}
	if len(fast.C) != 5 || b.Len() != 1 {
		t.Errorf("fast: %d, subs: %d", len(fast.C), b.Len())
	}
	slow.Close() // already closed: no panic
	fast.Close()
	fast.Close()
	if b.Len() != 0 {
		t.Error("close must unsubscribe")
	}
}

func TestConcurrent(t *testing.T) {
	b := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				b.Publish("t", j)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s := b.Subscribe(4)
				s.Close()
			}
		}()
	}
	wg.Wait()
}
