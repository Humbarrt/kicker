// Package auth holds accounts, password hashing, sessions and login throttling (spec 03).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Humbarrt/kicker/server/internal/store"
)

// Statuses and session kinds.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusDisabled = "disabled"

	KindWeb = "web"
	KindApp = "app"
)

// Session lifetimes, renewed on use (spec 03 §5).
const (
	WebLifetime = 30 * 24 * time.Hour
	AppLifetime = 365 * 24 * time.Hour
	touchEvery  = 5 * time.Minute
	cacheFor    = time.Minute // re-read from the database after this, so console changes apply
)

// GroupRef names an account's group.
type GroupRef struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Builtin string `json:"builtin,omitempty"` // admin | member | "" for own groups
}

// Account is the logged-in user as the rest of the server sees it.
type Account struct {
	ID                 int64    `json:"id"`
	Username           string   `json:"username"`
	Group              GroupRef `json:"group"`
	Status             string   `json:"status"`
	PlayerID           *int64   `json:"player_id"` // the linked player (D2)
	MustChangePassword bool     `json:"must_change_password"`
	CreatedAt          int64    `json:"created_at"`
	LastLoginAt        *int64   `json:"last_login_at"`
	// What a pending account asked for at sign-up (spec 03 §3).
	RequestedPlayerID   *int64  `json:"requested_player_id,omitempty"`
	RequestedPlayerName *string `json:"requested_player_name,omitempty"`
}

// IsApproved reports whether a is an approved account. Nil-safe.
func (a *Account) IsApproved() bool { return a != nil && a.Status == StatusApproved }

// IsAdmin reports whether a is an approved account in the Admin group. Nil-safe.
func (a *Account) IsAdmin() bool { return a.IsApproved() && a.Group.Builtin == GroupAdmin }

// Errors.
var (
	ErrInvalidCredentials = errors.New("wrong username or password")
	ErrDisabled           = errors.New("this account is disabled")
	ErrInvalidUsername    = errors.New("usernames are 3–32 characters: a–z, 0–9, _ . -")
	ErrUsernameTaken      = errors.New("this username is taken")
	ErrInvalidGroup       = errors.New("no such group, or nobody can be put into it")
)

// ThrottledError means: too many failed attempts, try again after RetryAfter.
type ThrottledError struct{ RetryAfter time.Duration }

func (e *ThrottledError) Error() string {
	return fmt.Sprintf("too many failed attempts, try again in %s", e.RetryAfter.Round(time.Second))
}

// Options configure the service.
type Options struct {
	Store  *store.Store
	Logger *slog.Logger
	Now    func() time.Time
}

// Service is the auth service. One per server.
type Service struct {
	st     *store.Store
	log    *slog.Logger
	now    func() time.Time
	thr    *throttle
	hashes chan struct{} // at most two argon2 hashes at a time (64 MiB each)

	mu     sync.Mutex
	params Params
	cache  map[string]*cached // token hash →
	groups groupCache

	dummyOnce sync.Once
	dummy     string // hash of a random password, for unknown usernames
}

type cached struct {
	session store.Session
	account store.Account
	loaded  time.Time
}

// New loads the hashing parameters and returns the service.
func New(ctx context.Context, opts Options) (*Service, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	s := &Service{st: opts.Store, log: opts.Logger.With("component", "auth"), now: opts.Now, thr: newThrottle(),
		hashes: make(chan struct{}, 2), params: DefaultParams, cache: map[string]*cached{}}
	var raw string
	if ok, err := s.st.GetSetting(ctx, store.SettingArgon2, &raw); err != nil {
		return nil, err
	} else if ok {
		p, err := ParseParams(raw)
		if err != nil {
			return nil, err
		}
		s.params = p
	}
	if err := s.loadGroups(ctx, true); err != nil {
		return nil, err
	}
	return s, nil
}

