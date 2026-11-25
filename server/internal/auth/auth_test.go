package auth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/store"
)

// fast parameters keep the tests quick; the real ones are calibrated during onboarding.
var fast = Params{Memory: 8 * 1024, Time: 1, Threads: 1}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newService(t *testing.T) (*Service, *store.Store, *clock) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	clk := &clock{t: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	s, err := New(ctx, Options{Store: st, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetParams(ctx, fast); err != nil {
		t.Fatal(err)
	}
	return s, st, clk
}

func TestHashAndVerify(t *testing.T) {
	h, err := Hash("correct horse battery", fast)
	if err != nil || !strings.HasPrefix(h, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("%s %v", h, err)
	}
	if ok, outdated, err := Verify("correct horse battery", h, fast); !ok || outdated || err != nil {
		t.Errorf("verify: %v %v %v", ok, outdated, err)
	}
	if ok, _, _ := Verify("correct horse batterY", h, fast); ok {
		t.Error("wrong password accepted")
	}
	if _, outdated, _ := Verify("correct horse battery", h, DefaultParams); !outdated {
		t.Error("other parameters must be reported as outdated")
	}
	for _, bad := range []string{"", "$argon2i$v=19$m=8192,t=1,p=1$c2FsdA$a2V5", "$argon2id$v=19$m=1,t=1,p=1$c2FsdA$a2V5", "$argon2id$v=19$m=8192,t=1,p=1$!!$a2V5"} {
		if _, _, err := Verify("x", bad, fast); err == nil {
			t.Errorf("malformed hash accepted: %q", bad)
		}
	}
	h2, _ := Hash("correct horse battery", fast)
	if h == h2 {
		t.Error("salts must differ")
	}
}

func TestPasswordRules(t *testing.T) {
	for pw, want := range map[string]error{
		"short":                  ErrPasswordTooShort,
		"qwertyuiop":             ErrPasswordCommon,
		"QWERTYUIOP":             ErrPasswordCommon,
		"1234567890":             ErrPasswordCommon,
		"blue tiger dances":      nil,
		"ääääääääää":             nil, // 10 characters, 20 bytes
		strings.Repeat("x", 201): ErrPasswordTooLong,
	} {
		if err := CheckPassword(pw); !errors.Is(err, want) {
			t.Errorf("%q: %v, want %v", pw, err, want)
		}
	}
	if len(common) != 10000 {
		t.Errorf("common list has %d entries", len(common))
	}
}

func TestParams(t *testing.T) {
	p, err := ParseParams(DefaultParams.String())
	if err != nil || p != DefaultParams {
		t.Errorf("%+v %v", p, err)
	}
	if _, err := ParseParams("m=1024,t=1,p=1"); err == nil {
		t.Error("too little memory accepted")
	}
	if testing.Short() {
		return
	}
	c := Calibrate(50 * time.Millisecond)
	if c.Memory < 16*1024 || c.Time < 1 || c.Time > 10 || c.Threads != 4 {
		t.Errorf("calibrated %+v", c)
	}
}

func TestLoginAndSessions(t *testing.T) {
	ctx := context.Background()
	s, st, clk := newService(t)
	if _, err := s.CreateAccount(ctx, NewAccount{Username: " Anna ", Password: "blue tiger dances", GroupID: s.BuiltinGroup(GroupAdmin)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAccount(ctx, NewAccount{Username: "ANNA", Password: "blue tiger dances"}); !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("case-insensitive duplicate: %v", err)
	}
	for _, u := range []string{"an", "anna maria", "änna", strings.Repeat("a", 33)} {
		if _, err := s.CreateAccount(ctx, NewAccount{Username: u, Password: "blue tiger dances"}); !errors.Is(err, ErrInvalidUsername) {
			t.Errorf("username %q: %v", u, err)
		}
	}
	if _, err := s.CreateAccount(ctx, NewAccount{Username: "bob", Password: "blue tiger dances", GroupID: 999}); !errors.Is(err, ErrInvalidGroup) {
		t.Errorf("unknown group: %v", err)
	}
	if _, err := s.CreateAccount(ctx, NewAccount{Username: "bob", Password: "blue tiger dances", GroupID: s.BuiltinGroup(GroupAnybody)}); !errors.Is(err, ErrInvalidGroup) {
		t.Errorf("anybody group: %v", err)
	}

	if _, _, err := s.Login(ctx, LoginRequest{Username: "anna", Password: "wrong password", IP: "1.2.3.4"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong password: %v", err)
	}
	if _, _, err := s.Login(ctx, LoginRequest{Username: "nobody", Password: "blue tiger dances", IP: "1.2.3.4"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("unknown user must look like a wrong password: %v", err)
	}
	web, a, err := s.Login(ctx, LoginRequest{Username: "ANNA", Password: "blue tiger dances", IP: "1.2.3.4", UserAgent: "Firefox"})
	if err != nil || a.Username != "anna" || !a.IsAdmin() || len(web) != 43 {
		t.Fatalf("login: %v %+v %q", err, a, web)
	}
	app, _, _ := s.Login(ctx, LoginRequest{Username: "anna", Password: "blue tiger dances", Kind: KindApp, DeviceName: "Pixel"})

	id, err := s.Authenticate(ctx, web)
	if err != nil || id == nil || id.Account.ID != a.ID {
		t.Fatalf("authenticate: %+v %v", id, err)
	}
	if id, _ := s.Authenticate(ctx, web[:42]+"x"); id != nil {
		t.Error("unknown token accepted")
	}
	if id, _ := s.Authenticate(ctx, "short"); id != nil {
		t.Error("malformed token accepted")
	}

	// web sessions renew on use and expire after 30 days without
	clk.add(29 * 24 * time.Hour)
	if id, _ := s.Authenticate(ctx, web); id == nil {
		t.Fatal("29 days: still valid")
	}
	clk.add(29 * 24 * time.Hour)
	if id, _ := s.Authenticate(ctx, web); id == nil {
		t.Fatal("renewed: still valid")
	}
	clk.add(31 * 24 * time.Hour)
	if id, _ := s.Authenticate(ctx, web); id != nil {
		t.Error("30 days unused: expired")
	}
	if id, _ := s.Authenticate(ctx, app); id == nil {
		t.Error("app sessions last a year")
	}

	// a password change keeps the current session and ends the others
	web2, _, _ := s.Login(ctx, LoginRequest{Username: "anna", Password: "blue tiger dances"})
	id2, _ := s.Authenticate(ctx, web2)
	if err := s.ChangePassword(ctx, id2, "wrong one!!", "green tiger dances", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong current password: %v", err)
	}
	if err := s.ChangePassword(ctx, id2, "blue tiger dances", "qwertyuiop", ""); !errors.Is(err, ErrPasswordCommon) {
		t.Errorf("weak new password: %v", err)
	}
	if err := s.ChangePassword(ctx, id2, "blue tiger dances", "green tiger dances", ""); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.Authenticate(ctx, web2); id == nil {
		t.Error("the current session stays")
	}
	if id, _ := s.Authenticate(ctx, app); id != nil {
		t.Error("other sessions must be revoked")
	}
	if _, _, err := s.Login(ctx, LoginRequest{Username: "anna", Password: "green tiger dances"}); err != nil {
		t.Errorf("new password: %v", err)
	}

	// logout
	if err := s.Logout(ctx, web2); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.Authenticate(ctx, web2); id != nil {
		t.Error("logged out")
	}

	// console: set password ends all sessions
	web3, _, _ := s.Login(ctx, LoginRequest{Username: "anna", Password: "green tiger dances"})
	if err := s.SetPassword(ctx, "anna", "red tiger dances"); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.Authenticate(ctx, web3); id != nil {
		t.Error("console password reset must end all sessions")
	}
	log, _ := st.AuditLog(ctx, 10)
	if len(log) != 2 || log[0].Action != "password_set_console" || log[1].Action != "password_changed" {
		t.Errorf("audit: %+v", log)
	}
}

func TestPendingAndDisabled(t *testing.T) {
	ctx := context.Background()
	s, st, _ := newService(t)
	p, _ := s.CreateAccount(ctx, NewAccount{Username: "pat", Password: "blue tiger dances", Status: StatusPending})
	tok, a, err := s.Login(ctx, LoginRequest{Username: "pat", Password: "blue tiger dances"})
	if err != nil || a.IsApproved() {
		t.Fatalf("pending accounts can log in but aren't members: %+v %v", a, err)
	}
	id, _ := s.Authenticate(ctx, tok)
	if id == nil || id.Account.IsApproved() || id.Account.Status != StatusPending {
		t.Errorf("pending identity: %+v", id)
	}
	if err := s.SetStatus(ctx, Actor{}, p.ID, StatusApproved); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.Authenticate(ctx, tok); id == nil || !id.Account.IsApproved() {
		t.Errorf("approval applies to the open session at once: %+v", id)
	}
	if err := s.SetStatus(ctx, Actor{}, p.ID, StatusDisabled); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.Authenticate(ctx, tok); id != nil {
		t.Error("disabling ends the sessions")
	}
	if _, _, err := s.Login(ctx, LoginRequest{Username: "pat", Password: "blue tiger dances"}); !errors.Is(err, ErrDisabled) {
		t.Errorf("disabled login: %v", err)
	}
	if n, _ := st.CountAdmins(ctx); n != 0 {
		t.Errorf("admins: %d", n)
	}
}

func TestThrottle(t *testing.T) {
	ctx := context.Background()
	s, st, clk := newService(t)
	_, _ = s.CreateAccount(ctx, NewAccount{Username: "anna", Password: "blue tiger dances"})
	for i := 0; i < 5; i++ {
		if _, _, err := s.Login(ctx, LoginRequest{Username: "anna", Password: "nope nope nope", IP: "10.0.0.1"}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	// now blocked for 1 s – even with the right password, and from another IP for this user
	var th *ThrottledError
	if _, _, err := s.Login(ctx, LoginRequest{Username: "anna", Password: "blue tiger dances", IP: "10.0.0.2"}); !errors.As(err, &th) || th.RetryAfter != time.Second {
		t.Fatalf("throttled: %v", err)
	}
	clk.add(time.Second)
	_, _, _ = s.Login(ctx, LoginRequest{Username: "anna", Password: "nope nope nope", IP: "10.0.0.1"})
	if _, _, err := s.Login(ctx, LoginRequest{Username: "anna", Password: "blue tiger dances"}); !errors.As(err, &th) || th.RetryAfter != 2*time.Second {
		t.Fatalf("doubled: %v", err)
	}
	clk.add(2 * time.Second)
	if _, _, err := s.Login(ctx, LoginRequest{Username: "anna", Password: "blue tiger dances", IP: "10.0.0.3"}); err != nil {
		t.Fatalf("after the wait: %v", err)
	}
	// success reset the user key
	if _, _, err := s.Login(ctx, LoginRequest{Username: "anna", Password: "nope nope nope", IP: "10.0.0.3"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("reset: %v", err)
	}
	log, _ := st.AuditLog(ctx, 10)
	if len(log) != 1 || log[0].Action != "login_throttled" || log[0].Target != "anna" {
		t.Errorf("one audit entry per burst: %+v", log)
	}

	th2 := newThrottle()
	now := time.Now()
	for i := 0; i < 40; i++ {
		th2.fail(now, "k")
	}
	if d := th2.wait(now, "k"); d != 15*time.Minute {
		t.Errorf("cap: %v", d)
	}
}
