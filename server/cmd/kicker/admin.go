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
	"time"

	"golang.org/x/term"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/onboarding"
	"github.com/Humbarrt/kicker/server/internal/store"
)

// admin is the console for whoever has a shell on the Pi (spec 03 §7, "physical access"):
//
//	kicker admin create [--data DIR] <username>        create an admin (finishes the setup)
//	kicker admin set-password [--data DIR] <username>  set a new password, ends all sessions
//	kicker admin reset-code [--data DIR] <username>    print a one-time reset code (24 h)
//
// It works while the server is running.
func admin(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: kicker admin <create|set-password|reset-code> [--data DIR] <username>")
	}
	sub := args[0]
	fs := flag.NewFlagSet("admin "+sub, flag.ExitOnError)
	dataDir := fs.String("data", "./data", "data folder")
	_ = fs.Parse(args[1:])
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: kicker admin %s [--data DIR] <username>", sub)
	}
	username := fs.Arg(0)

	ctx := context.Background()
	path := filepath.Join(*dataDir, "kicker.db")
	if _, err := os.Stat(path); err != nil && sub != "create" {
		return fmt.Errorf("no database at %s", path)
	}
	if err := os.MkdirAll(*dataDir, 0o750); err != nil {
		return err
	}
	st, err := store.Open(ctx, path)
	if err != nil {
		return err
	}
	defer st.Close()
	as, err := auth.New(ctx, auth.Options{Store: st})
	if err != nil {
		return err
	}

	switch sub {
	case "create":
		if _, err := auth.NormalizeUsername(username); err != nil {
			return err
		}
		password, err := readNewPassword()
		if err != nil {
			return err
		}
		var raw string
		if ok, err := st.GetSetting(ctx, store.SettingArgon2, &raw); err != nil {
			return err
		} else if !ok {
			fmt.Println("Calibrating password hashing on this machine…")
			if err := as.SetParams(ctx, auth.Calibrate(250*time.Millisecond)); err != nil {
				return err
			}
		}
		a, err := as.CreateAccount(ctx, auth.NewAccount{Username: username, Password: password, GroupID: as.BuiltinGroup(auth.GroupAdmin)})
		if err != nil {
			return err
		}
		as.Audit(ctx, nil, "", "admin_created_console", a.Username, "")
		if err := onboarding.CompleteFromConsole(ctx, st); err != nil {
			return err
		}
		fmt.Printf("Admin %q created. You can log in now.\n", a.Username)
	case "set-password":
		password, err := readNewPassword()
		if err != nil {
			return err
		}
		if err := as.SetPassword(ctx, username, password); errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("no account %q", username)
		} else if err != nil {
			return err
		}
		fmt.Printf("Password of %q changed; all its sessions have ended.\n", strings.ToLower(username))
	case "reset-code":
		a, err := st.AccountByUsername(ctx, strings.ToLower(username))
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("no account %q", username)
		} else if err != nil {
			return err
		}
		code, exp, err := as.CreateResetCode(ctx, auth.Actor{}, a.ID)
		if err != nil {
			return err
		}
		fmt.Printf("Reset code for %q: %s\nValid until %s. On the login page: \"Forgot password?\" → \"Reset code\".\n",
			a.Username, code, exp.Local().Format("2006-01-02 15:04"))
	default:
		return fmt.Errorf("unknown admin command %q", sub)
	}
	return nil
}

// readNewPassword asks twice without echo on a terminal, or reads one line from a pipe.
func readNewPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("reading the password: %w", err)
		}
		pw := strings.TrimRight(line, "\r\n")
		return pw, auth.CheckPassword(pw)
	}
	for {
		fmt.Print("New password (at least 10 characters): ")
		a, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return "", err
		}
		if err := auth.CheckPassword(string(a)); err != nil {
			fmt.Println(err)
			continue
		}
		fmt.Print("Repeat: ")
		b, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return "", err
		}
		if string(a) != string(b) {
			fmt.Println("The passwords don't match.")
			continue
		}
		return string(a), nil
	}
}
