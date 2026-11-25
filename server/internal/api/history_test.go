package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/store"
)

func TestHistoryAndEdits(t *testing.T) {
	f := newTableFixture(t)
	ctx := context.Background()
	mia, _ := f.st.CreatePlayer(ctx, store.Player{Name: "Mia", Color: "#1e3a8a"}, 0)
	tom, _ := f.st.CreatePlayer(ctx, store.Player{Name: "Tom", Color: "#1e3a8a"}, 0)
	lena, _ := f.st.CreatePlayer(ctx, store.Player{Name: "Lena", Color: "#1e3a8a"}, 0)

	// A member linked to Mia, and one linked to nobody.
	member := func(name string, player int64) string {
		a, err := f.as.CreateAccount(ctx, auth.NewAccount{Username: name, Password: "blue tiger dances",
			GroupID: f.as.BuiltinGroup(auth.GroupMember), Status: auth.StatusApproved})
		if err != nil {
			t.Fatal(err)
		}
		if player != 0 {
			if err := f.as.SetAccountPlayer(ctx, auth.Actor{}, a.ID, player); err != nil {
				t.Fatal(err)
			}
		}
		token, _, err := f.as.Login(ctx, auth.LoginRequest{Username: name, Password: "blue tiger dances", Kind: auth.KindApp})
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	miaToken, otherToken := member("mia", mia), member("otto", 0)
	as := func(token, method, path, body string) (int, map[string]any) {
		t.Helper()
		old := f.token
		f.token = token
		defer func() { f.token = old }()
		return f.do(t, method, path, body)
	}

	// Mia against Tom, short game (first to 6), home wins 6:0.
	f.post(t, "/api/table/registrations", fmt.Sprintf(`{"side":"home","player_id":%d}`, mia))
	f.post(t, "/api/table/registrations", fmt.Sprintf(`{"side":"away","player_id":%d}`, tom))
	f.post(t, "/api/table/start", `{"preset_id": 2}`)
	id := f.tbl.View().Game.ID
	for range 6 {
		f.post(t, "/api/table/score", `{"side":"home","delta":1}`)
	}

	// The history.
	req, _ := http.NewRequest(http.MethodGet, f.srv.URL+"/api/games", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var list []GameSummaryView
	decode(t, resp, &list)
	if len(list) != 1 || list[0].ID != id || list[0].Home != 6 || *list[0].Winner != "home" || len(list[0].Roster["away"]) != 1 {
		t.Fatalf("history: %+v", list)
	}
	if code, body := f.do(t, http.MethodGet, fmt.Sprintf("/api/games?player=%d", lena), ""); code != http.StatusOK {
		t.Errorf("filter: %d %v", code, body)
	}

	// The game page: admins may do everything; the log has the game's events.
	code, body := f.do(t, http.MethodGet, fmt.Sprintf("/api/games/%d", id), "")
	events := body["events"].([]any)
	if code != http.StatusOK || body["can_edit"] != true || body["can_void"] != true || body["can_revert"] != true ||
		events[0].(map[string]any)["type"] != "game_started" {
		t.Fatalf("detail: %d %v", code, body)
	}
	if code, _ := f.do(t, http.MethodGet, "/api/games/999", ""); code != http.StatusNotFound {
		t.Errorf("unknown game: %d", code)
	}
	code, body = as(miaToken, http.MethodGet, fmt.Sprintf("/api/games/%d", id), "")
	if body["can_edit"] != true || body["can_void"] != false || body["can_revert"] != false {
		t.Errorf("member, own game: %v", body)
	}
	if _, body = as(otherToken, http.MethodGet, fmt.Sprintf("/api/games/%d", id), ""); body["can_edit"] != false {
		t.Errorf("member, someone else's game: %v", body)
	}

	edits := fmt.Sprintf("/api/games/%d/edits", id)
	// Mia: Lena played with her; one goal was an own mistake.
	code, body = as(miaToken, http.MethodPost, edits, fmt.Sprintf(`{"kind":"set_player","side":"home","slot":2,"player_id":%d}`, lena))
	if code != http.StatusOK || body["game"].(map[string]any)["edits"] != 1.0 {
		t.Fatalf("set player: %d %v", code, body)
	}
	firstGoal := int(body["game"].(map[string]any)["goals"].([]any)[0].(map[string]any)["seq"].(float64))
	code, body = as(miaToken, http.MethodPost, edits, fmt.Sprintf(`{"kind":"void_goal","target_seq":%d}`, firstGoal))
	if code != http.StatusOK || body["game"].(map[string]any)["home"] != 5.0 {
		t.Fatalf("void goal: %d %v", code, body)
	}
	if code, body = as(miaToken, http.MethodPost, edits, `{"kind":"void_game"}`); code != http.StatusForbidden {
		t.Errorf("members may not void: %d %v", code, body)
	}
	if code, body = as(otherToken, http.MethodPost, edits, `{"kind":"swap_sides"}`); code != http.StatusForbidden {
		t.Errorf("not her game: %d %v", code, body)
	}
	if code, body = as(miaToken, http.MethodPost, edits, `{"kind":"void_goal","target_seq":1}`); code != http.StatusBadRequest || body["code"] != "invalid_edit" {
		t.Errorf("bad edit: %d %v", code, body)
	}
	if code, body = as(miaToken, http.MethodPost, edits, `{"kind":"set_player","side":"home","slot":2,"player_id":99999}`); code != http.StatusBadRequest || body["code"] != "invalid_player" {
		t.Errorf("unknown player: %d %v", code, body)
	}

	// The admin voids the game, sees the feed and reverts the void.
	code, body = f.post(t, edits, `{"kind":"void_game"}`)
	if code != http.StatusOK || body["game"].(map[string]any)["voided"] != true {
		t.Fatalf("void: %d %v", code, body)
	}
	resp2, _ := http.NewRequest(http.MethodGet, f.srv.URL+"/api/edits", nil)
	resp2.Header.Set("Authorization", "Bearer "+f.token)
	r2, err := http.DefaultClient.Do(resp2)
	if err != nil {
		t.Fatal(err)
	}
	var feed []EditFeedView
	decode(t, r2, &feed)
	if len(feed) != 3 || feed[0].Type != "game_voided" || *feed[0].Author != "anna" || feed[2].Type != "player_joined" ||
		*feed[2].Author != "mia" {
		t.Fatalf("feed: %+v", feed)
	}

	// The audit log: members see it (log.view) without IP addresses (log.ip), admins with them.
	audit := func(token string) (int, []AuditView) {
		req, _ := http.NewRequest(http.MethodGet, f.srv.URL+"/api/audit", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var out []AuditView
		if r.StatusCode == http.StatusOK {
			decode(t, r, &out)
		} else {
			r.Body.Close()
		}
		return r.StatusCode, out
	}
	code, entries := audit(miaToken)
	if code != http.StatusOK || len(entries) == 0 || entries[0].Action != "game_edited" || entries[0].Account != "anna" {
		t.Fatalf("member audit: %d %+v", code, entries)
	}
	for _, e := range entries {
		if e.IP != "" {
			t.Fatalf("members must not see IPs: %+v", e)
		}
	}
	if _, entries = audit(f.token); entries[0].IP == "" {
		t.Errorf("admins see IPs: %+v", entries[0])
	}
	if code, _ := as(miaToken, http.MethodGet, "/api/edits", ""); code != http.StatusForbidden {
		t.Errorf("members don't see the feed: %d", code)
	}
	code, body = f.post(t, edits, fmt.Sprintf(`{"kind":"revert","target_seq":%d}`, feed[0].Seq))
	if code != http.StatusOK || body["game"].(map[string]any)["voided"] != false {
		t.Fatalf("revert: %d %v", code, body)
	}
	evs := body["events"].([]any)
	var reverted int
	for _, e := range evs {
		if e.(map[string]any)["reverted"] == true {
			reverted++
		}
	}
	if reverted != 1 || !strings.Contains(fmt.Sprint(evs[len(evs)-1]), "reverted") {
		t.Errorf("log: %v", evs)
	}
	row, _, _ := f.st.Game(ctx, id)
	ps, _ := f.st.Participants(ctx, id)
	if row.Voided || row.HomeGoals != 5 || row.Edits != 2 || len(ps) != 3 {
		t.Errorf("projection: %+v %+v", row, ps)
	}

	// A running game can't be edited.
	f.post(t, "/api/table/start", ``)
	running := f.tbl.View().Game.ID
	if code, body = f.post(t, fmt.Sprintf("/api/games/%d/edits", running), `{"kind":"swap_sides"}`); code != http.StatusConflict || body["code"] != "not_finished" {
		t.Errorf("running game: %d %v", code, body)
	}
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}
