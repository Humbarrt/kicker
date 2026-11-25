package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/onboarding"
)

func TestGroupAdministration(t *testing.T) {
	f := newAuthFixture(t)
	_ = onboarding.CompleteFromConsole(context.Background(), f.st)
	admin := newClient(t, f.srv.URL)
	admin.bearer = makeAccount(t, f.as, "anna", auth.GroupAdmin, auth.StatusApproved)
	mia := newClient(t, f.srv.URL)
	mia.bearer = makeAccount(t, f.as, "mia", auth.GroupMember, auth.StatusApproved)
	anon := newClient(t, f.srv.URL)

	// the catalog is public, the groups are not
	if code, _ := anon.do("GET", "/api/permissions", ""); code != 200 {
		t.Errorf("catalog: %d", code)
	}
	if code, _ := mia.do("GET", "/api/groups", ""); code != 403 {
		t.Errorf("member lists groups: %d", code)
	}

	// me: effective permissions
	_, me := mia.do("GET", "/api/auth/me", "")
	perms := me["permissions"].(map[string]any)
	if me["admin"] != false || perms["table.score"] != 1.0 || perms["games.edit"] != 1.0 || perms["games.edit_days"] != 7.0 || perms["presets.manage"] != 0.0 {
		t.Errorf("member me: %v", me)
	}
	_, me = anon.do("GET", "/api/auth/me", "")
	if p := me["permissions"].(map[string]any); p["view.live"] != 1.0 || p["table.score"] != 0.0 {
		t.Errorf("anybody me: %v", me)
	}
	_, me = admin.do("GET", "/api/auth/me", "")
	if p := me["permissions"].(map[string]any); me["admin"] != true || p["games.edit"] != 2.0 || p["games.edit_days"] != -1.0 {
		t.Errorf("admin me: %v", me)
	}

	// create a group from Member and tune it
	code, g := admin.do("POST", "/api/groups", `{"name":"Tournament crew","copy_from":2}`)
	if code != 201 || g["builtin"] != nil {
		t.Fatalf("create: %d %v", code, g)
	}
	crew := int64(g["id"].(float64))
	code, g = admin.do("PATCH", fmt.Sprintf("/api/groups/%d", crew), `{"permissions":{"tournaments.manage":1,"games.edit":2,"games.edit_days":-1,"table.score":0}}`)
	if p := g["permissions"].(map[string]any); code != 200 || p["tournaments.manage"] != 1.0 || p["games.edit_days"] != -1.0 || p["table.score"] != 0.0 {
		t.Fatalf("patch: %d %v", code, g)
	}
	for name, body := range map[string]string{
		"unknown permission": `{"permissions":{"table.fly":1}}`,
		"bad on/off value":   `{"permissions":{"table.score":2}}`,
		"bad scope":          `{"permissions":{"games.edit":3}}`,
		"bad days":           `{"permissions":{"games.edit_days":366}}`,
		"empty name":         `{"name":"  "}`,
	} {
		if code, body := admin.do("PATCH", fmt.Sprintf("/api/groups/%d", crew), body); code != 400 {
			t.Errorf("%s: %d %v", name, code, body)
		}
	}
	if code, _ := admin.do("POST", "/api/groups", `{"name":"tournament CREW"}`); code != 409 {
		t.Errorf("duplicate name: %d", code)
	}
	// Admin: fixed permissions, but can be renamed
	if code, body := admin.do("PATCH", "/api/groups/1", `{"permissions":{"view.live":0}}`); code != 400 || body["code"] != "invalid_permission" {
		t.Errorf("admin permissions: %d %v", code, body)
	}
	if code, _ := admin.do("PATCH", "/api/groups/1", `{"name":"Owners"}`); code != 200 {
		t.Errorf("rename admin: %d", code)
	}
	// Anybody: no login-required permissions
	if code, body := admin.do("PATCH", "/api/groups/3", `{"permissions":{"games.edit":2}}`); code != 400 || body["code"] != "invalid_permission" {
		t.Errorf("anybody login-required: %d %v", code, body)
	}

	// move mia into the crew: her permissions change at once
	// An account created by an admin has a temporary password unless told otherwise.
	if code, a := admin.do("POST", "/api/accounts", `{"username":"kim","password":"purple owl sings loud"}`); code != 201 || a["must_change_password"] != true {
		t.Fatalf("create account: %d %v", code, a)
	}
	if code, a := admin.do("POST", "/api/accounts", `{"username":"lou","password":"purple owl sings loud","must_change_password":false}`); code != 201 || a["must_change_password"] != false {
		t.Fatalf("create account: %d %v", code, a)
	}
	_, accs := admin.do("GET", "/api/accounts", "")
	_ = accs
	miaID := accountID(t, f, "mia")
	if code, a := admin.do("PATCH", fmt.Sprintf("/api/accounts/%d", miaID), fmt.Sprintf(`{"group_id":%d}`, crew)); code != 200 || a["group"].(map[string]any)["name"] != "Tournament crew" {
		t.Fatalf("move: %d %v", code, a)
	}
	_, me = mia.do("GET", "/api/auth/me", "")
	if p := me["permissions"].(map[string]any); p["tournaments.manage"] != 1.0 || p["table.score"] != 0.0 || p["games.edit_days"] != -1.0 {
		t.Errorf("crew me: %v", me)
	}
	if code, _ := mia.do("POST", "/api/table/undo", ""); code != 403 && code != 404 {
		t.Errorf("crew has no table.score: %d", code)
	}

	// Anybody's permissions apply to everyone
	if code, _ := admin.do("PATCH", "/api/groups/3", `{"permissions":{"table.score":1}}`); code != 200 {
		t.Fatal("anybody table.score")
	}
	_, me = mia.do("GET", "/api/auth/me", "")
	if p := me["permissions"].(map[string]any); p["table.score"] != 1.0 {
		t.Errorf("anybody's permission must reach the crew: %v", me)
	}
	_, gs := admin.do("GET", "/api/groups", "")
	_ = gs

	// nobody can be moved into Anybody, the last admin can't leave
	if code, body := admin.do("PATCH", fmt.Sprintf("/api/accounts/%d", miaID), `{"group_id":3}`); code != 400 || body["code"] != "invalid_group" {
		t.Errorf("into anybody: %d %v", code, body)
	}
	annaID := accountID(t, f, "anna")
	if code, body := admin.do("PATCH", fmt.Sprintf("/api/accounts/%d", annaID), `{"group_id":2}`); code != 409 || body["code"] != "last_admin" {
		t.Errorf("last admin: %d %v", code, body)
	}
	if code, body := admin.do("PATCH", fmt.Sprintf("/api/accounts/%d", annaID), `{"status":"disabled"}`); code != 409 || body["code"] != "last_admin" {
		t.Errorf("disable last admin: %d %v", code, body)
	}

	// delete the crew: mia moves to Member
	if code, body := admin.do("DELETE", "/api/groups/2?move_to=1", ""); code != 409 || body["code"] != "group_builtin" {
		t.Errorf("delete built-in: %d %v", code, body)
	}
	if code, _ := admin.do("DELETE", fmt.Sprintf("/api/groups/%d?move_to=3", crew), ""); code != 400 {
		t.Errorf("move to anybody: %d", code)
	}
	if code, _ := admin.do("DELETE", fmt.Sprintf("/api/groups/%d?move_to=2", crew), ""); code != 204 {
		t.Errorf("delete: %d", code)
	}
	_, me = mia.do("GET", "/api/auth/me", "")
	if a := me["account"].(map[string]any); a["group"].(map[string]any)["builtin"] != "member" {
		t.Errorf("after delete: %v", me)
	}

	// disabling ends the sessions
	if code, _ := admin.do("PATCH", fmt.Sprintf("/api/accounts/%d", miaID), `{"status":"disabled"}`); code != 200 {
		t.Fatal("disable")
	}
	if _, me := mia.do("GET", "/api/auth/me", ""); me["account"] != nil {
		t.Errorf("disabled account still logged in: %v", me)
	}

	// everything is in the audit log
	_, _ = admin.do("GET", "/api/audit", "")
	log, _ := f.as.AuditLog(context.Background(), 50)
	seen := map[string]bool{}
	for _, e := range log {
		seen[e.Action] = true
	}
	for _, a := range []string{"group_created", "group_permissions_changed", "group_renamed", "account_group_changed", "group_deleted", "account_status_changed"} {
		if !seen[a] {
			t.Errorf("audit log misses %s: %+v", a, log)
		}
	}
}

