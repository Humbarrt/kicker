package protocol

import "encoding/json"

// Type is the `t` field of a message.
type Type string

// Message types (spec 01 §4).
const (
	// server → module
	THello    Type = "hello"
	TReady    Type = "ready"
	TPing     Type = "ping"
	TCfg      Type = "cfg"
	TCmd      Type = "cmd"
	TSleep    Type = "sleep"
	TWake     Type = "wake"
	TIdentify Type = "identify"
	TReboot   Type = "reboot"
	TUpdate   Type = "update"

	// module → server
	TAnnounce Type = "announce"
	TPong     Type = "pong"
	TEvt      Type = "evt"
	TNack     Type = "nack"
	TStatus   Type = "status"
	TLog      Type = "log"

	// both directions
	TAck Type = "ack"
	TErr Type = "err"
)

// Direction is who sends a message.
type Direction int

const (
	ServerToModule Direction = iota
	ModuleToServer
)

// Message is implemented by every wire message.
// Field rules used by the decoder and the schema generator:
//   - a json tag without omitempty means the key is required
//   - `enum:"a|b"` restricts strings, `min`/`max` restrict integers
//   - `doc` is copied into the schema as description
type Message interface {
	Type() Type
}

// validator is implemented by messages with rules beyond the field tags.
type validator interface {
	validate() *Error
}

// --- server → module -------------------------------------------------------

type Hello struct {
	T    Type  `json:"t"`
	V    int   `json:"v" min:"1" doc:"highest protocol version the server supports"`
	VMin int   `json:"vmin" min:"1" doc:"lowest protocol version the server supports"`
	ST   int64 `json:"st" min:"0" doc:"server time in µs"`
}

type Ready struct {
	T       Type   `json:"t"`
	SID     string `json:"sid" doc:"session id"`
	Adopted bool   `json:"adopted" doc:"unadopted modules are shown in the UI but never affect a game"`
}

type Ping struct {
	T  Type   `json:"t"`
	ID uint32 `json:"id"`
}

type Cfg struct {
	T  Type            `json:"t"`
	ID uint32          `json:"id"`
	I  string          `json:"i,omitempty" doc:"capability instance; missing = whole module"`
	D  json.RawMessage `json:"d" doc:"config keys, merged into the current config (partial update)"`
}

type Cmd struct {
	T  Type            `json:"t"`
	ID uint32          `json:"id"`
	I  string          `json:"i"`
	C  string          `json:"c" doc:"command name"`
	D  json.RawMessage `json:"d,omitempty"`
}

// Simple covers sleep, wake, reboot and update: a type and a command id.
type Simple struct {
	T  Type   `json:"t" enum:"sleep|wake|reboot|update"`
	ID uint32 `json:"id"`
}

type IdentifyArgs struct {
	S int `json:"s" min:"1" max:"60" doc:"seconds to blink / beep"`
}

type Identify struct {
	T  Type         `json:"t"`
	ID uint32       `json:"id"`
	D  IdentifyArgs `json:"d"`
}

// --- module → server -------------------------------------------------------

type Firmware struct {
	Name string `json:"name"`
	Ver  string `json:"ver" doc:"semver"`
}

type CapEntry struct {
	I  string          `json:"i" doc:"instance name, unique within the module"`
	C  string          `json:"c" doc:"capability id, e.g. goal_sensor or vendor.name/thing"`
	CV int             `json:"cv" min:"1" doc:"capability version"`
	P  json.RawMessage `json:"p,omitempty" doc:"static params"`
	W  int             `json:"w,omitempty" min:"0" max:"1" doc:"1 = can wake the table"`
}

type Announce struct {
	T    Type       `json:"t"`
	V    int        `json:"v" min:"1" doc:"protocol version chosen by the module"`
	UID  string     `json:"uid" doc:"stable unique id (flash id, MAC, …)"`
	TS   int64      `json:"ts" min:"0" doc:"module time in µs since boot; tells a reboot (time went back) from a repeated announce"`
	FW   Firmware   `json:"fw"`
	MF   string     `json:"mf" doc:"manifest id vendor/name"`
	HW   string     `json:"hw,omitempty" doc:"board name, shown in the UI"`
	Upd  string     `json:"upd,omitempty" enum:"uf2|esptool|none"`
	Caps []CapEntry `json:"caps"`
}

type Pong struct {
	T  Type   `json:"t"`
	ID uint32 `json:"id"`
	TS int64  `json:"ts" min:"0" doc:"module time in µs since boot"`
}

type Evt struct {
	T  Type            `json:"t"`
	I  string          `json:"i"`
	E  string          `json:"e" doc:"event name"`
	N  uint32          `json:"n" doc:"sequence number, wraps to 0"`
	TS int64           `json:"ts" min:"0" doc:"module time in µs since boot"`
	D  json.RawMessage `json:"d,omitempty"`
}

