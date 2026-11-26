package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/emergency"
	"github.com/Humbarrt/kicker/server/internal/store"
)

func setup(t *testing.T) (*store.Store, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.SetSetting(ctx, store.SettingName, "Office Kicker"); err != nil {
		t.Fatal(err)
	}
	if _, err := emergency.Passphrase(dir); err != nil {
		t.Fatal(err)
	}
	must(t, os.MkdirAll(filepath.Join(dir, "media", "avatars"), 0o750))
	must(t, os.WriteFile(filepath.Join(dir, "media", "avatars", "ab12.webp"), []byte("picture"), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "ca.key"), []byte("ca key"), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "ca.crt"), []byte("ca cert"), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "server.key"), []byte("server key"), 0o600))
	return st, dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func pass(t *testing.T, dir string) string {
	p, err := emergency.Passphrase(dir)
	must(t, err)
	return p
}

// A backup opens with its passphrase and holds the database, pictures, CA and passphrase – not
// the server certificate; a wrong passphrase or a changed byte is refused before anything is
// written outside the target folder.
func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, dir := setup(t)
	id, _ := st.ServerID(ctx)
	var buf bytes.Buffer
	m, err := Write(ctx, &buf, Source{Store: st, DataDir: dir, Name: "Office Kicker", ServerID: id})
	must(t, err)
	if m.Schema != store.Schema() || len(m.Files) != 5 {
		t.Fatalf("manifest: %+v", m)
	}
	if bytes.Contains(buf.Bytes(), []byte("ca key")) {
		t.Fatal("the backup isn't encrypted")
	}

	out := filepath.Join(t.TempDir(), "x")
	if _, err := Extract(ctx, bytes.NewReader(buf.Bytes()), "wrong words", out); !errors.Is(err, ErrPassphrase) {
		t.Errorf("wrong passphrase: %v", err)
	}
	bad := bytes.Clone(buf.Bytes())
	bad[len(bad)-40] ^= 1
	if _, err := Extract(ctx, bytes.NewReader(bad), pass(t, dir), filepath.Join(t.TempDir(), "y")); err == nil {
		t.Error("a damaged backup was accepted")
	}

	got, err := Extract(ctx, bytes.NewReader(buf.Bytes()), "  "+pass(t, dir)+"\n", out)
	must(t, err)
	if got.ServerID != id || got.Name != "Office Kicker" {
		t.Errorf("manifest %+v", got)
	}
	for _, f := range []string{"kicker.db", "media/avatars/ab12.webp", "identity/ca.key", "identity/ca.crt", "backup.passphrase"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "identity", "server.key")); err == nil {
		t.Error("the server key is in the backup")
	}
}

// Stage + ApplyPending: the live files move aside, the backup takes their place.
func TestRestore(t *testing.T) {
	ctx := context.Background()
	st, dir := setup(t)
	var buf bytes.Buffer
	_, err := Write(ctx, &buf, Source{Store: st, DataDir: dir, Name: "Office Kicker"})
	must(t, err)
	p := pass(t, dir)

	// Things change after the backup …
	must(t, st.SetSetting(ctx, store.SettingName, "Renamed"))
	must(t, os.WriteFile(filepath.Join(dir, "ca.key"), []byte("new ca key"), 0o600))
	if _, err := Stage(ctx, dir, bytes.NewReader(buf.Bytes()), "wrong"); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("stage with a wrong passphrase: %v", err)
	}
	if m, _ := Pending(dir); m != nil {
		t.Fatal("a failed stage is pending")
	}
	_, err = Stage(ctx, dir, bytes.NewReader(buf.Bytes()), p)
	must(t, err)
	st.Close()

	m, err := ApplyPending(dir)
	must(t, err)
	if m == nil || m.Name != "Office Kicker" {
		t.Fatalf("applied %+v", m)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "ca.key")); string(b) != "ca key" {
		t.Errorf("ca.key %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "server.key")); err == nil {
		t.Error("the old server key stayed (it must be reissued from the restored CA)")
	}
	re, err := store.Open(ctx, filepath.Join(dir, "kicker.db"))
	must(t, err)
	defer re.Close()
	var name string
	re.GetSetting(ctx, store.SettingName, &name)
	if name != "Office Kicker" {
		t.Errorf("restored name %q", name)
	}
	befores, _ := os.ReadDir(filepath.Join(dir, beforeDir))
	if len(befores) != 1 {
		t.Fatalf("%d before-restore folders", len(befores))
	}
	if b, _ := os.ReadFile(filepath.Join(dir, beforeDir, befores[0].Name(), "ca.key")); string(b) != "new ca key" {
		t.Errorf("the replaced ca.key wasn't kept: %q", b)
	}
	if m, _ := ApplyPending(dir); m != nil {
		t.Error("applied twice")
	}
}

