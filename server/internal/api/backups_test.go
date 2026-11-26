package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/backup"
	"github.com/Humbarrt/kicker/server/internal/emergency"
	"github.com/Humbarrt/kicker/server/internal/onboarding"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/webui"
)

// Settings → Backups: admins only; run, download, restore (staged + restart), passphrase.
func TestBackupRoutes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	as := newAuth(t, st)
	admin := makeAccount(t, as, "anna", auth.GroupAdmin, auth.StatusApproved)
	member := makeAccount(t, as, "mia", auth.GroupMember, auth.StatusApproved)
	pass, _ := emergency.Passphrase(dir)
	svc := backup.New(backup.Options{Store: st, DataDir: dir, Name: func() string { return "Test" }})
	var restarts atomic.Int32 // Restart runs after the response is flushed
	srv := httptest.NewServer(NewRouter(Options{DataDir: dir, Auth: as, Backups: svc, Restart: func() { restarts.Add(1) },
		UI: webui.HandlerFS(fstest.MapFS{})}))
	defer srv.Close()
	do := func(token, method, path string, body io.Reader, ctype string) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, body)
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}
	json1 := func(token, method, path, body string) (int, map[string]any) {
		resp, b := do(token, method, path, strings.NewReader(body), "application/json")
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return resp.StatusCode, m
	}

	if code, _ := json1(member, "GET", "/api/backups", ""); code != 403 {
		t.Errorf("member: %d", code)
	}
	code, v := json1(admin, "GET", "/api/backups", "")
	if code != 200 || v["config"].(map[string]any)["schedule"] != "daily" {
		t.Fatalf("view: %d %v", code, v)
	}
	share := t.TempDir()
	cfg := `{"schedule":"daily","time":"04:00","copies":3,"locations":[{"id":"local","kind":"local","enabled":true},{"id":"nas","name":"NAS","kind":"folder","path":"` + share + `","enabled":true}]}`
	if code, v := json1(admin, "PUT", "/api/backups/config", cfg); code != 200 {
		t.Fatalf("config: %d %v", code, v)
	}
	if code, _ := json1(admin, "PUT", "/api/backups/config", `{"schedule":"daily","time":"4 am"}`); code != 400 {
		t.Errorf("bad config: %d", code)
	}
	code, v = json1(admin, "POST", "/api/backups/run", "")
	if code != 200 {
		t.Fatalf("run: %d %v", code, v)
	}
	if es, _ := os.ReadDir(share); len(es) != 1 {
		t.Errorf("share has %d files", len(es))
	}
	list := v["backups"].([]any)
	stored := list[0].(map[string]any)["file"].(string)
	if len(list) != 1 || len(list[0].(map[string]any)["locations"].([]any)) != 2 {
		t.Errorf("list %v", list)
	}
	// Restore from the list without a passphrase: the current one opens it.
	if code, v := json1(admin, "POST", "/api/backups/files/"+stored+"/restore", `{}`); code != 200 || !restartsAre(&restarts, 1) {
		t.Fatalf("restore from the list: %d %v", code, v)
	}
	if resp, b := do(admin, "GET", "/api/backups/files/"+stored+"?location=nas", nil, ""); resp.StatusCode != 200 || len(b) < 100 {
		t.Errorf("stored download: %d", resp.StatusCode)
	}
	if code, _ := json1(admin, "GET", "/api/backups/files/..%2Fkicker.db", ""); code != 404 {
		t.Errorf("path trick: %d", code)
	}

	resp, file := do(admin, "GET", "/api/backups/download", nil, "")
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "kicker-backup-test-") {
		t.Fatalf("download: %d %v", resp.StatusCode, resp.Header)
	}
	upload := func(p string) (int, map[string]any) {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		fw, _ := w.CreateFormFile("file", "backup.age")
		fw.Write(file)
		w.WriteField("passphrase", p)
		w.Close()
		resp, b := do(admin, "POST", "/api/backups/restore", &body, w.FormDataContentType())
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return resp.StatusCode, m
	}
	if code, v := upload("wrong words"); code != 400 || v["code"] != "wrong_passphrase" || !restartsAre(&restarts, 1) {
		t.Errorf("wrong passphrase: %d %v, %d restarts", code, v, restarts.Load())
	}
	if code, v := upload(pass); code != 200 || v["restarting"] != true || !restartsAre(&restarts, 2) {
		t.Fatalf("restore: %d %v, %d restarts", code, v, restarts.Load())
	}
	if m, _ := backup.Pending(dir); m == nil || m.Name != "Test" {
		t.Errorf("pending %+v", m)
	}

	if code, _ := json1(admin, "POST", "/api/backups/passphrase", `{"password":"wrong password","passphrase":""}`); code != 401 {
		t.Errorf("passphrase without the password: %d", code)
	}
	if code, v := json1(admin, "POST", "/api/backups/passphrase", `{"password":"blue tiger dances","passphrase":"too short"}`); code != 400 || v["code"] != "short_passphrase" {
		t.Errorf("short: %d %v", code, v)
	}
	code, v = json1(admin, "POST", "/api/backups/passphrase", `{"password":"blue tiger dances","passphrase":""}`)
	if code != 200 || v["passphrase"] == pass || len(strings.Fields(v["passphrase"].(string))) != 8 {
		t.Errorf("new words: %d %v", code, v)
	}
	if p, _ := emergency.Passphrase(dir); p != v["passphrase"] {
		t.Error("the new passphrase wasn't saved")
	}
}

