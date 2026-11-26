// Package release is the signed release manifest of spec 10 §3: release.json lists the server
// binaries and the module firmware of a version with their sha256, and only it is signed
// (minisign, Ed25519). The updater (M7 task 5) trusts a file only if release.json verifies with
// the public key built into the binary (version.UpdatePubKey) and the file's sha256 matches.
package release

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"aead.dev/minisign"
)

// File names of a release.
const (
	ManifestFile  = "release.json"
	SignatureFile = "release.json.minisig"
)

// Format is the release.json format version this build understands.
const Format = 1

// Manifest is release.json.
type Manifest struct {
	Format  int       `json:"format"`
	Version string    `json:"version"` // semver without "v"
	Date    time.Time `json:"date"`
	Notes   string    `json:"notes"` // markdown
	// MinVersion: the oldest version that may update to this one directly ("" = any). Older
	// kickers first update to an intermediate release.
	MinVersion string     `json:"min_version,omitempty"`
	Server     []Server   `json:"server"`
	Firmware   []Firmware `json:"firmware"`
}

// Asset is a file of the release, checked by its sha256.
type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"` // hex
	Size   int64  `json:"size"`
}

// Server is a server binary.
type Server struct {
	Asset
	OS   string `json:"os"`   // linux
	Arch string `json:"arch"` // arm64, amd64
}

// Firmware is a module firmware image, matched to modules by the fields of their announce
// (spec 01 §3: mf, hw, fw.name, upd).
type Firmware struct {
	Asset
	MF    string `json:"mf"`     // manifest id, e.g. kicker/goal-sensor
	HW    string `json:"hw"`     // board, e.g. waveshare_rp2040_zero
	FW    string `json:"fw"`     // firmware name, e.g. kicker-goal-sensor
	FWVer string `json:"fw_ver"` // semver
	Upd   string `json:"upd"`    // update method: uf2
}

// Errors of Verify.
var (
	ErrBadKey       = errors.New("release: the public key is not valid")
	ErrBadSignature = errors.New("release: the signature doesn't match release.json – not a release signed with this build's key")
	ErrBadManifest  = errors.New("release: release.json is not valid")
)

// Verify checks release.json against its signature and the public key (base64, as in
// minisign.pub's second line) and parses it. Nothing in an unverified file is trusted.
func Verify(manifest, signature []byte, publicKey string) (Manifest, error) {
	var pub minisign.PublicKey
	if err := pub.UnmarshalText([]byte(strings.TrimSpace(publicKey))); err != nil {
		return Manifest{}, ErrBadKey
	}
	if !minisign.Verify(pub, manifest, signature) {
		return Manifest{}, ErrBadSignature
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(manifest))
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrBadManifest, err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Validate checks the fields a client relies on.
func (m Manifest) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrBadManifest}, a...)...)
	}
	if m.Format != Format {
		return bad("format %d, this build reads %d", m.Format, Format)
	}
	if _, err := ParseVersion(m.Version); err != nil {
		return bad("version %q", m.Version)
	}
	if m.MinVersion != "" {
		if _, err := ParseVersion(m.MinVersion); err != nil {
			return bad("min_version %q", m.MinVersion)
		}
	}
	check := func(a Asset) error {
		if a.Name == "" || strings.ContainsAny(a.Name, `/\`) || a.URL == "" || a.Size <= 0 {
			return bad("asset %q", a.Name)
		}
		if b, err := hex.DecodeString(a.SHA256); err != nil || len(b) != sha256.Size {
			return bad("asset %q: sha256", a.Name)
		}
		return nil
	}
	for _, s := range m.Server {
		if err := check(s.Asset); err != nil {
			return err
		}
		if s.OS == "" || s.Arch == "" {
			return bad("server %q: os/arch", s.Name)
		}
	}
	for _, f := range m.Firmware {
		if err := check(f.Asset); err != nil {
			return err
		}
		if _, err := ParseVersion(f.FWVer); err != nil || f.MF == "" || f.HW == "" || f.FW == "" || f.Upd == "" {
			return bad("firmware %q", f.Name)
		}
	}
	return nil
}

// ServerFor returns the server binary for an OS and architecture.
func (m Manifest) ServerFor(goos, goarch string) (Server, bool) {
	for _, s := range m.Server {
		if s.OS == goos && s.Arch == goarch {
			return s, true
		}
	}
	return Server{}, false
}

// FirmwareFor returns the firmware for a module (its manifest id and board).
func (m Manifest) FirmwareFor(mf, hw string) (Firmware, bool) {
	for _, f := range m.Firmware {
		if f.MF == mf && f.HW == hw {
			return f, true
		}
	}
	return Firmware{}, false
}

// ErrChecksum: a downloaded file is not the one release.json names.
var ErrChecksum = errors.New("release: the file's sha256 doesn't match release.json")

// Check reads r to the end and compares its size and sha256 with the asset.
func (a Asset) Check(r io.Reader) error {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return err
	}
	if n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return ErrChecksum
	}
	return nil
}

// CheckFile checks a downloaded file against the asset.
func (a Asset) CheckFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return a.Check(f)
}

// --- keys and signing (CI and `kicker release`) ---------------------------------------------

// GenerateKey makes a new key pair: the public key file (minisign format) and the private key,
// encrypted with password (minisign format, scrypt). Both files work with the minisign tool. An
// empty password leaves the private key unencrypted – only for test keys.
func GenerateKey(password string) (publicFile, privateFile []byte, err error) {
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	if password == "" {
		privateFile, err = priv.MarshalText()
	} else {
		privateFile, err = minisign.EncryptKey(password, priv)
	}
	if err != nil {
		return nil, nil, err
	}
	publicFile, _ = pub.MarshalText()
	return append(publicFile, '\n'), privateFile, nil
}

// Encrypted reports whether a private key file is encrypted with a password.
func Encrypted(privateFile []byte) bool { return minisign.IsEncrypted(privateFile) }

// PublicKeyString returns the base64 key line of a public key file (what -ldflags sets as
// version.UpdatePubKey).
func PublicKeyString(publicFile []byte) (string, error) {
	var pub minisign.PublicKey
	if err := pub.UnmarshalText(publicFile); err != nil {
		return "", ErrBadKey
	}
	return pub.String(), nil
}

// Sign signs release.json with a private key (minisign format, encrypted with password) and returns the
// signature file. Prehashed (BLAKE2b), like the minisign tool's default, so `minisign -Vm
// release.json -p release.pub` checks it too. The trusted comment names the version.
func Sign(manifest, privateFile []byte, password string) ([]byte, error) {
	var priv minisign.PrivateKey
	var err error
	if minisign.IsEncrypted(privateFile) {
		priv, err = minisign.DecryptKey(password, privateFile)
	} else {
		err = priv.UnmarshalText(privateFile)
	}
	if err != nil {
		return nil, fmt.Errorf("release: the private key or its password is wrong: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadManifest, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	r := minisign.NewReader(bytes.NewReader(manifest))
	if _, err := io.Copy(io.Discard, r); err != nil {
		return nil, err
	}
	trusted := fmt.Sprintf("kicker %s release.json timestamp:%d", m.Version, time.Now().Unix())
	return r.SignWithComments(priv, trusted, "signature from the kicker release key"), nil
}

// marshal formats release.json: indented, with a final newline, so it reads well on GitHub.
func marshal(m Manifest) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
