package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/store"
)

// Ratings is the rating projection (D9), implemented by *store.Store.
type Ratings interface {
	PlayerRatings(ctx context.Context, playerID int64) ([]store.PlayerRating, error)
	Leaderboard(ctx context.Context, key string, limit int) ([]store.PlayerRating, error)
	GameRatings(ctx context.Context, gameID int64) ([]store.RatingRow, error)
}

// RatingView is a current rating.
type RatingView struct {
	PlayerID int64   `json:"player_id"`
	Key      string  `json:"key"` // solo | team
	Rating   float64 `json:"rating"`
	Games    int     `json:"games"`
}

// RatingChangeView is the rating change of one player in one game.
type RatingChangeView struct {
	PlayerID int64   `json:"player_id"`
	Key      string  `json:"key"`
	Before   float64 `json:"before"`
	After    float64 `json:"after"`
}

func (o Options) ratingRoutes(api *gin.RouterGroup) {
	r := o.Ratings
	view := o.Require(auth.ViewStats)
	id := func(c *gin.Context) (int64, bool) {
		n, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || n <= 0 {
			fail(c, http.StatusBadRequest, "invalid_input", "ids are positive numbers")
			return 0, false
		}
		return n, true
	}
	ratingViews := func(rs []store.PlayerRating) []RatingView {
		out := make([]RatingView, len(rs))
		for i, x := range rs {
			out[i] = RatingView{PlayerID: x.PlayerID, Key: x.Key, Rating: x.Rating, Games: x.Games}
		}
		return out
	}

	// GET /api/ratings?key=team&limit=50: the leaderboard of one key.
	api.GET("/ratings", view, func(c *gin.Context) {
		key := c.DefaultQuery("key", "team")
		if key != "solo" && key != "team" {
			fail(c, http.StatusBadRequest, "invalid_input", "key is solo or team")
			return
		}
		limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
		if err != nil || limit < 1 || limit > 500 {
			fail(c, http.StatusBadRequest, "invalid_input", "limit is 1–500")
			return
		}
		list, err := r.Leaderboard(c.Request.Context(), key, limit)
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, ratingViews(list))
	})
	api.GET("/players/:id/ratings", view, func(c *gin.Context) {
		pid, ok := id(c)
		if !ok {
			return
		}
		list, err := r.PlayerRatings(c.Request.Context(), pid)
		if err != nil {
			internalError(c, err)
			return
		}
		c.JSON(http.StatusOK, ratingViews(list))
	})
	api.GET("/games/:id/ratings", view, func(c *gin.Context) {
		gid, ok := id(c)
		if !ok {
			return
		}
		rows, err := r.GameRatings(c.Request.Context(), gid)
		if err != nil {
			internalError(c, err)
			return
		}
		out := make([]RatingChangeView, len(rows))
		for i, x := range rows {
			out[i] = RatingChangeView{PlayerID: x.PlayerID, Key: x.Key, Before: x.Before, After: x.After}
		}
		c.JSON(http.StatusOK, out)
	})
}

func internalError(c *gin.Context, err error) {
	slog.Error("request failed", "path", c.FullPath(), "err", err)
	fail(c, http.StatusInternalServerError, "internal", "internal error")
}
