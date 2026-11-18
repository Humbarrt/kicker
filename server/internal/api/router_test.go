package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter(Options{Sim: true})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/info", nil))
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
	gin.SetMode(gin.TestMode)
	r := NewRouter(Options{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/nope", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
