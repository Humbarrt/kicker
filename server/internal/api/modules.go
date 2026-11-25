package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/coins"
	"github.com/Humbarrt/kicker/server/internal/modules"
	"github.com/Humbarrt/kicker/server/internal/modules/transport"
	"github.com/Humbarrt/kicker/server/internal/modules/virtual"
	"github.com/Humbarrt/kicker/server/internal/sim"
)

// ModuleHost is the part of the module host the API uses.
type ModuleHost interface {
	Modules() []modules.ModuleInfo
	Console(uid string) []modules.ConsoleEvent
	Adopt(ctx context.Context, uid, name string, roles map[string]string) error
	Release(ctx context.Context, uid string) error
	Identify(ctx context.Context, uid string, seconds int) error
	SetConfig(ctx context.Context, uid, instance string, partial json.RawMessage) error
	UnknownPorts() []transport.PortInfo
	Probe(port string)
}

// Simulator is the simulator of `kicker serve --sim`.
type Simulator interface {
	Modules() []sim.Module
	Emit(uid, instance, event string) error
	Acceptors() []sim.Acceptor
	Insert(uid, coin string) error
	PressButton(uid string) error
}

// SimView is GET /api/sim.
type SimView struct {
	Modules   []sim.Module   `json:"modules"`
	Acceptors []sim.Acceptor `json:"acceptors"`
}

