package backup

import (
	"os"
	"path/filepath"
	"syscall"
)

// isMountPoint reports whether dir is a mounted file system: a folder under the USB root that
// is left behind after a drive was pulled is not (backups would land on the SD card).
func isMountPoint(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	parent, err := os.Stat(filepath.Dir(dir))
	if err != nil {
		return false
	}
	a, ok1 := fi.Sys().(*syscall.Stat_t)
	b, ok2 := parent.Sys().(*syscall.Stat_t)
	return ok1 && ok2 && a.Dev != b.Dev
}
