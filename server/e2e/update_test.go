// Package e2e runs the real kicker binary end to end (spec 10 §8, M7 task 8): a server update
// from a locally signed test release, and an update that fails and is rolled back – with a
// test-side "systemd" that starts <data>/bin/current again whenever it exits.
//
// It builds the binary three times (a minute or so); `go test -short` skips it.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/emergency"
	"github.com/Humbarrt/kicker/server/internal/release"
)

const (
	repo     = "test/kicker"
	username = "anna"
	password = "blue tiger dances"
)

// build compiles the kicker binary with the given version and update settings.
func build(t *testing.T, out, ver, pub string, crash bool) {
	t.Helper()
	pkg := "github.com/Humbarrt/kicker/server/internal/version"
	ld := fmt.Sprintf("-X %[1]s.Version=%[2]s -X %[1]s.UpdateRepo=%[3]s -X %[1]s.UpdatePubKey=%[4]s", pkg, ver, repo, pub)
	if crash {
		ld += fmt.Sprintf(" -X %s.CrashAfterStart=yes", pkg)
	}
	cmd := exec.Command("go", "build", "-ldflags", ld, "-o", out, "./cmd/kicker")
	cmd.Dir = ".."
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building %s: %v\n%s", ver, err, b)
	}
}

// fakeGitHub serves the releases API and the release files; latest says which tag is newest.
type fakeGitHub struct {
	*httptest.Server
	mu     sync.Mutex
	latest string
	dirs   map[string]string // tag → folder with the release files
}

func (g *fakeGitHub) setLatest(tag string) {
	g.mu.Lock()
	g.latest = tag
	g.mu.Unlock()
}

func newGitHub(t *testing.T) *fakeGitHub {
	g := &fakeGitHub{dirs: map[string]string{}}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		prefix := "/repos/" + repo + "/releases/"
		switch {
		case strings.HasPrefix(r.URL.Path, prefix):
			tag := strings.TrimPrefix(r.URL.Path, prefix)
			if tag == "latest" {
				tag = g.latest
			}
			tag = strings.TrimPrefix(tag, "tags/")
			dir, ok := g.dirs[tag]
			if !ok {
				http.NotFound(w, r)
				return
			}
			var assets []map[string]string
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				assets = append(assets, map[string]string{"name": e.Name(), "browser_download_url": g.URL + "/dl/" + tag + "/" + e.Name()})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "html_url": g.URL + "/" + tag, "assets": assets})
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/dl/"), "/", 2)
			if dir, ok := g.dirs[parts[0]]; ok && len(parts) == 2 {
				http.ServeFile(w, r, filepath.Join(dir, filepath.Base(parts[1])))
				return
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(g.Close)
	return g
}

// publish makes a signed release of a binary.
func (g *fakeGitHub) publish(t *testing.T, ver, binary string, key []byte) {
	t.Helper()
	tag := "v" + ver
	dir := t.TempDir()
	name := filepath.Join(dir, "kicker-"+runtime.GOOS+"-"+runtime.GOARCH)
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := release.Build{Version: ver, Notes: "Release " + ver, Date: time.Now(), BaseURL: g.URL + "/dl/" + tag, Out: dir, Servers: []string{name}}.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := release.Sign(manifest, key, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, release.ManifestFile), manifest, 0o644)
	_ = os.WriteFile(filepath.Join(dir, release.SignatureFile), sig, 0o644)
	g.mu.Lock()
	g.dirs[tag] = dir
	g.mu.Unlock()
}

// supervisor stands in for systemd: it runs <data>/bin/current and starts it again when it
// exits (Restart=always).
type supervisor struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	stop bool
	log  syncBuffer
	done chan struct{}
}

// syncBuffer collects the server's output from several goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func supervise(t *testing.T, data, addr, api string) *supervisor {
	s := &supervisor{done: make(chan struct{})}
	go func() {
		defer close(s.done)
		for {
			s.mu.Lock()
			if s.stop {
				s.mu.Unlock()
				return
			}
			cmd := exec.Command(filepath.Join(data, "bin", "current"), "serve", "--data", data, "--addr", addr,
				"--no-serial", "--update-api", api, "--update-health-delay", "3s")
			cmd.Stdout, cmd.Stderr = &s.log, &s.log
			s.cmd = cmd
			err := cmd.Start()
			s.mu.Unlock()
			if err == nil {
				err = cmd.Wait()
			}
			fmt.Fprintf(&s.log, "=== exited: %v\n", err)
			time.Sleep(300 * time.Millisecond)
		}
	}()
	t.Cleanup(func() {
		s.mu.Lock()
		s.stop = true
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		s.mu.Unlock()
		<-s.done
		if t.Failed() {
			t.Logf("server log:\n%s", s.log.String())
		}
	})
	return s
}

// client talks to the kicker's API like the web UI does.
type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.base)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0 // restarting
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func (c *client) login() {
	c.t.Helper()
	if code := c.do("POST", "/api/auth/login", map[string]string{"username": username, "password": password}, nil); code != 200 {
		c.t.Fatalf("login: %d", code)
	}
}

// waitFor polls until ok returns true.
func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type info struct {
	Version string `json:"version"`
}

