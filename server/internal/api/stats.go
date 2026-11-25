package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/stats"
)

// Stats are the stats views (M3 task 8), implemented by stats.Reader.
type Stats interface {
	Profile(ctx context.Context, playerID int64) (stats.Profile, error)
	Leaderboard(ctx context.Context, key string) (stats.Leaderboard, error)
	Overview(ctx context.Context) (stats.Overview, error)
}

// statsRoutes: player stats, the leaderboard and the overview, for view.stats.
func (o Options) statsRoutes(api *gin.RouterGroup) {
	s := o.Stats
	view := o.Require(auth.ViewStats)

	// GET /api/players/:id/stats: record, ratings with history, streaks, teammates and opponents.
	api.GET("/players/:id/stats", view, func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			fail(c, http.StatusBadRequest, "invalid_input", "ids are positive numbers")
			return
		}
		p, err := s.Profile(c.Request.Context(), id)
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, p)
	})
	// GET /api/leaderboard?key=team|solo
	api.GET("/leaderboard", view, func(c *gin.Context) {
		key := c.DefaultQuery("key", "team")
		if key != "solo" && key != "team" {
			fail(c, http.StatusBadRequest, "invalid_input", "key is solo or team")
			return
		}
		lb, err := s.Leaderboard(c.Request.Context(), key)
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, lb)
	})
	// GET /api/overview: the last days in numbers, the top of the leaderboards, highlights.
	api.GET("/overview", view, func(c *gin.Context) {
		ov, err := s.Overview(c.Request.Context())
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, ov)
	})
}
