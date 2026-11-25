package auth

import (
	"errors"
	"fmt"
)

// Kind is how a permission's value is read (spec 03 §4, §14).
type Kind string

const (
	KindOnOff Kind = "on_off" // 0 off, 1 on
	KindScope Kind = "scope"  // 0 off, 1 own, 2 all
	KindDays  Kind = "days"   // 0 off, 1–365 days, -1 unlimited
)

// Scope values.
const (
	ScopeOff = 0
	ScopeOwn = 1
	ScopeAll = 2
)

// Unlimited is the days value for "no limit".
const Unlimited = -1

// Permission keys.
const (
	ViewLive        = "view.live"
	ViewStats       = "view.stats"
	ViewTournaments = "view.tournaments"

	TableJoin    = "table.join"
	TableScore   = "table.score"
	TableControl = "table.control"
	TablePlayers = "table.players"
	TableRules   = "table.rules"
	TablePreset  = "table.preset"
	TableBalls   = "table.balls"

	PlayersCreate = "players.create"
	ProfileOwn    = "profile.own"
	ProfileAny    = "profile.any"
	CoinsManage   = "coins.manage"

	GamesEdit     = "games.edit"
	GamesEditDays = "games.edit_days"
	GamesRevert   = "games.revert"
	GamesVoid     = "games.void"

	TournamentsCreate = "tournaments.create"
	TournamentsRun    = "tournaments.run"
	TournamentsEdit   = "tournaments.edit"
	TournamentsManage = "tournaments.manage"

	PresetsManage = "presets.manage"

	LogView = "log.view"
	LogIP   = "log.ip"
)

// Permission describes one grantable permission.
type Permission struct {
	Key           string `json:"key"`
	Area          string `json:"area"` // viewing | table | players | games | tournaments | presets | log
	Kind          Kind   `json:"kind"`
	LoginRequired bool   `json:"login_required"` // can't be granted to Anybody
	Member        int    `json:"default_member"`
	Anybody       int    `json:"default_anybody"`
}

// Catalog is the list of permissions in display order. Adding one here is all it takes: the
// built-in groups get the defaults at the next start, own groups get it switched off.
var Catalog = []Permission{
	{Key: ViewLive, Area: "viewing", Kind: KindOnOff, Member: 1, Anybody: 1},
	{Key: ViewStats, Area: "viewing", Kind: KindOnOff, Member: 1, Anybody: 1},
	{Key: ViewTournaments, Area: "viewing", Kind: KindOnOff, Member: 1, Anybody: 1},

	{Key: TableJoin, Area: "table", Kind: KindOnOff, LoginRequired: true, Member: 1},
	{Key: TableScore, Area: "table", Kind: KindOnOff, Member: 1},
	{Key: TableControl, Area: "table", Kind: KindOnOff, Member: 1},
	{Key: TablePlayers, Area: "table", Kind: KindOnOff, Member: 1},
	{Key: TableRules, Area: "table", Kind: KindOnOff, Member: 1},
	{Key: TablePreset, Area: "table", Kind: KindOnOff, Member: 1},
	{Key: TableBalls, Area: "table", Kind: KindOnOff, Member: 1},

	{Key: PlayersCreate, Area: "players", Kind: KindOnOff, LoginRequired: true, Member: 1},
	{Key: ProfileOwn, Area: "players", Kind: KindOnOff, LoginRequired: true, Member: 1},
	{Key: ProfileAny, Area: "players", Kind: KindOnOff, LoginRequired: true},
	{Key: CoinsManage, Area: "players", Kind: KindOnOff, LoginRequired: true, Member: 1},

	{Key: GamesEdit, Area: "games", Kind: KindScope, LoginRequired: true, Member: ScopeOwn},
	{Key: GamesEditDays, Area: "games", Kind: KindDays, LoginRequired: true, Member: 7},
	{Key: GamesRevert, Area: "games", Kind: KindOnOff, LoginRequired: true},
	{Key: GamesVoid, Area: "games", Kind: KindOnOff, LoginRequired: true},

	{Key: TournamentsCreate, Area: "tournaments", Kind: KindOnOff, LoginRequired: true, Member: 1},
	{Key: TournamentsRun, Area: "tournaments", Kind: KindScope, LoginRequired: true, Member: ScopeOwn},
	{Key: TournamentsEdit, Area: "tournaments", Kind: KindScope, LoginRequired: true, Member: ScopeOwn},
	{Key: TournamentsManage, Area: "tournaments", Kind: KindOnOff, LoginRequired: true},

	{Key: PresetsManage, Area: "presets", Kind: KindOnOff, LoginRequired: true},

	{Key: LogView, Area: "log", Kind: KindOnOff, LoginRequired: true, Member: 1},
	{Key: LogIP, Area: "log", Kind: KindOnOff, LoginRequired: true},
}

