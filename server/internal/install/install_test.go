package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRoot is a minimal Linux root: the account files useradd needs, a hostname, the old layout.
func fakeRoot(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("useradd --root needs root")
	}
	if _, err := exec.LookPath("useradd"); err != nil {
		t.Skip("no useradd")
	}
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "etc"), 0o755)
	files := map[string]string{
		"passwd":   "root:x:0:0:root:/root:/bin/bash\npi:x:1000:1000::/home/pi:/bin/bash\n",
		"group":    "root:x:0:\ndialout:x:20:pi\npi:x:1000:\n",
		"shadow":   "root:*:19000:0:99999:7:::\npi:*:19000:0:99999:7:::\n",
		"gshadow":  "root:*::\ndialout:!::pi\npi:!::\n",
		"hostname": "raspberrypi\n",
		"hosts":    "127.0.0.1\tlocalhost\n127.0.1.1\traspberrypi\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, "etc", name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(root, "opt/kicker"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "opt/kicker/kicker"), []byte("old"), 0o755)
	return root
}

func binary(t *testing.T, content string) string {
	p := filepath.Join(t.TempDir(), "kicker-linux-arm64")
	_ = os.WriteFile(p, []byte(content), 0o755)
	return p
}

func TestInstallIntoImage(t *testing.T) {
	root := fakeRoot(t)
	o := Options{Root: root, Binary: binary(t, "v1"), Version: "0.6.0", Hostname: "kicker"}
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	uid, gid, ok := lookup(root, User)
	if !ok || uid == 0 || gid == 0 {
		t.Fatalf("user: %d %d %v", uid, gid, ok)
	}
	if g, _ := os.ReadFile(filepath.Join(root, "etc/group")); !strings.Contains(string(g), "dialout:x:20:pi,kicker") {
		t.Errorf("dialout: %s", g)
	}
	bin := filepath.Join(root, DataDir, "bin")
	if cur, _ := os.Readlink(filepath.Join(bin, "current")); cur != "kicker-0.6.0" {
		t.Errorf("current: %q", cur)
	}
	if data, _ := os.ReadFile(filepath.Join(bin, "kicker-0.6.0")); string(data) != "v1" {
		t.Error("binary not copied")
	}
	if fi, _ := os.Stat(filepath.Join(root, DataDir)); fi == nil || fi.Sys() == nil {
		t.Fatal("no data folder")
	}
	for _, p := range []string{UnitPath, RulesPath, USBRoot} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Error(err)
		}
	}
	if l, _ := os.Readlink(filepath.Join(root, "etc/systemd/system/multi-user.target.wants/kicker.service")); l != UnitPath {
		t.Errorf("not enabled: %q", l)
	}
	if u, _ := os.ReadFile(filepath.Join(root, UnitPath)); !strings.Contains(string(u), "ExecStart=/var/lib/kicker/bin/current serve") {
		t.Error("unit")
	}
	if h, _ := os.ReadFile(filepath.Join(root, "etc/hostname")); string(h) != "kicker\n" {
		t.Errorf("hostname %q", h)
	}
	if h, _ := os.ReadFile(filepath.Join(root, "etc/hosts")); !strings.Contains(string(h), "127.0.1.1\tkicker\n") || strings.Contains(string(h), "raspberrypi") {
		t.Errorf("hosts %q", h)
	}
	if _, err := os.Stat(filepath.Join(root, "opt/kicker")); !os.IsNotExist(err) {
		t.Error("old layout left")
	}

	// installing again: a new version becomes current, the old one previous; the user stays
	o.Binary, o.Version, o.Hostname = binary(t, "v2"), "0.7.0", ""
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if cur, _ := os.Readlink(filepath.Join(bin, "current")); cur != "kicker-0.7.0" {
		t.Errorf("current: %q", cur)
	}
	if prev, _ := os.Readlink(filepath.Join(bin, "previous")); prev != "kicker-0.6.0" {
		t.Errorf("previous: %q", prev)
	}
	if p, _ := os.ReadFile(filepath.Join(root, "etc/passwd")); strings.Count(string(p), "\nkicker:") != 1 {
		t.Errorf("passwd: %s", p)
	}
	if h, _ := os.ReadFile(filepath.Join(root, "etc/hostname")); string(h) != "kicker\n" {
		t.Errorf("hostname changed: %q", h)
	}
}

func TestInstallRefuses(t *testing.T) {
	if err := Run(Options{Root: "relative", Version: "1.0.0"}); err == nil {
		t.Error("relative root")
	}
	if err := Run(Options{Root: t.TempDir(), Version: "../x"}); err == nil {
		t.Error("version with a path")
	}
	if err := Run(Options{Root: t.TempDir(), Version: "1.0.0"}); err == nil {
		t.Error("not a Linux root")
	}
	if err := setHostname(Options{Root: t.TempDir()}, "Not_Valid"); err == nil {
		t.Error("bad host name")
	}
}