func accountID(t *testing.T, f *authFixture, name string) int64 {
	t.Helper()
	accs, err := f.as.Accounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accs {
		if a.Username == name {
			return a.ID
		}
	}
	t.Fatalf("no account %s", name)
	return 0
}

// Anybody can be given table permissions, e.g. for a home table without logins.
func TestAnybodyTablePermissions(t *testing.T) {
	f := newTableFixture(t)
	anon := newClient(t, f.srv.URL)
	if code, _ := anon.do("POST", "/api/table/start", ""); code != 401 {
		t.Fatalf("default: %d", code)
	}
	admin := newClient(t, f.srv.URL)
	admin.bearer = f.token
	if code, body := admin.do("PATCH", "/api/groups/3", `{"permissions":{"table.control":1,"table.score":1}}`); code != 200 {
		t.Fatalf("patch: %d %v", code, body)
	}
	if code, body := anon.do("POST", "/api/table/start", ""); code != 200 {
		t.Errorf("anybody start: %d %v", code, body)
	}
	if code, _ := anon.do("POST", "/api/table/score", `{"side":"home","delta":1}`); code != 200 {
		t.Errorf("anybody score: %d", code)
	}
	// choosing another preset needs table.preset
	if code, body := anon.do("POST", "/api/table/start", `{"preset_id":3}`); code != 401 {
		t.Errorf("anybody preset: %d %v", code, body)
	}
	// hiding the live table from Anybody
	_, _ = admin.do("PATCH", "/api/groups/3", `{"permissions":{"view.live":0}}`)
	if code, _ := anon.do("GET", "/api/table", ""); code != 401 {
		t.Errorf("private table: %d", code)
	}
	if code, _ := admin.do("GET", "/api/table", ""); code != 200 {
		t.Errorf("admin still sees it: %d", code)
	}
}
