package table

import (
	"time"

	"github.com/Humbarrt/kicker/server/internal/engine"
)

// View is the table as the web UI and the app see it: GET /api/table and the "table" SSE event.
// Clients run the game clock themselves from play_time_ms at server_time while clock_running.
type View struct {
	Rev        uint64    `json:"rev"`         // increases with every change
	Phase      string    `json:"phase"`       // sleeping | idle | pregame | playing | finished
	ServerTime int64     `json:"server_time"` // Unix ms
	Game       *GameView `json:"game"`        // current game, or the last one while idle; null = none yet
	// Coins inside each side's acceptor ("home", "away"); player_id null = unknown coin.
	Coins map[string][]CoinView `json:"coins"`
	// Registrations per side ("home", "away"), by slot (spec 02 §4).
	Registrations map[string][]RegView `json:"registrations"`
	// Sides are the display names and colors (D17); name "" = the translated default.
	Sides map[string]SideSettings `json:"sides"`
	// KickoffAt is the end of the kickoff countdown (Unix ms), in pregame only.
	KickoffAt *int64 `json:"kickoff_at"`
	// Next are the rules the next game starts with; null = no preset exists.
	Next *NextView `json:"next"`
	// Paused is the paused games list (D16), newest pause first.
	Paused []PausedGame `json:"paused"`
	// AbandonAfterHours: paused games are given up after this long (D16).
	AbandonAfterHours int `json:"abandon_after_hours"`
}

// NextView are the rules of the next game.
type NextView struct {
	PresetID *int64    `json:"preset_id"`
	Rules    RulesView `json:"rules"`
}

// RegView is a player – or an unknown coin (player_id null) – registered in a slot.
type RegView struct {
	Slot     int          `json:"slot"`
	PlayerID *int64       `json:"player_id"`
	CoinUID  *string      `json:"coin_uid"`
	Stale    bool         `json:"stale"` // the player has left; replaced by the next one, gone at kickoff
	Sources  []SourceView `json:"sources"`
}

// SourceView is why a registration exists.
type SourceView struct {
	Via   string `json:"via"` // coin | corner | manual
	Ref   string `json:"ref,omitempty"`
	Stale bool   `json:"stale"`
}

// SeatView is a slot of a game's roster.
type SeatView struct {
	Slot     int     `json:"slot"`
	PlayerID *int64  `json:"player_id"` // null = an unknown coin (counts as guest)
	CoinUID  *string `json:"coin_uid"`
	Via      string  `json:"via"`
}

// CoinView is a coin inside an acceptor.
type CoinView struct {
	UID      string `json:"uid"`
	PlayerID *int64 `json:"player_id"`
}

// RulesView are the win conditions of a game.
type RulesView struct {
	WinScore   int    `json:"win_score"`
	WinBy      int    `json:"win_by"`
	MaxScore   *int   `json:"max_score"`
	TimeLimitS *int   `json:"time_limit_s"`
	TimeTie    string `json:"time_tie"`
}

// GameView is one game.
type GameView struct {
	ID           int64      `json:"id"`
	Current      bool       `json:"current"` // false = the last game, shown while idle
	PresetID     *int64     `json:"preset_id"`
	Rules        RulesView  `json:"rules"`
	Status       string     `json:"status"` // active | paused | finished
	Home         int        `json:"home"`
	Away         int        `json:"away"`
	Target       int        `json:"target"` // score needed to win right now (D10)
	Winner       *string    `json:"winner"` // home | away; null while running or for a draw
	EndReason    string     `json:"end_reason,omitempty"`
	StartedAt    int64      `json:"started_at"`
	EndedAt      *int64     `json:"ended_at"`
	PlayTimeMs   int64      `json:"play_time_ms"` // at server_time
	ClockRunning bool       `json:"clock_running"`
	ClockExpired bool       `json:"clock_expired"`
	GoldenGoal   bool       `json:"golden_goal"`
	NextBall     *string    `json:"next_ball"`
	FirstBall    *string    `json:"first_ball"`
	Goals        []GoalView `json:"goals"`
	// Roster per side ("home", "away"): who plays this game.
	Roster map[string][]SeatView `json:"roster"`
	// PairingID: a tournament game's pairing (spec 09); PairingASide: where team A plays.
	PairingID    int64  `json:"pairing_id,omitempty"`
	PairingASide string `json:"pairing_a_side,omitempty"`
	// Voided: the game counts for nothing. Edits: corrections after the game in force.
	Voided bool `json:"voided"`
	Edits  int  `json:"edits"`
}

// GoalView is one goal, voided or not.
type GoalView struct {
	Seq        int    `json:"seq"`
	Side       string `json:"side"` // scoring side
	Source     string `json:"source"`
	At         int64  `json:"at"`
	PlayTimeMs int64  `json:"play_time_ms"`
	Voided     bool   `json:"voided"`
}