// Step 2 of the wizard: whoever holds the wizard can restore a backup instead of starting fresh;
// once an admin exists, not any more.
func TestOnboardingRestore(t *testing.T) {
	ctx := context.Background()
	// A backup of another kicker.
	src := t.TempDir()
	other, err := store.Open(ctx, filepath.Join(src, "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	pass, _ := emergency.Passphrase(src)
	var file bytes.Buffer
	if _, err := backup.Write(ctx, &file, backup.Source{Store: other, DataDir: src, Name: "Old"}); err != nil {
		t.Fatal(err)
	}
	other.Close()

	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	as := newAuth(t, st)
	ob, err := onboarding.New(ctx, onboarding.Options{Store: st, Auth: as, Calibrate: func() auth.Params { return fastParams }})
	if err != nil {
		t.Fatal(err)
	}
	var restarts atomic.Int32 // Restart runs after the response is flushed
	srv := httptest.NewServer(NewRouter(Options{ServerID: "sid", DataDir: dir, Auth: as, Onboarding: ob, Restart: func() { restarts.Add(1) },
		UI: webui.HandlerFS(fstest.MapFS{})}))
	defer srv.Close()
	upload := func(c *client, p string) (int, map[string]any) {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		fw, _ := w.CreateFormFile("file", "backup.age")
		fw.Write(file.Bytes())
		w.WriteField("passphrase", p)
		w.Close()
		req, _ := http.NewRequest("POST", srv.URL+"/api/onboarding/restore", &body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.Header.Set("Origin", srv.URL)
		resp, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}
	a, b := newClient(t, srv.URL), newClient(t, srv.URL)
	if code, _ := upload(a, pass); code != 409 {
		t.Errorf("restore without the wizard: %d", code)
	}
	a.do("POST", "/api/onboarding/start", "")
	if code, _ := upload(b, pass); code != 409 {
		t.Errorf("restore from another browser: %d", code)
	}
	if code, v := upload(a, "wrong"); code != 400 || v["code"] != "wrong_passphrase" {
		t.Errorf("wrong passphrase: %d %v", code, v)
	}
	if code, v := upload(a, pass); code != 200 || !restartsAre(&restarts, 1) {
		t.Fatalf("restore: %d %v", code, v)
	}
	if m, _ := backup.Pending(dir); m == nil || m.Name != "Old" {
		t.Errorf("pending %+v", m)
	}
}

// restartsAre waits a moment for the restart count to reach n (Restart is called after the
// response went out).
func restartsAre(r *atomic.Int32, n int32) bool {
	for range 200 {
		if r.Load() == n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return r.Load() == n
}
