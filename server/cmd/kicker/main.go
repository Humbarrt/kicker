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
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/api"
	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/backup"
	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/coins"
	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/firmware"
	"github.com/Humbarrt/kicker/server/internal/modules"
	"github.com/Humbarrt/kicker/server/internal/modules/virtual"
	"github.com/Humbarrt/kicker/server/internal/onboarding"
	"github.com/Humbarrt/kicker/server/internal/players"
	simpkg "github.com/Humbarrt/kicker/server/internal/sim"
	"github.com/Humbarrt/kicker/server/internal/stats"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/table"
	"github.com/Humbarrt/kicker/server/internal/tlsca"
	"github.com/Humbarrt/kicker/server/internal/tournaments"
	"github.com/Humbarrt/kicker/server/internal/update"
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
	case "monitor":
		err = monitor(args)
	case "admin":
		err = admin(args)
	case "loadtest":
		err = loadtestCmd(args)
	case "backup":
		err = backupCmd(args)
	case "restore":
		err = restoreCmd(args)
	case "release":
		err = releaseCmd(args)
	case "update":
		err = updateCmd(args)
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
	if reexec != "" {
		// A restore (or an update) asked for a restart: the same process id, so systemd keeps
		// watching it (spec 10 §2–3).
		slog.Info("restarting", "binary", reexec)
		if err := syscall.Exec(reexec, os.Args, os.Environ()); err != nil {
			slog.Error("restart", "err", err)
			os.Exit(1)
		}
	}
}

// reexec is the binary serve asked to be restarted with ("" = just exit).
var reexec string

