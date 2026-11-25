package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Humbarrt/kicker/server/internal/modules/session"
	"github.com/Humbarrt/kicker/server/internal/modules/transport"
	"github.com/Humbarrt/kicker/server/internal/protocol"
)

const monitorHelp = `Commands (type and press Enter):
  {...}                     send a raw JSON line as-is (to test the module's error handling)
  cmd <instance> <name> [json]   send a capability command, e.g.  cmd x honk {"ms":300}
  cfg <instance|-> <json>   send a (partial) config, e.g.  cfg a {"lockout_ms":1500}
  identify [seconds]        blink the module's LED
  sleep | wake | reboot | update
  pings                     toggle showing ping/pong lines
  help | quit`

// monitor acts as the server for one module on a serial port (D8, spec 04 §3).
func monitor(args []string) error {
	fs := flag.NewFlagSet("monitor", flag.ExitOnError)
	list := fs.Bool("list", false, "list serial ports and exit")
	adopted := fs.Bool("adopted", true, "tell the module it is adopted")
	showPings := fs.Bool("pings", false, "show ping/pong lines")
	noColor := fs.Bool("no-color", false, "disable colors")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: kicker monitor [flags] [port]\n\nWithout a port, the only connected module-like USB port is used.\n\nFlags:")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, "\n"+monitorHelp)
	}
	_ = fs.Parse(args)

	ports, err := transport.ListSerial()
	if err != nil {
		return fmt.Errorf("list serial ports: %w", err)
	}
	if *list {
		printPorts(ports)
		return nil
	}
	port := fs.Arg(0)
	if port == "" {
		var candidates []transport.PortInfo
		for _, p := range ports {
			if _, known := transport.KnownVendors[p.VID]; p.USB && known {
				candidates = append(candidates, p)
			}
		}
		if len(candidates) != 1 {
			printPorts(ports)
			return fmt.Errorf("found %d module-like USB ports, name one: kicker monitor <port>", len(candidates))
		}
		port = candidates[0].Name
	}

	out := newPrinter(!*noColor && isTerminal(os.Stdout), *showPings)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var (
		mu      sync.Mutex
		current *session.Session
	)
	go readCommands(ctx, stop, out, func() *session.Session {
		mu.Lock()
		defer mu.Unlock()
		return current
	})

	out.info("monitoring %s – type `help` for commands, Ctrl-C to quit", port)
	waiting := false
	for ctx.Err() == nil {
		conn, err := transport.OpenSerial(port)
		if err != nil {
			if !waiting {
				out.warn("cannot open %s (%v) – waiting for the device…", port, err)
				waiting = true
			}
			sleep(ctx, time.Second)
			continue
		}
		waiting = false
		sess := session.New(conn, session.Options{Adopted: *adopted, OnEvent: out.event})
		mu.Lock()
		current = sess
		mu.Unlock()

		runCtx, cancel := context.WithCancel(ctx)
		go func() { <-runCtx.Done(); conn.Close() }() // unblocks the reader
		err = sess.Run(runCtx)
		cancel()
		mu.Lock()
		current = nil
		mu.Unlock()

		switch {
		case ctx.Err() != nil:
		case errors.Is(err, session.ErrNoAnnounce):
			out.fail("no valid announce within 500 ms. Is the firmware running KickerModule? Try resetting the board.")
			sleep(ctx, 2*time.Second)
		default:
			out.warn("connection closed (%v) – waiting for the device…", err)
			sleep(ctx, time.Second)
		}
	}
	fmt.Println()
	return nil
}

func printPorts(ports []transport.PortInfo) {
	if len(ports) == 0 {
		fmt.Println("no serial ports found")
		return
	}
	for _, p := range ports {
		vendor := transport.KnownVendors[p.VID]
		usb := ""
		if p.USB {
			usb = fmt.Sprintf("USB %s:%s %s %s", p.VID, p.PID, vendor, p.Product)
		}
		fmt.Printf("  %-28s %s\n", p.Name, strings.TrimSpace(usb))
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// --- stdin commands -------------------------------------------------------------------

func readCommands(ctx context.Context, stop func(), out *printer, current func() *session.Session) {
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		switch line {
		case "quit", "exit":
			stop()
			return
		case "help", "?":
			out.info("%s", monitorHelp)
			continue
		case "pings":
			out.togglePings()
			continue
		}
		sess := current()
		if sess == nil || sess.Announce() == nil {
			out.fail("no module connected")
			continue
		}
		go runCommand(ctx, sess, line, out)
	}
}

func runCommand(ctx context.Context, sess *session.Session, line string, out *printer) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if strings.HasPrefix(line, "{") {
		if err := sess.SendRaw(ctx, line); err != nil {
			out.fail("%v", err)
		}
		return
	}
	fields := strings.Fields(line)
	verb, rest := fields[0], strings.TrimSpace(strings.TrimPrefix(line, fields[0]))

	var build func(id uint32) protocol.Message
	switch verb {
	case "cmd":
		parts := strings.SplitN(rest, " ", 3)
		if len(parts) < 2 {
			out.fail("usage: cmd <instance> <name> [json]")
			return
		}
		var data json.RawMessage
		if len(parts) == 3 {
			if !json.Valid([]byte(parts[2])) {
				out.fail("not valid JSON: %s", parts[2])
				return
			}
			data = json.RawMessage(parts[2])
		}
		build = func(id uint32) protocol.Message {
			return &protocol.Cmd{T: protocol.TCmd, ID: id, I: parts[0], C: parts[1], D: data}
		}
	case "cfg":
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) < 2 || !json.Valid([]byte(parts[1])) {
			out.fail("usage: cfg <instance|-> <json>")
			return
		}
		instance := parts[0]
		if instance == "-" {
			instance = ""
		}
		build = func(id uint32) protocol.Message {
			return &protocol.Cfg{T: protocol.TCfg, ID: id, I: instance, D: json.RawMessage(parts[1])}
		}
	case "identify":
		s := 5
		if rest != "" {
			n, err := strconv.Atoi(rest)
			if err != nil {
				out.fail("usage: identify [seconds]")
				return
			}
			s = n
		}
		build = func(id uint32) protocol.Message {
			return &protocol.Identify{T: protocol.TIdentify, ID: id, D: protocol.IdentifyArgs{S: s}}
		}
	case "sleep", "wake", "reboot", "update":
		t := protocol.Type(verb)
		build = func(id uint32) protocol.Message { return &protocol.Simple{T: t, ID: id} }
	default:
		out.fail("unknown command %q – type `help`", verb)
		return
	}

	res, err := sess.Command(ctx, build)
	switch {
	case err != nil:
		out.fail("%v", err)
	case res.OK:
		out.ok("ack")
	default:
		out.fail("nack %s %s", res.Code, res.Msg)
	}
}

