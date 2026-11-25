package modules

import (
	"encoding/json"
	"sync"
)

// MemoryStore keeps adoptions in memory (tests, and until the SQLite store exists).
type MemoryStore struct {
	mu        sync.Mutex
	adoptions map[string]Adoption
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{adoptions: map[string]Adoption{}} }

func (s *MemoryStore) Adoptions() ([]Adoption, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Adoption, 0, len(s.adoptions))
	for _, a := range s.adoptions {
		out = append(out, cloneAdoption(a))
	}
	return out, nil
}

func (s *MemoryStore) SaveAdoption(a Adoption) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adoptions[a.UID] = cloneAdoption(a)
	return nil
}

func (s *MemoryStore) DeleteAdoption(uid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.adoptions, uid)
	return nil
}

func cloneAdoption(a Adoption) Adoption {
	c := a
	c.Roles = make(map[string]string, len(a.Roles))
	for k, v := range a.Roles {
		c.Roles[k] = v
	}
	c.Caps = make(map[string]string, len(a.Caps))
	for k, v := range a.Caps {
		c.Caps[k] = v
	}
	c.Config = make(map[string]json.RawMessage, len(a.Config))
	for k, v := range a.Config {
		c.Config[k] = append(json.RawMessage(nil), v...)
	}
	return c
}

// console is a ring of the last lines of one module (D8).
type console struct {
	lines []ConsoleEvent
	next  int
	full  bool
}

const consoleSize = 500

func (c *console) add(e ConsoleEvent) {
	if c.lines == nil {
		c.lines = make([]ConsoleEvent, consoleSize)
	}
	c.lines[c.next] = e
	c.next = (c.next + 1) % consoleSize
	if c.next == 0 {
		c.full = true
	}
}

func (c *console) snapshot() []ConsoleEvent {
	if !c.full {
		return append([]ConsoleEvent(nil), c.lines[:c.next]...)
	}
	out := make([]ConsoleEvent, 0, consoleSize)
	out = append(out, c.lines[c.next:]...)
	return append(out, c.lines[:c.next]...)
}
