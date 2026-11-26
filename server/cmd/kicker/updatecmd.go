package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/update"
)

// kicker update [--data DIR] [--check | --rollback]: server updates from a shell (spec 10 §3).
//   - no flag: install the newest release into <data>/bin (with a backup first); restart the
//     service afterwards – the new version is health-checked and rolled back like any update.
//   - --check: only look for a newer release.
//   - --rollback: back to the previous version and the data from before the last update (data
//     since then is lost); restart the service afterwards.
func updateCmd(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	dataDir := fs.String("data", "./data", "data folder")
	check := fs.Bool("check", false, "only check for a newer version")
	rollback := fs.Bool("rollback", false, "go back to the previous version and the backup made before the last update")
	api := fs.String("update-api", "", "GitHub API base URL (testing)")
	_ = fs.Parse(args)
	ctx := context.Background()

	if *rollback {
		prev, err := os.Readlink(filepath.Join(*dataDir, "bin", "previous"))
		if err != nil {
			return errors.New("there is no previous version in " + filepath.Join(*dataDir, "bin"))
		}
		if cur, _ := os.Readlink(update.Current(*dataDir)); cur == prev {
			return fmt.Errorf("%s is already running – there is no older version to go back to", prev)
		}
		file := update.NewestBackup(*dataDir)
		if file == "" {
			return errors.New("there is no backup from before an update")
		}
		if err := update.Rollback(ctx, *dataDir, &update.State{Previous: prev, Backup: file}); err != nil {
			return err
		}
		fmt.Printf("Back to %s with the data from %s. Restart the service now: sudo systemctl restart kicker\n", prev, filepath.Base(file))
		return nil
	}

	path := filepath.Join(*dataDir, "kicker.db")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no database at %s", path)
	}
	st, err := store.Open(ctx, path)
	if err != nil {
		return err
	}
	defer st.Close()
	svc := update.New(update.Options{Store: st, DataDir: *dataDir, API: *api})
	status, err := svc.Check(ctx)
	if err != nil {
		return err
	}
	if status.Available == nil {
		fmt.Printf("kicker %s is the newest version.\n", svc.Version())
		return nil
	}
	fmt.Printf("Version %s is available (running %s).\n", status.Available.Version, svc.Version())
	if *check {
		return nil
	}
	fmt.Println("Make sure no game is being played: the update restarts the kicker.")
	if _, err := svc.InstallNow(ctx); err != nil {
		return err
	}
	fmt.Printf("Installed %s. Restart the service now: sudo systemctl restart kicker\n", status.Available.Version)
	return nil
}
