package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Humbarrt/kicker/server/internal/backup"
	"github.com/Humbarrt/kicker/server/internal/store"
)

// kicker backup [--data DIR] [--out FILE]: an encrypted backup from a shell (spec 10 §2). It
// works while the server runs: the snapshot is consistent.
func backupCmd(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	dataDir := fs.String("data", "./data", "data folder")
	out := fs.String("out", "", "file to write (default: kicker-backup-<name>-<time>.tar.gz.age in the current folder)")
	_ = fs.Parse(args)
	ctx := context.Background()
	path := filepath.Join(*dataDir, "kicker.db")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no database at %s", path)
	}
	st, err := store.Open(ctx, path)
	if err != nil {
		return err
	}
	defer st.Close()
	var name string
	if _, err := st.GetSetting(ctx, store.SettingName, &name); err != nil {
		return err
	}
	id, err := st.ServerID(ctx)
	if err != nil {
		return err
	}
	svc := backup.New(backup.Options{Store: st, DataDir: *dataDir, ServerID: id, Name: func() string { return name }})
	if *out == "" {
		*out = svc.Filename()
	}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	m, err := svc.Download(ctx, f)
	if err = errors.Join(err, f.Close()); err != nil {
		_ = os.Remove(*out)
		return err
	}
	fmt.Printf("%s: %d files, schema v%d. It opens with the backup passphrase from the emergency kit.\n", *out, len(m.Files), m.Schema)
	return nil
}

// kicker restore [--data DIR] FILE: checks a backup with its passphrase (asked on stdin) and stages
// it; the server swaps it in at its next start.
func restoreCmd(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	dataDir := fs.String("data", "./data", "data folder")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("usage: kicker restore [--data DIR] <backup file>")
	}
	f, err := os.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Print("Backup passphrase: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return err
	}
	if err := os.MkdirAll(*dataDir, 0o750); err != nil {
		return err
	}
	m, err := backup.Stage(context.Background(), *dataDir, f, strings.TrimSpace(line))
	if err != nil {
		return err
	}
	fmt.Printf("Checked: %q from %s (server %s). It replaces this kicker's data at the next start of the server, e.g. `sudo systemctl restart kicker`.\n",
		m.Name, m.Created.Local().Format("2006-01-02 15:04"), m.Version)
	return nil
}
