package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/onboarding"
)

// Cookies. Over plain HTTP (M1–M2) browsers refuse Secure cookies, so the session cookie drops the
// __Host- prefix there; with HTTPS (M3) it is __Host-kicker_session as in spec 03 §5.
const (
	cookieSession       = "kicker_session"
	cookieSessionSecure = "__Host-kicker_session"
	cookieOnboarding    = "kicker_onboarding"
	cookieOnboardingTLS = "__Host-kicker_onboarding"

	ctxIdentity  = "kicker.identity"
	ctxViaCookie = "kicker.via_cookie"
)

func secure(c *gin.Context) bool { return c.Request.TLS != nil }

func cookieName(c *gin.Context, plain, tls string) string {
	if secure(c) {
		return tls
	}
	return plain
}

func setCookie(c *gin.Context, name, value string, maxAge time.Duration) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: int(maxAge / time.Second),
		HttpOnly: true, Secure: secure(c), SameSite: http.SameSiteStrictMode,
	})
}

func clearCookie(c *gin.Context, name string) {
	http.SetCookie(c.Writer, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure(c), SameSite: http.SameSiteStrictMode})
}

// identity returns the logged-in account of the request, or nil.
func identity(c *gin.Context) *auth.Identity {
	if v, ok := c.Get(ctxIdentity); ok {
		return v.(*auth.Identity)
	}
	return nil
}

func account(c *gin.Context) *auth.Account {
	if id := identity(c); id != nil {
		return &id.Account
	}
	return nil
}

// sessionToken reads the bearer token (app) or the session cookie (web).
func sessionToken(c *gin.Context) (token string, viaCookie bool) {
	if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:]), false
	}
	if ck, err := c.Cookie(cookieName(c, cookieSession, cookieSessionSecure)); err == nil && ck != "" {
		return ck, true
	}
	return "", false
}

// --- middleware -------------------------------------------------------------------------------

// securityHeaders are set on every response (spec 03 §13). The Content-Security-Policy is set
// by the web UI handler, which knows the hashes of the page's inline scripts.
func securityHeaders(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	c.Next()
}

// limitBody caps API request bodies at 64 KB; picture uploads set their own limit.
func limitBody(c *gin.Context) {
	if !(c.Request.Method == http.MethodPut && strings.HasSuffix(c.Request.URL.Path, "/avatar")) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	}
	c.Next()
}

