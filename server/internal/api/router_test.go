package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/modules"
	"github.com/Humbarrt/kicker/server/internal/modules/session"
	"github.com/Humbarrt/kicker/server/internal/modules/transport"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/webui"
)

type fakeHost struct{}

func (fakeHost) Modules() []modules.ModuleInfo {
	return []modules.ModuleInfo{{UID: "G1", Name: "Goal sensors", Online: true, Adopted: true,
		Caps: []modules.CapView{{Instance: "a", Capability: "goal_sensor@1", Role: "home_goal"}}}}
}

func (fakeHost) Console(uid string) []modules.ConsoleEvent {
	if uid != "G1" {
		return nil
	}
	return []modules.ConsoleEvent{{UID: "G1", Port: "/dev/ttyACM0", Line: session.ConsoleLine{Dir: session.In, Text: `{"t":"pong"}`}}}
}

func (fakeHost) Adopt(context.Context, string, string, map[string]string) error { return nil }
func (fakeHost) Release(context.Context, string) error                          { return nil }
func (fakeHost) Identify(context.Context, string, int) error                    { return modules.ErrOffline }
func (fakeHost) UnknownPorts() []transport.PortInfo {
	return []transport.PortInfo{{Name: "/dev/ttyUSB0", VID: "1A86", PID: "7523"}}
}
func (fakeHost) Probe(string) {}
func (fakeHost) SetConfig(_ context.Context, _, _ string, p json.RawMessage) error {
	if strings.Contains(string(p), "-1") {
		return fmt.Errorf("%w: lockout_ms below minimum", modules.ErrInvalidConfig)
	}
	return nil
}

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func TestModulesRoutes(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	as := newAuth(t, st)
	token := makeAccount(t, as, "anna", auth.GroupAdmin, auth.StatusApproved)
	r := NewRouter(Options{Auth: as, Modules: fakeHost{}, UI: webui.HandlerFS(fstest.MapFS{})})
	get := func(r http.Handler, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	w := get(r, "/api/modules")
	var mods []modules.ModuleInfo
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &mods) != nil || len(mods) != 1 || mods[0].Caps[0].Role != "home_goal" {
		t.Fatalf("GET /api/modules: %d %s", w.Code, w.Body.String())
	}
	w = get(r, "/api/modules/G1/console")
	var lines []ConsoleLine
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &lines) != nil || len(lines) != 1 || lines[0].Dir != "in" {
		t.Fatalf("GET console: %d %s", w.Code, w.Body.String())
	}
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := do("POST", "/api/modules/G1/adopt", `{"name":"Goals","roles":{"a":"home_goal"}}`); w.Code != 204 {
		t.Errorf("adopt: %d %s", w.Code, w.Body.String())
	}
	if w := do("POST", "/api/modules/G1/identify", ``); w.Code != 409 || !strings.Contains(w.Body.String(), "offline") {
		t.Errorf("identify offline: %d %s", w.Code, w.Body.String())
	}
	if w := do("PATCH", "/api/modules/G1/config/a", `{"lockout_ms":1500}`); w.Code != 204 {
		t.Errorf("config: %d %s", w.Code, w.Body.String())
	}
	if w := do("PATCH", "/api/modules/G1/config/a", `{"lockout_ms":-1}`); w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_config") {
		t.Errorf("bad config: %d %s", w.Code, w.Body.String())
	}
	if w := get(r, "/api/modules/ports"); !strings.Contains(w.Body.String(), `"vid":"1A86"`) {
		t.Errorf("ports: %s", w.Body.String())
	}
	if w := do("POST", "/api/modules/ports/probe", `{"port":"/dev/ttyUSB0"}`); w.Code != 202 {
		t.Errorf("probe: %d", w.Code)
	}
	if log, _ := as.AuditLog(context.Background(), 5); len(log) != 2 || log[1].Action != "module_adopted" || log[0].Action != "module_config_changed" {
		t.Errorf("audit: %+v", log)
	}
	if w := get(r, "/api/modules/NOPE/console"); w.Body.String() != "[]" {
		t.Errorf("unknown module console must be an empty list, got %s", w.Body.String())
	}
}

func testRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ui := webui.HandlerFS(fstest.MapFS{
		"index.html":    {Data: []byte("<html>index</html>")},
		"_app/entry.js": {Data: []byte("js")},
	})
	return NewRouter(Options{Sim: true, UI: ui})
}

func get(r http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestInfo(t *testing.T) {
	w := get(testRouter(t), "/api/info")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var info Info
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if !info.Sim || info.Version == "" {
		t.Errorf("unexpected info: %+v", info)
	}
}

func TestUnknownAPIRouteIsJSON404(t *testing.T) {
	w := get(testRouter(t), "/api/nope")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("got %d %q, want JSON 404", w.Code, w.Body.String())
	}
}

// Regression: client-side routes must get index.html with 200 through gin's NoRoute.
func TestSPAFallbackThroughGin(t *testing.T) {
	r := testRouter(t)
	for _, path := range []string{"/", "/foo", "/games/42"} {
		w := get(r, path)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "index") {
			t.Errorf("%s: got %d %q, want 200 with index.html", path, w.Code, w.Body.String())
		}
	}
	if w := get(r, "/_app/entry.js"); w.Code != http.StatusOK || w.Body.String() != "js" {
		t.Errorf("static file: got %d %q", w.Code, w.Body.String())
	}
}
