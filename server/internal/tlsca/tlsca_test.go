package tlsca

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuthority(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	a, err := Open(Options{Dir: dir, Name: "Office", Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{CAKeyFile, CACertFile, ServerKeyFile, ServerCertFile} {
		if fi, err := os.Stat(filepath.Join(dir, f)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, fi, err)
		}
	}
	ca := a.CA()
	if !ca.IsCA || ca.Subject.CommonName != "Kicker Local CA (Office)" || ca.NotAfter.Sub(now) < 19*365*24*time.Hour {
		t.Errorf("CA: %+v", ca.Subject)
	}
	fp := a.CAFingerprint()
	if len(strings.Fields(fp)) != 16 || strings.ToUpper(fp) != fp {
		t.Errorf("fingerprint %q", fp)
	}
	leaf := a.Leaf()
	if d := leaf.NotAfter.Sub(now); d < 396*24*time.Hour || d > 398*24*time.Hour {
		t.Errorf("server lifetime %s", d)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	for _, name := range []string{"kicker.local", "localhost", "127.0.0.1"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: name, CurrentTime: now}); err != nil {
			t.Errorf("verify %s: %v", name, err)
		}
	}

	// Reopening keeps both; a near expiry renews the server certificate, not the CA.
	b, err := Open(Options{Dir: dir, Name: "Office", Now: clock})
	if err != nil || b.CAFingerprint() != fp || b.Leaf().SerialNumber.Cmp(leaf.SerialNumber) != 0 {
		t.Fatalf("reopen: %v", err)
	}
	now = now.Add(370 * 24 * time.Hour)
	if renewed, err := b.Check(); err != nil || !renewed || b.CAFingerprint() != fp || b.Leaf().SerialNumber.Cmp(leaf.SerialNumber) == 0 {
		t.Errorf("renew: %v %v", renewed, err)
	}
	if renewed, _ := b.Check(); renewed {
		t.Error("nothing to renew right after")
	}

	// A missing name triggers a reissue.
	c, _ := Open(Options{Dir: dir, Name: "Office", Now: clock, ExtraNames: []string{"kicker.example.lan"}})
	if !strings.Contains(strings.Join(c.Leaf().DNSNames, " "), "kicker.example.lan") {
		t.Errorf("extra name: %v", c.Leaf().DNSNames)
	}

	// A CA with only one of its files is refused instead of silently replaced.
	if err := os.Remove(filepath.Join(dir, CAKeyFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Options{Dir: dir}); err == nil {
		t.Error("incomplete CA accepted")
	}
}

func TestServesHTTPS(t *testing.T) {
	a, err := Open(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	srv.TLS = a.TLSConfig()
	srv.StartTLS()
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(a.CA())
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost"}}}
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	resp, err := client.Get("https://127.0.0.1:" + port + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
}
