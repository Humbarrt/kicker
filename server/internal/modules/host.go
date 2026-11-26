package modules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Humbarrt/kicker/server/internal/modules/session"
	"github.com/Humbarrt/kicker/server/internal/modules/transport"
	"github.com/Humbarrt/kicker/server/internal/modules/virtual"
	"github.com/Humbarrt/kicker/server/internal/protocol"
)

var (
	ErrUnknownModule = errors.New("unknown module")
	ErrOffline       = errors.New("module is offline")
	ErrNoTarget      = errors.New("no online module has this role")
	ErrInvalidRoles  = errors.New("invalid roles")
	ErrInvalidConfig = errors.New("invalid config")
)

// PortSource lists and opens serial ports. SerialPorts() is the real one.
type PortSource interface {
	List() ([]transport.PortInfo, error)
	Open(name string) (io.ReadWriteCloser, error)
}

type serialPorts struct{}

func (serialPorts) List() ([]transport.PortInfo, error)          { return transport.ListSerial() }
func (serialPorts) Open(name string) (io.ReadWriteCloser, error) { return transport.OpenSerial(name) }

// SerialPorts returns the PortSource for this machine's serial ports.
func SerialPorts() PortSource { return serialPorts{} }

// Options configure the host.
type Options struct {
	Store            Store
	Ports            PortSource        // nil = no serial discovery
	Virtual          []*virtual.Module // simulator modules, connected at start
	AutoAdoptVirtual bool              // adopt virtual modules with default roles (simulator)
	ScanInterval     time.Duration     // default 2 s (D7)
	Vendors          map[string]string // USB vendor ids probed automatically; default transport.KnownVendors
	Session          session.Options   // timings; the host sets the hooks and OnEvent
	OnEvent          func(Event)       // called without locks held; must not block for long
	Logger           *slog.Logger
	Now              func() time.Time
}

// Host manages all modules.
type Host struct {
	opts Options
	log  *slog.Logger

	mu        sync.Mutex
	adoptions map[string]Adoption
	modules   map[string]*moduleState       // by uid
	conns     map[string]*connState         // by port
	skipped   map[string]bool               // no valid announce: skipped until the port disappears (replug rule)
	unknown   map[string]transport.PortInfo // unknown vendor: listed, not probed
	forced    map[string]bool               // user asked to probe

	rescan chan struct{}
	wg     sync.WaitGroup
	autoMu sync.Mutex
	asleep bool // the table sleeps: modules that come online get `sleep` (guarded by mu)
}

type moduleState struct {
	uid         string
	port        string
	virtual     bool
	online      bool
	connectedAt time.Time
	announce    *protocol.Announce
	caps        []session.CapInfo
	status      map[string][2]string // instance → status, msg
	console     console
	sess        *session.Session
}

type connState struct {
	port string
	sess *session.Session
	uid  string
	pre  []ConsoleEvent // console lines before the announce
}

