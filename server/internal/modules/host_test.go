package modules

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/modules/session"
	"github.com/Humbarrt/kicker/server/internal/modules/transport"
	"github.com/Humbarrt/kicker/server/internal/modules/virtual"
	"github.com/Humbarrt/kicker/server/internal/protocol"
)

// --- fake serial ports backed by virtual modules ---------------------------------------------

type fakeDevice struct {
	info    transport.PortInfo
	make    func() *virtual.Module // nil = silent device (never answers)
	module  *virtual.Module
	devSide net.Conn
}

type fakePorts struct {
	ctx     context.Context
	mu      sync.Mutex
	devices map[string]*fakeDevice
	opens   map[string]int
}

func newFakePorts(ctx context.Context) *fakePorts {
	return &fakePorts{ctx: ctx, devices: map[string]*fakeDevice{}, opens: map[string]int{}}
}

func (f *fakePorts) plug(name, vid string, mk func() *virtual.Module) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devices[name] = &fakeDevice{info: transport.PortInfo{Name: name, USB: true, VID: vid, PID: "000A"}, make: mk}
}

func (f *fakePorts) unplug(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d := f.devices[name]; d != nil && d.devSide != nil {
		d.devSide.Close()
	}
	delete(f.devices, name)
}

func (f *fakePorts) module(name string) *virtual.Module {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d := f.devices[name]; d != nil {
		return d.module
	}
	return nil
}

func (f *fakePorts) openCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens[name]
}

func (f *fakePorts) List() ([]transport.PortInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []transport.PortInfo
	for _, d := range f.devices {
		out = append(out, d.info)
	}
	return out, nil
}

func (f *fakePorts) Open(name string) (io.ReadWriteCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.devices[name]
	if d == nil {
		return nil, errors.New("no such port")
	}
	f.opens[name]++
	hostSide, devSide := net.Pipe()
	d.devSide = devSide
	if d.make == nil {
		go func() { _, _ = io.Copy(io.Discard, devSide) }()
	} else {
		d.module = d.make()
		go func(m *virtual.Module) { _ = m.Run(f.ctx, devSide) }(d.module)
	}
	return hostSide, nil
}

// --- harness ----------------------------------------------------------------------------------

type events struct {
	mu  sync.Mutex
	all []Event
}

func (e *events) add(ev Event) {
	e.mu.Lock()
	e.all = append(e.all, ev)
	e.mu.Unlock()
}

func (e *events) capEvents() []CapabilityEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []CapabilityEvent
	for _, ev := range e.all {
		if c, ok := ev.(CapabilityEvent); ok {
			out = append(out, c)
		}
	}
	return out
}

type harness struct {
	host  *Host
	ports *fakePorts
	ev    *events
	store *MemoryStore
}

func startHost(t *testing.T, store *MemoryStore, configure func(*Options)) *harness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if store == nil {
		store = NewMemoryStore()
	}
	ports := newFakePorts(ctx)
	ev := &events{}
	opts := Options{
		Store: store, Ports: ports, ScanInterval: 30 * time.Millisecond, OnEvent: ev.add,
		Session: session.Options{HelloTimeout: 100 * time.Millisecond, PingInterval: 50 * time.Millisecond, CommandWait: 50 * time.Millisecond},
	}
	if configure != nil {
		configure(&opts)
	}
	host, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = host.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("host did not stop")
		}
	})
	return &harness{host: host, ports: ports, ev: ev, store: store}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func (h *harness) module(uid string) (ModuleInfo, bool) {
	for _, m := range h.host.Modules() {
		if m.UID == uid {
			return m, true
		}
	}
	return ModuleInfo{}, false
}

func (h *harness) online(t *testing.T, uid string) {
	t.Helper()
	eventually(t, uid+" online", func() bool { m, ok := h.module(uid); return ok && m.Online })
}

func goalSensor(uid string) func() *virtual.Module {
	return func() *virtual.Module { return virtual.GoalSensorModule(uid) }
}

