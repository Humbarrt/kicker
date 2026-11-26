package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Humbarrt/kicker/server/internal/emergency"
	"github.com/Humbarrt/kicker/server/internal/tlsca"
)

// A restore happens in two steps (spec 10 §2): Stage unpacks and checks the backup next to the
// live data; after a restart, ApplyPending swaps it in before the database is opened.
const (
	restoreDir   = "restore"
	readyFile    = "READY" // the manifest of a checked backup waiting in restore/files
	beforeDir    = "backups/before-restore"
	keepBeforeRe = 3
)

// Stage unpacks and checks a backup into <data>/restore. Only a backup that passed every check
// is marked ready; nothing of the live data changes.
func Stage(ctx context.Context, dataDir string, r io.Reader, passphrase string) (Manifest, error) {
	dir := filepath.Join(dataDir, restoreDir)
	if err := os.RemoveAll(dir); err != nil {
		return Manifest{}, err
	}
	m, err := Extract(ctx, r, passphrase, filepath.Join(dir, "files"))
	if err != nil {
		_ = os.RemoveAll(dir)
		return m, err
	}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, readyFile), raw, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return m, err
	}
	return m, nil
}

// Pending returns the manifest of a staged restore, if one waits for the restart.
func Pending(dataDir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, restoreDir, readyFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var m Manifest
	return &m, json.Unmarshal(raw, &m)
}

// The live files a restore replaces, relative to the data folder.
func liveFiles() []string {
	return []string{"kicker.db", "kicker.db-wal", "kicker.db-shm", "media",
		tlsca.CAKeyFile, tlsca.CACertFile, tlsca.ServerKeyFile, tlsca.ServerCertFile, emergency.PassphraseFile}
}

// ApplyPending swaps a staged restore in; call it before the database is opened. The current
// files move to <data>/backups/before-restore/<time> (the last 3 are kept; they don't leave the
// Pi). The server certificate isn't restored: it is reissued from the restored CA.
func ApplyPending(dataDir string) (*Manifest, error) {
	m, err := Pending(dataDir)
	if err != nil || m == nil {
		return nil, err
	}
	files := filepath.Join(dataDir, restoreDir, "files")
	before := filepath.Join(dataDir, beforeDir, time.Now().UTC().Format("2006-01-02-150405"))
	if err := os.MkdirAll(before, 0o750); err != nil {
		return nil, err
	}
	for _, f := range liveFiles() {
		if err := os.Rename(filepath.Join(dataDir, f), filepath.Join(before, f)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("moving the current %s aside: %w", f, err)
		}
	}
	moves := map[string]string{"kicker.db": "kicker.db", "media": "media", emergency.PassphraseFile: emergency.PassphraseFile}
	for _, f := range identityFiles {
		moves["identity/"+f] = f
	}
	for from, to := range moves {
		if err := os.Rename(filepath.Join(files, filepath.FromSlash(from)), filepath.Join(dataDir, to)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("restoring %s: %w (the previous files are in %s)", from, err, before)
		}
	}
	if err := os.RemoveAll(filepath.Join(dataDir, restoreDir)); err != nil {
		return nil, err
	}
	prune(filepath.Dir(before), keepBeforeRe)
	return m, nil
}

// prune keeps the newest n entries of a folder (names sort by time).
func prune(dir string, n int) {
	es, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for len(names) > n {
		_ = os.RemoveAll(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}