// New creates a host and loads the adoptions.
func New(opts Options) (*Host, error) {
	if opts.Store == nil {
		opts.Store = NewMemoryStore()
	}
	if opts.ScanInterval == 0 {
		opts.ScanInterval = 2 * time.Second
	}
	if opts.Vendors == nil {
		opts.Vendors = transport.KnownVendors
	}
	if opts.OnEvent == nil {
		opts.OnEvent = func(Event) {}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	h := &Host{
		opts: opts, log: opts.Logger.With("component", "modules"),
		adoptions: map[string]Adoption{}, modules: map[string]*moduleState{}, conns: map[string]*connState{},
		skipped: map[string]bool{}, unknown: map[string]transport.PortInfo{}, forced: map[string]bool{},
		rescan: make(chan struct{}, 1),
	}
	ads, err := opts.Store.Adoptions()
	if err != nil {
		return nil, fmt.Errorf("load adoptions: %w", err)
	}
	for _, a := range ads {
		h.adoptions[a.UID] = a
	}
	return h, nil
}

// Run connects the virtual modules and scans the serial ports until ctx ends.
func (h *Host) Run(ctx context.Context) error {
	for _, vm := range h.opts.Virtual {
		h.startVirtual(ctx, vm)
	}
	if h.opts.Ports != nil {
		h.scan(ctx)
		ticker := time.NewTicker(h.opts.ScanInterval)
		defer ticker.Stop()
	loop:
		for {
			select {
			case <-ctx.Done():
				break loop
			case <-ticker.C:
				h.scan(ctx)
			case <-h.rescan:
				h.scan(ctx)
			}
		}
	} else {
		<-ctx.Done()
	}
	h.wg.Wait()
	return ctx.Err()
}

// --- discovery ------------------------------------------------------------------------------

func (h *Host) scan(ctx context.Context) {
	ports, err := h.opts.Ports.List()
	if err != nil {
		h.log.Warn("listing serial ports failed", "err", err)
		return
	}
	present := map[string]bool{}
	for _, p := range ports {
		present[p.Name] = true
	}

	h.mu.Lock()
	changed := false
	for name := range h.skipped {
		if !present[name] { // unplugged: probe again when it comes back
			delete(h.skipped, name)
		}
	}
	for name := range h.unknown {
		if !present[name] {
			delete(h.unknown, name)
			changed = true
		}
	}
	var start []string
	for _, p := range ports {
		if _, busy := h.conns[p.Name]; busy || h.skipped[p.Name] {
			continue
		}
		if !h.forced[p.Name] && !(p.USB && h.opts.Vendors[p.VID] != "") {
			if _, known := h.unknown[p.Name]; !known {
				h.unknown[p.Name] = p
				changed = true
			}
			continue
		}
		delete(h.unknown, p.Name)
		start = append(start, p.Name)
	}
	h.mu.Unlock()

	if changed {
		h.opts.OnEvent(ModulesChanged{})
	}
	for _, name := range start {
		rwc, err := h.opts.Ports.Open(name)
		if err != nil {
			h.log.Debug("cannot open port, retrying on the next scan", "port", name, "err", err)
			continue
		}
		h.startConn(ctx, name, rwc, false)
	}
}

func (h *Host) startVirtual(ctx context.Context, vm *virtual.Module) {
	serverEnd, moduleEnd := net.Pipe()
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		go func() { <-ctx.Done(); moduleEnd.Close() }()
		_ = vm.Run(ctx, moduleEnd)
	}()
	h.startConn(ctx, "virtual:"+vm.UID, serverEnd, true)
}

func (h *Host) startConn(ctx context.Context, port string, rwc io.ReadWriteCloser, isVirtual bool) {
	cctx, cancel := context.WithCancel(ctx)
	c := &connState{port: port}

	opts := h.opts.Session
	opts.OnEvent = func(e session.Event) { h.onSessionEvent(c, isVirtual, e) }
	opts.AcceptAnnounce = func(a *protocol.Announce) *protocol.Error { return h.accept(c, a) }
	opts.AdoptedFor = func(a *protocol.Announce) bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		_, ok := h.adoptions[a.UID]
		return ok
	}
	opts.ConfigOnReady = func(a *protocol.Announce) []session.ConfigItem { return h.configFor(a.UID) }
	c.sess = session.New(rwc, opts)

	h.mu.Lock()
	h.conns[port] = c
	h.mu.Unlock()

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		go func() { <-cctx.Done(); rwc.Close() }()
		err := c.sess.Run(cctx)
		cancel()
		h.connEnded(c, err)
	}()
}

