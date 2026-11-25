package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/table"
)

// Table is the part of the table actor the API uses.
type Table interface {
	View() table.View
	Do(ctx context.Context, in engine.Input) error
	Start(ctx context.Context, req table.StartRequest) error
	Register(ctx context.Context, side engine.Side, playerID, author int64) error
	Settings() table.Settings
	SetSettings(ctx context.Context, s table.Settings, presetID int64) error
	Game(ctx context.Context, id int64) (*engine.Game, error)
	ResumeGame(ctx context.Context, id, author int64) error
	ReinstateGame(ctx context.Context, id, author int64) error
	AbandonedGames(ctx context.Context, limit int) ([]table.GameView, error)
	GameDetail(ctx context.Context, id int64) (table.GameDetail, error)
	EditGame(ctx context.Context, id int64, ed engine.Edit) (*engine.Game, error)
	CurrentPreset(ctx context.Context) (store.Preset, error)
}

// Presets lists the rule presets.
type Presets interface {
	Presets(ctx context.Context) ([]store.Preset, error)
}

// PresetView is one rule preset.
type PresetView struct {
	ID      int64           `json:"id"`
	Label   string          `json:"label"`
	Rules   table.RulesView `json:"rules"`
	Builtin bool            `json:"builtin"`
}

// APIError is the body of every error response. Code is stable and meant for translations.
type APIError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func fail(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, APIError{Error: msg, Code: code})
}

// engineError maps table/engine errors to HTTP.
func engineError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, engine.ErrNoGame):
		fail(c, http.StatusConflict, "no_game", err.Error())
	case errors.Is(err, engine.ErrNothingToUndo):
		fail(c, http.StatusConflict, "nothing_to_undo", err.Error())
	case errors.Is(err, engine.ErrInvalidRules):
		fail(c, http.StatusBadRequest, "invalid_rules", err.Error())
	case errors.Is(err, engine.ErrSideFull):
		fail(c, http.StatusConflict, "side_full", err.Error())
	case errors.Is(err, engine.ErrNotRegistered):
		fail(c, http.StatusNotFound, "not_registered", err.Error())
	case errors.Is(err, table.ErrInvalidPlayer):
		fail(c, http.StatusBadRequest, "invalid_player", err.Error())
	case errors.Is(err, engine.ErrNotPaused):
		fail(c, http.StatusConflict, "not_paused", err.Error())
	case errors.Is(err, table.ErrNoSuchGame):
		fail(c, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, engine.ErrTournament):
		fail(c, http.StatusConflict, "tournament", err.Error())
	case errors.Is(err, engine.ErrInvalidInput):
		fail(c, http.StatusBadRequest, "invalid_input", err.Error())
	case errors.Is(err, table.ErrNotRunning), errors.Is(err, context.DeadlineExceeded):
		fail(c, http.StatusServiceUnavailable, "unavailable", err.Error())
	default:
		fail(c, http.StatusInternalServerError, "internal", "internal error")
	}
}

// RulesInput are custom rules in a start request.
type RulesInput struct {
	WinScore   int    `json:"win_score"`
	WinBy      int    `json:"win_by"`
	MaxScore   *int   `json:"max_score"`
	TimeLimitS *int   `json:"time_limit_s"`
	TimeTie    string `json:"time_tie"`
}

// StartInput is the body of POST /api/table/start. Both empty = the current preset.
type StartInput struct {
	PresetID int64       `json:"preset_id"`
	Rules    *RulesInput `json:"rules"`
}

// RegisterInput is the body of POST /api/table/registrations.
type RegisterInput struct {
	Side     string `json:"side"`
	PlayerID int64  `json:"player_id"`
}

// ScoreInput is the body of POST /api/table/score.
type ScoreInput struct {
	Side  string `json:"side"`
	Delta int    `json:"delta"`
}

