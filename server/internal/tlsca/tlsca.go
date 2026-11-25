// Package tlsca is the kicker's own certificate authority and its HTTPS certificate (spec 03 §9).
//
// On first start it creates a local CA (ECDSA P-256, 20 years) and a server certificate signed by
// it (397 days, so Apple devices accept it). The server certificate covers kicker.local, the
// machine's host name (plain and .local), localhost and every current IP address; it is reissued
// 30 days before it expires and whenever a name or an address is missing. Apps pin the CA, not
// the server certificate, so reissuing breaks nothing. The files live in the data folder
// (mode 0600) and go into backups, so a restored kicker keeps its identity.
package tlsca

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Files in the data folder (spec 03 §14).
const (
	CAKeyFile      = "ca.key"
	CACertFile     = "ca.crt"
	ServerKeyFile  = "server.key"
	ServerCertFile = "server.crt"
)

// Lifetimes (spec 03 §9).
const (
	CALifetime     = 20 * 365 * 24 * time.Hour
	ServerLifetime = 397 * 24 * time.Hour
	RenewBefore    = 30 * 24 * time.Hour
)

// Names returns what the server certificate must cover: kicker.local, the host name (plain and
// .local), localhost, extra names, and the loopback and interface addresses.
type Names struct {
	DNS []string
	IPs []net.IP
}

// CurrentNames collects the names for this machine. extra are additional DNS names (flag).
func CurrentNames(extra []string) Names {
	dns := []string{"kicker.local", "localhost"}
	if h, err := os.Hostname(); err == nil && h != "" {
		h = strings.ToLower(strings.TrimSuffix(h, "."))
		host, _, _ := strings.Cut(h, ".")
		dns = append(dns, h, host+".local")
	}
	for _, e := range extra {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			dns = append(dns, e)
		}
	}
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() && !n.IP.IsLinkLocalMulticast() {
				ips = append(ips, n.IP)
			}
		}
	}
	return normalize(Names{DNS: dns, IPs: ips})
}

func normalize(n Names) Names {
	slices.Sort(n.DNS)
	n.DNS = slices.Compact(n.DNS)
	for i, ip := range n.IPs {
		if v4 := ip.To4(); v4 != nil {
			n.IPs[i] = v4
		}
	}
	slices.SortFunc(n.IPs, func(a, b net.IP) int { return bytes.Compare(a, b) })
	n.IPs = slices.CompactFunc(n.IPs, func(a, b net.IP) bool { return a.Equal(b) })
	return n
}

// covers reports whether cert has every name and address of n.
func covers(cert *x509.Certificate, n Names) bool {
	for _, d := range n.DNS {
		if !slices.Contains(cert.DNSNames, d) {
			return false
		}
	}
	for _, ip := range n.IPs {
		if !slices.ContainsFunc(cert.IPAddresses, ip.Equal) {
			return false
		}
	}
	return true
}

// Fingerprint formats the SHA-256 of a certificate as "AB12 CD34 …" (spec 03 §9).
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	var b strings.Builder
	for i := 0; i < len(h); i += 4 {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(h[i : i+4])
	}
	return b.String()
}

// Authority is the CA plus the current server certificate. It is safe for concurrent use.
type Authority struct {
	dir   string
	name  string // the kicker's name, in the CA's subject
	extra []string
	now   func() time.Time
	log   *slog.Logger

	caKey  *ecdsa.PrivateKey
	caCert *x509.Certificate

	mu     sync.RWMutex
	server *tls.Certificate
	leaf   *x509.Certificate
}

// Options configure Open.
type Options struct {
	Dir        string   // the data folder
	Name       string   // the kicker's name ("Kicker Local CA (<name>)")
	ExtraNames []string // more DNS names for the server certificate
	Now        func() time.Time
	Logger     *slog.Logger
}

