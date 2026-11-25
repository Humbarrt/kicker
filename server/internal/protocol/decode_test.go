package protocol

import (
	"errors"
	"strings"
	"testing"
)

func code(err error) ErrCode {
	var perr *Error
	if errors.As(err, &perr) {
		return perr.Code
	}
	return ""
}

func TestDecodeValid(t *testing.T) {
	tests := []struct {
		dir  Direction
		line string
		want Type
	}{
		{ModuleToServer, `{"t":"announce","v":1,"uid":"e66141040b3c2a29","ts":1000,"fw":{"name":"kicker-goal-sensor","ver":"1.2.0"},"mf":"kicker/goal-sensor","hw":"rp2040-zero","upd":"uf2","caps":[{"i":"a","c":"goal_sensor","cv":1},{"i":"b","c":"goal_sensor","cv":1,"w":1}]}`, TAnnounce},
		{ModuleToServer, `{"t":"pong","id":2,"ts":5321443}`, TPong},
		{ModuleToServer, `{"t":"evt","i":"a","e":"goal","n":0,"ts":9012345}`, TEvt},
		{ModuleToServer, `{"t":"evt","i":"a","e":"goal","n":4294967295,"ts":1}`, TEvt},
		{ModuleToServer, `{"t":"ack","id":7}`, TAck},
		{ModuleToServer, `{"t":"nack","id":7,"code":"busy"}`, TNack},
		{ModuleToServer, `{"t":"status","i":"a","s":"fault","msg":"beam blocked"}`, TStatus},
		{ModuleToServer, `{"t":"log","l":"i","m":"beam level 512"}`, TLog},
		{ModuleToServer, `{"t":"err","code":"bad_json","msg":"x"}`, TErr},
		{ServerToModule, `{"t":"hello","v":1,"vmin":1,"st":1731920000000000}`, THello},
		{ServerToModule, `{"t":"ready","sid":"k3f9","adopted":true}`, TReady},
		{ServerToModule, `{"t":"ping","id":2}`, TPing},
		{ServerToModule, `{"t":"cfg","id":1,"i":"a","d":{"lockout_ms":1000}}`, TCfg},
		{ServerToModule, `{"t":"cmd","id":9,"i":"home","c":"anim","d":{"name":"goal_for"}}`, TCmd},
		{ServerToModule, `{"t":"ack","n":0}`, TAck},
		{ServerToModule, `{"t":"sleep","id":3}`, TSleep},
		{ServerToModule, `{"t":"update","id":3}`, TUpdate},
		{ServerToModule, `{"t":"identify","id":4,"d":{"s":5}}`, TIdentify},
	}
	for _, tt := range tests {
		msg, warnings, err := DecodeFrom(tt.dir, []byte(tt.line))
		if err != nil {
			t.Errorf("%s: unexpected error %v", tt.line, err)
			continue
		}
		if msg.Type() != tt.want {
			t.Errorf("%s: type %q, want %q", tt.line, msg.Type(), tt.want)
		}
		if len(warnings) > 0 {
			t.Errorf("%s: unexpected warnings %v", tt.line, warnings)
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	tests := []struct {
		name string
		dir  Direction
		line string
		want ErrCode
		msg  string // substring of the error message
	}{
		{"not json", ModuleToServer, `{"t":"evt",`, ErrBadJSON, ""},
		{"array", ModuleToServer, `[1,2]`, ErrSchema, "object"},
		{"no t", ModuleToServer, `{"id":1}`, ErrSchema, "missing field t"},
		{"t not string", ModuleToServer, `{"t":5}`, ErrSchema, "t must be a string"},
		{"unknown type", ModuleToServer, `{"t":"dance"}`, ErrUnknownType, "dance"},
		{"wrong direction", ModuleToServer, `{"t":"hello","v":1,"vmin":1,"st":0}`, ErrUnknownType, "hello"},
		{"missing n", ModuleToServer, `{"t":"evt","i":"a","e":"goal","ts":1}`, ErrSchema, "missing field n"},
		{"n wrong type", ModuleToServer, `{"t":"evt","i":"a","e":"goal","n":"1","ts":1}`, ErrSchema, "n"},
		{"n negative", ModuleToServer, `{"t":"evt","i":"a","e":"goal","n":-1,"ts":1}`, ErrSchema, "n"},
		{"n too large", ModuleToServer, `{"t":"evt","i":"a","e":"goal","n":4294967296,"ts":1}`, ErrSchema, "n"},
		{"ts negative", ModuleToServer, `{"t":"evt","i":"a","e":"goal","n":1,"ts":-5}`, ErrSchema, "ts"},
		{"empty instance", ModuleToServer, `{"t":"evt","i":"","e":"goal","n":1,"ts":1}`, ErrSchema, "i and e"},
		{"bad enum", ModuleToServer, `{"t":"status","s":"angry"}`, ErrSchema, "s"},
		{"null required", ModuleToServer, `{"t":"log","l":"i","m":null}`, ErrSchema, "null"},
		{"nested missing", ModuleToServer, `{"t":"announce","v":1,"uid":"x","ts":1000,"fw":{"name":"a"},"mf":"m","caps":[]}`, ErrSchema, "fw.ver"},
		{"cap missing cv", ModuleToServer, `{"t":"announce","v":1,"uid":"x","ts":1000,"fw":{"name":"a","ver":"1"},"mf":"m","caps":[{"i":"a","c":"goal_sensor"}]}`, ErrSchema, "caps[0].cv"},
		{"duplicate instance", ModuleToServer, `{"t":"announce","v":1,"uid":"x","ts":1000,"fw":{"name":"a","ver":"1"},"mf":"m","caps":[{"i":"a","c":"x","cv":1},{"i":"a","c":"y","cv":1}]}`, ErrSchema, "twice"},
		{"empty uid", ModuleToServer, `{"t":"announce","v":1,"uid":"","ts":1000,"fw":{"name":"a","ver":"1"},"mf":"m","caps":[]}`, ErrSchema, "uid"},
		{"module ack without id", ModuleToServer, `{"t":"ack","n":1}`, ErrSchema, "id"},
		{"server ack without n", ServerToModule, `{"t":"ack","id":1}`, ErrSchema, "n"},
		{"ack with both", ModuleToServer, `{"t":"ack","id":1,"n":1}`, ErrSchema, "exactly one"},
		{"identify out of range", ServerToModule, `{"t":"identify","id":1,"d":{"s":0}}`, ErrSchema, "minimum"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := DecodeFrom(tt.dir, []byte(tt.line))
			if code(err) != tt.want {
				t.Fatalf("got %v, want code %s", err, tt.want)
			}
			if tt.msg != "" && !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("error %q should mention %q", err, tt.msg)
			}
		})
	}
}