func (o Options) tableRoutes(api *gin.RouterGroup) {
	t := o.Table
	// commandCtx bounds how long an HTTP request waits for the table.
	commandCtx := func(c *gin.Context) (context.Context, context.CancelFunc) {
		return context.WithTimeout(c.Request.Context(), 5*time.Second)
	}
	done := func(c *gin.Context, err error) {
		if err != nil {
			engineError(c, err)
			return
		}
		c.JSON(http.StatusOK, t.View())
	}

	author := func(c *gin.Context) int64 {
		if a := account(c); a != nil {
			return a.ID
		}
		return 0 // Anybody may have table permissions; then the events have no author
	}
	api.GET("/table", o.Require(auth.ViewLive), func(c *gin.Context) { c.JSON(http.StatusOK, t.View()) })

	// Live table actions (spec 03 §4); the account is the author of the events.
	cmd := api.Group("/table")
	cmd.POST("/start", o.Require(auth.TableControl), func(c *gin.Context) {
		var in StartInput
		if c.Request.ContentLength != 0 {
			if err := c.ShouldBindJSON(&in); err != nil {
				fail(c, http.StatusBadRequest, "invalid_input", err.Error())
				return
			}
		}
		req := table.StartRequest{PresetID: in.PresetID, Author: author(c)}
		// A preset other than the current one also needs table.preset; custom rules table.rules.
		if in.PresetID != 0 && !o.perms(c).Has(auth.TablePreset) {
			if cur, err := t.CurrentPreset(c.Request.Context()); err != nil || cur.ID != in.PresetID {
				deny(c)
				return
			}
		}
		if in.Rules != nil && !o.perms(c).Has(auth.TableRules) {
			deny(c)
			return
		}
		if r := in.Rules; r != nil {
			rules := engine.Rules{WinScore: r.WinScore, WinBy: r.WinBy, TimeTie: engine.TimeTie(r.TimeTie)}
			if r.MaxScore != nil {
				rules.MaxScore = *r.MaxScore
			}
			if r.TimeLimitS != nil {
				rules.TimeLimit = time.Duration(*r.TimeLimitS) * time.Second
			}
			req.Rules = &rules
		}
		ctx, cancel := commandCtx(c)
		defer cancel()
		done(c, t.Start(ctx, req))
	})
	cmd.POST("/score", o.Require(auth.TableScore), func(c *gin.Context) {
		var in ScoreInput
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}
		ctx, cancel := commandCtx(c)
		defer cancel()
		done(c, t.Do(ctx, engine.AdjustScore{Side: engine.Side(in.Side), Delta: in.Delta, Author: author(c)}))
	})
	// Pause, resume, wake (spec 02 §7.4, §9).
	simple := func(path string, in func(author int64) engine.Input) {
		cmd.POST(path, o.Require(auth.TableControl), func(c *gin.Context) {
			ctx, cancel := commandCtx(c)
			defer cancel()
			done(c, t.Do(ctx, in(author(c))))
		})
	}
	simple("/pause", func(a int64) engine.Input { return engine.PauseGame{Author: a} })
	simple("/resume", func(a int64) engine.Input { return engine.Resume{Author: a} })
	simple("/wake", func(a int64) engine.Input { return engine.Wake{Author: a} })
	cmd.POST("/undo", o.Require(auth.TableScore), func(c *gin.Context) {
		ctx, cancel := commandCtx(c)
		defer cancel()
		done(c, t.Do(ctx, engine.UndoGoal{Author: author(c)}))
	})

	// Registrations by hand (spec 02 §4, §7.3).
	cmd.POST("/registrations", o.Require(auth.TablePlayers), func(c *gin.Context) {
		var in RegisterInput
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}
		ctx, cancel := commandCtx(c)
		defer cancel()
		done(c, t.Register(ctx, engine.Side(in.Side), in.PlayerID, author(c)))
	})
	cmd.DELETE("/registrations/:side/:slot", o.Require(auth.TablePlayers), func(c *gin.Context) {
		slot, err := strconv.Atoi(c.Param("slot"))
		if err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", "slot must be 1 or 2")
			return
		}
		ctx, cancel := commandCtx(c)
		defer cancel()
		done(c, t.Do(ctx, engine.Unregister{Side: engine.Side(c.Param("side")), Slot: slot, Author: author(c)}))
	})

	if o.Presets != nil {
		api.GET("/presets", o.Require(auth.ViewLive), func(c *gin.Context) {
			ps, err := o.Presets.Presets(c.Request.Context())
			if err != nil {
				engineError(c, err)
				return
			}
			out := make([]PresetView, len(ps))
			for i, p := range ps {
				out[i] = PresetView{ID: p.ID, Label: p.Label, Builtin: p.Builtin, Rules: table.RulesView{
					WinScore: p.Rules.WinScore, WinBy: p.Rules.WinBy, MaxScore: p.Rules.MaxScore,
					TimeLimitS: p.Rules.TimeLimitS, TimeTie: p.Rules.TimeTie}}
			}
			c.JSON(http.StatusOK, out)
		})
	}
}