func displayModule(uid string) func() *virtual.Module {
	return func() *virtual.Module {
		return &virtual.Module{UID: uid, Manifest: "kicker/display", FWName: "d", FWVersion: "1", Caps: []virtual.Cap{
			{Instance: "screen", ID: "display", Version: 1, Params: protocol.DisplayParams{W: 240, H: 240, Shape: "round", Img: 64, Anims: protocol.DisplayAnimations}},
		}}
	}
}

var bothGoals = map[string]string{"a": "home_goal", "b": "away_goal"}

// --- tests ------------------------------------------------------------------------------------

func TestDiscoverAdoptAndEvents(t *testing.T) {
	h := startHost(t, nil, nil)
	h.ports.plug("/dev/ttyACM0", "2E8A", goalSensor("G1"))
	h.online(t, "G1")

	m, _ := h.module("G1")
	if m.Adopted || m.Port != "/dev/ttyACM0" || len(m.Caps) != 2 || m.Caps[0].Roles[0] != "home_goal" {
		t.Fatalf("unexpected module %+v", m)
	}
	vm := h.ports.module("/dev/ttyACM0")
	eventually(t, "ready", vm.Ready)
	if vm.Adopted() {
		t.Fatal("not adopted yet")
	}

	// unadopted events reach the UI, but without role
	_ = vm.Emit("a", "goal", nil)
	eventually(t, "unadopted event", func() bool { return len(h.ev.capEvents()) == 1 })
	if e := h.ev.capEvents()[0]; e.Adopted || e.Role != "" || e.Capability != "goal_sensor" {
		t.Errorf("unadopted event: %+v", e)
	}

	if err := h.host.Adopt(context.Background(), "G1", "Goal sensors", bothGoals); err != nil {
		t.Fatal(err)
	}
	eventually(t, "adopted ready", vm.Adopted)
	_ = vm.Emit("b", "goal", nil)
	eventually(t, "adopted event", func() bool { return len(h.ev.capEvents()) == 2 })
	if e := h.ev.capEvents()[1]; !e.Adopted || e.Role != "away_goal" || e.Name != "goal" || e.At.IsZero() {
		t.Errorf("adopted event: %+v", e)
	}
	if m, _ := h.module("G1"); m.Name != "Goal sensors" || m.Caps[1].Role != "away_goal" {
		t.Errorf("snapshot after adoption: %+v", m)
	}
}

func TestUnknownVendorOnlyAfterProbe(t *testing.T) {
	h := startHost(t, nil, nil)
	h.ports.plug("/dev/ttyUSB7", "FFFF", goalSensor("X1"))
	eventually(t, "listed as unknown", func() bool { return len(h.host.UnknownPorts()) == 1 })
	time.Sleep(100 * time.Millisecond)
	if h.ports.openCount("/dev/ttyUSB7") != 0 {
		t.Fatal("ports of unknown vendors must not be opened automatically")
	}
	h.host.Probe("/dev/ttyUSB7")
	h.online(t, "X1")
	if len(h.host.UnknownPorts()) != 0 {
		t.Error("probed port must leave the unknown list")
	}
}

func TestSilentPortSkippedUntilReplug(t *testing.T) {
	h := startHost(t, nil, nil)
	h.ports.plug("/dev/ttyACM1", "2E8A", nil) // a Pico running other firmware
	eventually(t, "first probe", func() bool { return h.ports.openCount("/dev/ttyACM1") == 1 })
	time.Sleep(400 * time.Millisecond) // many scans
	if n := h.ports.openCount("/dev/ttyACM1"); n != 1 {
		t.Fatalf("silent port probed %d times, want 1 until replugged", n)
	}
	h.ports.unplug("/dev/ttyACM1")
	time.Sleep(100 * time.Millisecond)
	h.ports.plug("/dev/ttyACM1", "2E8A", goalSensor("G2")) // flashed with kicker firmware
	h.online(t, "G2")
}

