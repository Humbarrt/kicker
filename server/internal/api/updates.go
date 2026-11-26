package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/update"
)

// UpdatesView is GET /api/updates: Settings → Updates (spec 10 §3).
type UpdatesView struct {
	Enabled bool          `json:"enabled"` // false: a development build or a fork
	Version string        `json:"version"` // running
	Repo    string        `json:"repo"`
	Config  update.Config `json:"config"`
	Status  update.Status `json:"status"`
	// Blocked: why the available update can't be installed now (code + text); "" = it can.
	Blocked     string `json:"blocked"`
	BlockedText string `json:"blocked_text"`
}

func updateCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, update.ErrDisabled):
		return "disabled"
	case errors.Is(err, update.ErrNotInstalled):
		return "not_installed"
	case errors.Is(err, update.ErrBusy):
		return "busy"
	case errors.Is(err, update.ErrNothing):
		return "none"
	case errors.Is(err, update.ErrTooOld):
		return "too_old"
	case errors.Is(err, update.ErrRunning):
		return "running"
	case errors.Is(err, update.ErrGitHubBusy):
		return "github_busy"
	}
	return "failed"
}

func (o Options) updatesView(c *gin.Context) (UpdatesView, error) {
	ctx := c.Request.Context()
	u := o.Updates
	cfg, err := u.Config(ctx)
	if err != nil {
		return UpdatesView{}, err
	}
	st, err := u.Status(ctx)
	if err != nil {
		return UpdatesView{}, err
	}
	v := UpdatesView{Enabled: u.Enabled(), Version: u.Version(), Repo: u.Repo(), Config: cfg, Status: st}
	if b := u.Blocked(ctx); b != nil {
		v.Blocked, v.BlockedText = updateCode(b), b.Error()
	}
	return v, nil
}

// updateRoutes: Settings → Updates (Admin only, spec 10 §7).
func (o Options) updateRoutes(api *gin.RouterGroup) {
	g := api.Group("/updates", RequireAdmin)
	view := func(c *gin.Context, status int) {
		v, err := o.updatesView(c)
		if err != nil {
			fail(c, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		c.JSON(status, v)
	}
	g.GET("", func(c *gin.Context) { view(c, http.StatusOK) })
	g.PUT("/config", func(c *gin.Context) {
		var cfg update.Config
		if !bind(c, &cfg) {
			return
		}
		if err := o.Updates.SetConfig(c.Request.Context(), cfg); err != nil {
			fail(c, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		view(c, http.StatusOK)
	})
	g.POST("/check", func(c *gin.Context) {
		// a failed check is part of the status (check_error), not an error of the request
		if _, err := o.Updates.Check(c.Request.Context()); errors.Is(err, update.ErrDisabled) {
			fail(c, http.StatusConflict, "disabled", err.Error())
			return
		}
		view(c, http.StatusOK)
	})
	g.POST("/install", func(c *gin.Context) {
		if err := o.Updates.Install(c.Request.Context()); err != nil {
			fail(c, http.StatusConflict, updateCode(err), err.Error())
			return
		}
		o.audit(c, "update_started", "", o.Updates.Version())
		view(c, http.StatusOK) // not 202: the client reads no body from a 202
	})
}
