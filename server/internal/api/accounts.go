package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/emergency"
	"github.com/Humbarrt/kicker/server/internal/players"
)

// TopicAccounts is published (without data) when sign-ups arrive or are decided; admins re-read
// the pending count (spec 03 §3: badge and live notification).
const TopicAccounts = "accounts"

// SignUpInput is the body of POST /api/auth/signup. "Who are you?": an existing player without
// an account (approval mode only; an admin confirms the link) or the name of a new player.
type SignUpInput struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	PlayerID   int64  `json:"player_id"`
	PlayerName string `json:"player_name"`
	DeviceName string `json:"device_name"`
}

// ResetInput is the body of POST /api/auth/reset (a reset code from an admin).
type ResetInput struct {
	Username   string `json:"username"`
	Code       string `json:"code"`
	Password   string `json:"password"`
	DeviceName string `json:"device_name"`
}

// RecoverInput is the body of POST /api/auth/recover (an admin's recovery key).
type RecoverInput struct {
	Username    string `json:"username"`
	RecoveryKey string `json:"recovery_key"`
	Password    string `json:"password"`
	DeviceName  string `json:"device_name"`
}

// KitView is the emergency kit: shown once, and offered as a text file.
type KitView struct {
	Name             string `json:"name"`
	ServerID         string `json:"server_id"`
	CAFingerprint    string `json:"ca_fingerprint"`
	RecoveryKey      string `json:"recovery_key"`
	BackupPassphrase string `json:"backup_passphrase"`
	CreatedAt        int64  `json:"created_at"`
	Filename         string `json:"filename"`
	Text             string `json:"text"`
}

// RecoverView answers a recovery: the admin is logged in and must save the new kit.
type RecoverView struct {
	Account auth.Account `json:"account"`
	Kit     KitView      `json:"kit"`
}

// ApproveInput is the body of POST /api/accounts/:id/approve. The player is an existing one
// (player_id), a new one (new_player_name) or none (both empty).
type ApproveInput struct {
	GroupID       int64  `json:"group_id"`
	PlayerID      int64  `json:"player_id"`
	NewPlayerName string `json:"new_player_name"`
}

// ResetCodeView is a new reset code, shown once to the admin who passes it on.
type ResetCodeView struct {
	Code      string `json:"code"`
	ExpiresAt int64  `json:"expires_at"`
}

// SecurityView is GET /api/settings/security.
type SecurityView struct {
	RecoveryKeyCreatedAt *int64 `json:"recovery_key_created_at"` // null = no kit made yet
	CAFingerprint        string `json:"ca_fingerprint"`          // "" = no HTTPS
	HTTPS                bool   `json:"https"`                   // this request came over HTTPS
}

// kit makes a new recovery key and assembles the emergency kit around it.
func (o Options) kit(c *gin.Context, by auth.Actor) (KitView, error) {
	pass, err := emergency.Passphrase(o.DataDir)
	if err != nil {
		return KitView{}, err
	}
	key, err := o.Auth.NewRecoveryKey(c.Request.Context(), by)
	if err != nil {
		return KitView{}, err
	}
	return o.kitView(key, pass), nil
}

func (o Options) kitView(key, pass string) KitView {
	k := emergency.Kit{Name: o.Name, ServerID: o.ServerID, CAFingerprint: o.caFingerprint(), RecoveryKey: key, BackupPassphrase: pass,
		CreatedAt: time.Now()}
	return KitView{Name: k.Name, ServerID: k.ServerID, CAFingerprint: k.CAFingerprint, RecoveryKey: key, BackupPassphrase: pass,
		CreatedAt: k.CreatedAt.UnixMilli(), Filename: k.Filename(), Text: k.Text(o.language())}
}

func (o Options) publishAccounts() {
	if o.Bus != nil {
		o.Bus.Publish(TopicAccounts, nil)
	}
}

