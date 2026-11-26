// Package api holds the gin HTTP routes, middleware, DTOs and the SSE hub.
package api

import (
	"github.com/Humbarrt/kicker/server/internal/backup"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/coins"
	"github.com/Humbarrt/kicker/server/internal/onboarding"
	"github.com/Humbarrt/kicker/server/internal/players"
	"github.com/Humbarrt/kicker/server/internal/tournaments"
	"github.com/Humbarrt/kicker/server/internal/version"
	"github.com/Humbarrt/kicker/server/internal/webui"
)

// Info is returned by GET /api/info (spec 03 §9). Fields are added as the specs get implemented.
type Info struct {
	ServerID       string `json:"server_id"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	Sim            bool   `json:"sim"`
	OnboardingDone bool   `json:"onboarding_done"`
	Language       string `json:"language"`       // the kicker's language (onboarding step 1); "" = not chosen
	Registration   string `json:"registration"`   // approval | open | closed (spec 03 §3)
	CAFingerprint  string `json:"ca_fingerprint"` // "" = no HTTPS (spec 03 §9)
}

// Options configure the router.
type Options struct {
	Sim      bool
	ServerID string
	Name     string
	DataDir  string // the data folder (backup passphrase); "" = a temp folder (tests)
	CA       CA     // the kicker's CA when it serves HTTPS; nil = plain HTTP
	// Auth is required for everything that needs a login; without it those routes refuse all.
	Auth       *auth.Service
	Onboarding *onboarding.Onboarding // nil = no wizard (tests)
	// UI serves everything outside /api. Defaults to the embedded web UI.
	UI        http.Handler
	Modules   ModuleHost // nil = no module routes
	Simulator Simulator  // nil = no simulator routes (only with --sim)
	Players   *players.Service
	Coins     *coins.Service
	Ratings   Ratings // nil = no rating routes
	History   History // nil = no history routes
	Stats     Stats   // nil = no stats routes
	Table     Table   // nil = no table routes
	Presets   Presets // nil = no preset routes
	// Tournaments runs tournaments (spec 09); nil = no tournament routes.
	Tournaments *tournaments.Service
	// Backups makes and restores backups (spec 10 §2); nil = no backup routes.
	Backups *backup.Service
	// Restart restarts the server (after a restore is staged); nil = not possible here.
	Restart func()
	Bus     *bus.Bus // nil = no SSE stream
	// Done ends long-lived responses (SSE) when the server shuts down.
	Done      <-chan struct{}
	Heartbeat time.Duration // SSE keep-alive comment interval; default 15 s
}

// NewRouter builds the HTTP handler: /api/* for the API, everything else for the web UI.
func NewRouter(opts Options) *gin.Engine {
	if opts.UI == nil {
		opts.UI = webui.Handler()
	}
	if opts.DataDir == "" {
		opts.DataDir, _ = os.MkdirTemp("", "kicker-data-")
	}

	if opts.Name == "" {
		opts.Name = "Kicker"
	}

	r := gin.New()
	_ = r.SetTrustedProxies(nil) // ClientIP = the TCP peer; nothing sits in front of the kicker
	r.Use(gin.Recovery(), securityHeaders)

	api := r.Group("/api", limitBody, opts.identify, csrf, opts.onboardingGate)
	api.GET("/info", func(c *gin.Context) {
		c.JSON(http.StatusOK, Info{
			ServerID:       opts.ServerID,
			Name:           opts.Name,
			Version:        version.Version,
			Sim:            opts.Sim,
			OnboardingDone: opts.Onboarding == nil || opts.Onboarding.Done(),
			Language:       opts.language(),
			Registration:   opts.registration(c),
			CAFingerprint:  opts.caFingerprint(),
		})
	})
	if opts.Auth != nil {
		opts.authRoutes(api)
		opts.accountRoutes(api)
		opts.adminRoutes(api)
	}
	if opts.Onboarding != nil {
		opts.onboardingRoutes(api)
		if opts.Auth != nil {
			opts.onboardingKitRoutes(api)
		}
	}

	if opts.Modules != nil {
		opts.moduleRoutes(api)
	}
	if opts.Simulator != nil {
		opts.simRoutes(api.Group("", opts.Require(auth.ViewLive)))
	}
	if opts.Players != nil {
		opts.playerRoutes(api)
	}
	if opts.Coins != nil {
		opts.coinRoutes(api)
	}
	if opts.Table != nil {
		opts.tableRoutes(api)
		opts.settingsRoutes(api)
		opts.gameRoutes(api)
	}
	if opts.Table != nil && opts.History != nil {
		opts.historyRoutes(api)
	}
	if opts.Stats != nil {
		opts.statsRoutes(api)
	}
	if opts.Ratings != nil {
		opts.ratingRoutes(api)
	}
	if opts.Tournaments != nil {
		opts.tournamentRoutes(api)
	}
	if opts.Backups != nil && opts.Auth != nil {
		opts.backupRoutes(api)
	}
	if opts.Bus != nil {
		opts.eventsRoute(api)
	}

	if opts.CA != nil {
		r.GET("/ca.crt", func(c *gin.Context) { serveCA(opts.CA, c.Writer) })
		r.GET("/trust", func(c *gin.Context) { serveTrust(opts.CA, opts.Name, opts.language(), "/", c.Writer) })
	}
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		// gin pre-sets 404 for unmatched routes; the UI handler decides the real status.
		c.Status(http.StatusOK)
		opts.UI.ServeHTTP(c.Writer, c.Request)
	})
	return r
}

func (o Options) caFingerprint() string {
	if o.CA == nil {
		return ""
	}
	return o.CA.CAFingerprint()
}

func (o Options) registration(c *gin.Context) string {
	if o.Auth == nil {
		return auth.RegClosed
	}
	r, err := o.Auth.Registration(c.Request.Context())
	if err != nil {
		return auth.RegClosed
	}
	return r.Mode
}

func (o Options) language() string {
	if o.Onboarding != nil {
		return o.Onboarding.Status("").Language
	}
	return "en"
}