// Open loads the CA and the server certificate from the data folder, creating what is missing,
// and reissues the server certificate if it doesn't fit this machine (any more).
func Open(o Options) (*Authority, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	a := &Authority{dir: o.Dir, name: o.Name, extra: o.ExtraNames, now: o.Now, log: o.Logger.With("component", "tls")}
	if err := a.loadCA(); err != nil {
		return nil, err
	}
	if err := a.loadServer(); err != nil {
		a.log.Warn("server certificate unreadable, making a new one", "err", err)
	}
	if _, err := a.Check(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Authority) path(f string) string { return filepath.Join(a.dir, f) }

func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func pemBlock(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

func readPEM(path, typ string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b, _ := pem.Decode(raw)
	if b == nil || b.Type != typ {
		return nil, fmt.Errorf("%s: no %s block", path, typ)
	}
	return b.Bytes, nil
}

func serial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

// loadCA reads ca.key/ca.crt or creates them. A half-present CA is an error: replacing it would
// silently change the kicker's identity, which paired apps refuse (spec 03 §10).
func (a *Authority) loadCA() error {
	keyDER, kerr := readPEM(a.path(CAKeyFile), "EC PRIVATE KEY")
	certDER, cerr := readPEM(a.path(CACertFile), "CERTIFICATE")
	switch {
	case kerr == nil && cerr == nil:
		key, err := x509.ParseECPrivateKey(keyDER)
		if err != nil {
			return fmt.Errorf("ca.key: %w", err)
		}
		cert, err := x509.ParseCertificate(certDER)
		if err != nil {
			return fmt.Errorf("ca.crt: %w", err)
		}
		a.caKey, a.caCert = key, cert
		return nil
	case errors.Is(kerr, fs.ErrNotExist) && errors.Is(cerr, fs.ErrNotExist):
		return a.createCA()
	case kerr != nil:
		return fmt.Errorf("the CA is incomplete (ca.key: %w) – restore it from a backup", kerr)
	default:
		return fmt.Errorf("the CA is incomplete (ca.crt: %w) – restore it from a backup", cerr)
	}
}

func (a *Authority) createCA() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	sn, err := serial()
	if err != nil {
		return err
	}
	now := a.now()
	name := "Kicker Local CA"
	if a.name != "" {
		name += " (" + a.name + ")"
	}
	tmpl := &x509.Certificate{
		SerialNumber:          sn,
		Subject:               pkix.Name{CommonName: name, Organization: []string{"Kicker"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(CALifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := writeFile(a.path(CAKeyFile), pemBlock("EC PRIVATE KEY", keyDER)); err != nil {
		return err
	}
	if err := writeFile(a.path(CACertFile), pemBlock("CERTIFICATE", der)); err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	a.caKey, a.caCert = key, cert
	a.log.Info("created the local CA", "fingerprint", Fingerprint(der))
	return nil
}

func (a *Authority) loadServer() error {
	pair, err := tls.LoadX509KeyPair(a.path(ServerCertFile), a.path(ServerKeyFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	if leaf.CheckSignatureFrom(a.caCert) != nil {
		return errors.New("server.crt isn't signed by this CA")
	}
	pair.Leaf = leaf
	a.mu.Lock()
	a.server, a.leaf = &pair, leaf
	a.mu.Unlock()
	return nil
}

// Check reissues the server certificate if there is none, it expires within RenewBefore, or it
// lacks a current name or address. It reports whether it did.
func (a *Authority) Check() (bool, error) {
	names := CurrentNames(a.extra)
	a.mu.RLock()
	leaf := a.leaf
	a.mu.RUnlock()
	switch {
	case leaf == nil:
	case a.now().Add(RenewBefore).After(leaf.NotAfter):
		a.log.Info("renewing the server certificate", "expires", leaf.NotAfter)
	case !covers(leaf, names):
		a.log.Info("names or addresses changed, reissuing the server certificate", "dns", names.DNS, "ips", names.IPs)
	default:
		return false, nil
	}
	return true, a.issue(names)
}

func (a *Authority) issue(n Names) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	sn, err := serial()
	if err != nil {
		return err
	}
	now := a.now()
	tmpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: n.DNS[0], Organization: []string{"Kicker"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(ServerLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     n.DNS,
		IPAddresses:  n.IPs,
	}
	if slices.Contains(n.DNS, "kicker.local") {
		tmpl.Subject.CommonName = "kicker.local"
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.caCert, &key.PublicKey, a.caKey)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	// The chain: server certificate, then the CA (browsers that trust the CA build it anyway).
	certPEM := append(pemBlock("CERTIFICATE", der), pemBlock("CERTIFICATE", a.caCert.Raw)...)
	if err := writeFile(a.path(ServerKeyFile), pemBlock("EC PRIVATE KEY", keyDER)); err != nil {
		return err
	}
	if err := writeFile(a.path(ServerCertFile), certPEM); err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	pair := &tls.Certificate{Certificate: [][]byte{der, a.caCert.Raw}, PrivateKey: key, Leaf: leaf}
	a.mu.Lock()
	a.server, a.leaf = pair, leaf
	a.mu.Unlock()
	a.log.Info("issued the server certificate", "dns", n.DNS, "ips", n.IPs, "expires", leaf.NotAfter.Format(time.DateOnly))
	return nil
}

// GetCertificate is for tls.Config: the current server certificate.
func (a *Authority) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.server, nil
}

// TLSConfig is the server's TLS configuration.
func (a *Authority) TLSConfig() *tls.Config {
	return &tls.Config{GetCertificate: a.GetCertificate, MinVersion: tls.VersionTLS12}
}

// CAPEM is the CA certificate as PEM (served as /ca.crt).
func (a *Authority) CAPEM() []byte { return pemBlock("CERTIFICATE", a.caCert.Raw) }

// CAFingerprint is the CA's SHA-256 fingerprint, "AB12 CD34 …".
func (a *Authority) CAFingerprint() string { return Fingerprint(a.caCert.Raw) }

// Leaf returns the current server certificate.
func (a *Authority) Leaf() *x509.Certificate {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.leaf
}

// CA returns the CA certificate.
func (a *Authority) CA() *x509.Certificate { return a.caCert }

// Run re-checks the server certificate every interval (addresses change, expiry approaches).
func (a *Authority) Run(done <-chan struct{}, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if _, err := a.Check(); err != nil {
				a.log.Error("checking the server certificate", "err", err)
			}
		}
	}
}