func (h *Host) connEnded(c *connState, err error) {
	h.mu.Lock()
	delete(h.conns, c.port)
	skip := errors.Is(err, session.ErrNoAnnounce) || errors.Is(err, session.ErrRejected)
	if skip {
		h.skipped[c.port] = true
	}
	if m := h.modules[c.uid]; m != nil && m.port == c.port {
		m.online = false
		m.sess = nil
	}
	h.mu.Unlock()

	switch {
	case skip:
		h.log.Info("port skipped until it is unplugged and reconnected", "port", c.port, "reason", err)
	case c.uid != "":
		h.log.Info("module disconnected", "uid", c.uid, "port", c.port, "err", err)
	}
	h.opts.OnEvent(ModulesChanged{})
}

// accept rejects a module whose uid is already connected on another port (spec 01 §5).
func (h *Host) accept(c *connState, a *protocol.Announce) *protocol.Error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m, ok := h.modules[a.UID]; ok && m.online && m.port != c.port {
		return &protocol.Error{Code: protocol.ErrDuplicateUID, Msg: fmt.Sprintf("uid %s is already connected on %s", a.UID, m.port)}
	}
	return nil
}

func (h *Host) configFor(uid string) []session.ConfigItem {
	h.mu.Lock()
	defer h.mu.Unlock()
	ad, ok := h.adoptions[uid]
	if !ok {
		return nil
	}
	instances := make([]string, 0, len(ad.Config))
	for inst := range ad.Config {
		instances = append(instances, inst)
	}
	sort.Strings(instances)
	items := make([]session.ConfigItem, 0, len(instances))
	for _, inst := range instances {
		items = append(items, session.ConfigItem{Instance: inst, Data: ad.Config[inst]})
	}
	return items
}

// --- session events ---------------------------------------------------------------------------

func (h *Host) onSessionEvent(c *connState, isVirtual bool, e session.Event) {
	switch ev := e.(type) {
	case session.ConsoleLine:
		h.mu.Lock()
		ce := ConsoleEvent{UID: c.uid, Port: c.port, Line: ev}
		if m := h.modules[c.uid]; m != nil {
			m.console.add(ce)
		} else if len(c.pre) < 50 {
			c.pre = append(c.pre, ce)
		}
		h.mu.Unlock()
		h.opts.OnEvent(ce)

	case session.Announced:
		uid := ev.Announce.UID
		h.mu.Lock()
		m := h.modules[uid]
		if m == nil {
			m = &moduleState{uid: uid, status: map[string][2]string{}}
			h.modules[uid] = m
		}
		fresh := ev.Rebooted || m.port != c.port || !m.online
		if fresh {
			m.connectedAt = h.opts.Now()
			m.status = map[string][2]string{}
		}
		m.port, m.virtual, m.online, m.sess = c.port, isVirtual, true, c.sess
		m.announce, m.caps = ev.Announce, ev.Caps
		c.uid = uid
		for _, ce := range c.pre {
			ce.UID = uid
			m.console.add(ce)
		}
		c.pre = nil
		_, adopted := h.adoptions[uid]
		h.mu.Unlock()
		if fresh {
			h.log.Info("module connected", "uid", uid, "port", c.port, "manifest", ev.Announce.MF, "adopted", adopted)
		}
		h.opts.OnEvent(ModulesChanged{})
		if isVirtual && h.opts.AutoAdoptVirtual && !adopted {
			go h.autoAdopt(uid)
		}

	case session.Online:
		h.mu.Lock()
		var sess *session.Session
		if m := h.modules[c.uid]; m != nil && m.port == c.port {
			m.online = ev.Online
			if _, adopted := h.adoptions[c.uid]; ev.Online && adopted && h.asleep {
				sess = m.sess
			}
		}
		h.mu.Unlock()
		if sess != nil {
			go h.power(sess, c.uid, protocol.TSleep)
		}
		h.opts.OnEvent(ModulesChanged{})

	case session.ModuleEvent:
		h.mu.Lock()
		capID, role, adopted := h.instanceInfo(c.uid, ev.Instance)
		h.mu.Unlock()
		h.opts.OnEvent(CapabilityEvent{
			UID: c.uid, Instance: ev.Instance, Capability: capID, Role: role, Adopted: adopted,
			Name: ev.Name, At: ev.At, Data: ev.Data, Raw: ev.Raw,
		})

	case session.StatusChanged:
		h.mu.Lock()
		if m := h.modules[c.uid]; m != nil {
			m.status[ev.Instance] = [2]string{ev.Status, ev.Msg}
		}
		capID, role, _ := h.instanceInfo(c.uid, ev.Instance)
		h.mu.Unlock()
		h.opts.OnEvent(StatusEvent{UID: c.uid, Instance: ev.Instance, Capability: capID, Role: role, Status: ev.Status, Msg: ev.Msg})
		h.opts.OnEvent(ModulesChanged{})
	}
}

