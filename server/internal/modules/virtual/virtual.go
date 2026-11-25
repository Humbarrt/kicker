// Package virtual implements in-process modules that speak the real protocol from the module
// side (spec 01). They power `kicker serve --sim` and the tests: everything above the
// transport – session, host, engine, UI – is the real code.
package virtual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/Humbarrt/kicker/server/internal/protocol"
)

// Cap is one capability instance of a virtual module.
type Cap struct {
	Instance string
	ID       string // e.g. "goal_sensor"
	Version  int
	CanWake  bool
	Params   any // static params, e.g. protocol.CoinAcceptorParams{Cap: 2}
}

// CommandFunc answers a command; return nil for ack.
type CommandFunc func(instance, name string, data json.RawMessage) *protocol.Nack

// Module is a virtual module. Configure the exported fields before Run.
type Module struct {
	UID       string
	Manifest  string // mf, e.g. "kicker/goal-sensor"
	FWName    string
	FWVersion string
	Hardware  string
	Caps      []Cap
	OnCommand CommandFunc // optional; default acks everything
	OnReady   func()      // optional; called after `ready`, e.g. for the state sync (spec 01 §6)

	mu       sync.Mutex
	w        io.Writer
	start    time.Time
	nextN    uint32
	ready    bool
	adopted  bool
	pending  map[uint32][]byte // events waiting for ack, in order of n
	order    []uint32
	config   map[string]map[string]json.RawMessage
	commands []Received
	asleep   bool
}

// Asleep reports whether the module got `sleep` (and no `wake` since).
func (m *Module) Asleep() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.asleep
}

// Received is a command the module got (for the simulator UI and tests).
type Received struct {
	Instance string
	Name     string
	Data     json.RawMessage
}

// GoalSensorModule returns the reference goal sensor: instances "a" and "b".
func GoalSensorModule(uid string) *Module {
	return &Module{
		UID: uid, Manifest: "kicker/goal-sensor", FWName: "kicker-goal-sensor", FWVersion: "sim", Hardware: "virtual",
		Caps: []Cap{
			{Instance: "a", ID: "goal_sensor", Version: 1, CanWake: true},
			{Instance: "b", ID: "goal_sensor", Version: 1, CanWake: true},
		},
	}
}

// Run speaks the protocol on conn until ctx ends or conn fails.
func (m *Module) Run(ctx context.Context, conn io.ReadWriter) error {
	m.mu.Lock()
	m.w = conn
	m.start = time.Now()
	m.ready = false
	if m.pending == nil {
		m.pending = map[uint32][]byte{}
		m.config = map[string]map[string]json.RawMessage{}
	}
	m.mu.Unlock()

	m.announce() // once on our own after "boot" (spec 01 §5)

	r := protocol.NewReader(conn)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, err := r.Next()
		if err != nil {
			return err
		}
		if line.Kind != protocol.LineJSON {
			continue
		}
		msg, _, err := protocol.DecodeFrom(protocol.ServerToModule, line.Data)
		if err != nil {
			var perr *protocol.Error
			if errors.As(err, &perr) {
				m.write(perr.Message())
			}
			continue
		}
		m.handle(msg)
	}
}

func (m *Module) handle(msg protocol.Message) {
	switch v := msg.(type) {
	case *protocol.Hello:
		m.mu.Lock()
		m.ready = false
		m.mu.Unlock()
		m.announce()
	case *protocol.Ready:
		m.mu.Lock()
		m.ready = true
		m.adopted = v.Adopted
		resend := make([][]byte, 0, len(m.order))
		for _, n := range m.order {
			if line, ok := m.pending[n]; ok {
				resend = append(resend, line)
			}
		}
		onReady := m.OnReady
		m.mu.Unlock()
		for _, line := range resend {
			m.writeLine(line)
		}
		if onReady != nil {
			onReady()
		}
	case *protocol.Ping:
		m.write(&protocol.Pong{T: protocol.TPong, ID: v.ID, TS: m.now()})
	case *protocol.Ack:
		if v.N != nil {
			m.mu.Lock()
			delete(m.pending, *v.N)
			m.mu.Unlock()
		}
	case *protocol.Cfg:
		m.mergeConfig(v.I, v.D)
		m.ackID(v.ID)
	case *protocol.Cmd:
		m.mu.Lock()
		m.commands = append(m.commands, Received{Instance: v.I, Name: v.C, Data: v.D})
		handler := m.OnCommand
		m.mu.Unlock()
		if handler != nil {
			if nack := handler(v.I, v.C, v.D); nack != nil {
				nack.T, nack.ID = protocol.TNack, v.ID
				m.write(nack)
				return
			}
		}
		m.ackID(v.ID)
	case *protocol.Simple:
		m.mu.Lock()
		switch v.T {
		case protocol.TSleep:
			m.asleep = true
		case protocol.TWake:
			m.asleep = false
		}
		m.mu.Unlock()
		m.ackID(v.ID)
	case *protocol.Identify:
		m.ackID(v.ID)
	}
}