// ConsoleLine is one module console line in the API and the "console" SSE event.
type ConsoleLine struct {
	UID      string   `json:"uid"`
	At       string   `json:"at"`
	Port     string   `json:"port"`
	Dir      string   `json:"dir"`
	Kind     string   `json:"kind"`
	Text     string   `json:"text"`
	Err      string   `json:"err,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// ConsoleLineOf converts a host console event.
func ConsoleLineOf(l modules.ConsoleEvent) ConsoleLine {
	return ConsoleLine{
		UID: l.UID, At: l.Line.At.Format("2006-01-02T15:04:05.000Z07:00"), Port: l.Port, Dir: string(l.Line.Dir),
		Kind: l.Line.Kind.String(), Text: l.Line.Text, Err: l.Line.Err, Warnings: l.Line.Warnings,
	}
}

// AdoptInput is the body of POST /api/modules/:uid/adopt (also used to rename and re-assign).
type AdoptInput struct {
	Name  string            `json:"name"`
	Roles map[string]string `json:"roles"` // instance → role; "" or missing = no role
}

// PortView is a serial port with an unknown USB vendor, which can be probed (D7).
type PortView struct {
	Name    string `json:"name"`
	VID     string `json:"vid"`
	PID     string `json:"pid"`
	Product string `json:"product"`
}

func moduleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, modules.ErrUnknownModule):
		fail(c, http.StatusNotFound, "unknown_module", err.Error())
	case errors.Is(err, modules.ErrOffline):
		fail(c, http.StatusConflict, "offline", err.Error())
	case errors.Is(err, modules.ErrInvalidRoles):
		fail(c, http.StatusBadRequest, "invalid_roles", err.Error())
	case errors.Is(err, modules.ErrInvalidConfig):
		fail(c, http.StatusBadRequest, "invalid_config", err.Error())
	default:
		fail(c, http.StatusBadGateway, "module_error", err.Error())
	}
}

// moduleRoutes: settings → modules, Admin only (spec 03 §4).
func (o Options) moduleRoutes(api *gin.RouterGroup) {
	h := o.Modules
	admin := api.Group("/modules", RequireAdmin)
	admin.GET("", func(c *gin.Context) { c.JSON(http.StatusOK, h.Modules()) })
	admin.GET("/:uid/console", func(c *gin.Context) {
		lines := h.Console(c.Param("uid"))
		out := make([]ConsoleLine, 0, len(lines))
		for _, l := range lines {
			out = append(out, ConsoleLineOf(l))
		}
		c.JSON(http.StatusOK, out)
	})
	admin.POST("/:uid/adopt", func(c *gin.Context) {
		var in AdoptInput
		if !bind(c, &in) {
			return
		}
		if err := h.Adopt(c.Request.Context(), c.Param("uid"), in.Name, in.Roles); err != nil {
			moduleError(c, err)
			return
		}
		o.audit(c, "module_adopted", c.Param("uid"), in.Name)
		c.Status(http.StatusNoContent)
	})
	admin.POST("/:uid/release", func(c *gin.Context) {
		if err := h.Release(c.Request.Context(), c.Param("uid")); err != nil {
			moduleError(c, err)
			return
		}
		o.audit(c, "module_released", c.Param("uid"), "")
		c.Status(http.StatusNoContent)
	})
	admin.POST("/:uid/identify", func(c *gin.Context) {
		if err := h.Identify(c.Request.Context(), c.Param("uid"), 5); err != nil {
			moduleError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	// Behaviour config of one instance (e.g. goal_sensor lockout_ms); the body is merged into the
	// stored config and sent to the module.
	admin.PATCH("/:uid/config/:instance", func(c *gin.Context) {
		var partial json.RawMessage
		if !bind(c, &partial) {
			return
		}
		if err := h.SetConfig(c.Request.Context(), c.Param("uid"), c.Param("instance"), partial); err != nil {
			moduleError(c, err)
			return
		}
		o.audit(c, "module_config_changed", c.Param("uid")+"/"+c.Param("instance"), string(partial))
		c.Status(http.StatusNoContent)
	})
	admin.GET("/ports", func(c *gin.Context) {
		out := []PortView{}
		for _, p := range h.UnknownPorts() {
			out = append(out, PortView{Name: p.Name, VID: p.VID, PID: p.PID, Product: p.Product})
		}
		c.JSON(http.StatusOK, out)
	})
	admin.POST("/ports/probe", func(c *gin.Context) {
		var in struct {
			Port string `json:"port"`
		}
		if !bind(c, &in) {
			return
		}
		h.Probe(in.Port)
		c.Status(http.StatusAccepted)
	})
}

// simRoutes: the simulator panel. Pressing "goal" is like dropping a ball into the goal, so
// whoever may watch the table may do it.
func (o Options) simRoutes(api *gin.RouterGroup) {
	g := api.Group("/sim")
	g.GET("", func(c *gin.Context) {
		v := SimView{Modules: o.Simulator.Modules(), Acceptors: o.Simulator.Acceptors()}
		if o.Modules != nil {
			roles := map[[2]string]string{}
			for _, m := range o.Modules.Modules() {
				for _, cp := range m.Caps {
					roles[[2]string{m.UID, cp.Instance}] = cp.Role
				}
			}
			for i := range v.Modules {
				for j := range v.Modules[i].Instances {
					v.Modules[i].Instances[j].Role = roles[[2]string{v.Modules[i].UID, v.Modules[i].Instances[j].Instance}]
				}
			}
			for i := range v.Acceptors {
				v.Acceptors[i].Role = roles[[2]string{v.Acceptors[i].UID, v.Acceptors[i].Instance}]
			}
		}
		c.JSON(http.StatusOK, v)
	})
	// The coins the panel can insert: the simulator plays the person holding them.
	g.GET("/coins", func(c *gin.Context) {
		if o.Coins == nil {
			c.JSON(http.StatusOK, []any{})
			return
		}
		list, err := o.Coins.All(c.Request.Context())
		if err != nil {
			coinError(c, err)
			return
		}
		c.JSON(http.StatusOK, list)
	})
	g.POST("/:uid/insert", func(c *gin.Context) {
		var in struct {
			Coin string `json:"coin"`
		}
		if !bind(c, &in) {
			return
		}
		uid, err := coins.NormalizeUID(in.Coin)
		if err == nil {
			err = o.Simulator.Insert(c.Param("uid"), uid)
		}
		simResult(c, err)
	})
	g.POST("/:uid/button", func(c *gin.Context) {
		simResult(c, o.Simulator.PressButton(c.Param("uid")))
	})
	g.POST("/:uid/:instance/:event", func(c *gin.Context) {
		if err := o.Simulator.Emit(c.Param("uid"), c.Param("instance"), c.Param("event")); err != nil {
			if errors.Is(err, sim.ErrUnknown) {
				fail(c, http.StatusNotFound, "not_found", err.Error())
			} else {
				fail(c, http.StatusInternalServerError, "internal", err.Error())
			}
			return
		}
		c.Status(http.StatusNoContent)
	})
}

// audit writes an audit log entry for the request's account.
func (o Options) audit(c *gin.Context, action, target, details string) {
	if o.Auth == nil {
		return
	}
	var id *int64
	if a := account(c); a != nil {
		id = &a.ID
	}
	o.Auth.Audit(c.Request.Context(), id, c.ClientIP(), action, target, details)
}

func simResult(c *gin.Context, err error) {
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, sim.ErrUnknown):
		fail(c, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, coins.ErrInvalidUID), errors.Is(err, virtual.ErrInvalidCoin):
		fail(c, http.StatusBadRequest, "invalid_coin_uid", err.Error())
	case errors.Is(err, virtual.ErrFull):
		fail(c, http.StatusConflict, "acceptor_full", err.Error())
	case errors.Is(err, virtual.ErrAlreadyIn):
		fail(c, http.StatusConflict, "coin_inside", err.Error())
	default:
		fail(c, http.StatusInternalServerError, "internal", err.Error())
	}
}
