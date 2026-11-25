// Package webui serves the SvelteKit build that is embedded into the binary (D1).
// `make web` copies web/build into ./dist before the Go build.
package webui

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the embedded web UI.
func Handler() http.Handler {
	root, _ := fs.Sub(dist, "dist")
	return HandlerFS(root)
}

// HandlerFS serves static files from fsys. Paths that don't match a file get index.html
// with status 200, so client-side routes like /games/42 work on reload (SPA fallback).
func HandlerFS(fsys fs.FS) http.Handler {
	files := http.FileServer(http.FS(fsys))
	index, _ := fs.ReadFile(fsys, "index.html")
	csp := ContentSecurityPolicy(index)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		if strings.HasPrefix(r.URL.Path, "/_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || p == "." {
			p = "index.html"
		}
		if info, err := fs.Stat(fsys, p); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		if index == nil {
			http.Error(w, "web UI not built – run `make web`", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(index)
	})
}

var inlineScript = regexp.MustCompile(`(?s)<script(?:\s[^>]*)?>(.*?)</script>`)

// ContentSecurityPolicy builds the strict policy of spec 03 §13 for an index.html: only the
// kicker itself as a source, plus the hashes of the inline scripts SvelteKit puts into the page.
// Styles allow 'unsafe-inline' because Svelte sets style attributes; scripts never do.
func ContentSecurityPolicy(index []byte) string {
	scripts := []string{"'self'"}
	for _, m := range inlineScript.FindAllSubmatchIndex(index, -1) {
		tag := string(index[m[0]:m[2]])
		if strings.Contains(tag, " src=") {
			continue
		}
		sum := sha256.Sum256(index[m[2]:m[3]])
		scripts = append(scripts, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + strings.Join(scripts, " "),
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"connect-src 'self'",
		"font-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}