func TestDuplicateUIDRejected(t *testing.T) {
	h := startHost(t, nil, nil)
	h.ports.plug("/dev/ttyACM0", "2E8A", goalSensor("SAME"))
	h.online(t, "SAME")
	h.ports.plug("/dev/ttyACM1", "2E8A", goalSensor("SAME"))
	eventually(t, "second probed", func() bool { return h.ports.openCount("/dev/ttyACM1") == 1 })
	time.Sleep(300 * time.Millisecond)
	if m, _ := h.module("SAME"); m.Port != "/dev/ttyACM0" || !m.Online {
		t.Errorf("first module must stay: %+v", m)
	}
	if n := h.ports.openCount("/dev/ttyACM1"); n != 1 {
		t.Errorf("rejected port must be skipped until replugged, opened %d times", n)
	}
	if h.ports.module("/dev/ttyACM1").Ready() {
		t.Error("duplicate must not get ready")
	}
}

func TestRoleValidation(t *testing.T) {
	h := startHost(t, nil, nil)
	h.ports.plug("/dev/ttyACM0", "2E8A", goalSensor("G1"))
	h.ports.plug("/dev/ttyACM1", "2E8A", goalSensor("G2"))
	h.ports.plug("/dev/ttyACM2", "2E8A", displayModule("D1"))
	h.ports.plug("/dev/ttyACM3", "2E8A", displayModule("D2"))
	for _, uid := range []string{"G1", "G2", "D1", "D2"} {
		h.online(t, uid)
	}
	ctx := context.Background()

	cases := []struct {
		uid   string
		roles map[string]string
		want  string
	}{
		{"G1", map[string]string{"c": "home_goal"}, "not announced"},
		{"G1", map[string]string{"a": "home"}, "not valid"},
		{"G1", map[string]string{"a": "home_goal", "b": "home_goal"}, "already assigned"},
	}
	for _, c := range cases {
		err := h.host.Adopt(ctx, c.uid, "", c.roles)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Adopt(%s, %v): got %v, want error containing %q", c.uid, c.roles, err, c.want)
		}
	}
	if err := h.host.Adopt(ctx, "G1", "", bothGoals); err != nil {
		t.Fatal(err)
	}
	if err := h.host.Adopt(ctx, "G2", "", map[string]string{"a": "home_goal"}); err == nil || !strings.Contains(err.Error(), "G1/a") {
		t.Errorf("exclusive role across modules must conflict, got %v", err)
	}
	if err := h.host.Adopt(ctx, "G2", "spare", map[string]string{}); err != nil {
		t.Errorf("adopting without roles must work: %v", err)
	}
	// displays may share a role (mirrors)
	if err := h.host.Adopt(ctx, "D1", "", map[string]string{"screen": "home"}); err != nil {
		t.Fatal(err)
	}
	if err := h.host.Adopt(ctx, "D2", "", map[string]string{"screen": "home"}); err != nil {
		t.Errorf("display mirrors must be allowed: %v", err)
	}
	if err := h.host.Adopt(ctx, "NOPE", "", nil); !errors.Is(err, ErrUnknownModule) {
		t.Errorf("unknown module: %v", err)
	}
}

