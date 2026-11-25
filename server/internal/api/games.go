package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/engine"
)

// Paused games (D16): resume from the paused list, the abandoned list, reinstating.
func (o Options) gameRoutes(api *gin.RouterGroup) {
	t := o.Table
	g := api.Group("/games")
	idParam := func(c *gin.Context) (int64, bool) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			fail(c, http.StatusBadRequest, "invalid_input", "game ids are positive numbers")
			return 0, false
		}
		return id, true
	}
	commandCtx := func(c *gin.Context) (context.Context, context.CancelFunc) {
		return context.WithTimeout(c.Request.Context(), 5*time.Second)
	}

	g.GET("/abandoned", o.Require(auth.ViewLive), func(c *gin.Context) {
		list, err := t.AbandonedGames(c.Request.Context(), 20)
		if err != nil {
			engineError(c, err)
			return
		}
		c.JSON(http.StatusOK, list)
	})
	g.POST("/:id/resume", o.Require(auth.TableControl), func(c *gin.Context) {
		id, ok := idParam(c)
		if !ok {
			return
		}
		ctx, cancel := commandCtx(c)
		defer cancel()
		if err := t.ResumeGame(ctx, id, authorOf(c)); err != nil {
			engineError(c, err)
			return
		}
		c.JSON(http.StatusOK, t.View())
	})
	// Reinstating is an edit of a past game: the group's games.edit scope and edit window apply
	// (spec 03 §4, D16). The window counts from when the game was paused.
	g.POST("/:id/reinstate", RequireLogin, func(c *gin.Context) {
		id, ok := idParam(c)
		if !ok {
			return
		}
		ctx, cancel := commandCtx(c)
		defer cancel()
		game, err := t.Game(ctx, id)
		if err != nil {
			engineError(c, err)
			return
		}
		if !mayEdit(o.perms(c), account(c), game, time.Now()) {
			deny(c)
			return
		}
		if err := t.ReinstateGame(ctx, id, authorOf(c)); err != nil {
			engineError(c, err)
			return
		}
		o.audit(c, "game_reinstated", strconv.FormatInt(id, 10), "")
		c.JSON(http.StatusOK, t.View())
	})
}

// mayEdit applies games.edit (own / all) and games.edit_days to a past game.
func mayEdit(p auth.Perms, a *auth.Account, g *engine.Game, now time.Time) bool {
	switch p.Value(auth.GamesEdit) {
	case auth.ScopeAll:
	case auth.ScopeOwn:
		if a == nil || a.PlayerID == nil || !g.Plays(*a.PlayerID) {
			return false
		}
	default:
		return false
	}
	days := p.Value(auth.GamesEditDays)
	switch {
	case days == auth.Unlimited:
		return true
	case days <= 0:
		return false
	}
	at := g.EndedAt // the window counts from when the game ended – or was paused
	if at.IsZero() {
		at = g.PausedAt
	}
	if at.IsZero() {
		at = g.StartedAt
	}
	return now.Sub(at) <= time.Duration(days)*24*time.Hour
}

func authorOf(c *gin.Context) int64 {
	if a := account(c); a != nil {
		return a.ID
	}
	return 0
}