// instanceInfo returns capability id and role of an instance. Caller holds h.mu.
func (h *Host) instanceInfo(uid, instance string) (capID, role string, adopted bool) {
	if m := h.modules[uid]; m != nil {
		for _, c := range m.caps {
			if c.Instance == instance {
				capID = c.ID
			}
		}
	}
	if ad, ok := h.adoptions[uid]; ok {
		return capID, ad.Roles[instance], true
	}
	return capID, "", false
}

// --- adoption and roles ----------------------------------------------------------------------

// RolesFor returns the roles an instance of a catalog capability can get. Capabilities without
// roles in the catalog (one per table, e.g. ball_dispenser) get one implicit role: their id.
func RolesFor(c *protocol.Capability) []string {
	if c == nil {
		return nil
	}
	if len(c.Roles) == 0 {
		return []string{c.ID}
	}
	return c.Roles
}

// Adopt adopts a module (or changes its name and roles). roles maps instance → role; instances
// without a role are not used in games. The module gets `ready{adopted:true}` and its config.
func (h *Host) Adopt(ctx context.Context, uid, name string, roles map[string]string) error {
	h.mu.Lock()
	m := h.modules[uid]
	if m == nil || m.announce == nil {
		h.mu.Unlock()
		return ErrUnknownModule
	}
	if err := h.validateRoles(uid, m, roles); err != nil {
		h.mu.Unlock()
		return err
	}
	ad, existed := h.adoptions[uid]
	ad.UID = uid
	switch {
	case name != "":
		ad.Name = name
	case ad.Name == "":
		ad.Name = m.announce.MF
	}
	ad.Roles = map[string]string{}
	for inst, role := range roles {
		if role != "" {
			ad.Roles[inst] = role
		}
	}
	ad.Caps = map[string]string{}
	for _, c := range m.caps {
		ad.Caps[c.Instance] = c.ID
	}
	if ad.Config == nil {
		ad.Config = map[string]json.RawMessage{}
	}
	if !existed {
		ad.AdoptedAt = h.opts.Now()
	}
	if err := h.opts.Store.SaveAdoption(ad); err != nil {
		h.mu.Unlock()
		return err
	}
	h.adoptions[uid] = ad
	sess := m.sess
	h.mu.Unlock()

	h.opts.OnEvent(ModulesChanged{})
	if sess != nil {
		return sess.Resync(ctx)
	}
	return nil
}

// Release forgets a module's adoption (roles and config). It stays connected, unadopted.
func (h *Host) Release(ctx context.Context, uid string) error {
	h.mu.Lock()
	if _, ok := h.adoptions[uid]; !ok {
		h.mu.Unlock()
		return ErrUnknownModule
	}
	if err := h.opts.Store.DeleteAdoption(uid); err != nil {
		h.mu.Unlock()
		return err
	}
	delete(h.adoptions, uid)
	var sess *session.Session
	if m := h.modules[uid]; m != nil {
		sess = m.sess
	}
	h.mu.Unlock()
	h.opts.OnEvent(ModulesChanged{})
	if sess != nil {
		return sess.Resync(ctx)
	}
	return nil
}

