package transport

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SysRoot is where sysfs is mounted (tests point it at a fake tree).
var SysRoot = "/sys"

// usbDevice matches a USB device directory in sysfs: "1-1", "1-1.2", "3-1.4.2" (not an
// interface like "1-1.2:1.0").
var usbDevice = regexp.MustCompile(`^\d+-\d+(\.\d+)*$`)

// USBPath returns the USB port a serial device is plugged into, e.g. "1-1.2" (Linux; "" if it
// can't be told). A module keeps this path when it reboots into its bootloader, which is how
// its boot drive is found (spec 10 §4).
func USBPath(port string) string {
	dev, err := filepath.EvalSymlinks(filepath.Join(SysRoot, "class", "tty", filepath.Base(port), "device"))
	if err != nil {
		return ""
	}
	return USBAncestor(dev)
}

// ResolvedSysRoot is SysRoot with symlinks resolved, so it compares with resolved sysfs paths
// (a test's temp folder on macOS lives behind /var → /private/var).
func ResolvedSysRoot() string {
	if r, err := filepath.EvalSymlinks(SysRoot); err == nil {
		return r
	}
	return SysRoot
}

// USBAncestor returns the nearest USB device directory name above a (resolved) sysfs path.
func USBAncestor(path string) string {
	root := ResolvedSysRoot()
	for p := path; p != "/" && p != "." && strings.HasPrefix(p, root); p = filepath.Dir(p) {
		if usbDevice.MatchString(filepath.Base(p)) {
			if _, err := os.Stat(filepath.Join(p, "idVendor")); err == nil {
				return filepath.Base(p)
			}
		}
	}
	return ""
}
