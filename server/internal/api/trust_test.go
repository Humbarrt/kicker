package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/onboarding"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/tlsca"
	"github.com/Humbarrt/kicker/server/internal/webui"
)

func TestPlainHandler(t *testing.T) {
	ca, err := tlsca.Open(tlsca.Options{Dir: t.TempDir(), Name: "Office"})
	if err != nil {
		t.Fatal(err)
	}
	lang := func() string { return "de" }
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, c := range []struct {
		port, method, path, host, want string
		code                           int
	}{
		{"443", "GET", "/players/3?x=1", "kicker.local", "https://kicker.local/players/3?x=1", 302},
		{"443", "POST", "/api/auth/login", "kicker.local:80", "https://kicker.local/api/auth/login", 307},
		{"8443", "GET", "/", "192.168.1.20:8080", "https://192.168.1.20:8443/", 302},
	} {
		srv := httptest.NewServer(PlainHandler(ca, "Office", lang, c.port))
		req, _ := http.NewRequest(c.method, srv.URL+c.path, nil)
		req.Host = c.host
		resp, err := noFollow.Do(req)
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != c.code || resp.Header.Get("Location") != c.want || resp.Header.Get("Strict-Transport-Security") != "" {
			t.Errorf("%s %s: %d %s", c.method, c.path, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	srv := httptest.NewServer(PlainHandler(ca, "Office", lang, "443"))
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/ca.crt")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(string(body), "-----BEGIN CERTIFICATE-----") ||
		resp.Header.Get("Content-Type") != "application/x-x509-ca-cert" {
		t.Errorf("ca.crt: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp, _ = http.Get(srv.URL + "/trust")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), ca.CAFingerprint()) || !strings.Contains(string(body), "Diesem Kicker vertrauen") {
		t.Errorf("trust page: %d", resp.StatusCode)
	}
}

// Over HTTPS the session cookie is __Host-kicker_session (Secure), and /api/info has the CA's
// fingerprint.
func TestHTTPSRouter(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	as := newAuth(t, st)
	if err := onboarding.CompleteFromConsole(ctx, st); err != nil {
		t.Fatal(err)
	}
	ob, _ := onboarding.New(ctx, onboarding.Options{Store: st, Auth: as})
	ca, err := tlsca.Open(tlsca.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(NewRouter(Options{ServerID: "sid", DataDir: dir, CA: ca, Auth: as, Onboarding: ob,
		UI: webui.HandlerFS(fstest.MapFS{})}))
	srv.TLS = ca.TLSConfig()
	srv.StartTLS()
	defer srv.Close()
	makeAccount(t, as, "anna", auth.GroupAdmin, auth.StatusApproved)
	pool := x509.NewCertPool()
	pool.AddCert(ca.CA())
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost"}}}

	resp, err := client.Get(srv.URL + "/api/info")
	if err != nil {
		t.Fatal(err)
	}
	var info map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&info)
	resp.Body.Close()
	if info["ca_fingerprint"] != ca.CAFingerprint() {
		t.Errorf("info: %v", info)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/auth/login", strings.NewReader(`{"username":"anna","password":"blue tiger dances"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	ck := resp.Cookies()
	if resp.StatusCode != 200 || len(ck) != 1 || ck[0].Name != "__Host-kicker_session" || !ck[0].Secure || !ck[0].HttpOnly {
		t.Errorf("login over HTTPS: %d %+v", resp.StatusCode, ck)
	}
	if resp, err := client.Get(srv.URL + "/ca.crt"); err != nil || resp.StatusCode != 200 {
		t.Errorf("ca.crt over HTTPS: %v", err)
	}
}
