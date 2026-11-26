package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/backup"
	"github.com/Humbarrt/kicker/server/internal/emergency"
	"github.com/Humbarrt/kicker/server/internal/release"
	"github.com/Humbarrt/kicker/server/internal/store"
)

// fakeBinary is a shell script that answers `version` like kicker does.
func fakeBinary(t *testing.T, path, ver string) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = version ]; then echo 'kicker %s (test), updates enabled: true'; exit 0; fi\nexit 1\n", ver)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	data     string
	st       *store.Store
	svc      *Service
	gh       *httptest.Server
	pub      string
	restarts atomic.Value // last binary
	files    map[string][]byte
	status   int // GitHub's answer to /releases/latest (0 = 200)
}

// newFixture: a kicker running 0.5.0 from <data>/bin/current, and a fake GitHub with a signed
// release of newVer whose server binary reports binVer.
func newFixture(t *testing.T, newVer, binVer string) *fixture {
	t.Helper()
	ctx := context.Background()
	f := &fixture{data: t.TempDir(), files: map[string][]byte{}}
	var err error
	if f.st, err = store.Open(ctx, filepath.Join(f.data, "kicker.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.st.Close() })
	if _, err := emergency.SetPassphrase(f.data, ""); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(f.data, "bin")
	_ = os.MkdirAll(bin, 0o755)
	fakeBinary(t, filepath.Join(bin, "kicker-0.5.0"), "0.5.0")
	if err := os.Symlink("kicker-0.5.0", filepath.Join(bin, "current")); err != nil {
		t.Fatal(err)
	}

	f.gh = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r/releases/latest":
			if f.status != 0 {
				w.WriteHeader(f.status)
				return
			}
			rel := map[string]any{"tag_name": "v" + newVer, "html_url": "https://example.com/rel", "assets": []map[string]string{}}
			var assets []map[string]string
			for name := range f.files {
				assets = append(assets, map[string]string{"name": name, "browser_download_url": "http://" + r.Host + "/dl/" + name})
			}
			rel["assets"] = assets
			_ = json.NewEncoder(w).Encode(rel)
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			data, ok := f.files[strings.TrimPrefix(r.URL.Path, "/dl/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.gh.Close)

	// the release
	src := t.TempDir()
	server := filepath.Join(src, "kicker-"+runtime.GOOS+"-"+runtime.GOARCH)
	fakeBinary(t, server, binVer)
	dist := filepath.Join(src, "dist")
	_ = os.MkdirAll(dist, 0o755)
	b := release.Build{Version: newVer, BaseURL: f.gh.URL + "/dl", Out: dist, Servers: []string{server}, Date: time.Now(), Notes: "New things."}
	manifest, _, err := b.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	pubFile, keyFile, err := release.GenerateKey("")
	if err != nil {
		t.Fatal(err)
	}
	f.pub, _ = release.PublicKeyString(pubFile)
	sig, err := release.Sign(manifest, keyFile, "")
	if err != nil {
		t.Fatal(err)
	}
	f.files[release.ManifestFile] = manifest
	f.files[release.SignatureFile] = sig
	f.files[filepath.Base(server)], _ = os.ReadFile(server)

	f.svc = f.service("0.5.0")
	return f
}

func (f *fixture) service(ver string) *Service {
	s := New(Options{Store: f.st, DataDir: f.data, API: f.gh.URL, Repo: "o/r", PublicKey: f.pub, Version: ver,
		HealthDelay: 50 * time.Millisecond, Logger: slog.New(slog.DiscardHandler),
		Restart: func(binary string) { f.restarts.Store(binary) }})
	s.fromCurrent = func() bool { return true }
	return s
}

func link(t *testing.T, data, name string) string {
	t.Helper()
	target, err := os.Readlink(filepath.Join(data, "bin", name))
	if err != nil {
		t.Fatal(err)
	}
	return target
}

// waitInstalled waits for the background installation to end.
func waitInstalled(t *testing.T, f *fixture) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := f.svc.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if st.Step == "" || st.Step == "restart" {
			if f.svc.mu.TryLock() {
				f.svc.mu.Unlock()
				st, _ = f.svc.Status(context.Background())
				return st
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("installation didn't finish: %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCheck(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "0.6.0", "0.6.0")
	st, err := f.svc.Check(ctx)
	if err != nil || st.Available == nil || st.Available.Version != "0.6.0" || st.Available.Notes != "New things." || st.LastCheck.IsZero() {
		t.Fatalf("check: %v %+v", err, st)
	}
	if !f.svc.UpdateAvailable(ctx) {
		t.Error("no badge")
	}
	// the same release, seen by 0.6.0 itself: nothing to do
	if st, err := f.service("0.6.0").Check(ctx); err != nil || st.Available != nil {
		t.Errorf("same version: %v %+v", err, st.Available)
	}

	// GitHub refuses (rate limit): kept as the check's error, the release stays offered
	f.status = http.StatusForbidden
	st, err = f.svc.Check(ctx)
	if !errors.Is(err, ErrGitHubBusy) || st.CheckError == "" {
		t.Errorf("rate limited: %v %+v", err, st)
	}
	f.status = 0

	// a release.json changed after signing is refused
	good := f.files[release.ManifestFile]
	f.files[release.ManifestFile] = []byte(strings.Replace(string(good), "New things.", "Evil things.", 1))
	if _, err := f.svc.Check(ctx); !errors.Is(err, release.ErrBadSignature) {
		t.Errorf("tampered: %v", err)
	}
	f.files[release.ManifestFile] = good

	// a build without repo or key doesn't check
	dev := New(Options{Store: f.st, DataDir: f.data, Version: "dev", Repo: "", PublicKey: ""})
	dev.o.Repo, dev.o.PublicKey = "", ""
	if _, err := dev.Check(ctx); !errors.Is(err, ErrDisabled) {
		t.Errorf("dev build: %v", err)
	}
}

func TestCheckTagMismatch(t *testing.T) {
	f := newFixture(t, "0.6.0", "0.6.0")
	// GitHub's tag says 0.7.0, the signed release.json says 0.6.0: an old release.json
	// re-published under a new tag is refused
	f.gh.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r/releases/latest" {
			_, _ = fmt.Fprintf(w, `{"tag_name":"v0.7.0","assets":[{"name":"release.json","browser_download_url":"http://%[1]s/dl/release.json"},{"name":"release.json.minisig","browser_download_url":"http://%[1]s/dl/release.json.minisig"}]}`, r.Host)
			return
		}
		_, _ = w.Write(f.files[strings.TrimPrefix(r.URL.Path, "/dl/")])
	})
	if _, err := f.svc.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "0.6.0") {
		t.Errorf("tag mismatch: %v", err)
	}
}

func TestInstallAndHealthy(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "0.6.0", "0.6.0")
	if err := f.svc.Install(ctx); !errors.Is(err, ErrNothing) {
		t.Errorf("install before a check: %v", err)
	}
	if _, err := f.svc.Check(ctx); err != nil {
		t.Fatal(err)
	}
	busy := true
	f.svc.o.Busy = func() bool { return busy }
	if err := f.svc.Install(ctx); !errors.Is(err, ErrBusy) {
		t.Errorf("during a game: %v", err)
	}
	busy = false
	f.svc.fromCurrent = func() bool { return false }
	if err := f.svc.Install(ctx); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("not from bin/current: %v", err)
	}
	f.svc.fromCurrent = func() bool { return true }

	if err := f.svc.Install(ctx); err != nil {
		t.Fatal(err)
	}
	st := waitInstalled(t, f)
	if st.Last != nil && st.Last.Error != "" {
		t.Fatalf("install failed: %+v", st.Last)
	}
	if got, _ := f.restarts.Load().(string); got != Current(f.data) {
		t.Fatalf("restart: %q", got)
	}
	if link(t, f.data, "current") != "kicker-0.6.0" || link(t, f.data, "previous") != "kicker-0.5.0" {
		t.Errorf("links: current %s, previous %s", link(t, f.data, "current"), link(t, f.data, "previous"))
	}
	state, err := ReadState(f.data)
	if err != nil || state == nil || state.From != "0.5.0" || state.To != "0.6.0" || state.Previous != "kicker-0.5.0" {
		t.Fatalf("state: %v %+v", err, state)
	}
	if _, err := os.Stat(state.Backup); err != nil || !strings.Contains(state.Backup, backup.KindPreUpdate) {
		t.Errorf("pre-update backup %q: %v", state.Backup, err)
	}

	// the new version starts: one attempt, then healthy after HealthDelay
	reexec, pending, err := Startup(ctx, f.data, "0.6.0", slog.New(slog.DiscardHandler))
	if err != nil || reexec != "" || !pending {
		t.Fatalf("startup: %q %v %v", reexec, pending, err)
	}
	fakeBinary(t, filepath.Join(f.data, "bin", "kicker-0.4.0"), "0.4.0") // an old leftover: pruned
	newSvc := f.service("0.6.0")
	if st, _ := newSvc.Status(ctx); st.Step != "verify" {
		t.Errorf("step while the new version is verified: %q", st.Step)
	}
	newSvc.Started(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if s, _ := ReadState(f.data); s == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never healthy")
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, _ = newSvc.Status(ctx)
	if st.Last == nil || !st.Last.OK || st.Last.To != "0.6.0" || st.Available != nil {
		t.Errorf("after the update: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(f.data, "bin", "kicker-0.4.0")); !os.IsNotExist(err) {
		t.Error("old version not pruned")
	}
	if _, err := os.Stat(filepath.Join(f.data, "bin", "kicker-0.5.0")); err != nil {
		t.Error("the previous version was pruned")
	}
}

// The new version crashes (or is stopped) before it is healthy: the next start rolls back to
// the previous version and the pre-update backup.
func TestRollback(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	f := newFixture(t, "0.6.0", "0.6.0")
	if _, err := f.svc.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Install(ctx); err != nil {
		t.Fatal(err)
	}
	waitInstalled(t, f)
	if _, pending, err := Startup(ctx, f.data, "0.6.0", log); err != nil || !pending {
		t.Fatalf("first start: %v %v", pending, err)
	}
	// … it crashes; systemd starts current (0.6.0) again
	reexec, _, err := Startup(ctx, f.data, "0.6.0", log)
	if err != nil || reexec != Current(f.data) {
		t.Fatalf("second start: %q %v", reexec, err)
	}
	if link(t, f.data, "current") != "kicker-0.5.0" {
		t.Errorf("current: %s", link(t, f.data, "current"))
	}
	if m, err := backup.Pending(f.data); err != nil || m == nil {
		t.Errorf("the pre-update backup is not staged: %v", err)
	}
	// 0.5.0 runs again: nothing to do at startup, the result is reported once it runs
	if reexec, pending, err := Startup(ctx, f.data, "0.5.0", log); err != nil || reexec != "" || pending {
		t.Fatalf("old version start: %q %v %v", reexec, pending, err)
	}
	old := f.service("0.5.0")
	old.Started(ctx)
	st, _ := old.Status(ctx)
	if st.Last == nil || st.Last.OK || st.Last.To != "0.6.0" || !strings.Contains(st.Last.Error, "stopped") {
		t.Errorf("result: %+v", st.Last)
	}
	if s, _ := ReadState(f.data); s != nil {
		t.Error("update.json left behind")
	}
	if st.Available == nil {
		t.Error("the release should still be offered (to try again)")
	}
}

func TestInstallFailures(t *testing.T) {
	ctx := context.Background()
	// the downloaded binary reports another version: never switched to
	f := newFixture(t, "0.6.0", "0.6.1")
	if _, err := f.svc.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Install(ctx); err != nil {
		t.Fatal(err)
	}
	st := waitInstalled(t, f)
	if st.Last == nil || !strings.Contains(st.Last.Error, "0.6.1") || link(t, f.data, "current") != "kicker-0.5.0" || f.restarts.Load() != nil {
		t.Errorf("wrong binary: %+v, current %s", st.Last, link(t, f.data, "current"))
	}
	if s, _ := ReadState(f.data); s != nil {
		t.Error("update.json written for a failed install")
	}

	// a download that doesn't match release.json's sha256
	f = newFixture(t, "0.6.0", "0.6.0")
	if _, err := f.svc.Check(ctx); err != nil {
		t.Fatal(err)
	}
	name := "kicker-" + runtime.GOOS + "-" + runtime.GOARCH
	f.files[name] = append([]byte("x"), f.files[name][1:]...)
	if err := f.svc.Install(ctx); err != nil {
		t.Fatal(err)
	}
	st = waitInstalled(t, f)
	if st.Last == nil || !strings.Contains(st.Last.Error, "sha256") || link(t, f.data, "current") != "kicker-0.5.0" {
		t.Errorf("bad download: %+v", st.Last)
	}
}

func TestTooOld(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "0.6.0", "0.6.0")
	if _, err := f.svc.Check(ctx); err != nil {
		t.Fatal(err)
	}
	_ = f.svc.saveStatus(ctx, func(st *Status) { st.Available.MinVersion = "0.5.5" })
	if err := f.svc.Blocked(ctx); !errors.Is(err, ErrTooOld) {
		t.Errorf("min version: %v", err)
	}
}

func TestWindow(t *testing.T) {
	s := New(Options{Time: func() string { return "03:30" }})
	loc := time.Local
	for at, want := range map[string]bool{"03:29": false, "03:30": true, "06:29": true, "06:30": false, "12:00": false} {
		h, m := 0, 0
		_, _ = fmt.Sscanf(at, "%d:%d", &h, &m)
		if got := s.inWindow(time.Date(2026, 11, 26, h, m, 0, 0, loc)); got != want {
			t.Errorf("%s: %v", at, got)
		}
	}
	late := New(Options{Time: func() string { return "23:00" }})
	if !late.inWindow(time.Date(2026, 11, 27, 1, 0, 0, 0, loc)) {
		t.Error("window across midnight")
	}
}
