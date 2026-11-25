package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Humbarrt/kicker/server/internal/loadtest"
)

// loadtestCmd is `kicker loadtest` (spec 04 §4): generate games through the real engine, then
// measure the budgets. It exits with an error if a measurement is over its budget.
func loadtestCmd(args []string) error {
	fs := flag.NewFlagSet("loadtest", flag.ExitOnError)
	dataDir := fs.String("data", filepath.Join(os.TempDir(), "kicker-loadtest"), "data folder for the test database (never the real one)")
	fresh := fs.Bool("fresh", false, "generate new games even if the folder has some")
	games := fs.Int("games", 100_000, "games to generate")
	players := fs.Int("players", 40, "players")
	requests := fs.Int("requests", 200, "requests per measured endpoint")
	clients := fs.Int("clients", 50, "SSE clients")
	scale := fs.Float64("budget-scale", 1, "multiply the time budgets (1 = Raspberry Pi 4; CI on x86 uses less)")
	verbose := fs.Bool("v", false, "show the server's log")
	_ = fs.Parse(args)

	if _, err := os.Stat(filepath.Join(*dataDir, "ca.key")); err == nil {
		return fmt.Errorf("%s looks like a real kicker data folder – use an empty one", *dataDir)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts := loadtest.Options{DataDir: *dataDir, Fresh: *fresh, Games: *games, Players: *players,
		Requests: *requests, Clients: *clients, Scale: *scale}
	if *verbose {
		opts.Log = os.Stderr
	}
	rep, err := loadtest.Run(ctx, opts)
	if err != nil {
		return err
	}
	if failed := rep.Failed(); len(failed) > 0 {
		var names []string
		for _, f := range failed {
			names = append(names, f.Name)
		}
		return errors.New("over budget: " + strings.Join(names, "; "))
	}
	fmt.Println("all budgets met")
	return nil
}