// Locations, schedule and retention: every enabled location gets the backup; a missing USB drive
// is reported; automatic backups are kept by the retention, manual ones stay.
func TestLocationsAndSchedule(t *testing.T) {
	ctx := context.Background()
	st, dir := setup(t)
	usb, share := t.TempDir(), t.TempDir()
	must(t, os.Mkdir(filepath.Join(usb, "STICK"), 0o750))
	now := time.Date(2026, 11, 25, 3, 0, 0, 0, time.Local) // a Wednesday
	busy := false
	s := New(Options{Store: st, DataDir: dir, Name: func() string { return "Office Kicker" }, USBRoot: usb, Mounted: func(string) bool { return true },
		Now: func() time.Time { return now }, Busy: func() bool { return busy }})
	if got := s.Drives(); len(got) != 1 || got[0] != "STICK" {
		t.Errorf("drives %v", got)
	}
	if at := s.AutoTime(); at < "03:00" || at > "04:59" {
		t.Errorf("automatic time %s", at)
	}
	c := Config{Schedule: ScheduleDaily, Time: "03:30", Copies: 2, Locations: []Location{
		{ID: "local", Kind: LocLocal, Enabled: true},
		{ID: "usb", Kind: LocUSB, Path: "STICK", Enabled: true},
		{ID: "nas", Kind: LocFolder, Path: share, Enabled: true},
		{ID: "gone", Kind: LocUSB, Path: "OTHER", Enabled: true},
		{ID: "off", Kind: LocFolder, Path: "/nowhere", Enabled: false},
	}}
	must(t, s.SetConfig(ctx, c))
	for _, bad := range []Config{
		{Schedule: ScheduleDaily, Time: "3:30", Copies: 1, Locations: c.Locations[:1]},
		{Schedule: ScheduleDays, Copies: 1, Locations: c.Locations[:1]},
		{Schedule: ScheduleDaily, Copies: 1, Locations: c.Locations[1:2]},
		{Schedule: ScheduleDaily, Copies: 1, Locations: []Location{c.Locations[0], {ID: "x", Kind: LocFolder, Path: "relative"}}},
	} {
		if err := s.SetConfig(ctx, bad); !errors.Is(err, ErrConfig) {
			t.Errorf("%+v: %v", bad, err)
		}
	}

	// Before 03:30: nothing. At 03:31 while a game is on: nothing. Then three days.
	s.tick(ctx)
	now = now.Add(31 * time.Minute)
	busy = true
	s.tick(ctx)
	if st, _ := s.Status(ctx); st.LastRun != nil {
		t.Fatal("ran too early or during a game")
	}
	busy = false
	for day := 0; day < 3; day++ {
		s.tick(ctx)
		s.tick(ctx) // once a day
		now = now.Add(24 * time.Hour)
	}
	_, err := s.RunNow(ctx) // manual: never pruned
	must(t, err)
	count := func(d string, kind string) int {
		n := 0
		for _, f := range listDir(d) {
			if p, _ := ParseFilename(f); p.Kind == kind {
				n++
			}
		}
		return n
	}
	if n := count(filepath.Join(dir, "backups"), KindAuto); n != 2 {
		t.Errorf("local keeps 2 automatic, has %d", n)
	}
	if n := count(share, KindAuto); n != 2 || count(share, KindManual) != 1 {
		t.Errorf("share: %d automatic, %d manual", n, count(share, KindManual))
	}
	stat, _ := s.Status(ctx)
	if r := stat.Locations["gone"]; !strings.Contains(r.Error, "not plugged in") {
		t.Errorf("missing drive: %+v", r)
	}
	if _, ok := stat.Locations["off"]; ok {
		t.Error("a disabled location ran")
	}
	if !s.Problem(ctx) {
		t.Error("a failing location is no problem")
	}

	// The list: one entry per file, with its locations.
	list, err := s.List(ctx)
	must(t, err)
	if len(list) != 3 || list[0].Kind != KindManual || !list[0].Own || len(list[0].Locations) != 3 {
		t.Fatalf("list %+v", list)
	}
	must(t, s.Delete(ctx, list[0].File, "usb"))
	if list, _ = s.List(ctx); len(list[0].Locations) != 2 {
		t.Errorf("after deleting on the stick: %v", list[0].Locations)
	}
	if err := s.Delete(ctx, "../kicker.db", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("path trick: %v", err)
	}

	// Restore from the list with the current passphrase; after a change it needs the old one.
	if _, err := s.Restore(ctx, list[0].File, ""); err != nil {
		t.Fatal(err)
	}
	old := pass(t, dir)
	_, err = emergency.SetPassphrase(dir, "")
	must(t, err)
	if _, err := s.Restore(ctx, list[0].File, ""); !errors.Is(err, ErrPassphrase) {
		t.Errorf("after a new passphrase: %v", err)
	}
	if _, err := s.Restore(ctx, list[0].File, old); err != nil {
		t.Errorf("with the old passphrase: %v", err)
	}

	// Weekdays and manual.
	c.Locations = c.Locations[:3]
	c.Schedule, c.Days = ScheduleDays, []int{1} // Mondays
	must(t, s.SetConfig(ctx, c))
	if n := s.NextRun(c); n == nil || n.Weekday() != time.Monday {
		t.Errorf("next run %v", n)
	}
	c.Schedule = ScheduleManual
	must(t, s.SetConfig(ctx, c))
	if s.NextRun(c) != nil || s.Problem(ctx) {
		t.Error("manual: a next run or a problem")
	}

	// Uploads keep a backup's name, others get one.
	name, err := s.Upload(strings.NewReader("x"), "my file.age")
	must(t, err)
	if p, ok := ParseFilename(name); !ok || p.Kind != KindUploaded {
		t.Errorf("uploaded as %s", name)
	}

	// Before updates/migrations: the last 3.
	for range 4 {
		_, err := Before(ctx, st, dir, KindPreUpdate)
		must(t, err)
	}
	if n := count(filepath.Join(dir, "backups", KindPreUpdate), KindPreUpdate); n < 1 || n > 3 {
		t.Errorf("pre-update keeps 3, has %d", n)
	}
}

func TestParseFilename(t *testing.T) {
	for f, want := range map[string]string{
		"kicker-backup-office-kicker-2026-11-25-031000-auto.tar.gz.age": "office-kicker auto",
		"kicker-backup-kicker-2026-11-25-0310.tar.gz.age":               "kicker manual",
		"kicker-backup-x-2026-11-25-031000-pre-update.tar.gz.age":       "x pre-update",
		"../kicker-backup-x-2026-11-25-031000.tar.gz.age":               "",
		"kicker.db": "",
	} {
		p, ok := ParseFilename(f)
		got := ""
		if ok {
			got = p.Kicker + " " + p.Kind
		}
		if got != want {
			t.Errorf("%s: %q, want %q", f, got, want)
		}
	}
}
