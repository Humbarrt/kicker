package table

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Humbarrt/kicker/server/internal/engine"
)

// SettingKey is where the table settings are stored (one JSON object in settings).
const SettingKey = "table_settings"

// Settings are the table settings of spec 02 §13 and D17. The current preset is stored on its
// own (store.SettingCurrentPreset) because "Start" with a preset changes it too.
type Settings struct {
	Home SideSettings `json:"home"`
	Away SideSettings `json:"away"`

	KickoffRequires string `json:"kickoff_requires"` // both_sides | any_player
	KickoffDelayS   int    `json:"kickoff_delay_s"`
	FirstBall       string `json:"first_ball"`      // random | lower_rating | loser_previous | alternate | home | away
	AfterGoalBall   string `json:"after_goal_ball"` // conceding | scoring | alternate
	FinishedHoldS   int    `json:"finished_hold_s"`

	// Used from M3 task 5 on (pause, sleep, wake) and M4 (corners).
	PauseAfterMinutes   int    `json:"pause_after_minutes"`
	SleepAfterMinutes   int    `json:"sleep_after_minutes"`
	OnWake              string `json:"on_wake"` // smart | resume | new_game
	AbandonAfterHours   int    `json:"abandon_after_hours"`
	CornerClaimTimeoutS int    `json:"corner_claim_timeout_s"`
}

// SideSettings are a side's display name and color (D17). An empty name = the translated
// default ("Home" / "Away").
type SideSettings struct {
	Name  string `json:"name"`
	Color string `json:"color"` // #rrggbb
}

// Default side colors, the same as the web UI's built-in theme.
const (
	DefaultHomeColor = "#1f6fd1"
	DefaultAwayColor = "#e2572b"
)

// DefaultSettings are the defaults of spec 02 §13.
func DefaultSettings() Settings {
	return Settings{
		Home: SideSettings{Color: DefaultHomeColor}, Away: SideSettings{Color: DefaultAwayColor},
		KickoffRequires: string(engine.KickoffBothSides), KickoffDelayS: 5, FirstBall: string(engine.FirstBallRandom),
		AfterGoalBall: string(engine.BallConceding), FinishedHoldS: 10,
		PauseAfterMinutes: 5, SleepAfterMinutes: 3, OnWake: "smart", AbandonAfterHours: 24, CornerClaimTimeoutS: 30,
	}
}

// SettingsError names the setting that is not valid.
type SettingsError struct{ Field string }

func (e *SettingsError) Error() string { return "invalid setting: " + e.Field }

// ErrInvalidSettings matches every SettingsError.
var ErrInvalidSettings = errors.New("invalid settings")

func (e *SettingsError) Is(target error) bool { return target == ErrInvalidSettings }

// Normalize trims the names and lower-cases the colors.
func (s *Settings) Normalize() {
	for _, side := range []*SideSettings{&s.Home, &s.Away} {
		side.Name = strings.TrimSpace(side.Name)
		side.Color = strings.ToLower(strings.TrimSpace(side.Color))
	}
}

// Validate checks every setting.
func (s Settings) Validate() error {
	bad := func(f string) error { return &SettingsError{Field: f} }
	for name, side := range map[string]SideSettings{"home": s.Home, "away": s.Away} {
		if utf8.RuneCountInString(side.Name) > 24 || strings.ContainsAny(side.Name, "\n\r\t") {
			return bad(name + ".name")
		}
		if !validColor(side.Color) {
			return bad(name + ".color")
		}
	}
	if s.Home.Name != "" && strings.EqualFold(s.Home.Name, s.Away.Name) {
		return bad("away.name")
	}
	in := func(v string, ok ...string) bool {
		for _, o := range ok {
			if v == o {
				return true
			}
		}
		return false
	}
	switch {
	case !in(s.KickoffRequires, "both_sides", "any_player"):
		return bad("kickoff_requires")
	case s.KickoffDelayS < 0 || s.KickoffDelayS > 60:
		return bad("kickoff_delay_s")
	case !in(s.FirstBall, "random", "lower_rating", "loser_previous", "alternate", "home", "away"):
		return bad("first_ball")
	case !in(s.AfterGoalBall, "conceding", "scoring", "alternate"):
		return bad("after_goal_ball")
	case s.FinishedHoldS < 3 || s.FinishedHoldS > 120:
		return bad("finished_hold_s")
	case s.PauseAfterMinutes < 1 || s.PauseAfterMinutes > 60:
		return bad("pause_after_minutes")
	case s.SleepAfterMinutes < 1 || s.SleepAfterMinutes > 60:
		return bad("sleep_after_minutes")
	case !in(s.OnWake, "smart", "resume", "new_game"):
		return bad("on_wake")
	case s.AbandonAfterHours < 1 || s.AbandonAfterHours > 720:
		return bad("abandon_after_hours")
	case s.CornerClaimTimeoutS < 5 || s.CornerClaimTimeoutS > 300:
		return bad("corner_claim_timeout_s")
	}
	return nil
}

func validColor(c string) bool {
	if len(c) != 7 || c[0] != '#' {
		return false
	}
	for _, r := range c[1:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// apply puts the settings the engine uses into a config.
func (s Settings) apply(c engine.Config) engine.Config {
	c.KickoffRequires = engine.KickoffRequires(s.KickoffRequires)
	c.KickoffDelay = time.Duration(s.KickoffDelayS) * time.Second
	c.FirstBall = engine.FirstBallRule(s.FirstBall)
	c.AfterGoalBall = engine.BallRule(s.AfterGoalBall)
	c.FinishedHold = time.Duration(s.FinishedHoldS) * time.Second
	c.PauseAfter = time.Duration(s.PauseAfterMinutes) * time.Minute
	c.SleepAfter = time.Duration(s.SleepAfterMinutes) * time.Minute
	c.OnWake = engine.OnWake(s.OnWake)
	return c
}

func (s Settings) String() string {
	return fmt.Sprintf("home=%q %s away=%q %s kickoff=%s/%ds first_ball=%s after_goal=%s hold=%ds pause=%dm sleep=%dm wake=%s abandon=%dh",
		s.Home.Name, s.Home.Color, s.Away.Name, s.Away.Color, s.KickoffRequires, s.KickoffDelayS, s.FirstBall,
		s.AfterGoalBall, s.FinishedHoldS, s.PauseAfterMinutes, s.SleepAfterMinutes, s.OnWake, s.AbandonAfterHours)
}
