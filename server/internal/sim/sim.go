// Package sim is the simulator behind `kicker serve --sim`: virtual modules that the simulator
// panel in the web UI can trigger, as if a ball had dropped into a real goal (spec 04 §3).
package sim

import (
	"errors"
	"slices"
	"sort"

	"github.com/Humbarrt/kicker/server/internal/modules/virtual"
	"github.com/Humbarrt/kicker/server/internal/protocol"
)

// Module is one virtual module as the panel shows it.
type Module struct {
	UID       string     `json:"uid"`
	Manifest  string     `json:"manifest"`
	Instances []Instance `json:"instances"`
}

// Instance is one capability instance with the events it can send.
type Instance struct {
	Instance   string   `json:"instance"`
	Capability string   `json:"capability"`
	Role       string   `json:"role"`   // filled in by the API from the module host
	Events     []string `json:"events"` // only events without payload, for now
}

// ErrUnknown is returned for a module, instance or event the simulator doesn't have.
var ErrUnknown = errors.New("no such simulated module, instance or event")

// Simulator wraps the virtual modules.
type Simulator struct {
	mods      []*virtual.Module
	acceptors []*virtual.CoinAcceptor
}

// New returns a simulator for the given virtual modules; coin acceptors among them get their
// own controls (insert a coin, press the button).
func New(mods []*virtual.Module, acceptors ...*virtual.CoinAcceptor) *Simulator {
	return &Simulator{mods: mods, acceptors: acceptors}
}

// Acceptor is a virtual coin acceptor as the panel shows it.
type Acceptor struct {
	UID      string   `json:"uid"`
	Instance string   `json:"instance"`
	Role     string   `json:"role"` // filled in by the API from the module host
	Cap      int      `json:"cap"`
	Coins    []string `json:"coins"`
}

// Acceptors lists the virtual coin acceptors with the coins inside.
func (s *Simulator) Acceptors() []Acceptor {
	out := []Acceptor{}
	for _, a := range s.acceptors {
		coins := a.Coins()
		if coins == nil {
			coins = []string{}
		}
		out = append(out, Acceptor{UID: a.UID, Instance: a.Instance(), Cap: a.Cap(), Coins: coins})
	}
	return out
}

func (s *Simulator) acceptor(uid string) *virtual.CoinAcceptor {
	for _, a := range s.acceptors {
		if a.UID == uid {
			return a
		}
	}
	return nil
}

// Insert pushes a coin into a virtual acceptor. A coin can't be in two acceptors at once.
func (s *Simulator) Insert(uid, coin string) error {
	a := s.acceptor(uid)
	if a == nil {
		return ErrUnknown
	}
	for _, o := range s.acceptors {
		if o != a && slices.Contains(o.Coins(), coin) {
			return virtual.ErrAlreadyIn
		}
	}
	return a.Insert(coin)
}

// PressButton presses a virtual acceptor's eject button.
func (s *Simulator) PressButton(uid string) error {
	a := s.acceptor(uid)
	if a == nil {
		return ErrUnknown
	}
	return a.PressButton()
}

// Modules lists the virtual modules.
func (s *Simulator) Modules() []Module {
	out := []Module{}
	for _, m := range s.mods {
		sm := Module{UID: m.UID, Manifest: m.Manifest}
		for _, c := range m.Caps {
			inst := Instance{Instance: c.Instance, Capability: c.ID, Events: []string{}}
			if cat, ok := protocol.Lookup(c.ID, c.Version); ok {
				for name, payload := range cat.Events {
					if payload == nil {
						inst.Events = append(inst.Events, name)
					}
				}
				sort.Strings(inst.Events)
			}
			sm.Instances = append(sm.Instances, inst)
		}
		out = append(out, sm)
	}
	return out
}

// Emit sends an event from a virtual module.
func (s *Simulator) Emit(uid, instance, event string) error {
	for _, sm := range s.Modules() {
		if sm.UID != uid {
			continue
		}
		for _, in := range sm.Instances {
			if in.Instance == instance && slices.Contains(in.Events, event) {
				for _, m := range s.mods {
					if m.UID == uid {
						return m.Emit(instance, event, nil)
					}
				}
			}
		}
	}
	return ErrUnknown
}