func usage() {
	fmt.Fprintln(os.Stderr, "usage: kicker <serve|monitor|admin|loadtest|backup|restore|update|release|version> [flags]\n       kicker admin <create|set-password|reset-code> [--data DIR] <username>")
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "HTTP listen address; with --tls-addr it only redirects to HTTPS and serves /ca.crt and /trust")
	tlsAddr := fs.String("tls-addr", "", `HTTPS listen address, e.g. ":443" (spec 03 §9); "" = plain HTTP only (development)`)
	tlsNames := fs.String("tls-names", "", "extra DNS names for the HTTPS certificate, comma-separated")
	dataDir := fs.String("data", "./data", "data folder (database, pictures, keys)")
	sim := fs.Bool("sim", false, "add virtual modules (simulator); real serial modules still work")
	noSerial := fs.Bool("no-serial", false, "don't scan serial ports for modules")
	dev := fs.Bool("dev", false, "development mode (verbose logs, gin debug)")
	usbRoot := fs.String("usb-root", "/media/kicker", "where USB drives are mounted (backup targets)")
	updateAPI := fs.String("update-api", "", "GitHub API base URL for update checks (testing; default https://api.github.com)")
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A new version that didn't come up healthy last time is rolled back before anything else
	// (spec 10 §3): re-execute the previous version, which swaps the pre-update backup in.
	if prev, _, err := update.Startup(ctx, *dataDir, version.Version, slog.Default()); err != nil {
		return err
	} else if prev != "" {
		reexec = prev
		return nil
	}
	// A restore staged before the restart is swapped in before the database opens (spec 10 §2).
	if m, err := backup.ApplyPending(*dataDir); err != nil {
		return fmt.Errorf("restoring the backup: %w", err)
	} else if m != nil {
		slog.Info("backup restored", "name", m.Name, "created", m.Created, "version", m.Version)
	}
	// An existing database is backed up before it is migrated (D14).
	st, err := store.OpenWith(ctx, filepath.Join(*dataDir, "kicker.db"), store.OpenOptions{
		BeforeMigrate: func(ctx context.Context, s *store.Store, from, to int) error {
			path, err := backup.Before(ctx, s, *dataDir, "pre-migration")
			if path != "" {
				slog.Info("backup before migrating the database", "from", from, "to", to, "file", path)
			}
			return err
		},
	})
	if err != nil {
		return err
	}
	defer st.Close()
	if version.CrashAfterStart != "" {
		return errors.New("crashing on purpose (a test build for the update rollback)")
	}

	serverID, err := st.ServerID(ctx)
	if err != nil {
		return err
	}
	var name string
	if _, err := st.GetSetting(ctx, store.SettingName, &name); err != nil {
		return err
	}
	authSvc, err := auth.New(ctx, auth.Options{Store: st})
	if err != nil {
		return err
	}
	authSvc.Cleanup(ctx)
	ob, err := onboarding.New(ctx, onboarding.Options{Store: st, Auth: authSvc})
	if err != nil {
		return err
	}
	if !ob.Done() {
		slog.Info("setup is not complete: open the web UI, or run `kicker admin create <username>`")
	}

	playerSvc, err := players.New(players.Options{Store: st, Auth: authSvc, MediaDir: filepath.Join(*dataDir, "media")})
	if err != nil {
		return err
	}

	events := bus.New()
	// Ratings and stats (D9): rebuilt from the events if the projections are older than this
	// version, then kept up to date as games finish.
	statsCache := stats.NewCache(stats.Reader{St: st}, events, nil)
	projector := stats.New(stats.Options{Store: st, Bus: events, Changed: statsCache.Changed})
	rebuilt, err := table.BackfillProjections(ctx, st, slog.Default())
	if err != nil {
		return fmt.Errorf("game projections: %w", err)
	}
	outdated, err := stats.Outdated(ctx, st)
	if err != nil {
		return fmt.Errorf("stats projections: %w", err)
	}
	if rebuilt || outdated {
		projector.ReplayAll()
	}
	go projector.Run(ctx)
	go statsCache.Run(ctx)

	var tbl *table.Table
	var host *modules.Host
	coinSvc := coins.New(coins.Options{Store: st, Auth: authSvc, OnChange: func(uid string, playerID int64) {
		tbl.Feed(engine.CoinAssigned{UID: uid, PlayerID: playerID})
	}})
	var tourneys *tournaments.Service // the tournament on the table (spec 09 §8)
	tbl = table.New(table.Options{
		Store:  st,
		Bus:    events,
		Config: engine.DefaultConfig(), // the saved table settings (table_settings) override it
		OnGameFinished: func(id int64) {
			go projector.GameFinished(ctx, id)
			tourneys.GameFinished(id)
		},
		OnCheckIn: func(c engine.CheckIn) { tourneys.CheckIn(c) },
		OnPhase:   func(from, to engine.Phase) { tourneys.Phase(from, to) },
		OnEffect: func(e engine.Effect) {
			switch e := e.(type) {
			case engine.EjectCoins:
				// Not on the table goroutine: a module command waits for the module's answer.
				go func() {
					cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
					defer cancel()
					if err := host.Command(cctx, "coin_acceptor", string(e.Side), "eject", nil); err != nil {
						slog.Warn("ejecting coins", "side", e.Side, "err", err)
					}
				}()
			case engine.Sleep:
				host.SetAsleep(true)
			case engine.WakeUp:
				host.SetAsleep(false)
			default:
				slog.Debug("effect without a module to run it", "effect", fmt.Sprintf("%+v", e)) // TODO(M5): dispenser
			}
		},
	})
	tableDone := make(chan error, 1)
	tourneys = tournaments.New(tournaments.Options{Store: st, Table: tbl, Bus: events})
	go func() { tableDone <- tbl.Run(ctx) }()
	go tourneys.Run(ctx)

	hostOpts := modules.Options{
		Store: st.Adoptions(),
		OnEvent: func(e modules.Event) {
			switch ev := e.(type) {
			case modules.ModulesChanged:
				events.Publish(api.TopicModules, nil)
				events.Publish(firmware.Topic, nil) // the offers depend on the connected modules
			case modules.ConsoleEvent:
				events.Publish(api.TopicConsole, api.ConsoleLineOf(ev))
			case modules.CapabilityEvent:
				slog.Info("module event", "uid", ev.UID, "instance", ev.Instance, "role", ev.Role, "event", ev.Name, "adopted", ev.Adopted)
				in, ok := table.InputFor(ev, func(uid string) int64 { return coinSvc.Resolve(ctx, uid) })
				if ok {
					tbl.Feed(in)
				}
			case modules.StatusEvent:
				slog.Info("module status", "uid", ev.UID, "instance", ev.Instance, "status", ev.Status, "msg", ev.Msg)
			}
		},
	}
	if !*noSerial {
		hostOpts.Ports = modules.SerialPorts()
	}
	var simulator api.Simulator
	if *sim {
		home, away := virtual.NewCoinAcceptor("sim-coins-home", 2), virtual.NewCoinAcceptor("sim-coins-away", 2)
		hostOpts.Virtual = []*virtual.Module{virtual.GoalSensorModule("sim-goal-sensor"), home.Module, away.Module}
		hostOpts.AutoAdoptVirtual = true
		simulator = simpkg.New(hostOpts.Virtual, home, away)
	}
	host, err = modules.New(hostOpts)
	if err != nil {
		return err
	}
	go func() { _ = host.Run(ctx) }()

	// HTTPS with the kicker's own CA (spec 03 §9).
	var ca *tlsca.Authority
	var caOpt api.CA
	if *tlsAddr != "" {
		var extra []string
		if *tlsNames != "" {
			extra = strings.Split(*tlsNames, ",")
		}
		if ca, err = tlsca.Open(tlsca.Options{Dir: *dataDir, Name: name, ExtraNames: extra}); err != nil {
			return fmt.Errorf("certificates: %w", err)
		}
		caOpt = ca
		go ca.Run(ctx.Done(), 10*time.Minute) // new addresses, renewal
	}

	backups := backup.New(backup.Options{Store: st, DataDir: *dataDir, ServerID: serverID, Bus: events, USBRoot: *usbRoot,
		Name: func() string {
			var n string
			_, _ = st.GetSetting(ctx, store.SettingName, &n)
			return n
		},
		Busy:  func() bool { return tbl.View().Phase == string(engine.PhasePlaying) },
		Ready: ob.Done})
	go backups.Run(ctx)
	// restart: the binary to re-execute ("" = this one)
	restart := make(chan string, 1)
	requestRestart := func(binary string) {
		select {
		case restart <- binary:
		default:
		}
	}
	updater := update.New(update.Options{Store: st, DataDir: *dataDir, Bus: events, API: *updateAPI,
		Busy:    func() bool { return tbl.View().Phase == string(engine.PhasePlaying) },
		Ready:   ob.Done,
		Time:    func() string { return backups.Time(ctx) },
		Restart: requestRestart,
	})
	go updater.Run(ctx)
	firmwareSvc := firmware.New(firmware.Options{Host: host, Releases: updater, DataDir: *dataDir, Bus: events,
		Busy: func() bool {
			p := tbl.View().Phase
			return p == string(engine.PhasePlaying) || p == string(engine.PhasePregame)
		},
	})
	go func() {
		select {
		case <-tbl.Ready(): // database open and migrated, the table restored
			updater.Started(ctx)
		case <-ctx.Done():
		}
	}()

	router := api.NewRouter(api.Options{
		Backups:  backups,
		Updates:  updater,
		Firmware: firmwareSvc,
		Restart:  func() { requestRestart("") },
		Sim:      *sim, ServerID: serverID, Name: name, DataDir: *dataDir, CA: caOpt, Auth: authSvc, Onboarding: ob,
		Modules: host, Simulator: simulator, Table: tbl, Players: playerSvc, Coins: coinSvc, Ratings: st, History: st, Stats: stats.Reader{St: st, Cache: statsCache}, Presets: st, Tournaments: tourneys, Bus: events, Done: ctx.Done(),
	})
	srv := &http.Server{Addr: *addr, Handler: router, ReadHeaderTimeout: 10 * time.Second}
	var tlsSrv *http.Server
	if ca != nil {
		_, port, _ := net.SplitHostPort(*tlsAddr)
		lang := func() string { return ob.Status("").Language }
		srv.Handler = api.PlainHandler(ca, name, lang, port)
		tlsSrv = &http.Server{Addr: *tlsAddr, Handler: router, TLSConfig: ca.TLSConfig(), ReadHeaderTimeout: 10 * time.Second}
	}

	errc := make(chan error, 2)
	go func() {
		slog.Info("kicker listening", "addr", *addr, "https", *tlsAddr, "data", *dataDir, "sim", *sim, "version", version.Version)
		errc <- srv.ListenAndServe()
	}()
	if tlsSrv != nil {
		slog.Info("HTTPS", "addr", *tlsAddr, "ca_fingerprint", ca.CAFingerprint())
		go func() { errc <- tlsSrv.ListenAndServeTLS("", "") }()
	}

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case err := <-tableDone:
		return err // the table could not be restored
	case <-ctx.Done():
		slog.Info("shutting down")
		updater.Stopping(context.Background())
	case binary := <-restart:
		slog.Info("shutting down for a restart")
		updater.Stopping(context.Background())
		switch {
		case binary != "":
			reexec = binary
		case update.RunningFromCurrent(*dataDir):
			reexec = update.Current(*dataDir) // after an update this is a new version
		default:
			if reexec, err = os.Executable(); err != nil {
				return err
			}
		}
	}
	stop() // ends the live streams (they wait on ctx), so Shutdown doesn't wait for browsers
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	if tlsSrv != nil {
		err = errors.Join(err, tlsSrv.Shutdown(shutdownCtx))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// Requests still running after 5 s are cut off; a restart must not fail because of them.
		slog.Warn("requests cut off at shutdown")
		err = errors.Join(srv.Close(), func() error {
			if tlsSrv != nil {
				return tlsSrv.Close()
			}
			return nil
		}())
	}
	<-tableDone // the last events are saved before the database closes
	return err
}
