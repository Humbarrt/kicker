// Command kicker is the single binary of the kicker server (spec 04 §1).
//
//	kicker serve     run the server (add --sim for virtual modules)
//	kicker monitor   act as a fake server for one module on a serial port (D8)
//	kicker admin     admin tasks on the Pi (reset-password, create)
//	kicker loadtest  generate games through the real engine and measure budgets
//	kicker version   print version information
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/api"
	"github.com/Humbarrt/kicker/server/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "serve":
		err = serve(args)
	case "monitor", "admin", "loadtest":
		err = fmt.Errorf("%s: not implemented yet", cmd)
	case "version":
		fmt.Printf("kicker %s (%s), updates enabled: %v\n", version.Version, version.Commit, version.UpdatesEnabled())
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: kicker <serve|monitor|admin|loadtest|version> [flags]")
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "HTTP listen address (plain HTTP until M3)")
	dataDir := fs.String("data", "./data", "data folder (database, pictures, keys)")
	sim := fs.Bool("sim", false, "start with virtual modules instead of / in addition to serial ones")
	dev := fs.Bool("dev", false, "development mode (verbose logs, gin debug)")
	_ = fs.Parse(args)

	level := slog.LevelInfo
	if *dev {
		level = slog.LevelDebug
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	if err := os.MkdirAll(*dataDir, 0o750); err != nil {
		return fmt.Errorf("data folder: %w", err)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.NewRouter(api.Options{Sim: *sim}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		slog.Info("kicker listening", "addr", *addr, "data", *dataDir, "sim", *sim, "version", version.Version)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("shutting down")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
