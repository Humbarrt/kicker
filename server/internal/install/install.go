// Package install sets a Raspberry Pi OS system up for kicker (spec 10 §5, spec 04 §6): the
// kicker user, the data folder with the binary in <data>/bin (so updates can switch it), the
// systemd unit, the udev rules and the host name. `kicker install` runs it on the Pi itself
// (the install script), and CI runs it from an x86 build against a mounted Raspberry Pi OS
// image (--root) to make the SD card image.
package install

import (
	"bufio"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

//go:embed files/kicker.service
var unit []byte

//go:embed files/99-kicker.rules
var rules []byte

// Paths on the target system.
const (
	DataDir   = "/var/lib/kicker"
	UnitPath  = "/etc/systemd/system/kicker.service"
	RulesPath = "/etc/udev/rules.d/99-kicker.rules"
	USBRoot   = "/media/kicker"
	User      = "kicker"
	oldDir    = "/opt/kicker" // the layout before M7 (spec 04 §6, first version)
)

// Options for Run.
type Options struct {
	Root     string // the system to set up: "/" or a mounted image
	Binary   string // the server binary for the target (its architecture)
	Version  string // its version: the file name in <data>/bin
	Hostname string // "" = leave it
	Out      io.Writer
	// Live: Root is the running system – reload systemd and udev and (re)start the service.
	Live  bool
	Start bool
}

var safeVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]*$`)

// Run installs (or updates the installation, keeping the data).
func Run(o Options) error {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if !filepath.IsAbs(o.Root) {
		return fmt.Errorf("the root %q is not an absolute path", o.Root)
	}
	if !safeVersion.MatchString(o.Version) {
		return fmt.Errorf("the version %q can't be a file name", o.Version)
	}
	if _, err := os.Stat(filepath.Join(o.Root, "etc", "passwd")); err != nil {
		return fmt.Errorf("%s doesn't look like a Linux system: %w", o.Root, err)
	}
	say := func(f string, a ...any) { fmt.Fprintf(o.Out, f+"\n", a...) }
	path := func(p string) string { return filepath.Join(o.Root, p) }

	// 1. the user (and its group), allowed to use the serial ports
	uid, gid, err := ensureUser(o.Root)
	if err != nil {
		return err
	}
	say("user %s (%d:%d)", User, uid, gid)

	// 2. the data folder and the binary: bin/kicker-<version>, bin/current → it
	bin := path(DataDir + "/bin")
	if err := os.MkdirAll(filepath.Dir(path(DataDir)), 0o755); err != nil { // /var/lib, if missing
		return err
	}
	for _, d := range []string{path(DataDir), bin} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return err
		}
		if err := os.Chown(d, uid, gid); err != nil {
			return err
		}
		if err := os.Chmod(d, 0o750); err != nil {
			return err
		}
	}
	name := "kicker-" + o.Version
	if err := copyExecutable(o.Binary, filepath.Join(bin, name), uid, gid); err != nil {
		return err
	}
	if cur, err := os.Readlink(filepath.Join(bin, "current")); err == nil && cur != name {
		if err := relink(bin, "previous", cur); err != nil {
			return err
		}
	}
	if err := relink(bin, "current", name); err != nil {
		return err
	}
	_ = os.Lchown(filepath.Join(bin, "current"), uid, gid)
	_ = os.Lchown(filepath.Join(bin, "previous"), uid, gid)
	say("binary %s/bin/%s (current)", DataDir, name)

	// 3. the unit, enabled; the udev rules; where USB drives are mounted
	if err := writeFile(path(UnitPath), unit, 0o644); err != nil {
		return err
	}
	wants := path("/etc/systemd/system/multi-user.target.wants")
	if err := os.MkdirAll(wants, 0o755); err != nil {
		return err
	}
	if err := relink(wants, "kicker.service", UnitPath); err != nil {
		return err
	}
	if err := writeFile(path(RulesPath), rules, 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(path(USBRoot), 0o755); err != nil {
		return err
	}
	say("systemd unit %s (enabled), udev rules %s", UnitPath, RulesPath)

	// 4. the layout before M7
	if _, err := os.Stat(path(oldDir)); err == nil {
		if err := os.RemoveAll(path(oldDir)); err != nil {
			return err
		}
		say("removed the old %s", oldDir)
	}

	// 5. the host name: kicker.local over mDNS
	if o.Hostname != "" {
		if err := setHostname(o, o.Hostname); err != nil {
			return err
		}
		say("host name %s (%s.local)", o.Hostname, o.Hostname)
	}

	if !o.Live {
		return nil
	}
	for _, c := range [][]string{{"systemctl", "daemon-reload"}, {"udevadm", "control", "--reload"}} {
		if err := run(c...); err != nil {
			return err
		}
	}
	if o.Start {
		if err := run("systemctl", "restart", "kicker"); err != nil {
			return err
		}
		say("kicker (re)started")
	}
	if _, err := os.Stat("/usr/sbin/avahi-daemon"); err != nil {
		say("note: avahi-daemon is not installed – kicker.local won't resolve (sudo apt install avahi-daemon)")
	}
	return nil
}

func run(args ...string) error {
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureUser creates the system user and its group in root's /etc (useradd --root works on a
// mounted image without running anything inside it) and adds it to dialout.
func ensureUser(root string) (uid, gid int, err error) {
	if _, _, ok := lookup(root, User); !ok {
		args := []string{"--system", "--user-group", "--home-dir", DataDir, "--no-create-home", "--shell", "/usr/sbin/nologin", User}
		if root != "/" {
			args = append([]string{"--root", root}, args...)
		}
		if err := run(append([]string{"useradd"}, args...)...); err != nil {
			return 0, 0, err
		}
	}
	if hasGroup(root, "dialout") {
		args := []string{"--append", "--groups", "dialout", User}
		if root != "/" {
			args = append([]string{"--root", root}, args...)
		}
		if err := run(append([]string{"usermod"}, args...)...); err != nil {
			return 0, 0, err
		}
	}
	uid, gid, ok := lookup(root, User)
	if !ok {
		return 0, 0, errors.New("the kicker user wasn't created")
	}
	return uid, gid, nil
}

// lookup reads a user's ids from root's /etc/passwd (not the host's: an image has its own).
func lookup(root, user string) (uid, gid int, ok bool) {
	f, err := os.Open(filepath.Join(root, "etc", "passwd"))
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), ":")
		if len(p) >= 4 && p[0] == user {
			u, err1 := strconv.Atoi(p[2])
			g, err2 := strconv.Atoi(p[3])
			return u, g, err1 == nil && err2 == nil
		}
	}
	return 0, 0, false
}

func hasGroup(root, group string) bool {
	data, err := os.ReadFile(filepath.Join(root, "etc", "group"))
	return err == nil && strings.Contains("\n"+string(data), "\n"+group+":")
}

func copyExecutable(src, dst string, uid, gid int) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chown(tmp, uid, gid); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}

// relink points dir/link at target, atomically.
func relink(dir, link, target string) error {
	tmp := filepath.Join(dir, "."+link+".new")
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, link))
}

// setHostname writes /etc/hostname and the 127.0.1.1 line of /etc/hosts (Raspberry Pi OS keeps
// the host name there); on the running system hostnamectl applies it at once.
func setHostname(o Options, name string) error {
	if !regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`).MatchString(name) {
		return fmt.Errorf("%q is not a valid host name", name)
	}
	if o.Live {
		if _, err := exec.LookPath("hostnamectl"); err == nil {
			if err := run("hostnamectl", "set-hostname", name); err != nil {
				return err
			}
		}
	}
	if err := os.WriteFile(filepath.Join(o.Root, "etc", "hostname"), []byte(name+"\n"), 0o644); err != nil {
		return err
	}
	hosts := filepath.Join(o.Root, "etc", "hosts")
	data, _ := os.ReadFile(hosts)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	found := false
	for i, l := range lines {
		if strings.HasPrefix(l, "127.0.1.1") {
			lines[i], found = "127.0.1.1\t"+name, true
		}
	}
	if !found {
		lines = append(lines, "127.0.1.1\t"+name)
	}
	return os.WriteFile(hosts, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}
