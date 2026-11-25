package api

import (
	"html/template"
	"net"
	"net/http"
	"strings"
)

// CA is the kicker's certificate authority, implemented by *tlsca.Authority.
type CA interface {
	CAPEM() []byte
	CAFingerprint() string
}

// serveCA answers /ca.crt: the CA certificate to install (spec 03 §9).
func serveCA(ca CA, w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "application/x-x509-ca-cert")
	h.Set("Content-Disposition", `attachment; filename="kicker-ca.crt"`)
	h.Set("Cache-Control", "no-cache")
	_, _ = w.Write(ca.CAPEM())
}

// serveTrust answers /trust: how to trust this kicker, per operating system. A static page with
// its own strict CSP – it must work over plain HTTP, before the certificate is trusted.
func serveTrust(ca CA, name, lang, httpsURL string, w http.ResponseWriter) {
	t := trustTexts["en"]
	if x, ok := trustTexts[lang]; ok {
		t = x
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	_ = trustPage.Execute(w, map[string]any{"T": t, "Name": name, "Lang": lang, "Fingerprint": ca.CAFingerprint(), "HTTPS": httpsURL})
}

// PlainHandler is the plain-HTTP side once HTTPS is on (spec 03 §9): /ca.crt and the help page,
// everything else redirects to HTTPS. No HSTS on purpose: after a reinstall without a backup the
// kicker has a new CA, and HSTS would lock browsers out instead of showing the normal warning.
// The redirects are temporary for the same reason.
func PlainHandler(ca CA, name string, lang func() string, httpsPort string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := httpsURL(r.Host, httpsPort)
		switch r.URL.Path {
		case "/ca.crt":
			serveCA(ca, w)
			return
		case "/trust":
			serveTrust(ca, name, lang(), target+"/", w)
			return
		}
		code := http.StatusFound
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			code = http.StatusTemporaryRedirect
		}
		http.Redirect(w, r, target+r.URL.RequestURI(), code)
	})
}

// httpsURL is https://<host of the request>[:port].
func httpsURL(hostport, port string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if strings.Contains(host, ":") { // IPv6
		host = "[" + host + "]"
	}
	if host == "" {
		host = "kicker.local"
	}
	if port == "" || port == "443" {
		return "https://" + host
	}
	return "https://" + host + ":" + port
}

type trustText struct {
	Title, Intro, Download, Compare, Continue, Note string
	OS                                              []struct{ Name, Steps string }
}

func steps(pairs ...string) []struct{ Name, Steps string } {
	var out []struct{ Name, Steps string }
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, struct{ Name, Steps string }{pairs[i], pairs[i+1]})
	}
	return out
}