// identify resolves the session, if any.
func (o Options) identify(c *gin.Context) {
	token, viaCookie := sessionToken(c)
	c.Set(ctxViaCookie, viaCookie || hasCookie(c, cookieName(c, cookieOnboarding, cookieOnboardingTLS)))
	if token != "" && o.Auth != nil {
		id, err := o.Auth.Authenticate(c.Request.Context(), token)
		if err != nil {
			fail(c, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		if id != nil {
			c.Set(ctxIdentity, id)
		}
	}
	c.Next()
}

func hasCookie(c *gin.Context, name string) bool {
	v, err := c.Cookie(name)
	return err == nil && v != ""
}

// csrf: a changing request must not come from another site (spec 03 §13). A foreign Origin is
// always refused; a cookie-authenticated request must carry a matching Origin. Requests with a
// bearer token or without credentials (curl, the app) need no Origin.
func csrf(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		c.Next()
		return
	}
	origin := c.GetHeader("Origin")
	if origin == "" {
		if v, _ := c.Get(ctxViaCookie); v == true {
			fail(c, http.StatusForbidden, "csrf", "missing Origin header")
			return
		}
		c.Next()
		return
	}
	u, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(u.Host, c.Request.Host) {
		fail(c, http.StatusForbidden, "csrf", "cross-site request refused")
		return
	}
	c.Next()
}

// onboardingGate: while the wizard isn't done, only /api/info and /api/onboarding answer
// (spec 03 §12).
func (o Options) onboardingGate(c *gin.Context) {
	if o.Onboarding == nil || o.Onboarding.Done() {
		c.Next()
		return
	}
	p := c.Request.URL.Path
	// Logging in is allowed too: step 4 (the emergency kit) belongs to the new admin's login.
	if p == "/api/info" || p == "/api/onboarding" || strings.HasPrefix(p, "/api/onboarding/") ||
		p == "/api/auth/me" || p == "/api/auth/login" || p == "/api/auth/logout" {
		c.Next()
		return
	}
	fail(c, http.StatusServiceUnavailable, "onboarding", "setup is not complete")
}

// RequireLogin lets any logged-in account through, pending ones included.
func RequireLogin(c *gin.Context) {
	if identity(c) == nil {
		fail(c, http.StatusUnauthorized, "login_required", "please log in")
		return
	}
	c.Next()
}

// perms returns the effective permissions of the request: the account's, or Anybody's. An
// account that must change its password has only Anybody's until it has done so.
func (o Options) perms(c *gin.Context) auth.Perms {
	if id := identity(c); id != nil && !id.Account.MustChangePassword {
		return id.Perms
	}
	if o.Auth == nil {
		return auth.Perms{} // no auth service: nothing is allowed
	}
	return o.Auth.PermsOf(nil)
}

// deny answers a missing permission: 401 if logging in might help, 403 otherwise.
func deny(c *gin.Context) {
	a := account(c)
	switch {
	case a != nil && a.MustChangePassword:
		fail(c, http.StatusForbidden, "password_change_required", "change your password first")
	case a == nil:
		fail(c, http.StatusUnauthorized, "login_required", "please log in")
	case !a.IsApproved():
		fail(c, http.StatusForbidden, "not_approved", "your account is waiting for approval")
	default:
		fail(c, http.StatusForbidden, "forbidden", "your group isn't allowed to do this")
	}
}

// Require lets a request through if its group (or Anybody) has the permission (spec 03 §4).
func (o Options) Require(key string) gin.HandlerFunc {
	if _, ok := auth.Lookup(key); !ok {
		panic("api: unknown permission " + key)
	}
	return func(c *gin.Context) {
		if !o.perms(c).Has(key) {
			deny(c)
			return
		}
		c.Next()
	}
}

// RequireAdmin is for the system permissions: settings, modules, accounts, groups, … (Admin only).
func RequireAdmin(c *gin.Context) {
	if a := account(c); a != nil && a.MustChangePassword {
		fail(c, http.StatusForbidden, "password_change_required", "change your password first")
		return
	}
	if !account(c).IsAdmin() {
		if account(c) == nil {
			fail(c, http.StatusUnauthorized, "login_required", "please log in")
		} else {
			fail(c, http.StatusForbidden, "admin_required", "only admins can do this")
		}
		return
	}
	c.Next()
}

// --- routes -----------------------------------------------------------------------------------

// MeView is GET /api/auth/me: who is asking and what they may do (the UI hides the rest).
type MeView struct {
	Account     *auth.Account  `json:"account"`     // null = not logged in
	Admin       bool           `json:"admin"`       // system permissions (settings, modules, …)
	Permissions map[string]int `json:"permissions"` // effective values, see GET /api/permissions
	Language    string         `json:"language"`
	// For admins: sign-ups waiting for approval, and whether an emergency kit was ever made.
	PendingAccounts int  `json:"pending_accounts,omitempty"`
	NeedsKit        bool `json:"needs_kit,omitempty"`
	// BackupProblem: a backup target failed, or has no backup younger than 48 h (spec 10 §2).
	BackupProblem bool `json:"backup_problem,omitempty"`
	// UpdateAvailable: a newer server version can be installed (spec 10 §3).
	UpdateAvailable bool `json:"update_available,omitempty"`
	// FirmwareUpdates: modules with a newer firmware in this version's release (spec 10 §4).
	FirmwareUpdates int `json:"firmware_updates,omitempty"`
}

// LoginInput is the body of POST /api/auth/login.
type LoginInput struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	Kind       string `json:"kind"`        // web (default, sets the cookie) | app (returns the token)
	DeviceName string `json:"device_name"` // shown on "My devices"
}

// LoginView is the answer to a login. Token is only set for kind=app.
type LoginView struct {
	Account auth.Account `json:"account"`
	Token   string       `json:"token,omitempty"`
}