// --- output -----------------------------------------------------------------------------

type printer struct {
	mu        sync.Mutex
	w         io.Writer
	color     bool
	showPings bool
	start     time.Time
}

func newPrinter(color, showPings bool) *printer {
	return &printer{w: os.Stdout, color: color, showPings: showPings, start: time.Now()}
}

const (
	cReset  = "\033[0m"
	cDim    = "\033[2m"
	cRed    = "\033[31m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cCyan   = "\033[36m"
)

func (p *printer) paint(c, s string) string {
	if !p.color {
		return s
	}
	return c + s + cReset
}

func (p *printer) print(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	stamp := p.paint(cDim, fmt.Sprintf("%9.3fs", time.Since(p.start).Seconds()))
	fmt.Fprintf(p.w, "%s  %s\n", stamp, fmt.Sprintf(format, args...))
}

func (p *printer) togglePings() {
	p.mu.Lock()
	p.showPings = !p.showPings
	on := p.showPings
	p.mu.Unlock()
	p.info("ping/pong lines %s", map[bool]string{true: "shown", false: "hidden"}[on])
}

func (p *printer) info(format string, args ...any) {
	p.print("%s", p.paint(cCyan, fmt.Sprintf(format, args...)))
}
func (p *printer) ok(format string, args ...any) {
	p.print("%s", p.paint(cGreen, "✓ "+fmt.Sprintf(format, args...)))
}
func (p *printer) warn(format string, args ...any) {
	p.print("%s", p.paint(cYellow, "! "+fmt.Sprintf(format, args...)))
}
func (p *printer) fail(format string, args ...any) {
	p.print("%s", p.paint(cRed, "✗ "+fmt.Sprintf(format, args...)))
}

func (p *printer) event(e session.Event) {
	switch ev := e.(type) {
	case session.ConsoleLine:
		if !p.showPings && (strings.Contains(ev.Text, `"t":"ping"`) || strings.Contains(ev.Text, `"t":"pong"`)) && ev.Err == "" {
			return
		}
		arrow := "←"
		if ev.Dir == session.Out {
			arrow = p.paint(cDim, "→")
		}
		text := ev.Text
		if ev.Kind == protocol.LineRaw {
			text = p.paint(cYellow, text+"   (not JSON – shown as module output)")
		}
		p.print("%s %s", arrow, text)
		for _, w := range ev.Warnings {
			p.warn("%s", w)
		}
		if ev.Err != "" {
			p.fail("%s", ev.Err)
		}
	case session.Announced:
		a := ev.Announce
		p.ok("module %s  %s %s  board %s  uid %s", a.MF, a.FW.Name, a.FW.Ver, a.HW, a.UID)
		for _, c := range ev.Caps {
			state := "catalog"
			if !c.Supported {
				state = "third-party / unsupported"
			}
			wake := ""
			if c.CanWake {
				wake = ", can wake"
			}
			p.info("    %-10s %s@%d (%s%s)", c.Instance, c.ID, c.Version, state, wake)
		}
	case session.ModuleEvent:
		data := ""
		if len(ev.Raw) > 0 {
			data = " " + string(ev.Raw)
		}
		p.ok("event %s.%s%s  (n=%d)", ev.Instance, ev.Name, data, ev.N)
	case session.StatusChanged:
		msg := ev.Status
		if ev.Msg != "" {
			msg += ": " + ev.Msg
		}
		if ev.Status == "ok" {
			p.ok("status %s %s", ev.Instance, msg)
		} else {
			p.warn("status %s %s", ev.Instance, msg)
		}
	case session.LogLine:
		line := fmt.Sprintf("log[%s] %s", ev.Level, ev.Message)
		switch ev.Level {
		case "e":
			p.fail("%s", line)
		case "w":
			p.warn("%s", line)
		default:
			p.info("%s", line)
		}
	case session.Online:
		if ev.Online {
			p.ok("online")
		} else {
			p.fail("offline – 3 pings unanswered")
		}
	}
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
