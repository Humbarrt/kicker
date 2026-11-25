package api

import (
	"net/http"
	"testing"
)

// The stats routes answer with view.stats (Anybody has it by default); a played game shows up.
func TestStatsRoutes(t *testing.T) {
	f := newTableFixture(t)
	if code, _ := f.do(t, http.MethodGet, "/api/leaderboard?key=duo", ""); code != http.StatusBadRequest {
		t.Errorf("unknown key: %d", code)
	}
	if code, _ := f.do(t, http.MethodGet, "/api/players/x/stats", ""); code != http.StatusBadRequest {
		t.Errorf("bad id: %d", code)
	}
	for _, path := range []string{"/api/leaderboard?key=solo", "/api/overview", "/api/players/1/stats"} {
		resp, err := http.Get(f.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("anonymous %s: %d", path, resp.StatusCode)
		}
	}
	code, lb := f.do(t, http.MethodGet, "/api/leaderboard", "")
	if code != http.StatusOK || lb["key"] != "team" || lb["min_ranked"] != float64(5) {
		t.Errorf("leaderboard: %d %v", code, lb)
	}
	code, ov := f.do(t, http.MethodGet, "/api/overview", "")
	if code != http.StatusOK || ov["window_days"] != float64(30) || ov["games"] != float64(0) {
		t.Errorf("overview: %d %v", code, ov)
	}

	// The game list filters reject unknown values.
	for _, q := range []string{"kind=duo", "player=1,2,3,4,5", "flags=voided,odd", "relation=near", "result=won", "from=x"} {
		if code, _ := f.do(t, http.MethodGet, "/api/games?"+q, ""); code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, code)
		}
	}
	if code, _ := f.do(t, http.MethodGet, "/api/games?player=1&player=2&relation=against&result=loss&kind=team&status=finished&flags=close,unknown&from=1&to=2", ""); code != http.StatusOK {
		t.Errorf("all filters: %d", code)
	}
}