// PasswordInput is the body of POST /api/auth/password.
type PasswordInput struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// authError maps auth and onboarding errors to HTTP.
func authError(c *gin.Context, err error) {
	var th *auth.ThrottledError
	switch {
	case errors.As(err, &th):
		c.Header("Retry-After", strconv.Itoa(int(th.RetryAfter.Seconds())+1))
		fail(c, http.StatusTooManyRequests, "throttled", err.Error())
	case errors.Is(err, auth.ErrInvalidCredentials):
		fail(c, http.StatusUnauthorized, "invalid_credentials", err.Error())
	case errors.Is(err, auth.ErrDisabled):
		fail(c, http.StatusForbidden, "disabled", err.Error())
	case errors.Is(err, auth.ErrInvalidUsername):
		fail(c, http.StatusBadRequest, "invalid_username", err.Error())
	case errors.Is(err, auth.ErrUsernameTaken):
		fail(c, http.StatusConflict, "username_taken", err.Error())
	case errors.Is(err, auth.ErrPasswordTooShort):
		fail(c, http.StatusBadRequest, "password_too_short", err.Error())
	case errors.Is(err, auth.ErrPasswordTooLong):
		fail(c, http.StatusBadRequest, "password_too_long", err.Error())
	case errors.Is(err, auth.ErrPasswordCommon):
		fail(c, http.StatusBadRequest, "password_common", err.Error())
	case errors.Is(err, auth.ErrUnknownPermission), errors.Is(err, auth.ErrInvalidValue),
		errors.Is(err, auth.ErrLoginRequired), errors.Is(err, auth.ErrAdminFixed):
		fail(c, http.StatusBadRequest, "invalid_permission", err.Error())
	case errors.Is(err, auth.ErrGroupName):
		fail(c, http.StatusBadRequest, "invalid_group_name", err.Error())
	case errors.Is(err, auth.ErrInvalidGroup):
		fail(c, http.StatusBadRequest, "invalid_group", err.Error())
	case errors.Is(err, auth.ErrGroupExists):
		fail(c, http.StatusConflict, "group_exists", err.Error())
	case errors.Is(err, auth.ErrGroupBuiltin):
		fail(c, http.StatusConflict, "group_builtin", err.Error())
	case errors.Is(err, auth.ErrLastAdmin):
		fail(c, http.StatusConflict, "last_admin", err.Error())
	case errors.Is(err, auth.ErrPlayerTaken):
		fail(c, http.StatusConflict, "player_taken", err.Error())
	case errors.Is(err, auth.ErrInvalidPlayer):
		fail(c, http.StatusBadRequest, "invalid_player", err.Error())
	case errors.Is(err, auth.ErrNotFound):
		fail(c, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, auth.ErrRegistrationClosed):
		fail(c, http.StatusForbidden, "registration_closed", err.Error())
	case errors.Is(err, auth.ErrInvalidRegistration):
		fail(c, http.StatusBadRequest, "invalid_registration", err.Error())
	case errors.Is(err, auth.ErrNotPending):
		fail(c, http.StatusConflict, "not_pending", err.Error())
	case errors.Is(err, auth.ErrInvalidCode):
		fail(c, http.StatusBadRequest, "invalid_code", err.Error())
	case errors.Is(err, auth.ErrNoRecoveryKey):
		fail(c, http.StatusConflict, "no_recovery_key", err.Error())
	case errors.Is(err, onboarding.ErrNeedAdmin):
		fail(c, http.StatusConflict, "admin_required_first", err.Error())
	case errors.Is(err, onboarding.ErrDone):
		fail(c, http.StatusConflict, "onboarding_done", err.Error())
	case errors.Is(err, onboarding.ErrInProgress):
		fail(c, http.StatusConflict, "onboarding_busy", err.Error())
	case errors.Is(err, onboarding.ErrNotClaimed):
		fail(c, http.StatusConflict, "onboarding_not_started", err.Error())
	case errors.Is(err, onboarding.ErrBadLanguage):
		fail(c, http.StatusBadRequest, "invalid_language", err.Error())
	case errors.Is(err, onboarding.ErrNeedLanguage):
		fail(c, http.StatusConflict, "language_required", err.Error())
	default:
		fail(c, http.StatusInternalServerError, "internal", "internal error")
	}
}

func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		fail(c, http.StatusBadRequest, "invalid_input", err.Error())
		return false
	}
	return true
}

// login creates a session and sets the cookie (web) or returns the token (app).
func (o Options) login(c *gin.Context, req auth.LoginRequest) {
	token, acct, err := o.Auth.Login(c.Request.Context(), req)
	if err != nil {
		authError(c, err)
		return
	}
	out := LoginView{Account: acct}
	if req.Kind == auth.KindApp {
		out.Token = token
	} else {
		setCookie(c, cookieName(c, cookieSession, cookieSessionSecure), token, auth.WebLifetime)
	}
	c.JSON(http.StatusOK, out)
}