// Emit sends an event; before `ready` it is queued (spec 01 §7). data may be nil.
func (m *Module) Emit(instance, event string, data any) error {
	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		raw = b
	}
	m.mu.Lock()
	n := m.nextN
	m.nextN++
	line, err := protocol.Encode(&protocol.Evt{T: protocol.TEvt, I: instance, E: event, N: n, TS: m.nowLocked(), D: raw})
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if m.pending == nil {
		m.pending = map[uint32][]byte{}
	}
	m.pending[n] = line
	m.order = append(m.order, n)
	if len(m.order) > 64 {
		m.order = m.order[len(m.order)-64:]
	}
	ready := m.ready
	m.mu.Unlock()
	if ready {
		m.writeLine(line)
	}
	return nil
}

// Status reports a capability's health.
func (m *Module) Status(instance, status, msg string) {
	m.write(&protocol.Status{T: protocol.TStatus, I: instance, S: status, Msg: msg})
}

// Log sends a log line.
func (m *Module) Log(level, text string) {
	m.write(&protocol.Log{T: protocol.TLog, L: level, M: text})
}

// Ready reports whether the server answered with `ready`; Adopted whether it said adopted.
func (m *Module) Ready() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ready
}

func (m *Module) Adopted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.adopted
}

// Config returns the merged config of an instance ("" = module level).
func (m *Module) Config(instance string) map[string]json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]json.RawMessage{}
	for k, v := range m.config[instance] {
		out[k] = v
	}
	return out
}

// Commands returns the commands received so far.
func (m *Module) Commands() []Received {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Received(nil), m.commands...)
}

// Pending returns the number of unacknowledged events.
func (m *Module) Pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pending)
}

// --- helpers --------------------------------------------------------------------------

func (m *Module) announce() {
	caps := make([]protocol.CapEntry, 0, len(m.Caps))
	for _, c := range m.Caps {
		e := protocol.CapEntry{I: c.Instance, C: c.ID, CV: c.Version}
		if c.CanWake {
			e.W = 1
		}
		if c.Params != nil {
			e.P = protocol.MustPayload(c.Params)
		}
		caps = append(caps, e)
	}
	m.write(&protocol.Announce{
		T: protocol.TAnnounce, V: protocol.Version, UID: m.UID, TS: m.now(),
		FW: protocol.Firmware{Name: m.FWName, Ver: m.FWVersion}, MF: m.Manifest, HW: m.Hardware, Upd: "none",
		Caps: caps,
	})
}

func (m *Module) mergeConfig(instance string, d json.RawMessage) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(d, &keys); err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.config[instance] == nil {
		m.config[instance] = map[string]json.RawMessage{}
	}
	for k, v := range keys {
		m.config[instance][k] = v
	}
}

func (m *Module) ackID(id uint32) { m.write(&protocol.Ack{T: protocol.TAck, ID: &id}) }

func (m *Module) now() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nowLocked()
}

func (m *Module) nowLocked() int64 {
	if m.start.IsZero() {
		return 0
	}
	return time.Since(m.start).Microseconds()
}

func (m *Module) write(msg protocol.Message) {
	line, err := protocol.Encode(msg)
	if err != nil {
		panic(fmt.Sprintf("virtual module: %v", err))
	}
	m.writeLine(line)
}

func (m *Module) writeLine(line []byte) {
	m.mu.Lock()
	w := m.w
	m.mu.Unlock()
	if w != nil {
		_, _ = w.Write(line)
	}
}
