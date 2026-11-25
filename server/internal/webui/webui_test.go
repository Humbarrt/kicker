package webui

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandlerFS(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":        {Data: []byte("<html>index</html>")},
		"_app/immutable.js": {Data: []byte("js")},
	}
	h := HandlerFS(fsys)

	tests := []struct {
		path     string
		wantCode int
		wantBody string
	}{
		{"/", 200, "index"},
		{"/foo", 200, "index"},      // SPA fallback
		{"/games/42", 200, "index"}, // nested client route
		{"/_app", 200, "index"},     // directory → fallback, no listing
		{"/_app/immutable.js", 200, "js"},
	}
	for _, tt := range tests {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if w.Code != tt.wantCode || !strings.Contains(w.Body.String(), tt.wantBody) {
			t.Errorf("%s: got %d %q, want %d containing %q", tt.path, w.Code, w.Body.String(), tt.wantCode, tt.wantBody)
		}
	}
}

func TestHandlerFSNotBuilt(t *testing.T) {
	w := httptest.NewRecorder()
	HandlerFS(fstest.MapFS{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/foo", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404 when the UI isn't built", w.Code)
	}
}

func TestContentSecurityPolicy(t *testing.T) {
	index := []byte(`<html><head><script type="module" src="/_app/x.js"></script></head>
<body><script>
	console.log("boot");
</script></body></html>`)
	csp := ContentSecurityPolicy(index)
	// sha256 of the inline script body, including its newlines and tab
	want := "'sha256-" + func() string {
		sum := sha256.Sum256([]byte("\n\tconsole.log(\"boot\");\n"))
		return base64.StdEncoding.EncodeToString(sum[:])
	}() + "'"
	if !strings.Contains(csp, "script-src 'self' "+want+";") || strings.Count(csp, "sha256-") != 1 {
		t.Errorf("csp: %s", csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'none'") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("csp: %s", csp)
	}
}