var catalogByKey = func() map[string]Permission {
	m := make(map[string]Permission, len(Catalog))
	for _, p := range Catalog {
		m[p.Key] = p
	}
	return m
}()

// Lookup returns a permission by key.
func Lookup(key string) (Permission, bool) {
	p, ok := catalogByKey[key]
	return p, ok
}

// Built-in group kinds.
const (
	GroupAdmin   = "admin"
	GroupMember  = "member"
	GroupAnybody = "anybody"
)

// Errors for permission changes.
var (
	ErrUnknownPermission = errors.New("unknown permission")
	ErrInvalidValue      = errors.New("invalid permission value")
	ErrLoginRequired     = errors.New("this permission needs a login and can't be given to Anybody")
	ErrAdminFixed        = errors.New("the Admin group always has every permission")
)

// ValidateValue checks a value for a permission in a group of the given built-in kind.
func ValidateValue(builtin, key string, v int) error {
	p, ok := Lookup(key)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownPermission, key)
	}
	if builtin == GroupAdmin {
		return ErrAdminFixed
	}
	switch p.Kind {
	case KindOnOff:
		if v != 0 && v != 1 {
			return fmt.Errorf("%w: %s must be 0 or 1", ErrInvalidValue, key)
		}
	case KindScope:
		if v < ScopeOff || v > ScopeAll {
			return fmt.Errorf("%w: %s must be 0 (off), 1 (own) or 2 (all)", ErrInvalidValue, key)
		}
	case KindDays:
		if v != Unlimited && (v < 0 || v > 365) {
			return fmt.Errorf("%w: %s must be 0–365 days or -1 (unlimited)", ErrInvalidValue, key)
		}
	}
	if builtin == GroupAnybody && p.LoginRequired && v != 0 {
		return fmt.Errorf("%w: %s", ErrLoginRequired, key)
	}
	return nil
}

// Defaults returns the default values for a built-in group ("" = own group: all off).
func Defaults(builtin string) map[string]int {
	m := make(map[string]int, len(Catalog))
	for _, p := range Catalog {
		switch builtin {
		case GroupMember:
			m[p.Key] = p.Member
		case GroupAnybody:
			m[p.Key] = p.Anybody
		default:
			m[p.Key] = 0
		}
	}
	return m
}

// Perms are the effective permissions of a request.
type Perms struct {
	Admin  bool           // the Admin group: everything, including the system permissions
	Values map[string]int // effective value per permission
}

// Value returns a permission's effective value (an admin gets the maximum).
func (p Perms) Value(key string) int {
	if p.Admin {
		def, ok := Lookup(key)
		if !ok {
			return 0
		}
		switch def.Kind {
		case KindScope:
			return ScopeAll
		case KindDays:
			return Unlimited
		}
		return 1
	}
	return p.Values[key]
}

// Has reports whether a permission is granted at all (on, any scope, any number of days).
func (p Perms) Has(key string) bool { return p.Value(key) != 0 }

// Map returns every permission's effective value, for the UI.
func (p Perms) Map() map[string]int {
	m := make(map[string]int, len(Catalog))
	for _, c := range Catalog {
		m[c.Key] = p.Value(c.Key)
	}
	return m
}

// merge combines a group's values with Anybody's: the more generous value wins.
func merge(kind Kind, a, b int) int {
	if kind == KindDays && (a == Unlimited || b == Unlimited) {
		return Unlimited
	}
	return max(a, b)
}

// effective computes a group's effective permissions on top of Anybody's.
func effective(group, anybody map[string]int) map[string]int {
	out := make(map[string]int, len(Catalog))
	for _, c := range Catalog {
		out[c.Key] = merge(c.Kind, group[c.Key], anybody[c.Key])
	}
	return out
}
