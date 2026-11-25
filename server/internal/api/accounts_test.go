package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/onboarding"
	"github.com/Humbarrt/kicker/server/internal/players"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/webui"
)

type accountsFixture struct {
	srv *httptest.Server
	st  *store.Store
	as  *auth.Service
}

func newAccountsFixture(t *testing.T) *accountsFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	as := newAuth(t, st)
	if err := onboarding.CompleteFromConsole(ctx, st); err != nil {
		t.Fatal(err)
	}
	ob, _ := onboarding.New(ctx, onboarding.Options{Store: st, Auth: as})
	ps, _ := players.New(players.Options{Store: st, Auth: as, MediaDir: filepath.Join(dir, "media")})
	r := NewRouter(Options{ServerID: "sid", Name: "Office", DataDir: dir, Auth: as, Onboarding: ob, Players: ps, Bus: bus.New(),
		UI: webui.HandlerFS(fstest.MapFS{})})
	srv := httptest.NewServer(r)
	t.Cleanup(func() { srv.Close(); st.Close() })
	return &accountsFixture{srv: srv, st: st, as: as}
}

// list does a request whose answer is a JSON array.
func (c *client) list(method, path string) (int, []map[string]any) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.base+path, nil)
	req.Header.Set("Origin", c.origin)
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out []map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestSignUpAndApproval(t *testing.T) {
	f := newAccountsFixture(t)
	ctx := context.Background()
	admin := newClient(t, f.srv.URL)
	admin.bearer = makeAccount(t, f.as, "anna", auth.GroupAdmin, auth.StatusApproved)
	mia, _ := f.st.CreatePlayer(ctx, store.Player{Name: "Mia", Color: "#1e3a8a"}, 0)
	taken, _ := f.st.CreatePlayer(ctx, store.Player{Name: "Tom", Color: "#1e3a8a"}, 0)
	if err := f.st.SetAccountPlayer(ctx, 1, taken); err != nil { // anna is Tom
		t.Fatal(err)
	}

	anon := newClient(t, f.srv.URL)
	if _, info := anon.do("GET", "/api/info", ""); info["registration"] != "approval" {
		t.Errorf("info: %v", info)
	}
	// Approval mode: a new player by name …
	kim := newClient(t, f.srv.URL)
	code, body := kim.do("POST", "/api/auth/signup", `{"username":"kim","password":"blue tiger dances","player_name":" Kim "}`)
	acct, _ := body["account"].(map[string]any)
	if code != 200 || acct["status"] != "pending" || acct["requested_player_name"] != "Kim" {
		t.Fatalf("sign-up: %d %v", code, body)
	}
	kimID := int64(acct["id"].(float64))
	if _, me := kim.do("GET", "/api/auth/me", ""); me["account"].(map[string]any)["status"] != "pending" {
		t.Errorf("logged in, pending: %v", me)
	}
	// … or an existing player without an account (the admin confirms the link).
	ben := newClient(t, f.srv.URL)
	code, body = ben.do("POST", "/api/auth/signup", fmt.Sprintf(`{"username":"ben","password":"blue tiger dances","player_id":%d}`, mia))
	if code != 200 || body["account"].(map[string]any)["requested_player_id"] != float64(mia) {
		t.Fatalf("sign-up as Mia: %d %v", code, body)
	}
	benID := int64(body["account"].(map[string]any)["id"].(float64))
	for _, c := range []struct {
		body, code string
		status     int
	}{
		{fmt.Sprintf(`{"username":"xx1","password":"blue tiger dances","player_id":%d}`, taken), "player_taken", 409},
		{`{"username":"xx2","password":"blue tiger dances","player_name":"mia"}`, "player_name_taken", 409},
		{`{"username":"xx3","password":"blue tiger dances"}`, "invalid_input", 400},
		{`{"username":"kim","password":"blue tiger dances","player_name":"Other"}`, "username_taken", 409},
		{`{"username":"xx4","password":"short","player_name":"Other"}`, "password_too_short", 400},
	} {
		if code, body := anon.do("POST", "/api/auth/signup", c.body); code != c.status || body["code"] != c.code {
			t.Errorf("%s: %d %v", c.body, code, body)
		}
	}
	if _, me := admin.do("GET", "/api/auth/me", ""); me["pending_accounts"] != float64(2) {
		t.Errorf("pending badge: %v", me)
	}

	member := f.as.BuiltinGroup(auth.GroupMember)
	if code, body := admin.do("POST", fmt.Sprintf("/api/accounts/%d/approve", kimID),
		fmt.Sprintf(`{"group_id":%d,"new_player_name":"Kim"}`, member)); code != 200 || body["status"] != "approved" || body["player_id"] == nil ||
		body["requested_player_name"] != nil {
		t.Fatalf("approve: %d %v", code, body)
	}
	if code, body := admin.do("POST", fmt.Sprintf("/api/accounts/%d/approve", kimID), fmt.Sprintf(`{"group_id":%d}`, member)); code != 409 ||
		body["code"] != "not_pending" {
		t.Errorf("approve twice: %d %v", code, body)
	}
	if _, me := kim.do("GET", "/api/auth/me", ""); me["permissions"].(map[string]any)["table.score"] != float64(1) {
		t.Errorf("kim is a member now: %v", me)
	}
	if code, _ := admin.do("POST", fmt.Sprintf("/api/accounts/%d/reject", benID), ""); code != 204 {
		t.Errorf("reject: %d", code)
	}
	if _, err := f.st.Account(ctx, benID); err == nil {
		t.Error("a rejected sign-up is deleted")
	}

	// Open: approved at once into the chosen group, with a new player.
	if code, body := admin.do("PUT", "/api/settings/registration", fmt.Sprintf(`{"mode":"open","open_group_id":%d,"default_group_id":%d}`,
		f.as.BuiltinGroup(auth.GroupAdmin), member)); code != 400 || body["code"] != "invalid_registration" {
		t.Errorf("open into Admin: %d %v", code, body)
	}
	if code, _ := admin.do("PUT", "/api/settings/registration", fmt.Sprintf(`{"mode":"open","open_group_id":%d,"default_group_id":%d}`,
		member, member)); code != 200 {
		t.Fatalf("open: %d", code)
	}
	if code, body := anon.do("POST", "/api/auth/signup", fmt.Sprintf(`{"username":"xx5","password":"blue tiger dances","player_id":%d}`, mia)); code != 400 {
		t.Errorf("open sign-up as an existing player: %d %v", code, body)
	}
	lou := newClient(t, f.srv.URL)
	if code, body := lou.do("POST", "/api/auth/signup", `{"username":"lou","password":"blue tiger dances","player_name":"Lou"}`); code != 200 ||
		body["account"].(map[string]any)["status"] != "approved" {
		t.Fatalf("open sign-up: %d %v", code, body)
	}
	if _, me := lou.do("GET", "/api/auth/me", ""); me["account"].(map[string]any)["player_id"] == nil {
		t.Errorf("lou has a player: %v", me)
	}
	// Closed.
	if code, _ := admin.do("PUT", "/api/settings/registration", fmt.Sprintf(`{"mode":"closed","open_group_id":%d,"default_group_id":%d}`,
		member, member)); code != 200 {
		t.Fatal("closed")
	}
	if code, body := anon.do("POST", "/api/auth/signup", `{"username":"xx6","password":"blue tiger dances","player_name":"X"}`); code != 403 ||
		body["code"] != "registration_closed" {
		t.Errorf("closed: %d %v", code, body)
	}
	if _, info := anon.do("GET", "/api/info", ""); info["registration"] != "closed" {
		t.Errorf("info: %v", info)
	}
}