// validateRoles checks instance names, roles and – for exclusive roles – conflicts with other
// instances. Caller holds h.mu.
func (h *Host) validateRoles(uid string, m *moduleState, roles map[string]string) error {
	taken := map[[2]string]string{} // (capability, role) → "uid/instance"
	for otherUID, ad := range h.adoptions {
		if otherUID == uid {
			continue
		}
		for inst, role := range ad.Roles {
			if capID := ad.Caps[inst]; capID != "" {
				taken[[2]string{capID, role}] = otherUID + "/" + inst
			}
		}
	}
	for inst, role := range roles {
		var info *session.CapInfo
		for i := range m.caps {
			if m.caps[i].Instance == inst {
				info = &m.caps[i]
			}
		}
		if info == nil {
			return fmt.Errorf("%w: instance %q is not announced by this module", ErrInvalidRoles, inst)
		}
		if role == "" {
			continue
		}
		if !info.Supported || info.Catalog == nil {
			return fmt.Errorf("%w: instance %q (%s@%d) is not a supported catalog capability and can't get a role", ErrInvalidRoles, inst, info.ID, info.Version)
		}
		if !slices.Contains(RolesFor(info.Catalog), role) {
			return fmt.Errorf("%w: role %q is not valid for %s, use one of %v", ErrInvalidRoles, role, info.Catalog.Key(), RolesFor(info.Catalog))
		}
		if info.Catalog.SharedRoles {
			continue
		}
		key := [2]string{info.ID, role}
		if other, ok := taken[key]; ok {
			return fmt.Errorf("%w: role %q is already assigned to %s", ErrInvalidRoles, role, other)
		}
		taken[key] = uid + "/" + inst
	}
	return nil
}

func (h *Host) autoAdopt(uid string) {
	// One at a time, so two virtual modules don't pick the same free role.
	h.autoMu.Lock()
	defer h.autoMu.Unlock()
	h.mu.Lock()
	m := h.modules[uid]
	if _, done := h.adoptions[uid]; m == nil || done {
		// A module announces on boot and again after `hello`: only the first adopts.
		h.mu.Unlock()
		return
	}
	roles := map[string]string{}
	used := map[[2]string]bool{}
	for _, ad := range h.adoptions {
		for inst, role := range ad.Roles {
			used[[2]string{ad.Caps[inst], role}] = true
		}
	}
	for _, c := range m.caps {
		if c.Catalog == nil || !c.Supported {
			continue
		}
		// A role named in the uid comes first ("sim-coins-away" → away), so the simulator's
		// modules always get the same roles.
		candidates := RolesFor(c.Catalog)
		for _, role := range candidates {
			if strings.Contains(uid, role) {
				candidates = append([]string{role}, candidates...)
				break
			}
		}
		for _, role := range candidates {
			key := [2]string{c.ID, role}
			if c.Catalog.SharedRoles || !used[key] {
				roles[c.Instance] = role
				used[key] = true
				break
			}
		}
	}
	name := "Simulator " + uid
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.Adopt(ctx, uid, name, roles); err != nil {
		h.log.Warn("auto-adopting virtual module failed", "uid", uid, "err", err)
	}
}

// --- config, commands -------------------------------------------------------------------------

