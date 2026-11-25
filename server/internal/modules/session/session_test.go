package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/protocol"
)

// fakeModule plays the module side over one end of a net.Pipe.
type fakeModule struct {
	t      *testing.T
	conn   net.Conn
	mu     sync.Mutex
	got    []map[string]any // every message the server sent
	silent bool             // don't answer hello
	mute   bool             // stop answering pings
	nackID uint32           // answer this command id with nack busy
	dropN  int              // ignore the first N sends of any cmd
	seen   map[float64]int
	ts     int64
}

const announceLine = `{"t":"announce","v":1,"uid":"e66141040b3c2a29","ts":1000,"fw":{"name":"kicker-goal-sensor","ver":"0.2.0"},"mf":"kicker/goal-sensor","hw":"rp2040-zero","upd":"uf2","caps":[{"i":"a","c":"goal_sensor","cv":1},{"i":"b","c":"goal_sensor","cv":1,"w":1},{"i":"x","c":"acme.test/horn","cv":1}]}`

func (m *fakeModule) run() {
	sc := bufio.NewScanner(m.conn)
	for sc.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			m.t.Errorf("server sent invalid JSON: %s", sc.Text())
			continue
		}
		m.mu.Lock()
		m.got = append(m.got, msg)
		silent, mute, nackID, dropN := m.silent, m.mute, m.nackID, m.dropN
		m.mu.Unlock()

		id, _ := msg["id"].(float64)
		switch msg["t"] {
		case "hello":
			if !silent {
				m.send(announceLine)
			}
		case "ping":
			if !mute {
				m.mu.Lock()
				m.ts += 1000
				ts := m.ts
				m.mu.Unlock()
				m.send(`{"t":"pong","id":` + itoa(id) + `,"ts":` + itoa(float64(ts)) + `}`)
			}
		case "cmd", "cfg":
			m.mu.Lock()
			if m.seen == nil {
				m.seen = map[float64]int{}
			}
			m.seen[id]++
			count := m.seen[id]
			m.mu.Unlock()
			if count <= dropN {
				continue
			}
			if uint32(id) == nackID {
				m.send(`{"t":"nack","id":` + itoa(id) + `,"code":"busy","msg":"later"}`)
			} else {
				m.send(`{"t":"ack","id":` + itoa(id) + `}`)
			}
		}
	}
}

func itoa(f float64) string { b, _ := json.Marshal(int64(f)); return string(b) }

func (m *fakeModule) send(line string) {
	if _, err := m.conn.Write([]byte(line + "\n")); err != nil {
		return
	}
}

func (m *fakeModule) sent(t string) []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []map[string]any
	for _, msg := range m.got {
		if msg["t"] == t {
			out = append(out, msg)
		}
	}
	return out
}

type recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *recorder) add(e Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *recorder) moduleEvents() []ModuleEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []ModuleEvent
	for _, e := range r.events {
		if me, ok := e.(ModuleEvent); ok {
			out = append(out, me)
		}
	}
	return out
}

func (r *recorder) find(pred func(Event) bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if pred(e) {
			return true
		}
	}
	return false
}

type harness struct {
	mod    *fakeModule
	rec    *recorder
	sess   *Session
	cancel context.CancelFunc
	done   chan error
}

