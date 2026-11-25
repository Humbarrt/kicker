package protocol

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

// Capability describes one entry of the capability catalog (spec 01 §10).
// Payload types are zero values of the structs that `p`, `cfg.d`, `evt.d` and `cmd.d` decode into.
// A nil payload type means the message carries no data.
type Capability struct {
	ID      string
	Version int
	Doc     string
	Roles   []string
	// SharedRoles: several instances may have the same role (display mirrors). Otherwise a role
	// belongs to exactly one instance on the table.
	SharedRoles bool
	Params      any
	Config      any
	Events      map[string]any
	Commands    map[string]any
}

// Key returns the catalog key, e.g. "goal_sensor@1".
func (c *Capability) Key() string { return fmt.Sprintf("%s@%d", c.ID, c.Version) }

// Side is one of the two table sides (D17).
type Side string

const (
	Home Side = "home"
	Away Side = "away"
)

// --- goal_sensor@1 ---------------------------------------------------------

type GoalSensorConfig struct {
	LockoutMS  int `json:"lockout_ms,omitempty" min:"0" doc:"ignore new triggers after a goal (default 1000)"`
	MinBlockUS int `json:"min_block_us,omitempty" min:"0" doc:"shorter interruptions are noise (default 300)"`
}

// --- coin_acceptor@1 -------------------------------------------------------

type CoinAcceptorParams struct {
	Cap int `json:"cap" min:"1" doc:"how many coins it can hold"`
}

type CoinHeld struct {
	UIDs []string `json:"uids" doc:"coins currently inside (state sync)"`
}

type CoinInserted struct {
	UID string `json:"uid" doc:"tag UID as hex string"`
}

type CoinEjected struct {
	By   string   `json:"by" enum:"cmd|button"`
	UIDs []string `json:"uids"`
}

// --- corner_presence@1 -----------------------------------------------------

type CornerPresenceConfig struct {
	ThresholdMM int `json:"threshold_mm,omitempty" min:"0" doc:"default 15"`
	DebounceMS  int `json:"debounce_ms,omitempty" min:"0" doc:"default 500"`
}

// --- ball_dispenser@1 ------------------------------------------------------

type DispenserParams struct {
	Sense bool `json:"sense" doc:"has an exit sensor that sees a ball leave"`
}

type DispenserRelease struct {
	Side  Side `json:"side" enum:"home|away"`
	Count int  `json:"count,omitempty" min:"1" doc:"default 1"`
}

type DispenserReleaseAll struct {
	Side Side `json:"side,omitempty" enum:"home|away" doc:"missing = alternate sides"`
}

type DispenserReleased struct {
	Side  Side `json:"side" enum:"home|away"`
	Count int  `json:"count" min:"1"`
}

type DispenserEmpty struct {
	Side Side `json:"side" enum:"home|away"`
}

// --- display@1 -------------------------------------------------------------

type DisplayParams struct {
	W     int      `json:"w" min:"1"`
	H     int      `json:"h" min:"1"`
	Shape string   `json:"shape" enum:"rect|round"`
	Img   int      `json:"img" min:"0" doc:"picture size in px, 0 = no pictures"`
	Anims []string `json:"anims" doc:"supported animation names"`
}

type TeamInfo struct {
	Name string `json:"name"`
	Col  string `json:"col" doc:"#RRGGBB"`
}

type DisplayConfig struct {
	Team *TeamInfo         `json:"team,omitempty" doc:"this side's display name and color"`
	Opp  *TeamInfo         `json:"opp,omitempty" doc:"the other side's display name and color"`
	Lang string            `json:"lang,omitempty"`
	Txt  map[string]string `json:"txt,omitempty" doc:"translated texts, key → string"`
}

type DisplayPlayer struct {
	N   string  `json:"n" doc:"name"`
	Col string  `json:"col" doc:"#RRGGBB"`
	Img *string `json:"img" doc:"picture hash or null"`
}

type DisplayClock struct {
	LeftMS int64 `json:"left_ms" min:"0"`
	Run    bool  `json:"run"`
}

type DisplayState struct {
	Ph  string          `json:"ph" enum:"idle|pregame|playing|paused|finished"`
	S   [2]int          `json:"s" doc:"[own score, opponent score]"`
	Pl  []DisplayPlayer `json:"pl"`
	Clk *DisplayClock   `json:"clk,omitempty"`
	Msg string          `json:"msg,omitempty" doc:"text key from cfg txt"`
}

type DisplayAnim struct {
	Name string `json:"name"`
}

type DisplayBrightness struct {
	V int `json:"v" min:"0" max:"100"`
}

type DisplayImg struct {
	H    string `json:"h" doc:"picture hash"`
	O    int    `json:"o" min:"0" doc:"byte offset"`
	Data string `json:"data" doc:"base64, up to 600 bytes"`
}

type DisplayImgEnd struct {
	H     string `json:"h"`
	CRC32 uint32 `json:"crc32"`
}

type DisplayImgMissing struct {
	H string `json:"h"`
}

