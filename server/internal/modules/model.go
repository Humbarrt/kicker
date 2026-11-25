// Package modules is the module host (D5, D7): it discovers modules on serial ports, runs one
// protocol session per module, remembers adoptions and roles, keeps a console per module and
// turns module events into role-based events for the game engine.
package modules

import (
	"encoding/json"
	"time"

	"github.com/Humbarrt/kicker/server/internal/modules/session"
)

// Adoption is what the server remembers about a module the user adopted.
type Adoption struct {
	UID       string
	Name      string                     // user-given name, e.g. "Goal sensors"
	Roles     map[string]string          // instance → role (missing = not used in games)
	Caps      map[string]string          // instance → capability id, as announced when adopted
	Config    map[string]json.RawMessage // instance → merged config ("" = module level)
	AdoptedAt time.Time
}

// Store persists adoptions. MemoryStore for tests; the SQLite store follows with task 7.
type Store interface {
	Adoptions() ([]Adoption, error)
	SaveAdoption(Adoption) error
	DeleteAdoption(uid string) error
}

// ModuleInfo is a snapshot of one module for the UI and the API.
type ModuleInfo struct {
	UID         string    `json:"uid"`
	Name        string    `json:"name"`
	Port        string    `json:"port"`
	Online      bool      `json:"online"`
	Adopted     bool      `json:"adopted"`
	Virtual     bool      `json:"virtual"`
	Manifest    string    `json:"manifest"`
	Firmware    string    `json:"firmware"`
	FWVersion   string    `json:"fw_version"`
	Hardware    string    `json:"hardware"`
	ConnectedAt time.Time `json:"connected_at"`
	Caps        []CapView `json:"caps"`
}

// CapView is one capability instance of a module.
type CapView struct {
	Instance   string   `json:"instance"`
	Capability string   `json:"capability"` // e.g. "goal_sensor@1"
	Supported  bool     `json:"supported"`  // in the catalog and understood by this server
	CanWake    bool     `json:"can_wake"`
	Role       string   `json:"role"`   // "" = no role
	Roles      []string `json:"roles"`  // roles that can be assigned
	Status     string   `json:"status"` // ok / degraded / fault, "" = unknown
	StatusMsg  string   `json:"status_msg"`
	// Config is the behaviour config the server sends (spec 01 §4); empty = module defaults.
	Config json.RawMessage `json:"config,omitempty"`
}

// --- events -------------------------------------------------------------------------------

// Event is emitted by the host. Use a type switch.
type Event interface{ isHostEvent() }

// ModulesChanged means Modules() changed (connect, disconnect, adoption, status).
type ModulesChanged struct{}

// CapabilityEvent is a valid event from a module instance. The engine only uses events with
// Adopted set and a Role; the UI shows all of them (e.g. "trigger the sensor to identify it").
type CapabilityEvent struct {
	UID        string
	Instance   string
	Capability string // capability id, e.g. "goal_sensor"
	Role       string
	Adopted    bool
	Name       string    // event name, e.g. "goal"
	At         time.Time // server time of the event (module clock corrected)
	Data       any       // decoded catalog payload, nil if none
	Raw        json.RawMessage
}

// StatusEvent is a status change of a capability instance.
type StatusEvent struct {
	UID, Instance, Capability, Role, Status, Msg string
}

// ConsoleEvent is one line for the live module console (D8). UID is empty before the announce.
type ConsoleEvent struct {
	UID  string
	Port string
	Line session.ConsoleLine
}

func (ModulesChanged) isHostEvent()  {}
func (CapabilityEvent) isHostEvent() {}
func (StatusEvent) isHostEvent()     {}
func (ConsoleEvent) isHostEvent()    {}
