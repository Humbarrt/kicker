package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Humbarrt/kicker/server/internal/install"
	"github.com/Humbarrt/kicker/server/internal/version"
)

// kicker install [--root DIR] [--binary FILE --version V] [--hostname NAME] [--no-start]:
// sets this Raspberry Pi up for kicker – or a mounted Raspberry Pi OS image (--root), which is
// how CI builds the SD card image (spec 10 §5). Run again to install a new binary by hand; the
// data stays.
func installCmd(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	root := fs.String("root", "/", "the system to set up: / or a mounted image")
	binary := fs.String("binary", "", "the server binary to install (default: this one)")
	ver := fs.String("version", "", "the binary's version (default: this one's)")
	hostname := fs.String("hostname", "auto", `host name ("auto": kicker on a new Raspberry Pi OS or image, otherwise kept; "": keep)`)
	noStart := fs.Bool("no-start", false, "don't (re)start the service")
	_ = fs.Parse(args)
	if os.Geteuid() != 0 {
		return errors.New("run it as root: sudo kicker install")
	}
	o := install.Options{Root: filepath.Clean(*root), Binary: *binary, Version: *ver, Hostname: *hostname, Out: os.Stdout, Start: !*noStart}
	o.Live = o.Root == "/"
	if o.Binary == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		o.Binary = exe
		if o.Version == "" {
			o.Version = version.Version
		}
	}
	if o.Version == "" {
		return errors.New("--version is needed with --binary")
	}
	if o.Hostname == "auto" {
		o.Hostname = ""
		cur, _ := os.ReadFile(filepath.Join(o.Root, "etc", "hostname"))
		if c := strings.TrimSpace(string(cur)); !o.Live || c == "" || c == "raspberrypi" || c == "localhost" {
			o.Hostname = "kicker"
		}
	}
	if err := install.Run(o); err != nil {
		return err
	}
	if o.Live && o.Start {
		host := o.Hostname
		if host == "" {
			h, _ := os.Hostname()
			host = h
		}
		fmt.Printf("\nkicker runs: https://%s.local (first visit: http://%s.local/trust)\n", host, host)
	}
	return nil
}
