package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/Humbarrt/kicker/server/internal/release"
	"github.com/Humbarrt/kicker/server/internal/version"
)

// passwordEnv holds the release key's password for scripts and CI (otherwise it is asked for).
const passwordEnv = "KICKER_RELEASE_PASSWORD"

// kicker release <keygen|manifest|sign|verify>: putting a signed release together (spec 10 §3).
// CI runs manifest + sign with the real key from the repo secrets; the same commands with a key
// made by keygen give a test release for local testing (docs/releasing.md).
func releaseCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: kicker release <keygen|manifest|sign|verify> [flags]")
	}
	switch args[0] {
	case "keygen":
		return releaseKeygen(args[1:])
	case "manifest":
		return releaseManifest(args[1:])
	case "sign":
		return releaseSign(args[1:])
	case "verify":
		return releaseVerify(args[1:])
	}
	return fmt.Errorf("unknown release command %q", args[0])
}

// kicker release keygen [--out DIR] [--name NAME] [--test]: a new key pair, NAME.pub and NAME.key
// (the private key, encrypted with a password; minisign format). --test: no password, for local
// test releases only.
func releaseKeygen(args []string) error {
	fs := flag.NewFlagSet("release keygen", flag.ExitOnError)
	out := fs.String("out", ".", "folder for the key files")
	name := fs.String("name", "kicker-release", "file name of the keys, without .pub / .key")
	test := fs.Bool("test", false, "a test key without a password (never for real releases)")
	_ = fs.Parse(args)
	pubPath, keyPath := filepath.Join(*out, *name+".pub"), filepath.Join(*out, *name+".key")
	for _, p := range []string{pubPath, keyPath} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s exists – not overwriting a key", p)
		}
	}
	pw := ""
	if !*test {
		var err error
		if pw, err = password(true); err != nil {
			return err
		}
		if pw == "" {
			return errors.New("the key needs a password (or --test for a test key)")
		}
	}
	pub, key, err := release.GenerateKey(pw)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(pubPath, pub, 0o644); err != nil {
		return err
	}
	line, _ := release.PublicKeyString(pub)
	secret := "keep it secret, with its password"
	if *test {
		secret = "a test key without a password – never for real releases"
	}
	fmt.Printf("Private key: %s (%s)\nPublic key:  %s\n             %s\n", keyPath, secret, pubPath, line)
	return nil
}

// kicker release manifest --version V --url URL --out DIR [--notes FILE] [--min-version V]
// [--firmware DIR] [server binaries…]: writes DIR/release.json and copies the firmware there.
func releaseManifest(args []string) error {
	fs := flag.NewFlagSet("release manifest", flag.ExitOnError)
	ver := fs.String("version", "", "release version, e.g. v1.2.3")
	url := fs.String("url", "", "download URL of the release files, e.g. https://github.com/OWNER/REPO/releases/download/v1.2.3")
	out := fs.String("out", "dist", "folder for release.json and the firmware files")
	notes := fs.String("notes", "", "release notes (markdown file)")
	minVer := fs.String("min-version", "", "oldest version that may update to this one directly")
	fw := fs.String("firmware", "", "firmware/modules folder with built modules (pio run)")
	_ = fs.Parse(args)
	b := release.Build{Version: *ver, MinVersion: *minVer, BaseURL: *url, Out: *out, Servers: fs.Args(), Firmware: *fw, Date: time.Now()}
	if *notes != "" {
		text, err := os.ReadFile(*notes)
		if err != nil {
			return err
		}
		b.Notes = strings.TrimSpace(string(text))
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	data, m, err := b.Manifest()
	if err != nil {
		return err
	}
	path := filepath.Join(*out, release.ManifestFile)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: version %s, %d server binaries, %d firmware files\n", path, m.Version, len(m.Server), len(m.Firmware))
	return nil
}

// kicker release sign --key FILE [--dir DIR]: signs DIR/release.json → release.json.minisig.
func releaseSign(args []string) error {
	fs := flag.NewFlagSet("release sign", flag.ExitOnError)
	keyPath := fs.String("key", "", "private key file (from keygen or minisign -G)")
	dir := fs.String("dir", "dist", "folder with release.json")
	_ = fs.Parse(args)
	key, err := os.ReadFile(*keyPath)
	if err != nil {
		return err
	}
	manifest, err := os.ReadFile(filepath.Join(*dir, release.ManifestFile))
	if err != nil {
		return err
	}
	pw := ""
	if release.Encrypted(key) {
		if pw, err = password(false); err != nil {
			return err
		}
	}
	sig, err := release.Sign(manifest, key, pw)
	if err != nil {
		return err
	}
	path := filepath.Join(*dir, release.SignatureFile)
	if err := os.WriteFile(path, sig, 0o644); err != nil {
		return err
	}
	fmt.Println("signed:", path)
	return nil
}

// kicker release verify [--pub FILE] [--dir DIR]: checks the signature and every file of
// release.json that is in DIR. Without --pub, the key built into this binary.
func releaseVerify(args []string) error {
	fs := flag.NewFlagSet("release verify", flag.ExitOnError)
	pubPath := fs.String("pub", "", "public key file (default: the key built into this binary)")
	dir := fs.String("dir", "dist", "folder with release.json, its signature and the files")
	_ = fs.Parse(args)
	pub := version.UpdatePubKey
	if *pubPath != "" {
		data, err := os.ReadFile(*pubPath)
		if err != nil {
			return err
		}
		if pub, err = release.PublicKeyString(data); err != nil {
			return err
		}
	}
	if pub == "" {
		return errors.New("this build has no release key: pass --pub")
	}
	manifest, err := os.ReadFile(filepath.Join(*dir, release.ManifestFile))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(filepath.Join(*dir, release.SignatureFile))
	if err != nil {
		return err
	}
	m, err := release.Verify(manifest, sig, pub)
	if err != nil {
		return err
	}
	var assets []release.Asset
	for _, s := range m.Server {
		assets = append(assets, s.Asset)
	}
	for _, f := range m.Firmware {
		assets = append(assets, f.Asset)
	}
	for _, a := range assets {
		if err := a.CheckFile(filepath.Join(*dir, a.Name)); err != nil {
			return fmt.Errorf("%s: %w", a.Name, err)
		}
	}
	fmt.Printf("release %s: signature ok, %d files ok\n", m.Version, len(assets))
	return nil
}

// password reads the key password: from KICKER_RELEASE_PASSWORD, from a terminal (twice for a
// new key) or one line from a pipe.
func password(confirm bool) (string, error) {
	if pw := os.Getenv(passwordEnv); pw != "" {
		return pw, nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("reading the password (or set %s): %w", passwordEnv, err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	for {
		fmt.Print("Key password: ")
		a, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil || !confirm {
			return string(a), err
		}
		if len(a) < 12 {
			fmt.Println("At least 12 characters, please.")
			continue
		}
		fmt.Print("Repeat: ")
		b, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return "", err
		}
		if string(a) == string(b) {
			return string(a), nil
		}
		fmt.Println("The passwords don't match.")
	}
}