type Nack struct {
	T    Type     `json:"t"`
	ID   uint32   `json:"id"`
	Code NackCode `json:"code" enum:"unknown_command|bad_args|busy|hw_fault|not_ready"`
	Msg  string   `json:"msg,omitempty"`
}

type Status struct {
	T   Type   `json:"t"`
	I   string `json:"i,omitempty"`
	S   string `json:"s" enum:"ok|degraded|fault"`
	Msg string `json:"msg,omitempty"`
}

type Log struct {
	T Type   `json:"t"`
	L string `json:"l" enum:"d|i|w|e" doc:"level: debug, info, warning, error"`
	M string `json:"m"`
}

// --- both directions -------------------------------------------------------

// Ack acknowledges an event (server → module, N set) or a command (module → server, ID set).
type Ack struct {
	T  Type    `json:"t"`
	ID *uint32 `json:"id,omitempty" doc:"command id (module → server)"`
	N  *uint32 `json:"n,omitempty" doc:"event sequence number (server → module)"`
}

type Err struct {
	T    Type    `json:"t"`
	Code ErrCode `json:"code" enum:"bad_json|too_long|unknown_type|unknown_instance|unknown_event|schema|proto_unsupported|duplicate_uid"`
	Msg  string  `json:"msg"`
	Ref  *uint32 `json:"ref,omitempty"`
}

func (*Hello) Type() Type    { return THello }
func (*Ready) Type() Type    { return TReady }
func (*Ping) Type() Type     { return TPing }
func (*Cfg) Type() Type      { return TCfg }
func (*Cmd) Type() Type      { return TCmd }
func (m *Simple) Type() Type { return m.T }
func (*Identify) Type() Type { return TIdentify }
func (*Announce) Type() Type { return TAnnounce }
func (*Pong) Type() Type     { return TPong }
func (*Evt) Type() Type      { return TEvt }
func (*Nack) Type() Type     { return TNack }
func (*Status) Type() Type   { return TStatus }
func (*Log) Type() Type      { return TLog }
func (*Ack) Type() Type      { return TAck }
func (*Err) Type() Type      { return TErr }

func (a *Ack) validate() *Error {
	if (a.ID == nil) == (a.N == nil) {
		return errf(ErrSchema, "ack needs exactly one of id or n")
	}
	return nil
}

func (a *Announce) validate() *Error {
	if a.UID == "" {
		return errf(ErrSchema, "uid is empty")
	}
	if len(a.Caps) > MaxCapabilities {
		return errf(ErrSchema, "caps: %d entries, at most %d allowed", len(a.Caps), MaxCapabilities)
	}
	seen := make(map[string]bool, len(a.Caps))
	for _, c := range a.Caps {
		if c.I == "" || c.C == "" {
			return errf(ErrSchema, "caps: i and c must not be empty")
		}
		if seen[c.I] {
			return errf(ErrSchema, "caps: instance %q announced twice", c.I)
		}
		seen[c.I] = true
	}
	return nil
}

func (e *Evt) validate() *Error {
	if e.I == "" || e.E == "" {
		return errf(ErrSchema, "evt: i and e must not be empty")
	}
	return nil
}

// messageTypes maps every type to a constructor and the directions it may travel in.
var messageTypes = map[Type]struct {
	new  func() Message
	dirs []Direction
}{
	THello:    {func() Message { return &Hello{} }, []Direction{ServerToModule}},
	TReady:    {func() Message { return &Ready{} }, []Direction{ServerToModule}},
	TPing:     {func() Message { return &Ping{} }, []Direction{ServerToModule}},
	TCfg:      {func() Message { return &Cfg{} }, []Direction{ServerToModule}},
	TCmd:      {func() Message { return &Cmd{} }, []Direction{ServerToModule}},
	TSleep:    {func() Message { return &Simple{} }, []Direction{ServerToModule}},
	TWake:     {func() Message { return &Simple{} }, []Direction{ServerToModule}},
	TReboot:   {func() Message { return &Simple{} }, []Direction{ServerToModule}},
	TUpdate:   {func() Message { return &Simple{} }, []Direction{ServerToModule}},
	TIdentify: {func() Message { return &Identify{} }, []Direction{ServerToModule}},
	TAnnounce: {func() Message { return &Announce{} }, []Direction{ModuleToServer}},
	TPong:     {func() Message { return &Pong{} }, []Direction{ModuleToServer}},
	TEvt:      {func() Message { return &Evt{} }, []Direction{ModuleToServer}},
	TNack:     {func() Message { return &Nack{} }, []Direction{ModuleToServer}},
	TStatus:   {func() Message { return &Status{} }, []Direction{ModuleToServer}},
	TLog:      {func() Message { return &Log{} }, []Direction{ModuleToServer}},
	TAck:      {func() Message { return &Ack{} }, []Direction{ServerToModule, ModuleToServer}},
	TErr:      {func() Message { return &Err{} }, []Direction{ServerToModule, ModuleToServer}},
}
