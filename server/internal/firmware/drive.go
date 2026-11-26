package firmware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Humbarrt/kicker/server/internal/modules/transport"
)

// Drive is an RP2 boot drive: a module in its bootloader (USB 2e8a:0003 RP2040, 2e8a:000f
// RP2350).
type Drive struct {
	Name    string `json:"name"`     // block device, e.g. sda
	Dev     string `json:"dev"`      // /dev/sda
	USBPath string `json:"usb_path"` // e.g. 1-1.2: the module's USB port
	Chip    string `json:"chip"`
}

// Drives lists boot drives.
type Drives interface {
	List() []Drive
	// Write writes a UF2 file to the drive; the chip reboots into the new firmware.
	Write(ctx context.Context, d Drive, u UF2) error
}

// SysDrives finds boot drives in sysfs and writes the blocks to the block device (spec 10 §9,
// decision C), falling back to mounting the drive with udisksctl and copying the file.
type SysDrives struct{}

var usbDevice = regexp.MustCompile(`^\d+-\d+(\.\d+)*$`)

// List finds the boot drives (Linux; none elsewhere).
func (SysDrives) List() []Drive {
	root := transport.ResolvedSysRoot()
	entries, _ := os.ReadDir(filepath.Join(root, "block"))
	var out []Drive
	for _, e := range entries {
		real, err := filepath.EvalSymlinks(filepath.Join(root, "block", e.Name()))
		if err != nil {
			continue
		}
		for p := filepath.Dir(real); strings.HasPrefix(p, root) && p != root; p = filepath.Dir(p) {
			if !usbDevice.MatchString(filepath.Base(p)) {
				continue
			}
			vid, _ := os.ReadFile(filepath.Join(p, "idVendor"))
			pid, _ := os.ReadFile(filepath.Join(p, "idProduct"))
			if strings.TrimSpace(string(vid)) == "2e8a" {
				chip := ""
				switch strings.TrimSpace(string(pid)) {
				case "0003":
					chip = ChipRP2040
				case "000f":
					chip = ChipRP2350
				}
				if chip != "" {
					out = append(out, Drive{Name: e.Name(), Dev: "/dev/" + e.Name(), USBPath: filepath.Base(p), Chip: chip})
				}
			}
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Write writes the blocks straight to the block device; without permission for it (no udev rule)
// it mounts the drive with udisksctl and copies the file.
func (s SysDrives) Write(ctx context.Context, d Drive, u UF2) error {
	err := writeRaw(d.Dev, u.Data)
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	if _, lerr := exec.LookPath("udisksctl"); lerr != nil {
		return fmt.Errorf("no permission to write %s (the udev rule of the install script gives it): %w", d.Dev, err)
	}
	return mountCopy(ctx, d, u.Data)
}

// writeRaw writes the file sector by sector. The boot ROM flashes every UF2 block it receives,
// whichever sector it is written to, and reboots after the last one – so an error once all
// blocks are written (the device going away) is not a failure.
func writeRaw(dev string, data []byte) error {
	f, err := os.OpenFile(dev, os.O_WRONLY|os.O_SYNC, 0)
	if err != nil {
		return err
	}
	written := 0
	for written < len(data) {
		n, err := f.Write(data[written:min(written+64*uf2Block, len(data))])
		written += n
		if err != nil {
			_ = f.Close()
			if written >= len(data) {
				return nil
			}
			return fmt.Errorf("writing %s: %w", dev, err)
		}
	}
	_ = f.Sync()
	_ = f.Close()
	return nil
}

var mountedAt = regexp.MustCompile(` at (/\S+)`)

// mountCopy mounts the drive's partition (or the whole drive) and copies the file onto it.
func mountCopy(ctx context.Context, d Drive, data []byte) error {
	part := d.Dev + "1"
	if _, err := os.Stat(part); err != nil {
		part = d.Dev
	}
	out, err := exec.CommandContext(ctx, "udisksctl", "mount", "--no-user-interaction", "-b", part).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mounting %s: %v: %s", part, err, bytes.TrimSpace(out))
	}
	m := mountedAt.FindSubmatch(out)
	if m == nil {
		return fmt.Errorf("mounting %s: unexpected answer %q", part, bytes.TrimSpace(out))
	}
	dst := filepath.Join(strings.TrimSuffix(string(m[1]), "."), "firmware.uf2")
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	_ = f.Sync()
	_ = f.Close()
	return err // the drive disappears when the chip reboots; no unmount needed
}