// SetConfig validates a partial config for one instance, stores it merged with the existing
// config and sends it to the module if it is online (spec 01 §4).
func (h *Host) SetConfig(ctx context.Context, uid, instance string, partial json.RawMessage) error {
	h.mu.Lock()
	ad, ok := h.adoptions[uid]
	m := h.modules[uid]
	if !ok || m == nil {
		h.mu.Unlock()
		return ErrUnknownModule
	}
	if instance != "" {
		for _, c := range m.caps {
			if c.Instance == instance && c.Catalog != nil {
				if _, _, err := c.Catalog.DecodeConfig(partial); err != nil {
					h.mu.Unlock()
					return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
				}
			}
		}
	}
	merged, err := mergeJSON(ad.Config[instance], partial)
	if err != nil {
		h.mu.Unlock()
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	ad = cloneAdoption(ad)
	ad.Config[instance] = merged
	if err := h.opts.Store.SaveAdoption(ad); err != nil {
		h.mu.Unlock()
		return err
	}
	h.adoptions[uid] = ad
	sess := m.sess
	h.mu.Unlock()

	if sess == nil {
		return nil // sent with the full config after the next ready
	}
	res, err := sess.Config(ctx, instance, partial)
	if err != nil {
		return err
	}
	if !res.OK {
		return fmt.Errorf("module refused the config: %s %s", res.Code, res.Msg)
	}
	return nil
}

func mergeJSON(base, partial json.RawMessage) (json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &out); err != nil {
			return nil, err
		}
	}
	var add map[string]json.RawMessage
	if err := json.Unmarshal(partial, &add); err != nil {
		return nil, fmt.Errorf("config must be a JSON object: %w", err)
	}
	for k, v := range add {
		out[k] = v
	}
	return json.Marshal(out)
}

// Identify makes a module blink / beep for a few seconds.
func (h *Host) Identify(ctx context.Context, uid string, seconds int) error {
	sess, err := h.session(uid)
	if err != nil {
		return err
	}
	res, err := sess.Command(ctx, func(id uint32) protocol.Message {
		return &protocol.Identify{T: protocol.TIdentify, ID: id, D: protocol.IdentifyArgs{S: seconds}}
	})
	if err == nil && !res.OK {
		err = fmt.Errorf("%s %s", res.Code, res.Msg)
	}
	return err
}

// EnterBootloader sends `update`: the module acks and reboots into its bootloader (spec 01 §9).
func (h *Host) EnterBootloader(ctx context.Context, uid string) error {
	sess, err := h.session(uid)
	if err != nil {
		return err
	}
	res, err := sess.Command(ctx, func(id uint32) protocol.Message { return &protocol.Simple{T: protocol.TUpdate, ID: id} })
	if err == nil && !res.OK {
		err = fmt.Errorf("%s %s", res.Code, res.Msg)
	}
	return err
}

// VirtualModule returns a virtual module by uid (the simulator's firmware updates), or nil.
func (h *Host) VirtualModule(uid string) *virtual.Module {
	for _, vm := range h.opts.Virtual {
		if vm.UID == uid {
			return vm
		}
	}
	return nil
}

// SetAsleep sends `sleep` or `wake` to every online adopted module (spec 01 §9). While the table
// sleeps, modules that come online get `sleep` too. Errors are logged, not returned: a module
// that misses it is still reachable and gets the next one.
func (h *Host) SetAsleep(asleep bool) {
	typ := protocol.TWake
	if asleep {
		typ = protocol.TSleep
	}
	h.mu.Lock()
	h.asleep = asleep
	type target struct {
		uid  string
		sess *session.Session
	}
	var targets []target
	for uid := range h.adoptions {
		if m := h.modules[uid]; m != nil && m.online && m.sess != nil {
			targets = append(targets, target{uid, m.sess})
		}
	}
	h.mu.Unlock()
	for _, t := range targets {
		go h.power(t.sess, t.uid, typ)
	}
}

func (h *Host) power(sess *session.Session, uid string, typ protocol.Type) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.Command(ctx, func(id uint32) protocol.Message { return &protocol.Simple{T: typ, ID: id} })
	if err == nil && !res.OK {
		err = fmt.Errorf("%s %s", res.Code, res.Msg)
	}
	if err != nil {
		h.log.Warn("module power command failed", "uid", uid, "cmd", typ, "err", err)
	}
}

// Asleep reports whether the table sleeps (as last set by SetAsleep).
func (h *Host) Asleep() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.asleep
}

