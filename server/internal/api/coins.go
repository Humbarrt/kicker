package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/coins"
)

// CoinInput is the body of PUT /api/coins/:uid: assign (or reassign) a coin.
type CoinInput struct {
	PlayerID int64  `json:"player_id"`
	Label    string `json:"label"`
}

func coinActor(o Options, c *gin.Context) coins.Actor {
	p := playerActor(o, c)
	return coins.Actor{AccountID: p.AccountID, PlayerID: p.PlayerID, Perms: p.Perms, IP: p.IP}
}

func coinError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, coins.ErrForbidden):
		deny(c)
	case errors.Is(err, coins.ErrNotFound):
		fail(c, http.StatusNotFound, "not_found", "no such coin")
	case errors.Is(err, coins.ErrInvalidUID):
		fail(c, http.StatusBadRequest, "invalid_coin_uid", err.Error())
	case errors.Is(err, coins.ErrLabel):
		fail(c, http.StatusBadRequest, "invalid_label", err.Error())
	case errors.Is(err, coins.ErrPlayer):
		fail(c, http.StatusBadRequest, "invalid_player", err.Error())
	default:
		fail(c, http.StatusInternalServerError, "internal", "internal error")
	}
}

// coinRoutes: coins.manage for everything, except that anyone sees their own player's coins.
func (o Options) coinRoutes(api *gin.RouterGroup) {
	cs := o.Coins
	g := api.Group("/coins", RequireLogin)
	g.GET("", func(c *gin.Context) {
		pid, _ := strconv.ParseInt(c.Query("player_id"), 10, 64)
		list, err := cs.List(c.Request.Context(), coinActor(o, c), pid)
		if err != nil {
			coinError(c, err)
			return
		}
		c.JSON(http.StatusOK, list)
	})
	g.PUT("/:uid", func(c *gin.Context) {
		var in CoinInput
		if !bind(c, &in) {
			return
		}
		coin, err := cs.Assign(c.Request.Context(), coinActor(o, c), c.Param("uid"), in.PlayerID, in.Label)
		if err != nil {
			coinError(c, err)
			return
		}
		c.JSON(http.StatusOK, coin)
	})
	g.PATCH("/:uid", func(c *gin.Context) {
		var in struct {
			Active *bool `json:"active"`
		}
		if !bind(c, &in) {
			return
		}
		if in.Active == nil {
			fail(c, http.StatusBadRequest, "invalid_input", "active is required")
			return
		}
		coin, err := cs.SetActive(c.Request.Context(), coinActor(o, c), c.Param("uid"), *in.Active)
		if err != nil {
			coinError(c, err)
			return
		}
		c.JSON(http.StatusOK, coin)
	})
	g.DELETE("/:uid", func(c *gin.Context) {
		if err := cs.Delete(c.Request.Context(), coinActor(o, c), c.Param("uid")); err != nil {
			coinError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}