// Params returns the current hashing parameters.
func (s *Service) Params() Params {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.params
}

// SetParams stores new hashing parameters. Existing hashes are upgraded at the next login.
func (s *Service) SetParams(ctx context.Context, p Params) error {
	if err := s.st.SetSetting(ctx, store.SettingArgon2, p.String()); err != nil {
		return err
	}
	s.mu.Lock()
	s.params = p
	s.mu.Unlock()
	return nil
}

func (s *Service) hash(password string) (string, error) {
	s.hashes <- struct{}{}
	defer func() { <-s.hashes }()
	return Hash(password, s.Params())
}

func (s *Service) verify(password, phc string) (ok, outdated bool, err error) {
	s.hashes <- struct{}{}
	defer func() { <-s.hashes }()
	return Verify(password, phc, s.Params())
}

var usernameRE = regexp.MustCompile(`^[a-z0-9_.-]{3,32}$`)

// NormalizeUsername lowercases and checks a username.
func NormalizeUsername(u string) (string, error) {
	u = strings.ToLower(strings.TrimSpace(u))
	if !usernameRE.MatchString(u) {
		return "", ErrInvalidUsername
	}
	return u, nil
}

// NewAccount describes an account to create.
type NewAccount struct {
	Username           string
	Password           string
	GroupID            int64 // 0 = Member
	Status             string
	MustChangePassword bool
	// Sign-up request (pending accounts): an existing player or a new player's name.
	RequestedPlayerID   *int64
	RequestedPlayerName *string
}

// CreateAccount checks the username and password rules and creates the account.
func (s *Service) CreateAccount(ctx context.Context, na NewAccount) (Account, error) {
	u, err := NormalizeUsername(na.Username)
	if err != nil {
		return Account{}, err
	}
	if na.GroupID == 0 {
		na.GroupID = s.BuiltinGroup(GroupMember)
	}
	if g, ok := s.group(na.GroupID); !ok || g.Builtin == GroupAnybody {
		return Account{}, ErrInvalidGroup
	}
	if na.Status == "" {
		na.Status = StatusApproved
	}
	if err := CheckPassword(na.Password); err != nil {
		return Account{}, err
	}
	h, err := s.hash(na.Password)
	if err != nil {
		return Account{}, err
	}
	a := store.Account{Username: u, PasswordHash: h, GroupID: na.GroupID, Status: na.Status,
		MustChangePassword: na.MustChangePassword, CreatedAt: s.now(), RequestedPlayerID: na.RequestedPlayerID,
		RequestedPlayerName: na.RequestedPlayerName}
	a.ID, err = s.st.CreateAccount(ctx, a)
	if errors.Is(err, store.ErrExists) {
		return Account{}, ErrUsernameTaken
	} else if err != nil {
		return Account{}, err
	}
	return s.accountOf(a), nil
}

// LoginRequest is a login attempt.
type LoginRequest struct {
	Username, Password string
	IP                 string
	Kind               string // web | app
	DeviceName         string
	UserAgent          string
}

