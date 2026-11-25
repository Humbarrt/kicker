package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/players"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/webui"
)

func TestPlayerRoutes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	as := newAuth(t, st)
	ps, _ := players.New(players.Options{Store: st, Auth: as, MediaDir: filepath.Join(dir, "media")})
	srv := httptest.NewServer(NewRouter(Options{Auth: as, Players: ps, UI: webui.HandlerFS(fstest.MapFS{})}))
	defer srv.Close()

	admin := newClient(t, srv.URL)
	admin.bearer = makeAccount(t, as, "anna", auth.GroupAdmin, auth.StatusApproved)
	member := newClient(t, srv.URL)
	member.bearer = makeAccount(t, as, "mia", auth.GroupMember, auth.StatusApproved)
	anon := newClient(t, srv.URL)

	if code, body := anon.do("POST", "/api/players", `{"name":"X"}`); code != 401 {
		t.Errorf("anonymous create: %d %v", code, body)
	}
	code, p := member.do("POST", "/api/players", `{"name":"Max"}`)
	if code != 201 || p["color"] == "" {
		t.Fatalf("create: %d %v", code, p)
	}
	maxID := int64(p["id"].(float64))
	if code, body := member.do("POST", "/api/players", `{"name":"max"}`); code != 409 || body["code"] != "player_name_taken" {
		t.Errorf("duplicate: %d %v", code, body)
	}
	if code, body := member.do("PATCH", fmt.Sprintf("/api/players/%d", maxID), `{"name":"Maxi"}`); code != 403 {
		t.Errorf("member edits someone else's player: %d %v", code, body)
	}
	// mia creates her own player and edits it
	code, mine := member.do("POST", "/api/players/me", `{"name":"Mia","color":"#7c3aed"}`)
	if code != 201 || mine["has_account"] != true || mine["color"] != "#7c3aed" {
		t.Fatalf("me: %d %v", code, mine)
	}
	miaID := int64(mine["id"].(float64))
	if _, me := member.do("GET", "/api/auth/me", ""); me["account"].(map[string]any)["player_id"] != float64(miaID) {
		t.Errorf("account links the player: %v", me)
	}
	if code, body := member.do("PATCH", fmt.Sprintf("/api/players/%d", miaID), `{"color":"#16a34a"}`); code != 200 || body["color"] != "#16a34a" {
		t.Errorf("own edit: %d %v", code, body)
	}

	// picture upload: raw body, larger than the 64 KB API limit
	img := image.NewRGBA(image.Rect(0, 0, 600, 600))
	seed := uint32(1)
	for i := range img.Pix { // noise does not compress
		seed = seed*1664525 + 1013904223
		img.Pix[i] = byte(seed >> 24)
	}
	img.Set(0, 0, color.Black)
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	if buf.Len() < 64<<10 {
		t.Fatalf("test picture too small to test the limit: %d", buf.Len())
	}
	req, _ := http.NewRequest("PUT", fmt.Sprintf("%s/api/players/%d/avatar", srv.URL, miaID), &buf)
	req.Header.Set("Authorization", "Bearer "+member.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var withPic map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&withPic)
	resp.Body.Close()
	hash, _ := withPic["avatar"].(string)
	if resp.StatusCode != 200 || len(hash) != 64 {
		t.Fatalf("upload: %d %v", resp.StatusCode, withPic)
	}
	resp, _ = http.Get(srv.URL + "/api/media/avatars/" + hash + "-128.jpg")
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" || resp.Header.Get("Cache-Control") == "" || len(data) < 100 {
		t.Errorf("picture: %d %v %d bytes", resp.StatusCode, resp.Header, len(data))
	}
	for _, bad := range []string{hash + "-64.jpg", "../kicker.db", hash + ".png"} {
		if resp, _ := http.Get(srv.URL + "/api/media/avatars/" + bad); resp.StatusCode != 404 {
			t.Errorf("%s: %d", bad, resp.StatusCode)
		}
	}
	req, _ = http.NewRequest("PUT", fmt.Sprintf("%s/api/players/%d/avatar", srv.URL, miaID), bytes.NewReader([]byte("<svg/>")))
	req.Header.Set("Authorization", "Bearer "+member.bearer)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("svg upload: %d", resp.StatusCode)
	}

	// anybody may look (view.stats), only admins delete
	if code, list := anon.do("GET", "/api/players", ""); code != 200 {
		t.Errorf("list: %d %v", code, list)
	}
	if code, _ := member.do("DELETE", fmt.Sprintf("/api/players/%d", maxID), ""); code != 403 {
		t.Errorf("member delete: %d", code)
	}
	if code, body := admin.do("DELETE", fmt.Sprintf("/api/players/%d", miaID), ""); code != 409 || body["code"] != "player_in_use" {
		t.Errorf("delete linked: %d %v", code, body)
	}
	if code, _ := admin.do("DELETE", fmt.Sprintf("/api/players/%d", maxID), ""); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if code, body := admin.do("PATCH", fmt.Sprintf("/api/players/%d", miaID), `{"archived":true}`); code != 200 || body["archived"] != true {
		t.Errorf("archive: %d %v", code, body)
	}
	if code, body := anon.do("GET", "/api/players/palette", ""); code != 200 || len(body["colors"].([]any)) != 16 {
		t.Errorf("palette: %d %v", code, body)
	}
}