func start(t *testing.T, configure func(*fakeModule), opts Options) *harness {
	t.Helper()
	server, module := net.Pipe()
	mod := &fakeModule{t: t, conn: module}
	if configure != nil {
		configure(mod)
	}
	go mod.run()
	rec := &recorder{}
	if opts.PingInterval == 0 {
		opts.PingInterval = 40 * time.Millisecond
	}
	if opts.CommandWait == 0 {
		opts.CommandWait = 30 * time.Millisecond
	}
	if opts.HelloTimeout == 0 {
		opts.HelloTimeout = 200 * time.Millisecond
	}
	opts.OnEvent = rec.add
	sess := New(server, opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sess.Run(ctx) }()
	h := &harness{mod: mod, rec: rec, sess: sess, cancel: cancel, done: done}
	t.Cleanup(func() {
		cancel()
		server.Close()
		module.Close()
	})
	return h
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func (h *harness) announced(t *testing.T) {
	t.Helper()
	eventually(t, "announce", func() bool { return h.sess.Announce() != nil })
}

// --- tests --------------------------------------------------------------------------

func TestHandshake(t *testing.T) {
	h := start(t, nil, Options{
		Adopted: true,
		ConfigOnReady: func(*protocol.Announce) []ConfigItem {
			return []ConfigItem{{Instance: "a", Data: protocol.GoalSensorConfig{LockoutMS: 1000}}}
		},
	})
	h.announced(t)
	eventually(t, "ready + cfg", func() bool { return len(h.mod.sent("ready")) == 1 && len(h.mod.sent("cfg")) == 1 })

	if h.mod.sent("hello")[0]["v"] != float64(1) {
		t.Errorf("hello must offer protocol v1")
	}
	if h.mod.sent("ready")[0]["adopted"] != true {
		t.Errorf("ready must carry adopted=true")
	}
	if cfg := h.mod.sent("cfg")[0]; cfg["i"] != "a" || cfg["d"].(map[string]any)["lockout_ms"] != float64(1000) {
		t.Errorf("unexpected cfg %v", cfg)
	}
	var ann Announced
	h.rec.find(func(e Event) bool { a, ok := e.(Announced); ann = a; return ok })
	if len(ann.Caps) != 3 || !ann.Caps[0].Supported || !ann.Caps[1].CanWake || ann.Caps[2].Supported {
		t.Errorf("caps not resolved correctly: %+v", ann.Caps)
	}
}

func TestRejectAndResync(t *testing.T) {
	h := start(t, nil, Options{AcceptAnnounce: func(a *protocol.Announce) *protocol.Error {
		return &protocol.Error{Code: protocol.ErrDuplicateUID, Msg: "uid " + a.UID + " is already connected"}
	}})
	select {
	case err := <-h.done:
		if !errors.Is(err, ErrRejected) {
			t.Fatalf("got %v, want ErrRejected", err)
		}
	case <-time.After(time.Second):
		t.Fatal("rejected session must end")
	}
	eventually(t, "err duplicate_uid", func() bool {
		errs := h.mod.sent("err")
		return len(errs) == 1 && errs[0]["code"] == "duplicate_uid"
	})
	if len(h.mod.sent("ready")) != 0 {
		t.Error("a rejected module must not get ready")
	}
}

func TestResyncAfterAdoption(t *testing.T) {
	adopted := false
	var mu sync.Mutex
	h := start(t, nil, Options{AdoptedFor: func(*protocol.Announce) bool {
		mu.Lock()
		defer mu.Unlock()
		return adopted
	}})
	h.announced(t)
	eventually(t, "first ready", func() bool { return len(h.mod.sent("ready")) == 1 })
	mu.Lock()
	adopted = true
	mu.Unlock()
	if err := h.sess.Resync(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "second ready", func() bool { return len(h.mod.sent("ready")) == 2 })
	readies := h.mod.sent("ready")
	if readies[0]["adopted"] != false || readies[1]["adopted"] != true {
		t.Errorf("expected ready adopted=false then true, got %v", readies)
	}
}

func TestNoAnnounce(t *testing.T) {
	h := start(t, func(m *fakeModule) { m.silent = true }, Options{HelloTimeout: 50 * time.Millisecond})
	select {
	case err := <-h.done:
		if !errors.Is(err, ErrNoAnnounce) {
			t.Fatalf("got %v, want ErrNoAnnounce", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run should give up after the hello timeout")
	}
}

func TestEventsAreAckedAndDeduplicated(t *testing.T) {
	h := start(t, nil, Options{})
	h.announced(t)
	h.mod.send(`{"t":"evt","i":"a","e":"goal","n":0,"ts":5000}`)
	h.mod.send(`{"t":"evt","i":"a","e":"goal","n":0,"ts":5000}`) // retransmission
	h.mod.send(`{"t":"evt","i":"b","e":"goal","n":4294967295,"ts":6000}`)
	eventually(t, "3 acks", func() bool { return len(h.mod.sent("ack")) == 3 })

	evs := h.rec.moduleEvents()
	if len(evs) != 2 {
		t.Fatalf("got %d module events, want 2 (duplicate dropped): %+v", len(evs), evs)
	}
	if evs[0].Instance != "a" || evs[0].Name != "goal" || evs[1].N != 4294967295 {
		t.Errorf("unexpected events %+v", evs)
	}
	if !h.rec.find(func(e Event) bool {
		cl, ok := e.(ConsoleLine)
		return ok && strings.Contains(strings.Join(cl.Warnings, ""), "duplicate")
	}) {
		t.Error("duplicate should be visible in the console")
	}
}

func TestInvalidEvents(t *testing.T) {
	h := start(t, nil, Options{})
	h.announced(t)
	h.mod.send(`{"t":"evt","i":"zz","e":"goal","n":1,"ts":1}`)
	h.mod.send(`{"t":"evt","i":"a","e":"own_goal","n":2,"ts":1}`)
	h.mod.send(`{"t":"evt","i":"a","e":"goal"`)
	h.mod.send(`hello from Serial.println`)
	eventually(t, "3 errs", func() bool { return len(h.mod.sent("err")) == 3 })

	errs := h.mod.sent("err")
	want := []string{"unknown_instance", "unknown_event", "bad_json"}
	for i, w := range want {
		if errs[i]["code"] != w {
			t.Errorf("err %d: got %v, want %s", i, errs[i]["code"], w)
		}
	}
	if errs[0]["ref"] != float64(1) || errs[1]["ref"] != float64(2) {
		t.Errorf("errors must reference the event n: %v", errs)
	}
	if len(h.rec.moduleEvents()) != 0 {
		t.Error("invalid events must not reach the module host")
	}
	if len(h.mod.sent("ack")) != 2 {
		t.Error("valid-JSON events are acked even when invalid, so the module stops resending")
	}
}

func TestCommandRetriesWithSameID(t *testing.T) {
	h := start(t, func(m *fakeModule) { m.dropN = 1 }, Options{})
	h.announced(t)
	res, err := h.sess.Cmd(context.Background(), "x", "honk", map[string]int{"ms": 300})
	if err != nil || !res.OK {
		t.Fatalf("got %+v %v", res, err)
	}
	cmds := h.mod.sent("cmd")
	if len(cmds) != 2 || cmds[0]["id"] != cmds[1]["id"] {
		t.Errorf("command must be resent with the same id: %v", cmds)
	}
}

func TestCommandTimeout(t *testing.T) {
	h := start(t, func(m *fakeModule) { m.dropN = 99 }, Options{})
	h.announced(t)
	_, err := h.sess.Cmd(context.Background(), "x", "honk", nil)
	if err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Fatalf("expected timeout after 3 tries, got %v", err)
	}
	if n := len(h.mod.sent("cmd")); n != 3 {
		t.Errorf("command sent %d times, want 3", n)
	}
}

func TestNackResult(t *testing.T) {
	h := start(t, nil, Options{})
	h.announced(t)
	res, err := h.sess.Command(context.Background(), func(id uint32) protocol.Message {
		h.mod.mu.Lock()
		h.mod.nackID = id // make the fake module refuse exactly this command
		h.mod.mu.Unlock()
		return &protocol.Cmd{T: protocol.TCmd, ID: id, I: "x", C: "honk"}
	})
	if err != nil || res.OK || res.Code != protocol.NackBusy || res.Msg != "later" {
		t.Fatalf("expected nack busy, got %+v %v", res, err)
	}
}

func TestHeartbeatOnlineOffline(t *testing.T) {
	h := start(t, nil, Options{})
	h.announced(t)
	eventually(t, "clock offset", func() bool { _, ok := h.sess.ClockOffset(); return ok })

	h.mod.mu.Lock()
	h.mod.mute = true
	h.mod.mu.Unlock()
	eventually(t, "offline", func() bool {
		return h.rec.find(func(e Event) bool { o, ok := e.(Online); return ok && !o.Online })
	})

	h.mod.mu.Lock()
	h.mod.mute = false
	h.mod.mu.Unlock()
	h.rec.mu.Lock()
	h.rec.events = nil
	h.rec.mu.Unlock()
	eventually(t, "online again", func() bool {
		return h.rec.find(func(e Event) bool { o, ok := e.(Online); return ok && o.Online })
	})
}

func TestRepeatedAnnounceKeepsDedup(t *testing.T) {
	h := start(t, nil, Options{})
	h.announced(t)
	h.mod.send(`{"t":"evt","i":"a","e":"goal","n":0,"ts":2000}`)
	eventually(t, "first event", func() bool { return len(h.rec.moduleEvents()) == 1 })
	// same module, uptime moved on: e.g. the reply to hello after the boot announce
	h.mod.send(strings.Replace(announceLine, `"ts":1000`, `"ts":3000`, 1))
	eventually(t, "second ready", func() bool { return len(h.mod.sent("ready")) == 2 })
	h.mod.send(`{"t":"evt","i":"a","e":"goal","n":0,"ts":2000}`) // resent after the new ready
	eventually(t, "ack", func() bool { return len(h.mod.sent("ack")) == 2 })
	time.Sleep(20 * time.Millisecond)
	if n := len(h.rec.moduleEvents()); n != 1 {
		t.Fatalf("resent event counted twice (%d events)", n)
	}
	if h.rec.find(func(e Event) bool { a, ok := e.(Announced); return ok && a.Rebooted && a.Announce.TS == 3000 }) {
		t.Error("repeated announce must not count as reboot")
	}
}

func TestRebootResetsDedup(t *testing.T) {
	h := start(t, nil, Options{})
	h.announced(t)
	h.mod.send(`{"t":"evt","i":"a","e":"goal","n":0,"ts":5000}`)
	eventually(t, "first event", func() bool { return len(h.rec.moduleEvents()) == 1 })
	h.mod.send(strings.Replace(announceLine, `"ts":1000`, `"ts":10`, 1)) // uptime went back: reboot
	eventually(t, "second ready", func() bool { return len(h.mod.sent("ready")) == 2 })
	h.mod.send(`{"t":"evt","i":"a","e":"goal","n":0,"ts":20}`)
	eventually(t, "event after reboot", func() bool { return len(h.rec.moduleEvents()) == 2 })
}

func TestClockOffset(t *testing.T) {
	var c clockSync
	base := time.Unix(1_700_000_000, 0)
	// module booted at base-10s: module ts = 10_000_000 µs at base
	off := c.add(base, base.Add(4*time.Millisecond), 10_000_000)
	got := time.UnixMicro(10_000_000).Add(off)
	if d := got.Sub(base.Add(2 * time.Millisecond)); d != 0 {
		t.Fatalf("offset wrong by %v", d)
	}
	// a slower round trip must not replace the better sample
	off2 := c.add(base.Add(time.Second), base.Add(time.Second+50*time.Millisecond), 11_000_000)
	if off2 != off {
		t.Errorf("best (lowest RTT) sample must win: %v vs %v", off2, off)
	}
}
