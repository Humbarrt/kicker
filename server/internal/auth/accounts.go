package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Humbarrt/kicker/server/internal/store"
)

// Full account handling of spec 03 (M3 task 9): registration modes, sign-up and approval,
// password reset codes, the admin recovery key and the session list ("My devices").

// Registration modes (spec 03 §3).
const (
	RegApproval = "approval" // new accounts wait for an admin
	RegOpen     = "open"     // new accounts are approved into Registration.OpenGroupID
	RegClosed   = "closed"   // only admins create accounts
)

// SettingRegistration stores the Registration.
const SettingRegistration = "registration"

// Registration is how new accounts come about.
type Registration struct {
	Mode           string `json:"mode"`
	OpenGroupID    int64  `json:"open_group_id"`    // the group of open sign-ups (not Admin)
	DefaultGroupID int64  `json:"default_group_id"` // preselected when approving
}

// Errors of this file.
var (
	ErrRegistrationClosed  = errors.New("sign-up is closed; ask an admin for an account")
	ErrInvalidRegistration = errors.New("invalid registration settings")
	ErrNotPending          = errors.New("this account isn't waiting for approval")
	ErrInvalidCode         = errors.New("wrong username or code, or the code has expired")
	ErrNoRecoveryKey       = errors.New("no recovery key has been made yet")
)

// Registration returns the registration settings (defaults: approval, Member).
func (s *Service) Registration(ctx context.Context) (Registration, error) {
	member := s.BuiltinGroup(GroupMember)
	r := Registration{Mode: RegApproval, OpenGroupID: member, DefaultGroupID: member}
	if _, err := s.st.GetSetting(ctx, SettingRegistration, &r); err != nil {
		return r, err
	}
	// A deleted group falls back to Member.
	if g, ok := s.group(r.OpenGroupID); !ok || g.Builtin == GroupAnybody || g.Builtin == GroupAdmin {
		r.OpenGroupID = member
	}
	if g, ok := s.group(r.DefaultGroupID); !ok || g.Builtin == GroupAnybody {
		r.DefaultGroupID = member
	}
	return r, nil
}

// SetRegistration changes the registration settings. Open sign-ups can't go into Admin.
func (s *Service) SetRegistration(ctx context.Context, by Actor, r Registration) (Registration, error) {
	switch r.Mode {
	case RegApproval, RegOpen, RegClosed:
	default:
		return r, ErrInvalidRegistration
	}
	if g, ok := s.group(r.OpenGroupID); !ok || g.Builtin == GroupAnybody || g.Builtin == GroupAdmin {
		return r, ErrInvalidRegistration
	}
	if g, ok := s.group(r.DefaultGroupID); !ok || g.Builtin == GroupAnybody {
		return r, ErrInvalidRegistration
	}
	if err := s.st.SetSetting(ctx, SettingRegistration, r); err != nil {
		return r, err
	}
	open, _ := s.group(r.OpenGroupID)
	def, _ := s.group(r.DefaultGroupID)
	s.auditAs(ctx, by, "registration_changed", r.Mode, fmt.Sprintf("open → %s, approve as %s", open.Name, def.Name))
	return r, nil
}

// SignUpAllowed throttles sign-ups per IP address: five freely, then with a growing delay. It
// records the attempt.
func (s *Service) SignUpAllowed(ip string) error {
	now := s.now()
	key := "signup:" + ip
	if d := s.thr.wait(now, key); d > 0 {
		return &ThrottledError{RetryAfter: d}
	}
	s.thr.fail(now, key)
	return nil
}

// PendingCount counts accounts waiting for approval.
func (s *Service) PendingCount(ctx context.Context) (int, error) { return s.st.CountPending(ctx) }