// DisplayAnimations are the standard animation names (spec 01 §10).
var DisplayAnimations = []string{
	"goal_for", "goal_against", "golden_goal", "win", "lose", "draw",
	"shutout_win", "shutout_lose", "kickoff", "wake", "sleep",
}

// DisplayTextKeys are the standard text keys with their English defaults (spec 01 §10).
var DisplayTextKeys = map[string]string{
	"paused":       "Paused",
	"golden_goal":  "Golden goal",
	"waiting":      "Waiting for players",
	"kickoff_in":   "Kickoff in",
	"return_ball":  "Put the ball in a goal",
	"unknown_coin": "Unknown coin",
	"winner":       "Winner",
	"draw":         "Draw",
	"no_server":    "No server",
	"sleeping":     "Sleeping",
}

// --- the catalog -----------------------------------------------------------

// Catalog lists all standard capabilities.
var Catalog = []*Capability{
	{
		ID: "goal_sensor", Version: 1,
		Doc:    "Detects a ball that went into one goal.",
		Roles:  []string{"home_goal", "away_goal"},
		Config: GoalSensorConfig{},
		Events: map[string]any{"goal": nil},
	},
	{
		ID: "coin_acceptor", Version: 1,
		Doc:      "Holds the NFC coins of one side. Ejects all coins at once.",
		Roles:    []string{"home", "away"},
		Params:   CoinAcceptorParams{},
		Events:   map[string]any{"held": CoinHeld{}, "inserted": CoinInserted{}, "ejected": CoinEjected{}},
		Commands: map[string]any{"eject": nil},
	},
	{
		ID: "corner_presence", Version: 1,
		Doc:    "Detects whether a phone is lying on one corner.",
		Roles:  []string{"home_1", "home_2", "away_1", "away_2"},
		Config: CornerPresenceConfig{},
		Events: map[string]any{"present": nil, "absent": nil},
	},
	{
		ID: "ball_dispenser", Version: 1,
		Doc:      "Releases balls to either side on command.",
		Params:   DispenserParams{},
		Events:   map[string]any{"released": DispenserReleased{}, "empty": DispenserEmpty{}},
		Commands: map[string]any{"release": DispenserRelease{}, "release_all": DispenserReleaseAll{}, "home": nil},
	},
	{
		ID: "display", Version: 1,
		Doc:         "One small screen showing one side's view.",
		Roles:       []string{"home", "away"},
		SharedRoles: true,
		Params:      DisplayParams{},
		Config:      DisplayConfig{},
		Events:      map[string]any{"img_missing": DisplayImgMissing{}},
		Commands: map[string]any{
			"state": DisplayState{}, "anim": DisplayAnim{}, "brightness": DisplayBrightness{},
			"img": DisplayImg{}, "img_end": DisplayImgEnd{},
		},
	},
}

// Lookup returns a catalog capability by id and version.
func Lookup(id string, version int) (*Capability, bool) {
	for _, c := range Catalog {
		if c.ID == id && c.Version == version {
			return c, true
		}
	}
	return nil, false
}

// DecodeEvent decodes the data of event name. Unknown names give ErrUnknownEvent.
func (c *Capability) DecodeEvent(name string, d json.RawMessage) (any, []string, error) {
	typ, ok := c.Events[name]
	if !ok {
		return nil, nil, errf(ErrUnknownEvent, "%s has no event %q", c.Key(), name)
	}
	return decodeInto(typ, d)
}

// DecodeCommand decodes the arguments of command name (used by virtual modules and tests).
func (c *Capability) DecodeCommand(name string, d json.RawMessage) (any, []string, error) {
	typ, ok := c.Commands[name]
	if !ok {
		return nil, nil, &Error{Code: ErrSchema, Msg: fmt.Sprintf("%s has no command %q", c.Key(), name)}
	}
	return decodeInto(typ, d)
}

// DecodeParams decodes the static params from an announce entry.
func (c *Capability) DecodeParams(p json.RawMessage) (any, []string, error) {
	return decodeInto(c.Params, p)
}

// DecodeConfig decodes a (partial) config.
func (c *Capability) DecodeConfig(d json.RawMessage) (any, []string, error) {
	return decodeInto(c.Config, d)
}

// EventNames returns the event names in sorted order.
func (c *Capability) EventNames() []string { return sortedKeys(c.Events) }

// CommandNames returns the command names in sorted order.
func (c *Capability) CommandNames() []string { return sortedKeys(c.Commands) }

func decodeInto(typ any, d json.RawMessage) (any, []string, error) {
	if typ == nil {
		var warnings []string
		if len(d) > 0 && string(d) != "null" && string(d) != "{}" {
			warnings = append(warnings, "unexpected data, this message carries none")
		}
		return nil, warnings, nil
	}
	ptr := reflect.New(reflect.TypeOf(typ))
	warnings, err := DecodePayload(d, ptr.Interface())
	if err != nil {
		return nil, warnings, err
	}
	return ptr.Interface(), warnings, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
