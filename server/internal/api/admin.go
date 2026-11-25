package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/auth"
)

// GroupInput is the body of POST /api/groups.
type GroupInput struct {
	Name     string `json:"name"`
	CopyFrom int64  `json:"copy_from"` // the group whose permissions are copied
}

// GroupPatch is the body of PATCH /api/groups/:id. Only the given fields change.
type GroupPatch struct {
	Name        *string        `json:"name"`
	Permissions map[string]int `json:"permissions"`
}

// AccountInput is the body of POST /api/accounts: an admin creates an approved account.
type AccountInput struct {
	Username           string `json:"username"`
	Password           string `json:"password"`             // temporary, see must_change_password
	GroupID            int64  `json:"group_id"`             // 0 = Member
	MustChangePassword *bool  `json:"must_change_password"` // default true: the password is temporary
}

// AccountPatch is the body of PATCH /api/accounts/:id.
type AccountPatch struct {
	GroupID  *int64  `json:"group_id"`
	PlayerID *int64  `json:"player_id"` // 0 = unlink
	Status   *string `json:"status"`    // pending | approved | disabled
}

// AuditView is one audit log entry.
type AuditView struct {
	At        int64  `json:"at"`
	AccountID *int64 `json:"account_id"`
	Account   string `json:"account"`      // username, "" for the console
	IP        string `json:"ip,omitempty"` // only with log.ip

	Action  string `json:"action"`
	Target  string `json:"target"`
	Details string `json:"details"`
}

func idParam(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		fail(c, http.StatusNotFound, "not_found", "not found")
		return 0, false
	}
	return id, true
}

func actor(c *gin.Context) auth.Actor {
	a := auth.Actor{IP: c.ClientIP()}
	if acc := account(c); acc != nil {
		a.AccountID = acc.ID
	}
	return a
}

// adminRoutes: groups, permissions and accounts – Admin group only (spec 03 §4). The audit log
// needs log.view; the IP addresses in it log.ip.
func (o Options) adminRoutes(api *gin.RouterGroup) {
	as := o.Auth
	// The permission catalog is public: the UI needs it to label what a group may do.
	api.GET("/permissions", func(c *gin.Context) { c.JSON(http.StatusOK, auth.Catalog) })

	admin := api.Group("", RequireAdmin)
	admin.GET("/groups", func(c *gin.Context) {
		gs, err := as.Groups(c.Request.Context())
		if err != nil {
			authError(c, err)
			return
		}
		c.JSON(http.StatusOK, gs)
	})
	admin.POST("/groups", func(c *gin.Context) {
		var in GroupInput
		if !bind(c, &in) {
			return
		}
		if in.CopyFrom == 0 {
			in.CopyFrom = as.BuiltinGroup(auth.GroupMember)
		}
		g, err := as.CreateGroup(c.Request.Context(), actor(c), in.Name, in.CopyFrom)
		if err != nil {
			authError(c, err)
			return
		}
		c.JSON(http.StatusCreated, g)
	})
	admin.PATCH("/groups/:id", func(c *gin.Context) {
		id, ok := idParam(c, "id")
		var in GroupPatch
		if !ok || !bind(c, &in) {
			return
		}
		g, err := as.UpdateGroup(c.Request.Context(), actor(c), id, auth.GroupChange{Name: in.Name, Permissions: in.Permissions})
		if err != nil {
			authError(c, err)
			return
		}
		c.JSON(http.StatusOK, g)
	})
	admin.DELETE("/groups/:id", func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		moveTo, err := strconv.ParseInt(c.Query("move_to"), 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", "move_to: the group that takes over the accounts")
			return
		}
		if err := as.DeleteGroup(c.Request.Context(), actor(c), id, moveTo); err != nil {
			authError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	admin.GET("/accounts", func(c *gin.Context) {
		accs, err := as.Accounts(c.Request.Context())
		if err != nil {
			authError(c, err)
			return
		}
		c.JSON(http.StatusOK, accs)
	})
	admin.POST("/accounts", func(c *gin.Context) {
		var in AccountInput
		if !bind(c, &in) {
			return
		}
		mustChange := in.MustChangePassword == nil || *in.MustChangePassword
		a, err := as.CreateAccount(c.Request.Context(), auth.NewAccount{Username: in.Username, Password: in.Password,
			GroupID: in.GroupID, MustChangePassword: mustChange})
		if err != nil {
			authError(c, err)
			return
		}
		o.audit(c, "account_created", a.Username, a.Group.Name)
		c.JSON(http.StatusCreated, a)
	})
	admin.PATCH("/accounts/:id", func(c *gin.Context) {
		id, ok := idParam(c, "id")
		var in AccountPatch
		if !ok || !bind(c, &in) {
			return
		}
		ctx := c.Request.Context()
		if in.GroupID != nil {
			if err := as.SetAccountGroup(ctx, actor(c), id, *in.GroupID); err != nil {
				authError(c, err)
				return
			}
		}
		if in.PlayerID != nil {
			if err := as.SetAccountPlayer(ctx, actor(c), id, *in.PlayerID); err != nil {
				authError(c, err)
				return
			}
		}
		if in.Status != nil {
			if err := as.SetStatus(ctx, actor(c), id, *in.Status); err != nil {
				authError(c, err)
				return
			}
		}
		a, err := as.Account(ctx, id)
		if err != nil {
			authError(c, err)
			return
		}
		c.JSON(http.StatusOK, a)
	})

	api.GET("/audit", o.Require(auth.LogView), func(c *gin.Context) {
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
		ctx := c.Request.Context()
		entries, err := as.AuditLog(ctx, min(max(limit, 1), 1000))
		if err != nil {
			authError(c, err)
			return
		}
		var ids []int64
		for _, e := range entries {
			if e.AccountID != nil {
				ids = append(ids, *e.AccountID)
			}
		}
		names, err := as.AccountNames(ctx, ids)
		if err != nil {
			authError(c, err)
			return
		}
		ip := o.perms(c).Has(auth.LogIP)
		out := make([]AuditView, len(entries))
		for i, e := range entries {
			out[i] = AuditView{At: e.At.UnixMilli(), AccountID: e.AccountID, Action: e.Action, Target: e.Target, Details: e.Details}
			if e.AccountID != nil {
				out[i].Account = names[*e.AccountID]
			}
			if ip {
				out[i].IP = e.IP
			}
		}
		c.JSON(http.StatusOK, out)
	})
}