// Approve approves a pending account into a group, linked to a player (0 = none). Linking to an
// existing player is exactly the confirmation spec 03 §3 asks for.
func (s *Service) Approve(ctx context.Context, by Actor, accountID, groupID, playerID int64) error {
	g, ok := s.group(groupID)
	if !ok || g.Builtin == GroupAnybody {
		return ErrInvalidGroup
	}
	a, err := s.st.Account(ctx, accountID)
	if err != nil {
		return err
	}
	if a.Status != StatusPending {
		return ErrNotPending
	}
	if playerID != 0 {
		p, err := s.st.Player(ctx, playerID)
		if errors.Is(err, store.ErrNotFound) {
			return ErrInvalidPlayer
		} else if err != nil {
			return err
		}
		if p.IsGuest {
			return ErrInvalidPlayer
		}
	}
	switch err := s.st.ApproveAccount(ctx, accountID, groupID, playerID); {
	case errors.Is(err, store.ErrExists):
		return ErrPlayerTaken
	case errors.Is(err, store.ErrNotFound):
		return ErrNotPending
	case err != nil:
		return err
	}
	s.forgetAccount(accountID)
	s.auditAs(ctx, by, "account_approved", a.Username, fmt.Sprintf("%s, player %d", g.Name, playerID))
	return nil
}

// Reject deletes a pending account (a refused sign-up).
func (s *Service) Reject(ctx context.Context, by Actor, accountID int64) error {
	a, err := s.st.Account(ctx, accountID)
	if err != nil {
		return err
	}
	if err := s.st.DeletePendingAccount(ctx, accountID); errors.Is(err, store.ErrNotFound) {
		return ErrNotPending
	} else if err != nil {
		return err
	}
	s.forgetAccount(accountID)
	s.auditAs(ctx, by, "signup_rejected", a.Username, "")
	return nil
}

// --- reset codes (spec 03 §7) ------------------------------------------------------------------

// ResetCodeLifetime is how long a reset code is valid.
const ResetCodeLifetime = 24 * time.Hour

// codeAlphabet has no 0/O, 1/I/L, so a code read aloud or from a screen can't be mistaken.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func newCode() (string, error) {
	b := make([]byte, 8)
	for i := range b {
		var r [2]byte
		for {
			if _, err := rand.Read(r[:]); err != nil {
				return "", err
			}
			// Rejection sampling keeps every letter equally likely.
			if v := binary.BigEndian.Uint16(r[:]); int(v) < 65536-65536%len(codeAlphabet) {
				b[i] = codeAlphabet[int(v)%len(codeAlphabet)]
				break
			}
		}
	}
	return string(b[:4]) + "-" + string(b[4:]), nil
}

// normalizeCode accepts lower case, spaces and dashes.
func normalizeCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToUpper(strings.TrimSpace(code)))
}

func codeHash(code string) string {
	h := sha256.Sum256([]byte(normalizeCode(code)))
	return hex.EncodeToString(h[:])
}

// CreateResetCode makes a one-time code for an account's password (by an admin, or the console
// with a zero Actor). It replaces an older code; the code itself is only returned here.
func (s *Service) CreateResetCode(ctx context.Context, by Actor, accountID int64) (string, time.Time, error) {
	a, err := s.st.Account(ctx, accountID)
	if err != nil {
		return "", time.Time{}, err
	}
	code, err := newCode()
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now()
	rc := store.ResetCode{AccountID: a.ID, CodeHash: codeHash(code), ExpiresAt: now.Add(ResetCodeLifetime), CreatedAt: now}
	if by.AccountID != 0 {
		rc.CreatedBy = &by.AccountID
	}
	if err := s.st.SetResetCode(ctx, rc); err != nil {
		return "", time.Time{}, err
	}
	s.auditAs(ctx, by, "reset_code_created", a.Username, "valid 24 h")
	return code, rc.ExpiresAt, nil
}