type updates struct {
	Status struct {
		Available *struct {
			Version string `json:"version"`
		} `json:"available"`
		Last *struct {
			OK    bool   `json:"ok"`
			From  string `json:"from"`
			To    string `json:"to"`
			Error string `json:"error"`
		} `json:"last"`
		Step string `json:"step"`
	} `json:"status"`
	Blocked string `json:"blocked"`
}

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func TestUpdateAndRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary three times")
	}
	if runtime.GOOS == "windows" {
		t.Skip("re-executing needs a Unix")
	}
	pubFile, key, err := release.GenerateKey("")
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := release.PublicKeyString(pubFile)

	bins := t.TempDir()
	for _, b := range []struct {
		ver   string
		crash bool
	}{{"1.0.0", false}, {"1.1.0", false}, {"1.2.0", true}} {
		build(t, filepath.Join(bins, b.ver), b.ver, pub, b.crash)
	}

	gh := newGitHub(t)
	gh.publish(t, "1.0.0", filepath.Join(bins, "1.0.0"), key)
	gh.publish(t, "1.1.0", filepath.Join(bins, "1.1.0"), key)
	gh.publish(t, "1.2.0", filepath.Join(bins, "1.2.0"), key)

	// A kicker running 1.0.0 from <data>/bin/current, set up with an admin.
	data := t.TempDir()
	_ = os.MkdirAll(filepath.Join(data, "bin"), 0o755)
	cur := filepath.Join(data, "bin", "kicker-1.0.0")
	if b, err := os.ReadFile(filepath.Join(bins, "1.0.0")); err != nil || os.WriteFile(cur, b, 0o755) != nil {
		t.Fatal(err)
	}
	_ = os.Symlink("kicker-1.0.0", filepath.Join(data, "bin", "current"))
	create := exec.Command(cur, "admin", "create", "--data", data, username)
	create.Stdin = strings.NewReader(password + "\n" + password + "\n")
	if b, err := create.CombinedOutput(); err != nil {
		t.Fatalf("admin create: %v\n%s", err, b)
	}
	if _, err := emergency.SetPassphrase(data, ""); err != nil { // the kit step of the wizard
		t.Fatal(err)
	}

	addr := freeAddr(t)
	supervise(t, data, addr, gh.URL)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: "http://" + addr, http: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
	version := func() string {
		var i info
		if c.do("GET", "/api/info", nil, &i) != 200 {
			return ""
		}
		return i.Version
	}
	waitFor(t, "1.0.0 to start", 30*time.Second, func() bool { return version() == "1.0.0" })
	c.login()
	link := func(name string) string {
		l, _ := os.Readlink(filepath.Join(data, "bin", name))
		return l
	}
	status := func() updates {
		var u updates
		c.do("GET", "/api/updates", nil, &u)
		return u
	}
	player := func(name string) {
		t.Helper()
		if code := c.do("POST", "/api/players", map[string]any{"name": name}, nil); code != 201 && code != 200 {
			t.Fatalf("new player %s: %d", name, code)
		}
	}
	hasPlayer := func(name string) bool {
		var ps []struct {
			Name string `json:"name"`
		}
		c.do("GET", "/api/players", nil, &ps)
		for _, p := range ps {
			if p.Name == name {
				return true
			}
		}
		return false
	}

	// 1. A good update: 1.0.0 → 1.1.0.
	gh.setLatest("v1.1.0")
	player("Before 1.1")
	var u updates
	c.do("POST", "/api/updates/check", nil, &u)
	if u.Status.Available == nil || u.Status.Available.Version != "1.1.0" || u.Blocked != "" {
		t.Fatalf("check: %+v", u)
	}
	if code := c.do("POST", "/api/updates/install", nil, nil); code != 200 {
		t.Fatalf("install: %d", code)
	}
	waitFor(t, "1.1.0 to run", 60*time.Second, func() bool { return version() == "1.1.0" })
	waitFor(t, "the health check", 30*time.Second, func() bool {
		l := status().Status.Last
		return l != nil && l.OK && l.To == "1.1.0"
	})
	if link("current") != "kicker-1.1.0" || link("previous") != "kicker-1.0.0" {
		t.Errorf("links after the update: current %s, previous %s", link("current"), link("previous"))
	}
	if _, err := os.Stat(filepath.Join(data, "update.json")); !os.IsNotExist(err) {
		t.Error("update.json left after a healthy update")
	}
	if !hasPlayer("Before 1.1") {
		t.Error("data lost by the update")
	}

	// 2. A broken update: 1.2.0 crashes after opening the database. The next start rolls back
	//    to 1.1.0 and the backup made before the update.
	gh.setLatest("v1.2.0")
	player("Before 1.2")
	c.do("POST", "/api/updates/check", nil, &u)
	if u.Status.Available == nil || u.Status.Available.Version != "1.2.0" {
		t.Fatalf("check 1.2.0: %+v", u)
	}
	if code := c.do("POST", "/api/updates/install", nil, nil); code != 200 {
		t.Fatalf("install 1.2.0: %d", code)
	}
	waitFor(t, "the rollback", 90*time.Second, func() bool {
		if version() != "1.1.0" {
			return false
		}
		// a rollback restores the backup, sessions included: log in again
		if c.do("GET", "/api/updates", nil, nil) == 401 {
			c.login()
		}
		l := status().Status.Last
		return l != nil && !l.OK && l.To == "1.2.0"
	})
	last := status().Status.Last
	if !strings.Contains(last.Error, "stopped before it was running properly") {
		t.Errorf("the reason: %q", last.Error)
	}
	if link("current") != "kicker-1.1.0" {
		t.Errorf("current after the rollback: %s", link("current"))
	}
	if !hasPlayer("Before 1.2") || !hasPlayer("Before 1.1") {
		t.Error("the data from before the update is not back")
	}
	if _, err := os.Stat(filepath.Join(data, "update.json")); !os.IsNotExist(err) {
		t.Error("update.json left after the rollback")
	}

	// 3. The failed version is still offered (to try again by hand).
	if status().Status.Available == nil {
		t.Error("1.2.0 is no longer offered")
	}
}