func TestDecodeTooManyCaps(t *testing.T) {
	var caps []string
	for i := range MaxCapabilities + 1 {
		caps = append(caps, `{"i":"x`+string(rune('a'+i))+`","c":"goal_sensor","cv":1}`)
	}
	line := `{"t":"announce","v":1,"uid":"x","ts":1000,"fw":{"name":"a","ver":"1"},"mf":"m","caps":[` + strings.Join(caps, ",") + `]}`
	if _, _, err := DecodeFrom(ModuleToServer, []byte(line)); code(err) != ErrSchema {
		t.Fatalf("got %v, want schema error for %d caps", err, MaxCapabilities+1)
	}
}

func TestDecodeErrorCarriesRef(t *testing.T) {
	_, _, err := DecodeFrom(ModuleToServer, []byte(`{"t":"evt","i":"a","n":42,"ts":1}`))
	var perr *Error
	if !errors.As(err, &perr) || perr.Ref == nil || *perr.Ref != 42 {
		t.Fatalf("error should reference n=42, got %+v", err)
	}
}

func TestDecodeUnknownFieldsWarn(t *testing.T) {
	msg, warnings, err := DecodeFrom(ModuleToServer,
		[]byte(`{"t":"announce","v":1,"uid":"x","ts":1000,"fw":{"name":"a","ver":"1","build":7},"mf":"m","caps":[{"i":"a","c":"goal_sensor","cv":1,"color":"red"}],"extra":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Type() != TAnnounce {
		t.Fatalf("wrong type %s", msg.Type())
	}
	want := []string{"unknown field caps[0].color", "unknown field extra", "unknown field fw.build"}
	if strings.Join(warnings, ";") != strings.Join(want, ";") {
		t.Errorf("warnings = %v, want %v", warnings, want)
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	id, n := uint32(7), uint32(0)
	msgs := []Message{
		&Hello{T: THello, V: 1, VMin: 1, ST: 123},
		&Ready{T: TReady, SID: "s1", Adopted: true},
		&Ping{T: TPing, ID: 4294967295},
		&Cfg{T: TCfg, ID: 1, I: "a", D: MustPayload(GoalSensorConfig{LockoutMS: 1000})},
		&Cmd{T: TCmd, ID: 2, I: "home", C: "anim", D: MustPayload(DisplayAnim{Name: "win"})},
		&Simple{T: TWake, ID: 3},
		&Identify{T: TIdentify, ID: 4, D: IdentifyArgs{S: 5}},
		&Ack{T: TAck, N: &n},
	}
	for _, m := range msgs {
		line, err := Encode(m)
		if err != nil {
			t.Fatalf("%s: %v", m.Type(), err)
		}
		if line[len(line)-1] != '\n' {
			t.Fatalf("%s: line must end with newline", m.Type())
		}
		got, warnings, err := DecodeFrom(ServerToModule, line[:len(line)-1])
		if err != nil || len(warnings) > 0 {
			t.Fatalf("%s: round trip failed: %v %v (%s)", m.Type(), err, warnings, line)
		}
		if got.Type() != m.Type() {
			t.Errorf("%s: decoded as %s", m.Type(), got.Type())
		}
	}
	// module → server ack
	line, _ := Encode(&Ack{T: TAck, ID: &id})
	if _, _, err := DecodeFrom(ModuleToServer, line); err != nil {
		t.Errorf("module ack round trip: %v", err)
	}
}

func TestEncodeTooLong(t *testing.T) {
	_, err := Encode(&Log{T: TLog, L: "i", M: strings.Repeat("x", MaxLine)})
	if code(err) != ErrTooLong {
		t.Fatalf("got %v, want too_long", err)
	}
}

func FuzzDecode(f *testing.F) {
	seeds := []string{
		`{"t":"evt","i":"a","e":"goal","n":0,"ts":9012345}`,
		`{"t":"announce","v":1,"uid":"x","ts":1000,"fw":{"name":"a","ver":"1"},"mf":"m","caps":[{"i":"a","c":"goal_sensor","cv":1}]}`,
		`{"t":"ack","id":1}`, `{"t":"cmd","id":1,"i":"x","c":"state","d":{"s":[1,2]}}`,
		`{`, `[]`, `null`, `{"t":null}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		for _, dir := range []Direction{ServerToModule, ModuleToServer} {
			msg, _, err := DecodeFrom(dir, []byte(line))
			if err == nil && msg == nil {
				t.Fatal("nil message without error")
			}
			if err != nil {
				var perr *Error
				if !errors.As(err, &perr) {
					t.Fatalf("error is not a protocol error: %v", err)
				}
			}
		}
	})
}