// ResetPassword sets a new password with a reset code. All sessions of the account end. Unknown
// usernames, wrong and expired codes get the same answer; attempts are throttled like logins.
func (s *Service) ResetPassword(ctx context.Context, username, code, password, ip string) error {
	now := s.now()
	u := strings.ToLower(strings.TrimSpace(username))
	keys := []string{"ip:" + ip, "reset:" + u}
	if d := s.thr.wait(now, keys...); d > 0 {
		return &ThrottledError{RetryAfter: d}
	}
	fail := func() error {
		if s.thr.fail(now, keys...) {
			s.audit(ctx, nil, ip, "reset_throttled", u, "5 wrong codes")
		}
		return ErrInvalidCode
	}
	a, err := s.st.AccountByUsername(ctx, u)
	if errors.Is(err, store.ErrNotFound) {
		return fail()
	} else if err != nil {
		return err
	}
	rc, err := s.st.ResetCodeOf(ctx, a.ID)
	if errors.Is(err, store.ErrNotFound) {
		return fail()
	} else if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(rc.CodeHash), []byte(codeHash(code))) != 1 || !now.Before(rc.ExpiresAt) {
		return fail()
	}
	if err := CheckPassword(password); err != nil {
		return err // the code stays valid: try again with a better password
	}
	h, err := s.hash(password)
	if err != nil {
		return err
	}
	n, err := s.st.UseResetCode(ctx, a.ID, h)
	if err != nil {
		return err
	}
	s.thr.reset(keys...)
	s.forgetAccount(a.ID)
	s.audit(ctx, &a.ID, ip, "password_reset", a.Username, fmt.Sprintf("with a reset code, %d sessions ended", n))
	return nil
}

// --- the recovery key (spec 03 §7, D18) --------------------------------------------------------

//go:embed bip39-english.txt
var wordList string

// Words is the BIP39 English word list: 2048 words, 11 bits each.
var Words = strings.Fields(wordList)

// RandomWords returns n words picked uniformly from Words (12 words = 132 bits).
func RandomWords(n int) (string, error) {
	out := make([]string, n)
	for i := range out {
		var b [2]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		out[i] = Words[int(binary.BigEndian.Uint16(b[:]))%len(Words)] // 65536 is a multiple of 2048
	}
	return strings.Join(out, " "), nil
}

// normalizeWords accepts any case and spacing, and numbered lists ("1. abandon 2. ability").
func normalizeWords(s string) string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.Trim(w, ".,;:()")
		if w != "" && strings.Trim(w, "0123456789") != "" {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}

// RecoveryKeyWords is the length of the recovery key.
const RecoveryKeyWords = 12

// NewRecoveryKey makes a new recovery key and stores its hash; the old one stops working. The
// key itself is only returned here.
func (s *Service) NewRecoveryKey(ctx context.Context, by Actor) (string, error) {
	key, err := RandomWords(RecoveryKeyWords)
	if err != nil {
		return "", err
	}
	h, err := s.hash(key)
	if err != nil {
		return "", err
	}
	if err := s.st.SetRecoveryKey(ctx, h, s.now()); err != nil {
		return "", err
	}
	s.auditAs(ctx, by, "recovery_key_created", "", "")
	return key, nil
}

// RecoveryKeyCreated returns when the current recovery key was made; nil if there is none.
func (s *Service) RecoveryKeyCreated(ctx context.Context) (*time.Time, error) {
	_, created, ok, err := s.st.RecoveryKey(ctx)
	if err != nil || !ok {
		return nil, err
	}
	return &created, nil
}