// accountRoutes: sign-up, reset codes, the recovery key, "My devices" (spec 03 §3, §5, §7, §8).
func (o Options) accountRoutes(api *gin.RouterGroup) {
	as := o.Auth
	a := api.Group("/auth")

	a.POST("/signup", func(c *gin.Context) {
		var in SignUpInput
		if !bind(c, &in) {
			return
		}
		ctx := c.Request.Context()
		reg, err := as.Registration(ctx)
		if err != nil {
			authError(c, err)
			return
		}
		if reg.Mode == auth.RegClosed {
			authError(c, auth.ErrRegistrationClosed)
			return
		}
		if o.Players == nil {
			fail(c, http.StatusServiceUnavailable, "unavailable", "players are not available")
			return
		}
		// Check everything before anything is created.
		if _, err := auth.NormalizeUsername(in.Username); err != nil {
			authError(c, err)
			return
		}
		if err := auth.CheckPassword(in.Password); err != nil {
			authError(c, err)
			return
		}
		na := auth.NewAccount{Username: in.Username, Password: in.Password}
		var name string
		switch {
		case in.PlayerID != 0 && in.PlayerName != "", in.PlayerID == 0 && in.PlayerName == "":
			fail(c, http.StatusBadRequest, "invalid_input", "pick a player or name a new one")
			return
		case in.PlayerID != 0:
			if reg.Mode == auth.RegOpen {
				fail(c, http.StatusBadRequest, "invalid_input", "open sign-ups get a new player")
				return
			}
			p, err := o.Players.Get(ctx, in.PlayerID)
			if errors.Is(err, players.ErrNotFound) || err == nil && (p.Guest || p.Archived) {
				authError(c, auth.ErrInvalidPlayer)
				return
			} else if err != nil {
				playerError(c, err)
				return
			}
			if p.HasAccount {
				authError(c, auth.ErrPlayerTaken)
				return
			}
			na.RequestedPlayerID = &p.ID
		default:
			if name, err = o.Players.CheckNewName(ctx, in.PlayerName); err != nil {
				playerError(c, err)
				return
			}
		}
		if err := as.SignUpAllowed(c.ClientIP()); err != nil {
			authError(c, err)
			return
		}
		if reg.Mode == auth.RegOpen {
			na.Status, na.GroupID = auth.StatusApproved, reg.OpenGroupID
		} else {
			na.Status, na.GroupID = auth.StatusPending, reg.DefaultGroupID
			if name != "" {
				na.RequestedPlayerName = &name
			}
		}
		acct, err := as.CreateAccount(ctx, na)
		if err != nil {
			authError(c, err)
			return
		}
		by := auth.Actor{AccountID: acct.ID, IP: c.ClientIP()}
		as.Audit(ctx, &acct.ID, c.ClientIP(), "signup", acct.Username, reg.Mode)
		if reg.Mode == auth.RegOpen {
			if _, err := o.Players.CreateLinked(ctx, players.Actor{AccountID: by.AccountID, IP: by.IP}, name, acct.ID); err != nil {
				playerError(c, err)
				return
			}
		} else {
			o.publishAccounts()
		}
		o.login(c, auth.LoginRequest{Username: in.Username, Password: in.Password, IP: c.ClientIP(), Kind: auth.KindWeb,
			DeviceName: in.DeviceName, UserAgent: c.Request.UserAgent()})
	})

	a.POST("/reset", func(c *gin.Context) {
		var in ResetInput
		if !bind(c, &in) {
			return
		}
		if err := as.ResetPassword(c.Request.Context(), in.Username, in.Code, in.Password, c.ClientIP()); err != nil {
			authError(c, err)
			return
		}
		o.login(c, auth.LoginRequest{Username: in.Username, Password: in.Password, IP: c.ClientIP(), Kind: auth.KindWeb,
			DeviceName: in.DeviceName, UserAgent: c.Request.UserAgent()})
	})

	a.POST("/recover", func(c *gin.Context) {
		var in RecoverInput
		if !bind(c, &in) {
			return
		}
		ctx := c.Request.Context()
		key, err := as.Recover(ctx, in.Username, in.RecoveryKey, in.Password, c.ClientIP())
		if err != nil {
			authError(c, err)
			return
		}
		pass, err := emergency.Passphrase(o.DataDir)
		if err != nil {
			authError(c, err)
			return
		}
		token, acct, err := as.Login(ctx, auth.LoginRequest{Username: in.Username, Password: in.Password, IP: c.ClientIP(),
			Kind: auth.KindWeb, DeviceName: in.DeviceName, UserAgent: c.Request.UserAgent()})
		if err != nil {
			authError(c, err)
			return
		}
		setCookie(c, cookieName(c, cookieSession, cookieSessionSecure), token, auth.WebLifetime)
		c.JSON(http.StatusOK, RecoverView{Account: acct, Kit: o.kitView(key, pass)})
	})

	a.GET("/sessions", RequireLogin, func(c *gin.Context) {
		list, err := as.Sessions(c.Request.Context(), identity(c))
		if err != nil {
			authError(c, err)
			return
		}
		c.JSON(http.StatusOK, list)
	})
	a.DELETE("/sessions/:id", RequireLogin, func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		if id == identity(c).SessionID {
			fail(c, http.StatusBadRequest, "invalid_input", "log out to end this session")
			return
		}
		if err := as.RevokeSession(c.Request.Context(), identity(c), id, c.ClientIP()); err != nil {
			authError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	a.DELETE("/sessions", RequireLogin, func(c *gin.Context) { // all others
		if err := as.RevokeSession(c.Request.Context(), identity(c), 0, c.ClientIP()); err != nil {
			authError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// --- admins ---
	admin := api.Group("", RequireAdmin)
	admin.GET("/settings/registration", func(c *gin.Context) {
		r, err := as.Registration(c.Request.Context())
		if err != nil {
			authError(c, err)
			return
		}
		c.JSON(http.StatusOK, r)
	})
	admin.PUT("/settings/registration", func(c *gin.Context) {
		var in auth.Registration
		if !bind(c, &in) {
			return
		}
		r, err := as.SetRegistration(c.Request.Context(), actor(c), in)
		if err != nil {
			authError(c, err)
			return
		}
		c.JSON(http.StatusOK, r)
	})
	admin.POST("/accounts/:id/approve", func(c *gin.Context) {
		id, ok := idParam(c, "id")
		var in ApproveInput
		if !ok || !bind(c, &in) {
			return
		}
		ctx := c.Request.Context()
		var name string
		if in.NewPlayerName != "" {
			if in.PlayerID != 0 {
				fail(c, http.StatusBadRequest, "invalid_input", "an existing player or a new one, not both")
				return
			}
			var err error
			if o.Players == nil {
				fail(c, http.StatusServiceUnavailable, "unavailable", "players are not available")
				return
			}
			if name, err = o.Players.CheckNewName(ctx, in.NewPlayerName); err != nil {
				playerError(c, err)
				return
			}
		}
		if err := as.Approve(ctx, actor(c), id, in.GroupID, in.PlayerID); err != nil {
			authError(c, err)
			return
		}
		if name != "" {
			by := actor(c)
			if _, err := o.Players.CreateLinked(ctx, players.Actor{AccountID: by.AccountID, IP: by.IP}, name, id); err != nil {
				playerError(c, err)
				return
			}
		}
		o.publishAccounts()
		acct, err := as.Account(ctx, id)
		if err != nil {
			authError(c, err)
			return
		}
		c.JSON(http.StatusOK, acct)
	})
	admin.POST("/accounts/:id/reject", func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		if err := as.Reject(c.Request.Context(), actor(c), id); err != nil {
			authError(c, err)
			return
		}
		o.publishAccounts()
		c.Status(http.StatusNoContent)
	})
	admin.POST("/accounts/:id/reset-code", func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		code, exp, err := as.CreateResetCode(c.Request.Context(), actor(c), id)
		if err != nil {
			authError(c, err)
			return
		}
		c.JSON(http.StatusOK, ResetCodeView{Code: code, ExpiresAt: exp.UnixMilli()})
	})
	admin.GET("/settings/security", func(c *gin.Context) {
		t, err := as.RecoveryKeyCreated(c.Request.Context())
		if err != nil {
			authError(c, err)
			return
		}
		v := SecurityView{CAFingerprint: o.caFingerprint(), HTTPS: c.Request.TLS != nil}
		if t != nil {
			ms := t.UnixMilli()
			v.RecoveryKeyCreatedAt = &ms
		}
		c.JSON(http.StatusOK, v)
	})
	// A new emergency kit: the old recovery key stops working. Re-entering the password guards
	// against someone at an unlocked admin session (spec 03 §8).
	admin.POST("/settings/emergency-kit", func(c *gin.Context) {
		var in struct {
			Password string `json:"password"`
		}
		if !bind(c, &in) {
			return
		}
		if err := as.VerifyPassword(c.Request.Context(), identity(c), in.Password, c.ClientIP()); err != nil {
			authError(c, err)
			return
		}
		k, err := o.kit(c, actor(c))
		if err != nil {
			authError(c, err)
			return
		}
		c.JSON(http.StatusOK, k)
	})
}

// onboardingKitRoutes: step 4 of the wizard, for the admin made in step 3 (logged in).
func (o Options) onboardingKitRoutes(api *gin.RouterGroup) {
	ob := o.Onboarding
	atKit := func(c *gin.Context) {
		if !ob.AtKit() {
			fail(c, http.StatusConflict, "onboarding_step", "the wizard isn't at the emergency kit")
			return
		}
		if !account(c).IsAdmin() {
			fail(c, http.StatusUnauthorized, "login_required", "log in as the new admin")
			return
		}
		c.Next()
	}
	g := api.Group("/onboarding", atKit)
	g.POST("/kit", func(c *gin.Context) {
		k, err := o.kit(c, actor(c))
		if err != nil {
			authError(c, err)
			return
		}
		c.JSON(http.StatusOK, k)
	})
	g.POST("/finish", func(c *gin.Context) {
		if t, err := o.Auth.RecoveryKeyCreated(c.Request.Context()); err != nil {
			authError(c, err)
			return
		} else if t == nil {
			authError(c, auth.ErrNoRecoveryKey)
			return
		}
		if err := ob.Finish(c.Request.Context()); err != nil {
			authError(c, err)
			return
		}
		o.audit(c, "onboarding_done", "", "")
		c.JSON(http.StatusOK, ob.Status(""))
	})
}
