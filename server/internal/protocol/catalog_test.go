package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustCap(t *testing.T, id string) *Capability {
	t.Helper()
	c, ok := Lookup(id, 1)
	if !ok {
		t.Fatalf("capability %s@1 missing", id)
	}
	return c
}

func TestLookup(t *testing.T) {
	for _, id := range []string{"goal_sensor", "coin_acceptor", "corner_presence", "ball_dispenser", "display"} {
		mustCap(t, id)
	}
	if _, ok := Lookup("goal_sensor", 2); ok {
		t.Error("goal_sensor@2 must not exist")
	}
}

func TestDecodeEvents(t *testing.T) {
	tests := []struct {
		cap, event, data string
		wantErr          ErrCode
	}{
		{"goal_sensor", "goal", ``, ""},
		{"goal_sensor", "own_goal", ``, ErrUnknownEvent},
		{"coin_acceptor", "inserted", `{"uid":"04a1b2c3d4e5f6"}`, ""},
		{"coin_acceptor", "inserted", `{}`, ErrSchema},
		{"coin_acceptor", "held", `{"uids":[]}`, ""},
		{"coin_acceptor", "ejected", `{"by":"button","uids":["04a1"]}`, ""},
		{"coin_acceptor", "ejected", `{"by":"magic","uids":[]}`, ErrSchema},
		{"corner_presence", "present", ``, ""},
		{"ball_dispenser", "released", `{"side":"away","count":1}`, ""},
		{"ball_dispenser", "released", `{"side":"middle","count":1}`, ErrSchema},
		{"ball_dispenser", "empty", `{"side":"home"}`, ""},
		{"display", "img_missing", `{"h":"9f2c41d0"}`, ""},
	}
	for _, tt := range tests {
		_, _, err := mustCap(t, tt.cap).DecodeEvent(tt.event, json.RawMessage(tt.data))
		if code(err) != tt.wantErr {
			t.Errorf("%s/%s %s: got %v, want %q", tt.cap, tt.event, tt.data, err, tt.wantErr)
		}
	}
}

func TestDecodeDisplayState(t *testing.T) {
	display := mustCap(t, "display")
	ok := `{"ph":"playing","s":[3,2],"pl":[{"n":"Anna","col":"#E4572E","img":"9f2c41d0"},{"n":"Ben","col":"#29335C","img":null}],"clk":{"left_ms":184000,"run":true},"msg":"golden_goal"}`
	v, warnings, err := display.DecodeCommand("state", json.RawMessage(ok))
	if err != nil || len(warnings) > 0 {
		t.Fatalf("spec example must decode: %v %v", err, warnings)
	}
	st := v.(*DisplayState)
	if st.S != [2]int{3, 2} || st.Pl[1].Img != nil || *st.Pl[0].Img != "9f2c41d0" {
		t.Errorf("decoded wrongly: %+v", st)
	}

	bad := map[string]string{
		"three scores":  `{"ph":"playing","s":[3,2,1],"pl":[]}`,
		"one score":     `{"ph":"playing","s":[3],"pl":[]}`,
		"bad phase":     `{"ph":"warmup","s":[0,0],"pl":[]}`,
		"img key gone":  `{"ph":"idle","s":[0,0],"pl":[{"n":"A","col":"#000000"}]}`,
		"no players":    `{"ph":"idle","s":[0,0]}`,
		"negative left": `{"ph":"playing","s":[0,0],"pl":[],"clk":{"left_ms":-1,"run":true}}`,
	}
	for name, data := range bad {
		if _, _, err := display.DecodeCommand("state", json.RawMessage(data)); code(err) != ErrSchema {
			t.Errorf("%s: got %v, want schema error", name, err)
		}
	}
}

func TestDecodeConfigPartial(t *testing.T) {
	display := mustCap(t, "display")
	for _, d := range []string{`{}`, `{"lang":"de"}`, `{"team":{"name":"Red Team","col":"#C0392B"}}`, `{"txt":{"paused":"Pausiert"}}`} {
		if _, _, err := display.DecodeConfig(json.RawMessage(d)); err != nil {
			t.Errorf("partial config %s: %v", d, err)
		}
	}
}

func TestDecodeParams(t *testing.T) {
	_, _, err := mustCap(t, "display").DecodeParams(json.RawMessage(`{"w":240,"h":240,"shape":"round","img":64,"anims":["win"]}`))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = mustCap(t, "coin_acceptor").DecodeParams(json.RawMessage(`{"cap":0}`))
	if code(err) != ErrSchema {
		t.Errorf("cap 0 must be rejected, got %v", err)
	}
}

func TestNoDataEventWarnsOnData(t *testing.T) {
	_, warnings, err := mustCap(t, "goal_sensor").DecodeEvent("goal", json.RawMessage(`{"speed":12}`))
	if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "unexpected data") {
		t.Errorf("got %v %v", warnings, err)
	}
}

func TestTextKeysMatchSpec(t *testing.T) {
	if len(DisplayTextKeys) != 10 || DisplayTextKeys["return_ball"] == "" {
		t.Errorf("display text keys out of sync with spec 01 §10: %v", DisplayTextKeys)
	}
}