// Recover sets a new password for an admin with the recovery key. Every session of that admin
// ends and a new recovery key replaces the used one, because it may have been seen; it is
// returned and must be saved. Wrong usernames (not an approved admin) and wrong keys get the same
// answer, and attempts are throttled.
func (s *Service) Recover(ctx context.Context, username, key, password, ip string) (string, error) {
	now := s.now()
	u := strings.ToLower(strings.TrimSpace(username))
	keys := []string{"ip:" + ip, "recovery"}
	if d := s.thr.wait(now, keys...); d > 0 {
		return "", &ThrottledError{RetryAfter: d}
	}
	hash, _, ok, err := s.st.RecoveryKey(ctx)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNoRecoveryKey
	}
	match, _, err := s.verify(normalizeWords(key), hash)
	if err != nil {
		return "", err
	}
	a, aerr := s.st.AccountByUsername(ctx, u)
	if aerr != nil && !errors.Is(aerr, store.ErrNotFound) {
		return "", aerr
	}
	isAdmin := false
	if aerr == nil {
		acct := s.accountOf(a)
		isAdmin = acct.IsAdmin()
	}
	if !match || !isAdmin {
		if s.thr.fail(now, keys...) {
			s.audit(ctx, nil, ip, "recovery_throttled", u, "5 failed attempts")
		}
		return "", ErrInvalidCredentials
	}
	if err := CheckPassword(password); err != nil {
		return "", err
	}
	h, err := s.hash(password)
	if err != nil {
		return "", err
	}
	if err := s.st.SetPasswordHash(ctx, a.ID, h, false); err != nil {
		return "", err
	}
	n, err := s.st.DeleteSessions(ctx, a.ID, 0)
	if err != nil {
		return "", err
	}
	s.thr.reset(keys...)
	s.forgetAccount(a.ID)
	s.audit(ctx, &a.ID, ip, "recovery_key_used", a.Username, fmt.Sprintf("password set, %d sessions ended", n))
	return s.NewRecoveryKey(ctx, Actor{AccountID: a.ID, IP: ip})
}

// VerifyPassword checks the password of the caller (to confirm sensitive actions such as a new
// emergency kit). Failures are throttled like logins.
func (s *Service) VerifyPassword(ctx context.Context, id *Identity, password, ip string) error {
	now := s.now()
	keys := []string{"ip:" + ip, "user:" + id.Account.Username}
	if d := s.thr.wait(now, keys...); d > 0 {
		return &ThrottledError{RetryAfter: d}
	}
	a, err := s.st.Account(ctx, id.Account.ID)
	if err != nil {
		return err
	}
	ok, _, err := s.verify(password, a.PasswordHash)
	if err != nil {
		return err
	}
	if !ok {
		s.thr.fail(now, keys...)
		return ErrInvalidCredentials
	}
	return nil
}

// --- My devices (spec 03 §5) -------------------------------------------------------------------

// SessionView is one of the caller's sessions.
type SessionView struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"` // web | app
	DeviceName string `json:"device_name"`
	UserAgent  string `json:"user_agent"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	Current    bool   `json:"current"` // the session of this request
}

// Sessions lists the caller's sessions.
func (s *Service) Sessions(ctx context.Context, id *Identity) ([]SessionView, error) {
	list, err := s.st.Sessions(ctx, id.Account.ID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := []SessionView{}
	for _, se := range list {
		if !now.Before(se.ExpiresAt) {
			continue
		}
		out = append(out, SessionView{ID: se.ID, Kind: se.Kind, DeviceName: se.DeviceName, UserAgent: se.UserAgent,
			CreatedAt: se.CreatedAt.UnixMilli(), LastSeenAt: se.LastSeenAt.UnixMilli(), Current: se.ID == id.SessionID})
	}
	return out, nil
}

// RevokeSession ends one of the caller's sessions (0 = all but the current one).
func (s *Service) RevokeSession(ctx context.Context, id *Identity, sessionID int64, ip string) error {
	a := id.Account
	if sessionID == 0 {
		n, err := s.st.DeleteSessions(ctx, a.ID, id.SessionID)
		if err != nil {
			return err
		}
		s.forgetAccount(a.ID)
		s.audit(ctx, &a.ID, ip, "sessions_revoked", a.Username, fmt.Sprintf("%d other sessions", n))
		return nil
	}
	if err := s.st.DeleteSessionOf(ctx, a.ID, sessionID); err != nil {
		return err
	}
	s.forgetAccount(a.ID)
	s.audit(ctx, &a.ID, ip, "session_revoked", a.Username, fmt.Sprintf("session %d", sessionID))
	return nil
}