func viewOf(s engine.State, rev uint64, now time.Time, set Settings) View {
	v := View{Rev: rev, Phase: string(s.Phase), ServerTime: now.UnixMilli(), Coins: map[string][]CoinView{"home": {}, "away": {}},
		Registrations: map[string][]RegView{"home": {}, "away": {}},
		Sides:         map[string]SideSettings{"home": set.Home, "away": set.Away}, AbandonAfterHours: set.AbandonAfterHours}
	if s.Phase == engine.PhasePregame && !s.KickoffAt.IsZero() {
		ms := s.KickoffAt.UnixMilli()
		v.KickoffAt = &ms
	}
	if s.Next.Rules.Validate() == nil {
		v.Next = &NextView{Rules: rulesView(s.Next.Rules)}
		if s.Next.PresetID != 0 {
			id := s.Next.PresetID
			v.Next.PresetID = &id
		}
	}
	for _, r := range s.Regs {
		rv := RegView{Slot: r.Slot, CoinUID: strOrNil(r.CoinUID), Stale: r.Stale(), Sources: make([]SourceView, len(r.Sources))}
		if r.PlayerID != 0 {
			rv.PlayerID = &r.PlayerID
		}
		for i, src := range r.Sources {
			rv.Sources[i] = SourceView{Via: string(src.Via), Ref: src.Ref, Stale: src.Stale}
		}
		v.Registrations[string(r.Side)] = append(v.Registrations[string(r.Side)], rv)
	}
	for side, coins := range s.Acceptors {
		for _, c := range coins {
			cv := CoinView{UID: c.UID}
			if c.PlayerID != 0 {
				id := c.PlayerID
				cv.PlayerID = &id
			}
			v.Coins[string(side)] = append(v.Coins[string(side)], cv)
		}
	}
	switch {
	case s.Game != nil:
		v.Game = gameView(s.Game, true, now)
	case s.Last != nil:
		v.Game = gameView(s.Last, false, now)
	}
	v.Paused = []PausedGame{}
	return v
}

func gameView(g *engine.Game, current bool, now time.Time) *GameView {
	gv := &GameView{
		ID: g.ID, Current: current, Status: string(g.Status), Home: g.Home, Away: g.Away,
		Rules: rulesView(g.Rules), FirstBall: strOrNil(g.FirstBall),
		Target: g.Rules.ScoreTarget(g.Home, g.Away), Winner: strOrNil(g.Winner), EndReason: g.EndReason,
		StartedAt: g.StartedAt.UnixMilli(), PlayTimeMs: g.PlayTime(now).Milliseconds(), ClockRunning: g.ClockRunning(),
		ClockExpired: g.ClockExpired, GoldenGoal: g.GoldenGoal, NextBall: strOrNil(g.NextBallSide),
		PairingID: pairingID(g), PairingASide: pairingASide(g),
		Goals: make([]GoalView, len(g.Goals)), Voided: g.Voided, Edits: g.Edits,
	}
	if g.PresetID != 0 {
		gv.PresetID = &g.PresetID
	}
	if !g.EndedAt.IsZero() {
		ms := g.EndedAt.UnixMilli()
		gv.EndedAt = &ms
	}
	gv.Roster = map[string][]SeatView{"home": {}, "away": {}}
	for _, side := range []engine.Side{engine.Home, engine.Away} {
		for i, st := range g.Roster.Side(side) {
			if !st.Taken() {
				continue
			}
			sv := SeatView{Slot: i + 1, CoinUID: strOrNil(st.CoinUID), Via: string(st.Via)}
			if st.PlayerID != 0 {
				id := st.PlayerID
				sv.PlayerID = &id
			}
			gv.Roster[string(side)] = append(gv.Roster[string(side)], sv)
		}
	}
	for i, gl := range g.Goals {
		gv.Goals[i] = GoalView{Seq: gl.Seq, Side: string(gl.Side), Source: string(gl.Source),
			At: gl.At.UnixMilli(), PlayTimeMs: gl.PlayTime.Milliseconds(), Voided: gl.Voided}
	}
	return gv
}

func rulesView(rules engine.Rules) RulesView {
	r := rulesToStore(rules)
	return RulesView{WinScore: r.WinScore, WinBy: r.WinBy, MaxScore: r.MaxScore, TimeLimitS: r.TimeLimitS, TimeTie: r.TimeTie}
}

func pairingID(g *engine.Game) int64 {
	if g.Pairing == nil {
		return 0
	}
	return g.Pairing.ID
}

func pairingASide(g *engine.Game) string {
	if g.Pairing == nil {
		return ""
	}
	return string(g.Pairing.ASide)
}