// Login checks the credentials and creates a session. The token is returned only here; the
// database keeps its hash.
func (s *Service) Login(ctx context.Context, req LoginRequest) (string, Account, error) {
	now := s.now()
	u := strings.ToLower(strings.TrimSpace(req.Username))
	keys := []string{"ip:" + req.IP, "user:" + u}
	if d := s.thr.wait(now, keys...); d > 0 {
		return "", Account{}, &ThrottledError{RetryAfter: d}
	}
	if req.Kind != KindApp {
		req.Kind = KindWeb
	}

	a, err := s.st.AccountByUsername(ctx, u)
	if errors.Is(err, store.ErrNotFound) {
		// Same work and the same answer as a wrong password (spec 03 §6).
		_, _, _ = s.verify(req.Password, s.dummyHash())
		return "", Account{}, s.loginFailed(ctx, now, req, u, keys)
	} else if err != nil {
		return "", Account{}, err
	}
	ok, outdated, err := s.verify(req.Password, a.PasswordHash)
	if err != nil {
		return "", Account{}, err
	}
	if !ok {
		return "", Account{}, s.loginFailed(ctx, now, req, u, keys)
	}
	s.thr.reset(keys...)
	if a.Status == StatusDisabled {
		return "", Account{}, ErrDisabled
	}
	if outdated {
		if h, err := s.hash(req.Password); err == nil {
			if err := s.st.SetPasswordHash(ctx, a.ID, h, a.MustChangePassword); err != nil {
				s.log.Warn("upgrading a password hash", "account", a.ID, "err", err)
			}
		}
	}
	token, err := s.newSession(ctx, a.ID, req, now)
	if err != nil {
		return "", Account{}, err
	}
	if err := s.st.TouchLogin(ctx, a.ID, now); err != nil {
		s.log.Warn("last login", "err", err)
	}
	return token, s.accountOf(a), nil
}

func (s *Service) loginFailed(ctx context.Context, now time.Time, req LoginRequest, user string, keys []string) error {
	if s.thr.fail(now, keys...) {
		s.audit(ctx, nil, req.IP, "login_throttled", user, "5 failed logins")
	}
	return ErrInvalidCredentials
}

func (s *Service) dummyHash() string {
	s.dummyOnce.Do(func() {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		s.dummy, _ = Hash(hex.EncodeToString(b), s.Params())
	})
	return s.dummy
}

func lifetime(kind string) time.Duration {
	if kind == KindApp {
		return AppLifetime
	}
	return WebLifetime
}

func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (s *Service) newSession(ctx context.Context, accountID int64, req LoginRequest, now time.Time) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	_, err := s.st.CreateSession(ctx, store.Session{
		AccountID: accountID, TokenHash: tokenHash(token), Kind: req.Kind,
		DeviceName: truncate(req.DeviceName, 80), UserAgent: truncate(req.UserAgent, 300),
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(lifetime(req.Kind)),
	})
	return token, err
}

func truncate(s string, n int) string {
	if len(s) > n {
		s = strings.ToValidUTF8(s[:n], "") // drops a rune cut in half
	}
	return s
}

// Identity is an authenticated request.
type Identity struct {
	Account   Account
	SessionID int64
	Perms     Perms
}

// Authenticate resolves a session token. It returns nil for unknown, expired or disabled
// sessions; a pending account is returned (it can log in, but is not a member, spec 03 §3).
func (s *Service) Authenticate(ctx context.Context, token string) (*Identity, error) {
	if len(token) != 43 {
		return nil, nil
	}
	h, now := tokenHash(token), s.now()

	s.mu.Lock()
	c := s.cache[h]
	s.mu.Unlock()
	if c == nil || now.Sub(c.loaded) > cacheFor {
		se, a, err := s.st.SessionByTokenHash(ctx, h)
		if errors.Is(err, store.ErrNotFound) {
			s.forget(h)
			return nil, nil
		} else if err != nil {
			return nil, err
		}
		c = &cached{session: se, account: a, loaded: now}
		s.mu.Lock()
		s.cache[h] = c
		s.mu.Unlock()
	}
	se, a := c.session, c.account
	if !now.Before(se.ExpiresAt) {
		s.forget(h)
		_ = s.st.DeleteSession(ctx, se.ID)
		return nil, nil
	}
	if a.Status == StatusDisabled {
		return nil, nil
	}
	if now.Sub(se.LastSeenAt) >= touchEvery {
		exp := now.Add(lifetime(se.Kind))
		if err := s.st.TouchSession(ctx, se.ID, now, exp); err == nil {
			s.mu.Lock()
			c.session.LastSeenAt, c.session.ExpiresAt = now, exp
			s.mu.Unlock()
		}
	}
	acct := s.accountOf(a)
	return &Identity{Account: acct, SessionID: se.ID, Perms: s.PermsOf(&acct)}, nil
}