func (o Options) authRoutes(api *gin.RouterGroup) {
	a := api.Group("/auth")
	a.GET("/me", func(c *gin.Context) {
		p := o.perms(c)
		me := MeView{Account: account(c), Admin: p.Admin, Permissions: p.Map(), Language: o.language()}
		if p.Admin {
			ctx := c.Request.Context()
			me.PendingAccounts, _ = o.Auth.PendingCount(ctx)
			if t, err := o.Auth.RecoveryKeyCreated(ctx); err == nil && t == nil {
				me.NeedsKit = true
			}
			if o.Backups != nil {
				me.BackupProblem = o.Backups.Problem(ctx)
			}
			if o.Updates != nil {
				me.UpdateAvailable = o.Updates.UpdateAvailable(ctx)
			}
			if o.Firmware != nil {
				me.FirmwareUpdates = o.Firmware.Count(ctx)
			}
		}
		c.JSON(http.StatusOK, me)
	})
	a.POST("/login", func(c *gin.Context) {
		var in LoginInput
		if !bind(c, &in) {
			return
		}
		o.login(c, auth.LoginRequest{Username: in.Username, Password: in.Password, IP: c.ClientIP(),
			Kind: in.Kind, DeviceName: in.DeviceName, UserAgent: c.Request.UserAgent()})
	})
	a.POST("/logout", func(c *gin.Context) {
		if token, _ := sessionToken(c); token != "" {
			if err := o.Auth.Logout(c.Request.Context(), token); err != nil {
				authError(c, err)
				return
			}
		}
		clearCookie(c, cookieName(c, cookieSession, cookieSessionSecure))
		c.Status(http.StatusNoContent)
	})
	a.POST("/password", RequireLogin, func(c *gin.Context) {
		var in PasswordInput
		if !bind(c, &in) {
			return
		}
		if err := o.Auth.ChangePassword(c.Request.Context(), identity(c), in.Current, in.New, c.ClientIP()); err != nil {
			authError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

// OnboardingAdminInput is the body of POST /api/onboarding/admin.
type OnboardingAdminInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (o Options) onboardingRoutes(api *gin.RouterGroup) {
	ob := o.Onboarding
	claim := func(c *gin.Context) string {
		v, _ := c.Cookie(cookieName(c, cookieOnboarding, cookieOnboardingTLS))
		return v
	}
	g := api.Group("/onboarding")
	g.GET("", func(c *gin.Context) {
		ob.Done()
		c.JSON(http.StatusOK, ob.Status(claim(c)))
	})
	g.POST("/start", func(c *gin.Context) {
		token, err := ob.Start(claim(c))
		if err != nil {
			authError(c, err)
			return
		}
		setCookie(c, cookieName(c, cookieOnboarding, cookieOnboardingTLS), token, 24*time.Hour)
		c.JSON(http.StatusOK, ob.Status(token))
	})
	g.PUT("/language", func(c *gin.Context) {
		var in struct {
			Language string `json:"language"`
		}
		if !bind(c, &in) {
			return
		}
		if err := ob.SetLanguage(c.Request.Context(), claim(c), in.Language); err != nil {
			authError(c, err)
			return
		}
		c.JSON(http.StatusOK, ob.Status(claim(c)))
	})
	g.POST("/admin", func(c *gin.Context) {
		var in OnboardingAdminInput
		if !bind(c, &in) {
			return
		}
		if _, err := ob.CreateAdmin(c.Request.Context(), claim(c), in.Username, in.Password, c.ClientIP()); err != nil {
			authError(c, err)
			return
		}
		clearCookie(c, cookieName(c, cookieOnboarding, cookieOnboardingTLS))
		// Log the new admin in right away.
		o.login(c, auth.LoginRequest{Username: in.Username, Password: in.Password, IP: c.ClientIP(),
			Kind: auth.KindWeb, DeviceName: "", UserAgent: c.Request.UserAgent()})
	})
	// Step 2: restore a backup instead of starting fresh (spec 10 §6); the restarted kicker is
	// the backup's, setup included.
	g.POST("/restore", func(c *gin.Context) {
		if err := ob.CanRestore(claim(c)); err != nil {
			authError(c, err)
			return
		}
		o.restore(c)
	})
}