var trustTexts = map[string]trustText{
	"en": {
		Title:    "Trust this kicker",
		Intro:    "The kicker encrypts its connections with its own certificate authority (CA). Browsers don’t know it, so they warn the first time. You can accept the warning – or install the kicker’s CA once, and the warning is gone for good.",
		Download: "Download the kicker’s CA",
		Compare:  "Compare this fingerprint with the one in the kicker’s settings or emergency kit:",
		Continue: "Continue to the kicker",
		Note:     "Install only a CA whose fingerprint matches. It is only valid for this kicker’s certificates.",
		OS: steps(
			"iPhone / iPad", "Open the download in Safari and allow the profile. Settings → General → VPN & Device Management → install the “Kicker Local CA” profile. Then Settings → General → About → Certificate Trust Settings → turn on “Kicker Local CA”.",
			"Android", "Settings → Security (or “Security & privacy”) → More → Encryption & credentials → Install a certificate → CA certificate → choose the downloaded file.",
			"macOS", "Open the downloaded file; Keychain Access adds it to “System”. Double-click “Kicker Local CA” → Trust → “When using this certificate: Always Trust”.",
			"Windows", "Open the downloaded file → Install Certificate → Local Machine → “Place all certificates in the following store” → Trusted Root Certification Authorities.",
			"Firefox (any system)", "Settings → Privacy & Security → Certificates → View Certificates → Authorities → Import → tick “Trust this CA to identify websites”.",
			"Linux", "sudo cp kicker-ca.crt /usr/local/share/ca-certificates/ && sudo update-ca-certificates",
		),
	},
	"de": {
		Title:    "Diesem Kicker vertrauen",
		Intro:    "Der Kicker verschlüsselt seine Verbindungen mit einer eigenen Zertifizierungsstelle (CA). Browser kennen sie nicht und warnen beim ersten Mal. Du kannst die Warnung bestätigen – oder die CA des Kickers einmal installieren, dann ist die Warnung für immer weg.",
		Download: "CA des Kickers herunterladen",
		Compare:  "Vergleiche diesen Fingerabdruck mit dem in den Einstellungen oder im Notfallkit des Kickers:",
		Continue: "Weiter zum Kicker",
		Note:     "Installiere nur eine CA, deren Fingerabdruck passt. Sie gilt nur für die Zertifikate dieses Kickers.",
		OS: steps(
			"iPhone / iPad", "Den Download in Safari öffnen und das Profil erlauben. Einstellungen → Allgemein → VPN und Geräteverwaltung → Profil „Kicker Local CA“ installieren. Dann Einstellungen → Allgemein → Info → Zertifikatsvertrauenseinstellungen → „Kicker Local CA“ einschalten.",
			"Android", "Einstellungen → Sicherheit (oder „Sicherheit & Datenschutz“) → Weitere → Verschlüsselung & Anmeldedaten → Zertifikat installieren → CA-Zertifikat → heruntergeladene Datei wählen.",
			"macOS", "Die heruntergeladene Datei öffnen; die Schlüsselbundverwaltung legt sie unter „System“ ab. „Kicker Local CA“ doppelklicken → Vertrauen → „Bei Verwendung dieses Zertifikats: Immer vertrauen“.",
			"Windows", "Die heruntergeladene Datei öffnen → Zertifikat installieren → Lokaler Computer → „Alle Zertifikate in folgendem Speicher speichern“ → Vertrauenswürdige Stammzertifizierungsstellen.",
			"Firefox (alle Systeme)", "Einstellungen → Datenschutz & Sicherheit → Zertifikate → Zertifikate anzeigen → Zertifizierungsstellen → Importieren → „Dieser CA vertrauen, um Websites zu identifizieren“ ankreuzen.",
			"Linux", "sudo cp kicker-ca.crt /usr/local/share/ca-certificates/ && sudo update-ca-certificates",
		),
	},
}

var trustPage = template.Must(template.New("trust").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.T.Title}} – {{.Name}}</title>
<style>
body{font:16px/1.5 system-ui,sans-serif;color:#1c1917;background:#f5f5f4;margin:0;padding:24px}
main{max-width:640px;margin:0 auto;background:#fff;border-radius:16px;padding:24px 28px;box-shadow:0 1px 3px #0001}
h1{font-size:1.6rem;margin:0 0 8px}h2{font-size:1rem;margin:20px 0 4px}p{margin:8px 0;color:#44403c}
.btn{display:inline-block;padding:10px 16px;border-radius:10px;background:#047857;color:#fff;text-decoration:none;font-weight:600;margin:8px 8px 8px 0}
.btn.alt{background:#fff;color:#1c1917;border:1px solid #d6d3d1}
code{display:block;font:600 14px ui-monospace,monospace;background:#f5f5f4;border-radius:10px;padding:12px;word-spacing:4px;overflow-wrap:anywhere}
.note{font-size:.9rem;color:#78716c}
</style></head><body><main>
<h1>{{.T.Title}}</h1>
<p>{{.T.Intro}}</p>
<p><a class="btn" href="/ca.crt">{{.T.Download}}</a><a class="btn alt" href="{{.HTTPS}}">{{.T.Continue}}</a></p>
<p>{{.T.Compare}}</p>
<code>{{.Fingerprint}}</code>
{{range .T.OS}}<h2>{{.Name}}</h2><p>{{.Steps}}</p>{{end}}
<p class="note">{{.T.Note}}</p>
</main></body></html>
`))
