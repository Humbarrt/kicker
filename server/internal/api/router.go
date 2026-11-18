// Package api holds the gin HTTP routes, middleware, DTOs and the SSE hub.
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/version"
	"github.com/Humbarrt/kicker/server/internal/webui"
)

// Info is returned by GET /api/info (spec 03 §9). Fields are added as the specs get implemented.
type Info struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	Sim            bool   `json:"sim"`
	OnboardingDone bool   `json:"onboarding_done"`
}

// Options configure the router.
type Options struct {
	Sim bool
}

// NewRouter builds the HTTP handler: /api/* for the API, everything else for the embedded web UI.
func NewRouter(opts Options) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	api := r.Group("/api")
	api.GET("/info", func(c *gin.Context) {
		c.JSON(http.StatusOK, Info{
			Name:    "kicker",
			Version: version.Version,
			Sim:     opts.Sim,
		})
	})

	ui := webui.Handler()
	r.NoRoute(func(c *gin.Context) {
		if len(c.Request.URL.Path) >= 5 && c.Request.URL.Path[:5] == "/api/" {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		ui.ServeHTTP(c.Writer, c.Request)
	})
	return r
}
