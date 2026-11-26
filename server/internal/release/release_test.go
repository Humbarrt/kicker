package release

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const pass = "correct horse battery"

// Test keys are unencrypted (fast); TestEncryptedKey covers the scrypt-encrypted kind.
var (
	keyOnce                             sync.Once
	testPubFile, testKeyFile, otherPubF []byte
)

func keys(t *testing.T) (pubFile, keyFile, otherPub []byte) {
	t.Helper()
	keyOnce.Do(func() {
		var err error
		if testPubFile, testKeyFile, err = GenerateKey(""); err != nil {
			t.Fatal(err)
		}
		if otherPubF, _, err = GenerateKey(""); err != nil {
			t.Fatal(err)
		}
	})
	return testPubFile, testKeyFile, otherPubF
}

func testRelease(t *testing.T) (dir string, manifest []byte, m Manifest) {
	t.Helper()
	dir = t.TempDir()
	srv := filepath.Join(dir, "kicker-linux-arm64")
	if err := os.WriteFile(srv, []byte("server binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	mods := filepath.Join(dir, "modules")
	mod := filepath.Join(mods, "goal-sensor")
	for _, env := range []string{"rp2040-zero", "pico-w"} {
		if err := os.MkdirAll(filepath.Join(mod, ".pio", "build", env), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mod, ".pio", "build", env, "firmware.uf2"), []byte("uf2 for "+env), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ini := `; a module
[platformio]
default_envs = rp2040-zero

[kicker]
manifest = kicker/goal-sensor
version = 0.4.0   ; the firmware version

[env]
framework = arduino
lib_deps =
    symlink://../../lib/KickerModule

[env:rp2040-zero]
board = waveshare_rp2040_zero

[env:pico-w]
board = rpipicow
`
	if err := os.WriteFile(filepath.Join(mod, "platformio.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "dist")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	b := Build{Version: "v1.2.0", Notes: "Fixes.", Date: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
		BaseURL: "https://github.com/Humbarrt/kicker/releases/download/v1.2.0/", Out: out, Servers: []string{srv}, Firmware: mods}
	manifest, m, err := b.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	return out, manifest, m
}

func TestManifest(t *testing.T) {
	dir, data, m := testRelease(t)
	if m.Version != "1.2.0" || len(m.Server) != 1 || len(m.Firmware) != 2 || !bytes.HasSuffix(data, []byte("}\n")) {
		t.Fatalf("manifest: %+v\n%s", m, data)
	}
	s, ok := m.ServerFor("linux", "arm64")
	if !ok || s.URL != "https://github.com/Humbarrt/kicker/releases/download/v1.2.0/kicker-linux-arm64" || s.Size != 13 {
		t.Errorf("server: %+v", s)
	}
	f, ok := m.FirmwareFor("kicker/goal-sensor", "waveshare_rp2040_zero")
	if !ok || f.Name != "kicker-goal-sensor-0.4.0-waveshare_rp2040_zero.uf2" || f.FW != "kicker-goal-sensor" || f.FWVer != "0.4.0" || f.Upd != "uf2" {
		t.Errorf("firmware: %+v", f)
	}
	if err := f.CheckFile(filepath.Join(dir, f.Name)); err != nil {
		t.Errorf("copied firmware: %v", err)
	}
	if _, ok := m.FirmwareFor("kicker/goal-sensor", "rpipicow"); !ok {
		t.Error("pico-w build missing")
	}
	if err := f.Check(strings.NewReader("something else")); !errors.Is(err, ErrChecksum) {
		t.Errorf("wrong file: %v", err)
	}
}

func TestSignAndVerify(t *testing.T) {
	_, data, _ := testRelease(t)
	pubFile, keyFile, otherPub := keys(t)
	pub, err := PublicKeyString(pubFile)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := Sign(data, keyFile, "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(sig, []byte("trusted comment: kicker 1.2.0 release.json")) {
		t.Errorf("signature:\n%s", sig)
	}
	m, err := Verify(data, sig, pub)
	if err != nil || m.Version != "1.2.0" {
		t.Fatalf("verify: %v %+v", err, m)
	}
	// the key line or the whole public key file both work
	if _, err := Verify(data, sig, string(pubFile)); err != nil {
		t.Errorf("verify with the key file: %v", err)
	}

	tampered := bytes.Replace(data, []byte(`"size": 13`), []byte(`"size": 14`), 1)
	if bytes.Equal(tampered, data) {
		t.Fatal("tampering didn't change anything")
	}
	if _, err := Verify(tampered, sig, pub); !errors.Is(err, ErrBadSignature) {
		t.Errorf("tampered manifest: %v", err)
	}
	other, _ := PublicKeyString(otherPub)
	if _, err := Verify(data, sig, other); !errors.Is(err, ErrBadSignature) {
		t.Errorf("another key: %v", err)
	}
	if _, err := Verify(data, sig, "not a key"); !errors.Is(err, ErrBadKey) {
		t.Errorf("bad key: %v", err)
	}
	badComment := bytes.Replace(sig, []byte("kicker 1.2.0"), []byte("kicker 9.9.9"), 1)
	if _, err := Verify(data, badComment, pub); !errors.Is(err, ErrBadSignature) {
		t.Errorf("changed trusted comment: %v", err)
	}
	// a correctly signed file that isn't a valid manifest is refused too
	if _, err := Verify([]byte(`{"format":2}`), sig, pub); !errors.Is(err, ErrBadSignature) {
		t.Errorf("other content: %v", err)
	}
}

// The real key is encrypted with a password (scrypt: a few seconds per use).
func TestEncryptedKey(t *testing.T) {
	_, data, _ := testRelease(t)
	pubFile, keyFile, err := GenerateKey(pass)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(keyFile, []byte("untrusted comment: minisign encrypted secret key")) {
		t.Errorf("key file:\n%s", keyFile)
	}
	if _, err := Sign(data, keyFile, "wrong password"); err == nil {
		t.Error("signed with a wrong password")
	}
	sig, err := Sign(data, keyFile, pass)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(data, sig, string(pubFile)); err != nil {
		t.Error(err)
	}
}

func TestValidate(t *testing.T) {
	_, _, m := testRelease(t)
	for name, change := range map[string]func(*Manifest){
		"format":      func(m *Manifest) { m.Format = 2 },
		"version":     func(m *Manifest) { m.Version = "1.2" },
		"min version": func(m *Manifest) { m.MinVersion = "x" },
		"path name":   func(m *Manifest) { m.Server[0].Name = "../kicker" },
		"sha256":      func(m *Manifest) { m.Server[0].SHA256 = "abc" },
		"arch":        func(m *Manifest) { m.Server[0].Arch = "" },
		"fw version":  func(m *Manifest) { m.Firmware[0].FWVer = "dev" },
	} {
		c := m
		c.Server = append([]Server(nil), m.Server...)
		c.Firmware = append([]Firmware(nil), m.Firmware...)
		change(&c)
		if err := c.Validate(); !errors.Is(err, ErrBadManifest) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestFirmwareModulesErrors(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "x")
	_ = os.MkdirAll(mod, 0o755)
	_ = os.WriteFile(filepath.Join(mod, "platformio.ini"), []byte("[env:a]\nboard = b\n"), 0o644)
	if _, err := FirmwareModules(dir); err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Errorf("no [kicker] section: %v", err)
	}
	b := Build{Version: "1.0.0", BaseURL: "u", Out: dir, Firmware: filepath.Join(dir, "none")}
	if _, _, err := b.Manifest(); err != nil {
		t.Errorf("no modules at all is fine: %v", err)
	}
	b.Servers = []string{filepath.Join(dir, "kicker.exe")}
	if _, _, err := b.Manifest(); err == nil {
		t.Error("badly named server binary accepted")
	}
}

func TestVersions(t *testing.T) {
	order := []string{"0.9.0", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.10.0", "2.0.0"}
	for i := range order {
		for j := range order {
			a, err := ParseVersion(order[i])
			if err != nil {
				t.Fatal(order[i], err)
			}
			b, _ := ParseVersion(order[j])
			want := sign(i - j)
			if got := a.Compare(b); got != want {
				t.Errorf("%s vs %s: %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	for _, bad := range []string{"", "dev", "1", "1.2", "1.2.3.4", "01.2.3", "1.2.3-", "1.2.3-01", "1.2.3-a..b", "1.2.-3", "1.2.3+", "a.b.c", "1.2.3-ü"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if v, err := ParseVersion("v1.2.3-rc.1+build.5"); err != nil || v.String() != "1.2.3-rc.1" || !v.Prerelease() {
		t.Errorf("v-prefix and build metadata: %v %v", v, err)
	}
	if !Newer("1.2.0", "1.1.9") || Newer("1.1.9", "1.2.0") || Newer("1.2.0", "dev") || Newer("dev", "1.0.0") {
		t.Error("Newer")
	}
}
