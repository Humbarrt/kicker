package auth

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Params are argon2id parameters. They are stored in every hash (PHC string format), so they can
// change without breaking existing hashes (spec 03 §2).
type Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
}

// DefaultParams are used until the parameters are calibrated during onboarding.
var DefaultParams = Params{Memory: 64 * 1024, Time: 2, Threads: 4}

func (p Params) String() string { return fmt.Sprintf("m=%d,t=%d,p=%d", p.Memory, p.Time, p.Threads) }

// ParseParams parses "m=…,t=…,p=…".
func ParseParams(s string) (Params, error) {
	var p Params
	if _, err := fmt.Sscanf(s, "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return p, fmt.Errorf("argon2 params %q: %w", s, err)
	}
	if p.Memory < 8*1024 || p.Time < 1 || p.Threads < 1 {
		return p, fmt.Errorf("argon2 params %q: too weak", s)
	}
	return p, nil
}

const keyLen = 32

// Hash returns the PHC string of password.
func Hash(password string, p Params) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$%s$%s$%s", argon2.Version, p, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

var errBadHash = errors.New("malformed password hash")

// Verify checks password against a PHC string. outdated reports whether the hash uses other
// parameters than want, so it should be replaced after a successful login.
func Verify(password, phc string, want Params) (ok, outdated bool, err error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false, false, errBadHash
	}
	p, err := ParseParams(parts[3])
	if err != nil {
		return false, false, errBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	key, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(key) == 0 {
		return false, false, errBadHash
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(key)))
	ok = subtle.ConstantTimeCompare(got, key) == 1
	return ok, p != want, nil
}

// Calibrate finds parameters for which one hash takes about target on this machine: 64 MiB and
// 4 threads (the Pi 4 has four cores), with as many passes as fit. On slow machines the memory
// is halved instead, down to 16 MiB.
func Calibrate(target time.Duration) Params {
	p := Params{Memory: 64 * 1024, Time: 1, Threads: 4}
	measure := func(p Params) time.Duration {
		start := time.Now()
		argon2.IDKey([]byte("calibration"), []byte("calibration-salt"), p.Time, p.Memory, p.Threads, keyLen)
		return time.Since(start)
	}
	d := measure(p)
	for d > target && p.Memory > 16*1024 {
		p.Memory /= 2
		d = measure(p)
	}
	if d < target {
		p.Time = min(uint32(target/max(d, time.Millisecond)), 10)
		p.Time = max(p.Time, 1)
	}
	return p
}

// --- password rules (spec 03 §2) ------------------------------------------------------------

//go:embed common-passwords.txt
var commonList string

// common holds the 10,000 most common passwords that are at least 10 characters long (from the
// xato.net 10-million list via SecLists, MIT licence), lowercased.
var common = func() map[string]struct{} {
	m := make(map[string]struct{}, 10000)
	sc := bufio.NewScanner(strings.NewReader(commonList))
	for sc.Scan() {
		m[sc.Text()] = struct{}{}
	}
	return m
}()

// Password rule errors.
var (
	ErrPasswordTooShort = errors.New("the password must be at least 10 characters long")
	ErrPasswordTooLong  = errors.New("the password must be at most 200 characters long")
	ErrPasswordCommon   = errors.New("this password is too common")
)

// CheckPassword applies the NIST-style rules: length and a list of common passwords, nothing else.
func CheckPassword(password string) error {
	n := utf8.RuneCountInString(password)
	switch {
	case n < 10:
		return ErrPasswordTooShort
	case len(password) > 200:
		return ErrPasswordTooLong
	}
	if _, bad := common[strings.ToLower(password)]; bad {
		return ErrPasswordCommon
	}
	return nil
}