func (s *Service) forget(hash string) {
	s.mu.Lock()
	delete(s.cache, hash)
	s.mu.Unlock()
}

// forgetAccount drops all cached sessions of an account (after revokes and account changes).
func (s *Service) forgetAccount(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for h, c := range s.cache {
		if c.account.ID == id {
			delete(s.cache, h)
		}
	}
}

// Logout ends the session of token.
func (s *Service) Logout(ctx context.Context, token string) error {
	id, err := s.Authenticate(ctx, token)
	if err != nil || id == nil {
		return err
	}
	s.forget(tokenHash(token))
	return s.st.DeleteSession(ctx, id.SessionID)
}

// ChangePassword sets a new password after checking the current one. All other sessions of the
// account are revoked (spec 03 §5).
func (s *Service) ChangePassword(ctx context.Context, id *Identity, current, next, ip string) error {
	now := s.now()
	keys := []string{"ip:" + ip, "user:" + id.Account.Username}
	if d := s.thr.wait(now, keys...); d > 0 {
		return &ThrottledError{RetryAfter: d}
	}
	a, err := s.st.Account(ctx, id.Account.ID)
	if err != nil {
		return err
	}
	ok, _, err := s.verify(current, a.PasswordHash)
	if err != nil {
		return err
	}
	if !ok {
		s.thr.fail(now, keys...)
		return ErrInvalidCredentials
	}
	if err := CheckPassword(next); err != nil {
		return err
	}
	h, err := s.hash(next)
	if err != nil {
		return err
	}
	if err := s.st.SetPasswordHash(ctx, a.ID, h, false); err != nil {
		return err
	}
	n, err := s.st.DeleteSessions(ctx, a.ID, id.SessionID)
	s.forgetAccount(a.ID)
	s.audit(ctx, &a.ID, ip, "password_changed", a.Username, fmt.Sprintf("%d other sessions revoked", n))
	return err
}

// SetPassword sets a password without knowing the old one (console, spec 03 §7). All sessions
// of the account are revoked.
func (s *Service) SetPassword(ctx context.Context, username, password string) error {
	a, err := s.st.AccountByUsername(ctx, strings.ToLower(username))
	if err != nil {
		return err
	}
	if err := CheckPassword(password); err != nil {
		return err
	}
	h, err := s.hash(password)
	if err != nil {
		return err
	}
	if err := s.st.SetPasswordHash(ctx, a.ID, h, false); err != nil {
		return err
	}
	n, err := s.st.DeleteSessions(ctx, a.ID, 0)
	s.forgetAccount(a.ID)
	s.audit(ctx, nil, "", "password_set_console", a.Username, fmt.Sprintf("%d sessions revoked", n))
	return err
}

// Audit writes an audit log entry; failures are only logged.
func (s *Service) Audit(ctx context.Context, actor *int64, ip, action, target, details string) {
	s.audit(ctx, actor, ip, action, target, details)
}

func (s *Service) audit(ctx context.Context, actor *int64, ip, action, target, details string) {
	err := s.st.Audit(context.WithoutCancel(ctx), store.AuditEntry{At: s.now(), AccountID: actor, IP: ip, Action: action, Target: target, Details: details})
	if err != nil {
		s.log.Error("audit log", "action", action, "err", err)
	}
}

// Cleanup deletes expired sessions. Call it now and then.
func (s *Service) Cleanup(ctx context.Context) {
	if err := s.st.DeleteExpiredResetCodes(ctx, s.now()); err != nil {
		s.log.Warn("deleting expired reset codes", "err", err)
	}
	if n, err := s.st.DeleteExpiredSessions(ctx, s.now()); err != nil {
		s.log.Warn("deleting expired sessions", "err", err)
	} else if n > 0 {
		s.log.Info("expired sessions deleted", "count", n)
	}
}