func TestCommandByRoleReachesMirrors(t *testing.T) {
	h := startHost(t, nil, nil)
	h.ports.plug("/dev/ttyACM2", "2E8A", displayModule("D1"))
	h.ports.plug("/dev/ttyACM3", "2E8A", displayModule("D2"))
	h.online(t, "D1")
	h.online(t, "D2")
	ctx := context.Background()
	for _, uid := range []string{"D1", "D2"} {
		if err := h.host.Adopt(ctx, uid, "", map[string]string{"screen": "home"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.host.Command(ctx, "display", "home", "anim", protocol.DisplayAnim{Name: "goal_for"}); err != nil {
		t.Fatal(err)
	}
	for _, port := range []string{"/dev/ttyACM2", "/dev/ttyACM3"} {
		cmds := h.ports.module(port).Commands()
		if len(cmds) != 1 || cmds[0].Name != "anim" || cmds[0].Instance != "screen" {
			t.Errorf("%s got %+v", port, cmds)
		}
	}
	if err := h.host.Command(ctx, "display", "away", "anim", nil); !errors.Is(err, ErrNoTarget) {
		t.Errorf("no away display: got %v", err)
	}
}

func TestConfigStoredAndResentAfterReconnect(t *testing.T) {
	store := NewMemoryStore()
	h := startHost(t, store, nil)
	h.ports.plug("/dev/ttyACM0", "2E8A", goalSensor("G1"))
	h.online(t, "G1")
	ctx := context.Background()
	if err := h.host.Adopt(ctx, "G1", "", bothGoals); err != nil {
		t.Fatal(err)
	}
	if err := h.host.SetConfig(ctx, "G1", "a", json.RawMessage(`{"lockout_ms":1500}`)); err != nil {
		t.Fatal(err)
	}
	if err := h.host.SetConfig(ctx, "G1", "a", json.RawMessage(`{"min_block_us":500}`)); err != nil {
		t.Fatal(err)
	}
	if err := h.host.SetConfig(ctx, "G1", "a", json.RawMessage(`{"lockout_ms":-1}`)); err == nil {
		t.Error("invalid config must be rejected before it reaches the module")
	}
	if cfg := h.ports.module("/dev/ttyACM0").Config("a"); string(cfg["lockout_ms"]) != "1500" || string(cfg["min_block_us"]) != "500" {
		t.Errorf("module config: %v", cfg)
	}

	// unplug and replug: a fresh module (no config) gets the stored, merged config after ready
	h.ports.unplug("/dev/ttyACM0")
	eventually(t, "offline", func() bool { m, _ := h.module("G1"); return !m.Online })
	h.ports.plug("/dev/ttyACM0", "2E8A", goalSensor("G1"))
	h.online(t, "G1")
	eventually(t, "config resent", func() bool {
		vm := h.ports.module("/dev/ttyACM0")
		cfg := vm.Config("a")
		return vm.Adopted() && string(cfg["lockout_ms"]) == "1500" && string(cfg["min_block_us"]) == "500"
	})

	ads, _ := store.Adoptions()
	if len(ads) != 1 || ads[0].Caps["a"] != "goal_sensor" {
		t.Errorf("stored adoption: %+v", ads)
	}
}

func TestAdoptedOfflineModuleListedAfterRestart(t *testing.T) {
	store := NewMemoryStore()
	_ = store.SaveAdoption(Adoption{UID: "G9", Name: "Old sensors", Roles: bothGoals, Caps: map[string]string{"a": "goal_sensor", "b": "goal_sensor"}})
	h := startHost(t, store, nil)
	m, ok := h.module("G9")
	if !ok || m.Online || !m.Adopted || m.Name != "Old sensors" {
		t.Fatalf("got %+v", m)
	}
	// the offline module still owns its roles
	h.ports.plug("/dev/ttyACM0", "2E8A", goalSensor("G1"))
	h.online(t, "G1")
	if err := h.host.Adopt(context.Background(), "G1", "", bothGoals); err == nil {
		t.Error("roles of an offline adopted module must still be taken")
	}
}

func TestSimulatorAutoAdopt(t *testing.T) {
	vm := virtual.GoalSensorModule("sim-goal")
	h := startHost(t, nil, func(o *Options) {
		o.Ports = nil
		o.Virtual = []*virtual.Module{vm}
		o.AutoAdoptVirtual = true
	})
	eventually(t, "auto adopted", func() bool { m, _ := h.module("sim-goal"); return m.Adopted && m.Online })
	eventually(t, "module told adopted", vm.Adopted)
	m, _ := h.module("sim-goal")
	if !m.Virtual || m.Caps[0].Role != "home_goal" || m.Caps[1].Role != "away_goal" {
		t.Errorf("auto roles: %+v", m)
	}
	_ = vm.Emit("a", "goal", nil)
	eventually(t, "goal event", func() bool {
		ev := h.ev.capEvents()
		return len(ev) == 1 && ev[0].Role == "home_goal"
	})
}

// Each virtual module is adopted once, with free roles: a module announces twice (on boot and
// after hello), and a second auto-adoption must not take its roles away again.
func TestSimulatorAutoAdoptSeveral(t *testing.T) {
	home, away := virtual.NewCoinAcceptor("coins-1", 2), virtual.NewCoinAcceptor("coins-2-away", 2)
	goals := virtual.GoalSensorModule("goals")
	h := startHost(t, nil, func(o *Options) {
		o.Ports = nil
		o.Virtual = []*virtual.Module{goals, home.Module, away.Module}
		o.AutoAdoptVirtual = true
	})
	roles := func() map[string]string {
		out := map[string]string{}
		for _, uid := range []string{"goals", "coins-1", "coins-2-away"} {
			m, _ := h.module(uid)
			for _, c := range m.Caps {
				out[uid+"/"+c.Instance] = c.Role
			}
		}
		return out
	}
	eventually(t, "all adopted", func() bool {
		r := roles()
		return r["goals/a"] != "" && r["goals/b"] != "" && r["coins-1/slot"] != "" && r["coins-2-away/slot"] != ""
	})
	time.Sleep(300 * time.Millisecond) // a second auto-adoption would have happened by now
	r := roles()
	// "away" in the uid wins, whichever module is adopted first
	if r["goals/a"] != "home_goal" || r["goals/b"] != "away_goal" || r["coins-1/slot"] != "home" || r["coins-2-away/slot"] != "away" {
		t.Errorf("roles: %v", r)
	}
}

func TestStatusAndConsole(t *testing.T) {
	h := startHost(t, nil, nil)
	h.ports.plug("/dev/ttyACM0", "2E8A", goalSensor("G1"))
	h.online(t, "G1")
	vm := h.ports.module("/dev/ttyACM0")
	eventually(t, "ready", vm.Ready)
	vm.Status("a", "fault", "beam blocked")
	eventually(t, "status in snapshot", func() bool {
		m, _ := h.module("G1")
		return m.Caps[0].Status == "fault" && m.Caps[0].StatusMsg == "beam blocked"
	})

	for i := 0; i < 600; i++ {
		vm.Log("i", "line")
	}
	eventually(t, "console capped at 500", func() bool { return len(h.host.Console("G1")) == consoleSize })
	lines := h.host.Console("G1")
	if lines[0].UID != "G1" || lines[len(lines)-1].Line.Text != `{"t":"log","l":"i","m":"line"}` {
		t.Errorf("console content: first %+v last %+v", lines[0], lines[len(lines)-1])
	}
}

// Sleep and wake reach every adopted module; a module that comes online while the table sleeps
// is sent to sleep too (spec 01 §9).
func TestSleepWake(t *testing.T) {
	a, b := virtual.GoalSensorModule("goals"), virtual.NewCoinAcceptor("coins-home", 2)
	h := startHost(t, nil, func(o *Options) {
		o.Ports = nil
		o.Virtual = []*virtual.Module{a, b.Module}
		o.AutoAdoptVirtual = true
	})
	eventually(t, "adopted", func() bool {
		m1, _ := h.module("goals")
		m2, _ := h.module("coins-home")
		return m1.Adopted && m2.Adopted && m1.Online && m2.Online
	})
	h.host.SetAsleep(true)
	eventually(t, "asleep", func() bool { return a.Asleep() && b.Asleep() })
	if !h.host.Asleep() {
		t.Error("host state")
	}
	h.host.SetAsleep(false)
	eventually(t, "awake", func() bool { return !a.Asleep() && !b.Asleep() })
}