func TestResetRecoveryAndDevices(t *testing.T) {
	f := newAccountsFixture(t)
	ctx := context.Background()
	admin := newClient(t, f.srv.URL)
	if code, _ := admin.do("POST", "/api/auth/login", `{"username":"anna","password":"blue tiger dances"}`); code != 401 {
		t.Fatal("no anna yet")
	}
	makeAccount(t, f.as, "anna", auth.GroupAdmin, auth.StatusApproved)
	if code, _ := admin.do("POST", "/api/auth/login", `{"username":"anna","password":"blue tiger dances","device_name":"Laptop"}`); code != 200 {
		t.Fatal("login")
	}
	mia := newClient(t, f.srv.URL)
	makeAccount(t, f.as, "mia", auth.GroupMember, auth.StatusApproved) // + an app session
	if code, _ := mia.do("POST", "/api/auth/login", `{"username":"mia","password":"blue tiger dances"}`); code != 200 {
		t.Fatal("mia login")
	}
	miaAcct, _ := f.st.AccountByUsername(ctx, "mia")

	// My devices: two sessions, the app one can be ended; this one only by logging out.
	code, list := mia.list("GET", "/api/auth/sessions")
	if code != 200 || len(list) != 2 || list[0]["current"] != true {
		t.Fatalf("sessions: %d %v", code, list)
	}
	if code, _ := mia.do("DELETE", fmt.Sprintf("/api/auth/sessions/%d", int64(list[0]["id"].(float64))), ""); code != 400 {
		t.Errorf("end the current session: %d", code)
	}
	if code, _ := mia.do("DELETE", "/api/auth/sessions", ""); code != 204 {
		t.Errorf("end the others: %d", code)
	}
	if _, list := mia.list("GET", "/api/auth/sessions"); len(list) != 1 {
		t.Errorf("after: %v", list)
	}
	if code, _ := mia.do("DELETE", "/api/auth/sessions/99999", ""); code != 404 {
		t.Errorf("someone else's session: %d", code)
	}

	// A reset code from an admin.
	code, rc := admin.do("POST", fmt.Sprintf("/api/accounts/%d/reset-code", miaAcct.ID), "")
	if code != 200 || len(rc["code"].(string)) != 9 {
		t.Fatalf("reset code: %d %v", code, rc)
	}
	if code, body := mia.do("POST", "/api/accounts/1/reset-code", ""); code != 403 {
		t.Errorf("members can't make codes: %d %v", code, body)
	}
	other := newClient(t, f.srv.URL)
	if code, body := other.do("POST", "/api/auth/reset", `{"username":"mia","code":"AAAA-AAAA","password":"green frog jumps"}`); code != 400 ||
		body["code"] != "invalid_code" {
		t.Errorf("wrong code: %d %v", code, body)
	}
	lower := strings.ToLower(strings.ReplaceAll(rc["code"].(string), "-", " "))
	if code, body := other.do("POST", "/api/auth/reset", `{"username":"Mia","code":"`+lower+`","password":"green frog jumps"}`); code != 200 ||
		body["account"].(map[string]any)["username"] != "mia" {
		t.Fatalf("reset: %d %v", code, body)
	}
	if _, me := mia.do("GET", "/api/auth/me", ""); me["account"] != nil {
		t.Errorf("the old sessions ended: %v", me)
	}
	if code, _ := other.do("POST", "/api/auth/reset", `{"username":"mia","code":"`+lower+`","password":"green frog jumps"}`); code != 400 {
		t.Errorf("a code works once: %d", code)
	}

	// The emergency kit and the recovery key.
	if _, me := admin.do("GET", "/api/auth/me", ""); me["needs_kit"] != true {
		t.Errorf("no kit yet: %v", me)
	}
	if code, _ := admin.do("POST", "/api/settings/emergency-kit", `{"password":"wrong one"}`); code != 401 {
		t.Errorf("kit without the password: %d", code)
	}
	code, kit := admin.do("POST", "/api/settings/emergency-kit", `{"password":"blue tiger dances"}`)
	if code != 200 || kit["filename"] != "kicker-emergency-kit-office.txt" || len(strings.Fields(kit["recovery_key"].(string))) != 12 {
		t.Fatalf("kit: %d %v", code, kit)
	}
	key := kit["recovery_key"].(string)
	if _, sec := admin.do("GET", "/api/settings/security", ""); sec["recovery_key_created_at"] == nil {
		t.Errorf("security: %v", sec)
	}
	if code, _ := other.do("POST", "/api/auth/recover", `{"username":"mia","recovery_key":"`+key+`","password":"red fox runs fast"}`); code != 401 {
		t.Errorf("the key is for admins only: %d", code)
	}
	numbered := ""
	for i, w := range strings.Fields(key) {
		numbered += fmt.Sprintf("%d. %s\n", i+1, strings.ToUpper(w))
	}
	enc, _ := json.Marshal(numbered)
	code, rec := other.do("POST", "/api/auth/recover", `{"username":"anna","recovery_key":`+string(enc)+`,"password":"red fox runs fast"}`)
	if code != 200 {
		t.Fatalf("recover: %d %v", code, rec)
	}
	newKey := rec["kit"].(map[string]any)["recovery_key"].(string)
	if newKey == key || rec["kit"].(map[string]any)["backup_passphrase"] != kit["backup_passphrase"] {
		t.Errorf("a new key, the same passphrase: %v", rec)
	}
	if _, me := admin.do("GET", "/api/auth/me", ""); me["account"] != nil {
		t.Errorf("anna's old sessions ended: %v", me)
	}
	if _, me := other.do("GET", "/api/auth/me", ""); me["account"].(map[string]any)["username"] != "anna" {
		t.Errorf("logged in after recovery: %v", me)
	}
	if code, _ := newClient(t, f.srv.URL).do("POST", "/api/auth/recover", `{"username":"anna","recovery_key":"`+key+`","password":"red fox runs fast"}`); code != 401 {
		t.Errorf("the used key stops working: %d", code)
	}
}