// Command sends a capability command to every online instance with this capability and role
// (e.g. both mirrors of the home display). It fails if there is none.
func (h *Host) Command(ctx context.Context, capability, role, name string, data any) error {
	type target struct {
		sess     *session.Session
		instance string
	}
	var targets []target
	h.mu.Lock()
	for uid, ad := range h.adoptions {
		m := h.modules[uid]
		if m == nil || !m.online || m.sess == nil {
			continue
		}
		for inst, r := range ad.Roles {
			if r == role && ad.Caps[inst] == capability {
				targets = append(targets, target{m.sess, inst})
			}
		}
	}
	h.mu.Unlock()
	if len(targets) == 0 {
		return fmt.Errorf("%w: %s %s", ErrNoTarget, capability, role)
	}
	var errs []error
	for _, t := range targets {
		res, err := t.sess.Cmd(ctx, t.instance, name, data)
		switch {
		case err != nil:
			errs = append(errs, err)
		case !res.OK:
			errs = append(errs, fmt.Errorf("%s: %s %s", t.instance, res.Code, res.Msg))
		}
	}
	return errors.Join(errs...)
}

func (h *Host) session(uid string) (*session.Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	m := h.modules[uid]
	if m == nil {
		return nil, ErrUnknownModule
	}
	if !m.online || m.sess == nil {
		return nil, ErrOffline
	}
	return m.sess, nil
}

// Probe asks the host to probe a port with an unknown USB vendor (D7).
func (h *Host) Probe(port string) {
	h.mu.Lock()
	h.forced[port] = true
	delete(h.skipped, port)
	delete(h.unknown, port)
	h.mu.Unlock()
	select {
	case h.rescan <- struct{}{}:
	default:
	}
}

// --- snapshots ----------------------------------------------------------------------------------

// Modules returns all known modules: connected ones and adopted ones that are offline.
func (h *Host) Modules() []ModuleInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []ModuleInfo
	seen := map[string]bool{}
	for uid, m := range h.modules {
		seen[uid] = true
		ad, adopted := h.adoptions[uid]
		info := ModuleInfo{
			UID: uid, Name: ad.Name, Port: m.port, Online: m.online, Adopted: adopted, Virtual: m.virtual,
			ConnectedAt: m.connectedAt,
		}
		if a := m.announce; a != nil {
			info.Manifest, info.Firmware, info.FWVersion, info.Hardware, info.Update = a.MF, a.FW.Name, a.FW.Ver, a.HW, a.Upd
		}
		if !m.virtual {
			info.USBPath = transport.USBPath(m.port)
		}
		if info.Name == "" {
			info.Name = info.Manifest
		}
		for _, c := range m.caps {
			view := CapView{
				Instance: c.Instance, Capability: fmt.Sprintf("%s@%d", c.ID, c.Version),
				Supported: c.Supported, CanWake: c.CanWake, Roles: RolesFor(c.Catalog),
			}
			if adopted {
				view.Role = ad.Roles[c.Instance]
				view.Config = ad.Config[c.Instance]
			}
			if st, ok := m.status[c.Instance]; ok {
				view.Status, view.StatusMsg = st[0], st[1]
			}
			info.Caps = append(info.Caps, view)
		}
		out = append(out, info)
	}
	for uid, ad := range h.adoptions { // adopted but not seen since the server started
		if !seen[uid] {
			out = append(out, ModuleInfo{UID: uid, Name: ad.Name, Adopted: true})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].UID < out[j].UID
	})
	return out
}

// Console returns the last console lines of a module (oldest first).
func (h *Host) Console(uid string) []ConsoleEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m := h.modules[uid]; m != nil {
		return m.console.snapshot()
	}
	return nil
}

// UnknownPorts returns serial ports with an unknown USB vendor that weren't probed (D7).
func (h *Host) UnknownPorts() []transport.PortInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]transport.PortInfo, 0, len(h.unknown))
	for _, p := range h.unknown {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
