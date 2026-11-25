package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/players"
)

// PlayerInput is the body of POST /api/players and /api/players/me.
type PlayerInput struct {
	Name  string `json:"name"`
	Color string `json:"color"` // "" = least-used palette color
}

// PlayerPatch is the body of PATCH /api/players/:id.
type PlayerPatch struct {
	Name     *string `json:"name"`
	Color    *string `json:"color"`
	Archived *bool   `json:"archived"`
}

// PaletteView is GET /api/players/palette.
type PaletteView struct {
	Colors    []string `json:"colors"`
	Suggested string   `json:"suggested"`
}

func playerActor(o Options, c *gin.Context) players.Actor {
	a := players.Actor{Perms: o.perms(c), IP: c.ClientIP()}
	if acc := account(c); acc != nil && !acc.MustChangePassword {
		a.AccountID = acc.ID
		if acc.PlayerID != nil {
			a.PlayerID = *acc.PlayerID
		}
	}
	return a
}

func playerError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, players.ErrForbidden):
		deny(c)
	case errors.Is(err, players.ErrNotFound):
		fail(c, http.StatusNotFound, "not_found", "no such player")
	case errors.Is(err, players.ErrInvalidName):
		fail(c, http.StatusBadRequest, "invalid_player_name", err.Error())
	case errors.Is(err, players.ErrInvalidColor):
		fail(c, http.StatusBadRequest, "invalid_color", err.Error())
	case errors.Is(err, players.ErrNameTaken):
		fail(c, http.StatusConflict, "player_name_taken", err.Error())
	case errors.Is(err, players.ErrHasPlayer):
		fail(c, http.StatusConflict, "has_player", err.Error())
	case errors.Is(err, players.ErrGuest):
		fail(c, http.StatusConflict, "guest_player", err.Error())
	case errors.Is(err, players.ErrInUse):
		fail(c, http.StatusConflict, "player_in_use", err.Error())
	case errors.Is(err, players.ErrBadImage):
		fail(c, http.StatusBadRequest, "bad_image", err.Error())
	default:
		fail(c, http.StatusInternalServerError, "internal", "internal error")
	}
}

// requireAny lets a request through if it has any of the permissions.
func (o Options) requireAny(keys ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		p := o.perms(c)
		for _, k := range keys {
			if p.Has(k) {
				c.Next()
				return
			}
		}
		deny(c)
	}
}

func (o Options) playerRoutes(api *gin.RouterGroup) {
	ps := o.Players
	view := o.Require(auth.ViewStats)
	g := api.Group("/players")

	g.GET("", view, func(c *gin.Context) {
		list, err := ps.List(c.Request.Context(), c.Query("all") == "1")
		if err != nil {
			playerError(c, err)
			return
		}
		c.JSON(http.StatusOK, list)
	})
	g.GET("/palette", func(c *gin.Context) {
		s, err := ps.SuggestColor(c.Request.Context())
		if err != nil {
			playerError(c, err)
			return
		}
		c.JSON(http.StatusOK, PaletteView{Colors: players.Palette, Suggested: s})
	})
	g.GET("/:id", view, func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		p, err := ps.Get(c.Request.Context(), id)
		if err != nil {
			playerError(c, err)
			return
		}
		c.JSON(http.StatusOK, p)
	})
	g.POST("", o.Require(auth.PlayersCreate), func(c *gin.Context) {
		var in PlayerInput
		if !bind(c, &in) {
			return
		}
		p, err := ps.Create(c.Request.Context(), playerActor(o, c), players.NewPlayer{Name: in.Name, Color: in.Color})
		if err != nil {
			playerError(c, err)
			return
		}
		c.JSON(http.StatusCreated, p)
	})
	g.POST("/me", RequireLogin, func(c *gin.Context) {
		var in PlayerInput
		if !bind(c, &in) {
			return
		}
		p, err := ps.CreateMine(c.Request.Context(), playerActor(o, c), players.NewPlayer{Name: in.Name, Color: in.Color})
		if err != nil {
			playerError(c, err)
			return
		}
		c.JSON(http.StatusCreated, p)
	})
	g.PATCH("/:id", RequireLogin, func(c *gin.Context) {
		id, ok := idParam(c, "id")
		var in PlayerPatch
		if !ok || !bind(c, &in) {
			return
		}
		p, err := ps.Update(c.Request.Context(), playerActor(o, c), id, players.Change{Name: in.Name, Color: in.Color, Archived: in.Archived})
		if err != nil {
			playerError(c, err)
			return
		}
		c.JSON(http.StatusOK, p)
	})
	// The picture is the raw request body (the web UI sends a JPEG it has already cropped and
	// scaled); the server checks it by content, re-encodes it and makes both sizes.
	g.PUT("/:id/avatar", RequireLogin, func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20))
		if err != nil {
			fail(c, http.StatusRequestEntityTooLarge, "bad_image", "the picture must be smaller than 8 MB")
			return
		}
		p, err := ps.SetAvatar(c.Request.Context(), playerActor(o, c), id, data)
		if err != nil {
			playerError(c, err)
			return
		}
		c.JSON(http.StatusOK, p)
	})
	g.DELETE("/:id/avatar", RequireLogin, func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		p, err := ps.SetAvatar(c.Request.Context(), playerActor(o, c), id, nil)
		if err != nil {
			playerError(c, err)
			return
		}
		c.JSON(http.StatusOK, p)
	})
	g.DELETE("/:id", RequireAdmin, func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		if err := ps.Delete(c.Request.Context(), playerActor(o, c), id); err != nil {
			playerError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// Pictures are named by their content hash, so they can be cached forever.
	api.GET("/media/avatars/:file", o.requireAny(auth.ViewLive, auth.ViewStats), func(c *gin.Context) {
		name, ok := strings.CutSuffix(c.Param("file"), ".jpg")
		hash, sizeStr, ok2 := strings.Cut(name, "-")
		size, _ := strconv.Atoi(sizeStr)
		path := ps.AvatarPath(hash, size)
		if !ok || !ok2 || path == "" {
			fail(c, http.StatusNotFound, "not_found", "not found")
			return
		}
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		c.Header("Content-Type", "image/jpeg")
		c.File(path)
	})
}
