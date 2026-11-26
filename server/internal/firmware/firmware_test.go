package firmware

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/modules"
	"github.com/Humbarrt/kicker/server/internal/modules/transport"
	"github.com/Humbarrt/kicker/server/internal/modules/virtual"
	"github.com/Humbarrt/kicker/server/internal/release"
)

// makeUF2 builds a UF2 file of n blocks for a family.
func makeUF2(n int, family uint32) []byte {
	out := make([]byte, n*uf2Block)
	for i := 0; i < n; i++ {
		b := out[i*uf2Block:]
		le := binary.LittleEndian
		le.PutUint32(b[0:], uf2Magic0)
		le.PutUint32(b[4:], uf2Magic1)
		le.PutUint32(b[8:], flagFamily)
		le.PutUint32(b[12:], 0x10000000+uint32(i*256))
		le.PutUint32(b[16:], 256)
		le.PutUint32(b[20:], uint32(i))
		le.PutUint32(b[24:], uint32(n))
		le.PutUint32(b[28:], family)
		le.PutUint32(b[508:], uf2MagicEnd)
	}
	return out
}

func TestParseUF2(t *testing.T) {
	u, err := ParseUF2(makeUF2(4, FamilyRP2040))
	if err != nil || u.Blocks != 4 || u.Size != 1024 || u.Chip() != ChipRP2040 {
		t.Fatalf("%+v %v", u, err)
	}
	if u, _ := ParseUF2(makeUF2(2, FamilyRP2350ARMS)); u.Chip() != ChipRP2350 {
		t.Errorf("rp2350: %s", u.Chip())
	}
	bad := makeUF2(3, FamilyRP2040)
	binary.LittleEndian.PutUint32(bad[uf2Block+508:], 0)
	for name, data := range map[string][]byte{
		"empty": nil, "odd size": make([]byte, 100), "magic": bad,
		"mixed": append(makeUF2(1, FamilyRP2040), makeUF2(1, FamilyRP2350ARMS)...),
		"text":  []byte(strings.Repeat("x", 512)),
	} {
		if _, err := ParseUF2(data); !errors.Is(err, ErrUF2) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// --- fakes: a host whose module reboots into a boot drive, and the drives -------------------

type fakeWorld struct {
	mu       sync.Mutex
	mods     map[string]*modules.ModuleInfo
	drives   []Drive
	written  []string
	flashVer string // the version the module comes back with
	noDrive  bool   // the module never shows up as a drive
	chip     string
}

func (w *fakeWorld) Modules() []modules.ModuleInfo {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []modules.ModuleInfo
	for _, m := range w.mods {
		out = append(out, *m)
	}
	return out
}

func (w *fakeWorld) EnterBootloader(_ context.Context, uid string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	m := w.mods[uid]
	m.Online = false
	if !w.noDrive {
		w.drives = append(w.drives, Drive{Name: "sda", Dev: "/dev/sda", USBPath: m.USBPath, Chip: w.chip})
	}
	return nil
}

func (w *fakeWorld) VirtualModule(string) *virtual.Module { return nil }

func (w *fakeWorld) List() []Drive {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Drive(nil), w.drives...)
}

func (w *fakeWorld) Write(_ context.Context, d Drive, u UF2) error {
	w.mu.Lock()
	w.written = append(w.written, d.Dev)
	w.drives = nil
	w.mu.Unlock()
	go func() {
		time.Sleep(50 * time.Millisecond)
		w.mu.Lock()
		defer w.mu.Unlock()
		for _, m := range w.mods {
			if m.USBPath == d.USBPath {
				m.Online, m.FWVersion, m.ConnectedAt = true, w.flashVer, time.Now()
			}
		}
	}()
	return nil
}

type fakeReleases struct {
	m     *release.Manifest
	files map[string][]byte
}

func (r fakeReleases) CurrentRelease(context.Context) (*release.Manifest, error) { return r.m, nil }
func (r fakeReleases) Download(_ context.Context, a release.Asset, path string) error {
	if err := os.WriteFile(path, r.files[a.Name], 0o644); err != nil {
		return err
	}
	return a.CheckFile(path)
}

func setup(t *testing.T) (*Service, *fakeWorld) {
	t.Helper()
	w := &fakeWorld{chip: ChipRP2040, flashVer: "0.5.0", mods: map[string]*modules.ModuleInfo{
		"m1": {UID: "m1", Name: "Goals", Online: true, Manifest: "kicker/goal-sensor", Hardware: "waveshare_rp2040_zero", FWVersion: "0.4.0", Update: "uf2", USBPath: "1-1.2", ConnectedAt: time.Now().Add(-time.Hour)},
		"m2": {UID: "m2", Name: "Coins", Online: true, Manifest: "kicker/coin-acceptor", Hardware: "waveshare_rp2040_zero", FWVersion: "0.1.0", Update: "uf2", USBPath: "1-1.3"},
	}}
	// the release, built like CI does
	dir := t.TempDir()
	mod := filepath.Join(dir, "modules", "goal-sensor")
	_ = os.MkdirAll(filepath.Join(mod, ".pio", "build", "zero"), 0o755)
	_ = os.WriteFile(filepath.Join(mod, "platformio.ini"), []byte("[kicker]\nmanifest = kicker/goal-sensor\nversion = 0.5.0\n[env:zero]\nboard = waveshare_rp2040_zero\n"), 0o644)
	_ = os.WriteFile(filepath.Join(mod, ".pio", "build", "zero", "firmware.uf2"), makeUF2(8, FamilyRP2040), 0o644)
	out := filepath.Join(dir, "dist")
	_ = os.MkdirAll(out, 0o755)
	_, m, err := release.Build{Version: "1.0.0", BaseURL: "http://x", Out: out, Firmware: filepath.Join(dir, "modules"), Date: time.Now()}.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range m.Firmware {
		files[f.Name], _ = os.ReadFile(filepath.Join(out, f.Name))
	}
	s := New(Options{Host: w, Drives: w, Releases: fakeReleases{&m, files}, DataDir: t.TempDir(),
		BootWait: time.Second, BackWait: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return s, w
}

func wait(t *testing.T, s *Service, uid string) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, j := range s.Status(context.Background()).Jobs {
			if j.UID == uid && (j.Step == "done" || j.Step == "failed") {
				return j
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not finished: %+v", uid, s.Status(context.Background()).Jobs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	s, w := setup(t)
	offers := s.Offers(ctx)
	if len(offers) != 1 || offers[0].UID != "m1" || offers[0].Version != "0.5.0" || s.Count(ctx) != 1 {
		t.Fatalf("offers: %+v", offers)
	}
	if err := s.Update(ctx, []string{"m2"}); !errors.Is(err, ErrNoOffer) {
		t.Errorf("no offer: %v", err)
	}
	busy := true
	s.o.Busy = func() bool { return busy }
	if err := s.Update(ctx, []string{"m1"}); !errors.Is(err, ErrBusy) {
		t.Errorf("during a game: %v", err)
	}
	busy = false
	if err := s.Update(ctx, []string{"m1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, []string{"m1"}); !errors.Is(err, ErrQueued) && !errors.Is(err, ErrNoOffer) {
		t.Errorf("twice: %v", err)
	}
	j := wait(t, s, "m1")
	if j.Step != "done" || len(w.written) != 1 || j.From != "0.4.0" || j.To != "0.5.0" {
		t.Fatalf("job: %+v written %v", j, w.written)
	}
	if len(s.Offers(ctx)) != 0 {
		t.Error("still offered after the update")
	}
	// the file was kept: a second module of the same kind doesn't download it again
	if _, err := os.Stat(filepath.Join(s.o.DataDir, "firmware", j.File)); err != nil {
		t.Error(err)
	}
}

func TestUpdateFailures(t *testing.T) {
	ctx := context.Background()

	// the boot drive never appears
	s, w := setup(t)
	w.noDrive = true
	_ = s.Update(ctx, []string{"m1"})
	if j := wait(t, s, "m1"); j.Step != "failed" || !strings.Contains(j.Error, "boot drive") {
		t.Errorf("no drive: %+v", j)
	}

	// the module comes back with another version
	s, w = setup(t)
	w.flashVer = "0.4.0"
	_ = s.Update(ctx, []string{"m1"})
	if j := wait(t, s, "m1"); j.Step != "failed" || !strings.Contains(j.Error, "0.4.0") {
		t.Errorf("old version: %+v", j)
	}

	// an RP2350 drive for an RP2040 file: not written, the module stays in its bootloader
	s, w = setup(t)
	w.chip = ChipRP2350
	_ = s.Update(ctx, []string{"m1"})
	j := wait(t, s, "m1")
	if j.Step != "failed" || !strings.Contains(j.Error, "RP2350") || len(w.written) != 0 {
		t.Errorf("wrong chip: %+v", j)
	}
	st := s.Status(ctx)
	if len(st.Drives) != 1 || st.Drives[0].USBPath != "1-1.2" {
		t.Fatalf("the waiting module should be listed: %+v", st.Drives)
	}
	// … and can be flashed with an uploaded file for its chip
	if err := s.FlashDrive(ctx, "sda", "own.uf2", makeUF2(2, FamilyRP2350ARMS)); err != nil {
		t.Fatal(err)
	}
	if j := wait(t, s, "drive:sda"); j.Step != "done" || len(w.written) != 1 {
		t.Errorf("flash the drive: %+v", j)
	}
}

func TestUpload(t *testing.T) {
	ctx := context.Background()
	s, w := setup(t)
	if err := s.Upload(ctx, "m2", "x.uf2", []byte("nope")); !errors.Is(err, ErrUF2) {
		t.Errorf("not a uf2: %v", err)
	}
	w.flashVer = "0.1.1-dev"
	if err := s.Upload(ctx, "m2", "coins.uf2", makeUF2(3, FamilyRP2040)); err != nil {
		t.Fatal(err)
	}
	if j := wait(t, s, "m2"); j.Step != "done" || j.File != "coins.uf2" || j.To != "" {
		t.Errorf("upload: %+v", j)
	}
}

// A fake sysfs: a boot drive on USB 1-1.2 and a module's serial port on 1-1.3.
func TestSysfs(t *testing.T) {
	root := t.TempDir()
	old := transport.SysRoot
	transport.SysRoot = root
	t.Cleanup(func() { transport.SysRoot = old })
	mk := func(dir, vid, pid string) string {
		p := filepath.Join(root, "devices/platform/scb/usb1/1-1", dir)
		_ = os.MkdirAll(p, 0o755)
		_ = os.WriteFile(filepath.Join(p, "idVendor"), []byte(vid+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(p, "idProduct"), []byte(pid+"\n"), 0o644)
		return p
	}
	boot := mk("1-1.2", "2e8a", "0003")
	blk := filepath.Join(boot, "1-1.2:1.0/host0/target0:0:0/0:0:0:0/block/sda")
	_ = os.MkdirAll(blk, 0o755)
	_ = os.MkdirAll(filepath.Join(root, "block"), 0o755)
	_ = os.Symlink(blk, filepath.Join(root, "block", "sda"))
	other := mk("1-1.4", "0781", "5581") // a USB stick: not a boot drive
	_ = os.MkdirAll(filepath.Join(other, "1-1.4:1.0/host1/block/sdb"), 0o755)
	_ = os.Symlink(filepath.Join(other, "1-1.4:1.0/host1/block/sdb"), filepath.Join(root, "block", "sdb"))

	mod := mk("1-1.3", "2e8a", "000a")
	iface := filepath.Join(mod, "1-1.3:1.0")
	_ = os.MkdirAll(iface, 0o755)
	_ = os.MkdirAll(filepath.Join(root, "class/tty/ttyACM0"), 0o755)
	_ = os.Symlink(iface, filepath.Join(root, "class/tty/ttyACM0/device"))

	drives := SysDrives{}.List()
	if len(drives) != 1 || drives[0] != (Drive{Name: "sda", Dev: "/dev/sda", USBPath: "1-1.2", Chip: ChipRP2040}) {
		t.Errorf("drives: %+v", drives)
	}
	if p := transport.USBPath("/dev/ttyACM0"); p != "1-1.3" {
		t.Errorf("usb path: %q", p)
	}
	if p := transport.USBPath("/dev/ttyUSB9"); p != "" {
		t.Errorf("unknown port: %q", p)
	}
}

func TestWriteRaw(t *testing.T) {
	dev := filepath.Join(t.TempDir(), "sda")
	_ = os.WriteFile(dev, nil, 0o600)
	data := makeUF2(100, FamilyRP2040)
	if err := writeRaw(dev, data); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dev); string(got) != string(data) {
		t.Error("written blocks differ")
	}
	_ = os.Chmod(dev, 0o400)
	if os.Getuid() != 0 { // root may write anyway
		if err := writeRaw(dev, data); !errors.Is(err, os.ErrPermission) {
			t.Errorf("read-only: %v", err)
		}
	}
}
