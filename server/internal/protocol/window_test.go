package protocol

import (
	"math"
	"testing"
)

func TestWindowDuplicates(t *testing.T) {
	w := NewWindow(64)
	if w.Seen(5) {
		t.Fatal("first time must not be a duplicate")
	}
	if !w.Seen(5) {
		t.Fatal("retransmission must be a duplicate")
	}
}

func TestWindowWrapAround(t *testing.T) {
	w := NewWindow(64)
	for n := uint32(math.MaxUint32 - 10); n != 10; n++ { // crosses 4294967295 → 0
		if w.Seen(n) {
			t.Fatalf("n=%d reported as duplicate during wrap-around", n)
		}
	}
	if !w.Seen(math.MaxUint32) || !w.Seen(0) || !w.Seen(9) {
		t.Fatal("recent numbers on both sides of the wrap must be remembered")
	}
}

func TestWindowForgetsOldest(t *testing.T) {
	w := NewWindow(8)
	for n := range uint32(8) {
		w.Seen(n)
	}
	w.Seen(100) // pushes out 0
	if w.Seen(0) {
		t.Fatal("0 should have been forgotten")
	}
	if !w.Seen(7) {
		t.Fatal("7 should still be remembered")
	}
}
