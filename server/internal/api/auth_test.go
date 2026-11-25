package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/onboarding"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/webui"
)

var fastParams = auth.Params{Memory: 8 * 1024, Time: 1, Threads: 1}

func newAuth(t *testing.T, st *store.Store) *auth.Service {
	t.Helper()
	as, err := auth.New(context.Background(), auth.Options{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	if err := as.SetParams(context.Background(), fastParams); err != nil {
		t.Fatal(err)
	}
	return as
}

// makeAccount creates an account and returns an app (bearer) token for it.
func makeAccount(t *testing.T, as *auth.Service, user, group, status string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := as.CreateAccount(ctx, auth.NewAccount{Username: user, Password: "blue tiger dances", GroupID: as.BuiltinGroup(group), Status: status}); err != nil {
		t.Fatal(err)
	}
	token, _, err := as.Login(ctx, auth.LoginRequest{Username: user, Password: "blue tiger dances", Kind: auth.KindApp})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// client is a browser: a cookie jar and an Origin header on every request.
type client struct {
	t      *testing.T
	base   string
	http   *http.Client
	origin string // "" = send none
	bearer string
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar}, origin: base}
}

func (c *client) do(method, path, body string) (int, map[string]any) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.base+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

type authFixture struct {
	srv *httptest.Server
	st  *store.Store
	as  *auth.Service
	ob  *onboarding.Onboarding
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	as := newAuth(t, st)
	ob, err := onboarding.New(ctx, onboarding.Options{Store: st, Auth: as, Calibrate: func() auth.Params { return fastParams }})
	if err != nil {
		t.Fatal(err)
	}
	ui := webui.HandlerFS(fstest.MapFS{"index.html": {Data: []byte("<html>index</html>")}})
	r := NewRouter(Options{ServerID: "sid", Auth: as, Onboarding: ob, Modules: fakeHost{}, UI: ui})
	srv := httptest.NewServer(r)
	t.Cleanup(func() { srv.Close(); st.Close() })
	return &authFixture{srv: srv, st: st, as: as, ob: ob}
}

func TestOnboardingFlow(t *testing.T) {
	f := newAuthFixture(t)
	a := newClient(t, f.srv.URL)
	b := newClient(t, f.srv.URL)

	code, info := a.do("GET", "/api/info", "")
	if code != 200 || info["onboarding_done"] != false || info["server_id"] != "sid" {
		t.Fatalf("info: %d %v", code, info)
	}
	// everything else is closed until setup is done
	if code, body := a.do("GET", "/api/modules", ""); code != 503 || body["code"] != "onboarding" {
		t.Errorf("gate: %d %v", code, body)
	}
	if code, body := a.do("POST", "/api/auth/login", `{"username":"x","password":"y"}`); code != 401 {
		t.Errorf("login during setup is allowed (step 4 needs it): %d %v", code, body)
	}
	// the web UI itself is served
	if resp, err := http.Get(f.srv.URL + "/"); err != nil || resp.StatusCode != 200 {
		t.Errorf("UI during setup: %v %v", resp, err)
	}

	if code, st := a.do("GET", "/api/onboarding", ""); code != 200 || st["step"] != "language" || st["busy"] != false {
		t.Errorf("status: %v", st)
	}
	if code, _ := a.do("POST", "/api/onboarding/start", ""); code != 200 {
		t.Fatalf("start: %d", code)
	}
	if code, body := b.do("POST", "/api/onboarding/start", ""); code != 409 || body["code"] != "onboarding_busy" {
		t.Errorf("second browser: %d %v", code, body)
	}
	if _, st := b.do("GET", "/api/onboarding", ""); st["busy"] != true || st["mine"] != false {
		t.Errorf("second browser status: %v", st)
	}
	// the claim cookie needs a matching Origin
	a.origin = ""
	if code, body := a.do("PUT", "/api/onboarding/language", `{"language":"de"}`); code != 403 || body["code"] != "csrf" {
		t.Errorf("no origin: %d %v", code, body)
	}
	a.origin = "http://evil.example"
	if code, _ := a.do("PUT", "/api/onboarding/language", `{"language":"de"}`); code != 403 {
		t.Errorf("foreign origin: %d", code)
	}
	a.origin = f.srv.URL
	if code, st := a.do("PUT", "/api/onboarding/language", `{"language":"de"}`); code != 200 || st["step"] != "admin" {
		t.Fatalf("language: %d %v", code, st)
	}
	if code, body := a.do("POST", "/api/onboarding/admin", `{"username":"anna","password":"short"}`); code != 400 || body["code"] != "password_too_short" {
		t.Errorf("short password: %d %v", code, body)
	}
	code, body := a.do("POST", "/api/onboarding/admin", `{"username":"Anna","password":"blue tiger dances"}`)
	if acct, _ := body["account"].(map[string]any); code != 200 || acct["group"].(map[string]any)["builtin"] != "admin" || acct["username"] != "anna" {
		t.Fatalf("admin: %d %v", code, body)
	}
	// Logged in via cookie; step 4, the emergency kit, is next – only for this admin.
	if _, me := a.do("GET", "/api/auth/me", ""); me["account"] == nil || me["needs_kit"] != true {
		t.Errorf("me after step 3: %v", me)
	}
	if code, st := b.do("GET", "/api/onboarding", ""); code != 200 || st["step"] != "kit" || st["mine"] != false {
		t.Errorf("status at the kit: %d %v", code, st)
	}
	if code, body := b.do("POST", "/api/onboarding/kit", ""); code != 401 {
		t.Errorf("kit for another browser: %d %v", code, body)
	}
	if code, body := a.do("POST", "/api/onboarding/finish", ""); code != 409 || body["code"] != "no_recovery_key" {
		t.Errorf("finish without a kit: %d %v", code, body)
	}
	code, kit := a.do("POST", "/api/onboarding/kit", "")
	if code != 200 || len(strings.Fields(kit["recovery_key"].(string))) != 12 || len(strings.Fields(kit["backup_passphrase"].(string))) != 8 ||
		!strings.Contains(kit["text"].(string), kit["recovery_key"].(string)[:5]) {
		t.Fatalf("kit: %d %v", code, kit)
	}
	if code, st := a.do("POST", "/api/onboarding/finish", ""); code != 200 || st["done"] != true {
		t.Fatalf("finish: %d %v", code, st)
	}
	if _, me := a.do("GET", "/api/auth/me", ""); me["needs_kit"] != nil {
		t.Errorf("me after setup: %v", me)
	}
	if _, info := a.do("GET", "/api/info", ""); info["onboarding_done"] != true {
		t.Errorf("info after setup: %v", info)
	}
	if code, _ := a.do("GET", "/api/modules", ""); code != 200 {
		t.Errorf("admin sees modules: %d", code)
	}
	if code, body := b.do("POST", "/api/onboarding/start", ""); code != 409 || body["code"] != "onboarding_done" {
		t.Errorf("start after done: %d %v", code, body)
	}

	// logout
	if code, _ := a.do("POST", "/api/auth/logout", ""); code != 204 {
		t.Errorf("logout: %d", code)
	}
	if _, me := a.do("GET", "/api/auth/me", ""); me["account"] != nil {
		t.Errorf("me after logout: %v", me)
	}
	if code, _ := a.do("GET", "/api/modules", ""); code != 401 {
		t.Errorf("modules after logout: %d", code)
	}
}

func TestLoginAndPermissions(t *testing.T) {
	f := newAuthFixture(t)
	if err := onboarding.CompleteFromConsole(context.Background(), f.st); err != nil {
		t.Fatal(err)
	}
	makeAccount(t, f.as, "admin", auth.GroupAdmin, auth.StatusApproved)
	member := makeAccount(t, f.as, "mia", auth.GroupMember, auth.StatusApproved)
	pending := makeAccount(t, f.as, "pete", auth.GroupMember, auth.StatusPending)

	web := newClient(t, f.srv.URL)
	if code, body := web.do("POST", "/api/auth/login", `{"username":"admin","password":"wrong password"}`); code != 401 || body["code"] != "invalid_credentials" {
		t.Errorf("wrong password: %d %v", code, body)
	}
	if code, body := web.do("POST", "/api/auth/login", `{"username":"admin","password":"blue tiger dances"}`); code != 200 || body["token"] != nil {
		t.Errorf("web login must not return the token: %d %v", code, body)
	}
	app := newClient(t, f.srv.URL)
	app.origin = ""
	code, body := app.do("POST", "/api/auth/login", `{"username":"mia","password":"blue tiger dances","kind":"app","device_name":"Pixel"}`)
	if tok, _ := body["token"].(string); code != 200 || len(tok) != 43 {
		t.Fatalf("app login: %d %v", code, body)
	}

	// permission matrix for the routes that exist so far
	type who struct {
		name   string
		bearer string
	}
	people := []who{{"anonymous", ""}, {"pending", pending}, {"member", member}}
	for _, p := range people {
		c := newClient(t, f.srv.URL)
		c.bearer = p.bearer
		code, _ := c.do("GET", "/api/modules", "")
		want := map[string]int{"anonymous": 401, "pending": 403, "member": 403}[p.name]
		if code != want {
			t.Errorf("%s GET /api/modules: %d, want %d", p.name, code, want)
		}
		_, me := c.do("GET", "/api/auth/me", "")
		if (me["account"] == nil) != (p.name == "anonymous") {
			t.Errorf("%s me: %v", p.name, me)
		}
	}
	if code, _ := web.do("GET", "/api/modules", ""); code != 200 {
		t.Errorf("admin web: %d", code)
	}

	// cookie + missing Origin is refused, bearer + missing Origin is fine
	web.origin = ""
	if code, _ := web.do("POST", "/api/auth/password", `{"current":"blue tiger dances","new":"green tiger dances"}`); code != 403 {
		t.Errorf("cookie without origin: %d", code)
	}
	web.origin = f.srv.URL
	if code, body := web.do("POST", "/api/auth/password", `{"current":"nope nope nope","new":"green tiger dances"}`); code != 401 {
		t.Errorf("wrong current password: %d %v", code, body)
	}
	if code, _ := web.do("POST", "/api/auth/password", `{"current":"blue tiger dances","new":"green tiger dances"}`); code != 204 {
		t.Errorf("password change: %d", code)
	}
	anon := newClient(t, f.srv.URL)
	if code, _ := anon.do("POST", "/api/auth/password", `{}`); code != 401 {
		t.Errorf("anonymous password change: %d", code)
	}

	// throttling answers 429 with Retry-After
	for i := 0; i < 5; i++ {
		anon.do("POST", "/api/auth/login", `{"username":"mia","password":"nope nope nope"}`)
	}
	req, _ := http.NewRequest("POST", f.srv.URL+"/api/auth/login", strings.NewReader(`{"username":"mia","password":"blue tiger dances"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" {
		t.Errorf("throttled: %d %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" || resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("security headers: %v", resp.Header)
	}
}

func TestTableNeedsMember(t *testing.T) {
	f := newTableFixture(t)
	anon := newClient(t, f.srv.URL)
	if code, body := anon.do("POST", "/api/table/start", ""); code != 401 || body["code"] != "login_required" {
		t.Errorf("anonymous start: %d %v", code, body)
	}
	if code, _ := anon.do("GET", "/api/table", ""); code != 200 {
		t.Errorf("anyone can watch: %d", code)
	}
}
