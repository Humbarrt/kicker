package protocol

// Window remembers the most recent sequence numbers to detect duplicates (spec 01 §3, §7).
//
// Numbers are only ever compared for equality, never with < or >, so the uint32
// wrap-around from 4294967295 to 0 needs no special handling. A module reboot starts
// a new session and therefore a new Window.
type Window struct {
	ring  []uint32
	count map[uint32]int
	pos   int
	full  bool
}

// NewWindow returns a window that remembers the last size numbers
// (the server uses 64 for events, modules use 8 for command ids).
func NewWindow(size int) *Window {
	return &Window{ring: make([]uint32, size), count: make(map[uint32]int, size)}
}

// Seen reports whether n is among the remembered numbers. If it isn't, n is remembered.
func (w *Window) Seen(n uint32) bool {
	if w.count[n] > 0 {
		return true
	}
	if w.full {
		old := w.ring[w.pos]
		if w.count[old]--; w.count[old] == 0 {
			delete(w.count, old)
		}
	}
	w.ring[w.pos] = n
	w.count[n]++
	w.pos++
	if w.pos == len(w.ring) {
		w.pos = 0
		w.full = true
	}
	return false
}
