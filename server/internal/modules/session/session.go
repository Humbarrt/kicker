// Package session runs the server side of the protocol with one module over one connection
// (spec 01 §6–7): hello/announce, ready, heartbeat with clock offset, event acks and
// deduplication, payload validation against the capability catalog, and commands with
// retries. It is used by the module host and by `kicker monitor`.
package session

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

// ErrNoAnnounce means the module didn't answer `hello` in time: the port is skipped until replugged.
var ErrNoAnnounce = errors.New("no valid announce within the handshake timeout")

// ErrClosed is returned by Command after the session ended.
var ErrClosed = errors.New("session closed")

// ErrRejected is returned by Run when AcceptAnnounce refused the module (e.g. duplicate uid).
var ErrRejected = errors.New("module rejected")

// Options configure a session. Zero values use the spec defaults.
type Options struct {
	Adopted      bool             // sent in `ready` (when AdoptedFor is nil)
	HelloTimeout time.Duration    // default 500 ms
	PingInterval time.Duration    // default 1 s
	MissedPings  int              // pongs missed before offline, default 3
	CommandWait  time.Duration    // wait for ack/nack, default 500 ms
	CommandTries int              // total sends of a command, default 3
	ErrRateLimit int              // err replies per second, default 5
	Now          func() time.Time // clock, default time.Now
	OnEvent      func(Event)      // called from the session goroutine; must not block for long

	// Optional hooks, evaluated on every announce:
	AcceptAnnounce func(*protocol.Announce) *protocol.Error // non-nil = reject (err sent, Run returns ErrRejected)
	AdoptedFor     func(*protocol.Announce) bool            // overrides Adopted
	ConfigOnReady  func(*protocol.Announce) []ConfigItem    // full config sent after every ready (spec 01 §4)
}

// ConfigItem is one (partial) config message for an instance ("" = whole module).
type ConfigItem struct {
	Instance string
	Data     any
}

func (o *Options) defaults() {
	if o.HelloTimeout == 0 {
		o.HelloTimeout = 500 * time.Millisecond
	}
	if o.PingInterval == 0 {
		o.PingInterval = time.Second
	}
	if o.MissedPings == 0 {
		o.MissedPings = 3
	}
	if o.CommandWait == 0 {
		o.CommandWait = 500 * time.Millisecond
	}
	if o.CommandTries == 0 {
		o.CommandTries = 3
	}
	if o.ErrRateLimit == 0 {
		o.ErrRateLimit = 5
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.OnEvent == nil {
		o.OnEvent = func(Event) {}
	}
}

// --- events emitted by a session ------------------------------------------------

// Event is anything a session reports. Use a type switch.
type Event interface{ isEvent() }

// Direction of a console line.
type Direction string

const (
	In  Direction = "in"  // module → server
	Out Direction = "out" // server → module
)

// ConsoleLine is one raw line for the module console (D8), with its validation result.
type ConsoleLine struct {
	At       time.Time
	Dir      Direction
	Kind     protocol.LineKind
	Text     string
	Err      string   // validation error, if any
	Warnings []string // e.g. unknown fields
}

// Announced is emitted on every announce. Rebooted is true for the first announce and whenever
// the module restarted (its uptime went back); a repeated announce of a running module –
// e.g. the reply to `hello` after the boot announce – has Rebooted=false.
type Announced struct {
	Announce *protocol.Announce
	Caps     []CapInfo
	Rebooted bool
}

// CapInfo is one announced capability instance, resolved against the catalog.
type CapInfo struct {
	Instance  string
	ID        string
	Version   int
	CanWake   bool
	Params    any                  // decoded params, nil if none or unknown capability
	Catalog   *protocol.Capability // nil for third-party / unsupported versions
	Supported bool
}

// ModuleEvent is a valid, deduplicated `evt`.
type ModuleEvent struct {
	Instance string
	Name     string
	N        uint32
	ModuleTS int64     // µs since module boot
	At       time.Time // server time, corrected with the clock offset
	Data     any       // decoded payload (catalog type) or nil
	Raw      json.RawMessage
}

// StatusChanged is a `status` message.
type StatusChanged struct {
	Instance string
	Status   string
	Msg      string
}

// LogLine is a `log` message from the module.
type LogLine struct {
	Level   string
	Message string
}

// Online reports heartbeat state changes (3 missed pongs → offline).
type Online struct{ Online bool }

func (ConsoleLine) isEvent()   {}
func (Announced) isEvent()     {}
func (ModuleEvent) isEvent()   {}
func (StatusChanged) isEvent() {}
func (LogLine) isEvent()       {}
func (Online) isEvent()        {}

// --- commands -----------------------------------------------------------------------

// Result is the module's answer to a command.
type Result struct {
	OK   bool
	Code protocol.NackCode // set when !OK
	Msg  string
}

type pendingCmd struct {
	id    uint32
	line  []byte
	tries int
	sent  time.Time
	done  chan cmdOutcome
}

type cmdOutcome struct {
	res Result
	err error
}

type cmdRequest struct {
	build func(id uint32) protocol.Message
	done  chan cmdOutcome
}

// --- session ------------------------------------------------------------------------

// Session is one module conversation. Create with New, then call Run.
type Session struct {
	rw   io.ReadWriter
	opts Options

	requests chan cmdRequest
	do       chan func()
	closed   chan struct{}
	once     sync.Once

	// state, owned by the Run goroutine
	announce  *protocol.Announce
	rejected  error
	moduleTS  int64 // latest module time seen, to detect reboots
	caps      map[string]CapInfo
	window    *protocol.Window
	nextID    uint32
	pending   map[uint32]*pendingCmd
	pings     map[uint32]time.Time
	missed    int
	online    bool
	clock     clockSync
	errTokens int
	errReset  time.Time

	mu        sync.RWMutex // protects the snapshot fields below
	snapshot  *protocol.Announce
	offsetVal time.Duration
	offsetOK  bool
}

// New prepares a session on a connection.
func New(rw io.ReadWriter, opts Options) *Session {
	opts.defaults()
	return &Session{
		rw:       rw,
		opts:     opts,
		requests: make(chan cmdRequest),
		do:       make(chan func()),
		closed:   make(chan struct{}),
		pending:  map[uint32]*pendingCmd{},
		pings:    map[uint32]time.Time{},
		nextID:   1,
	}
}

// Announce returns the module's last announce (nil before the handshake).
func (s *Session) Announce() *protocol.Announce {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

// ClockOffset returns server time − module time, from the best ping round trip.
func (s *Session) ClockOffset() (time.Duration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.offsetVal, s.offsetOK
}

type lineOrErr struct {
	line protocol.Line
	err  error
}

// Run performs the handshake and then serves the module until ctx ends or the connection
// fails. It returns ErrNoAnnounce if the module doesn't answer `hello` in time.
func (s *Session) Run(ctx context.Context) error {
	defer s.once.Do(func() { close(s.closed) })

	lines := make(chan lineOrErr, 16)
	go func() {
		r := protocol.NewReader(s.rw)
		for {
			l, err := r.Next()
			select {
			case lines <- lineOrErr{l, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	if err := s.handshake(ctx, lines); err != nil {
		return err
	}

	ticker := time.NewTicker(s.opts.PingInterval / 4) // fine-grained enough for command retries
	defer ticker.Stop()
	s.ping() // first ping right away, so the clock offset is known before the first goal
	lastPing := s.opts.Now()

	for {
		select {
		case <-ctx.Done():
			s.failPending(ErrClosed)
			return ctx.Err()
		case le := <-lines:
			if le.err != nil {
				s.failPending(ErrClosed)
				return le.err
			}
			s.handleLine(le.line)
			if s.rejected != nil {
				s.failPending(ErrClosed)
				return s.rejected
			}
		case fn := <-s.do:
			fn()
		case req := <-s.requests:
			s.startCommand(req)
		case <-ticker.C:
			now := s.opts.Now()
			if now.Sub(lastPing) >= s.opts.PingInterval {
				lastPing = now
				s.ping()
			}
			s.retryCommands(now)
		}
	}
}

func (s *Session) handshake(ctx context.Context, lines <-chan lineOrErr) error {
	s.send(&protocol.Hello{T: protocol.THello, V: protocol.Version, VMin: protocol.Version, ST: s.opts.Now().UnixMicro()})
	deadline := time.NewTimer(s.opts.HelloTimeout)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return ErrNoAnnounce
		case le := <-lines:
			if le.err != nil {
				return le.err
			}
			s.handleLine(le.line)
			if s.rejected != nil {
				return s.rejected
			}
			if s.announce != nil {
				return nil
			}
		}
	}
}

// --- incoming -----------------------------------------------------------------------

func (s *Session) handleLine(l protocol.Line) {
	now := s.opts.Now()
	cl := ConsoleLine{At: now, Dir: In, Kind: l.Kind, Text: string(l.Data)}
	switch l.Kind {
	case protocol.LineRaw:
		s.opts.OnEvent(cl)
		return
	case protocol.LineTooLong:
		cl.Err = "line longer than 1024 bytes, dropped"
		s.opts.OnEvent(cl)
		s.sendErr(&protocol.Error{Code: protocol.ErrTooLong, Msg: cl.Err})
		return
	}

	msg, warnings, err := protocol.DecodeFrom(protocol.ModuleToServer, l.Data)
	cl.Warnings = warnings
	if err != nil {
		cl.Err = err.Error()
		s.opts.OnEvent(cl)
		var perr *protocol.Error
		if errors.As(err, &perr) {
			s.sendErr(perr)
		}
		return
	}

	switch m := msg.(type) {
	case *protocol.Announce:
		s.opts.OnEvent(cl)
		s.handleAnnounce(m)
	case *protocol.Pong:
		s.opts.OnEvent(cl)
		s.handlePong(m, now)
	case *protocol.Evt:
		s.handleEvt(m, cl)
	case *protocol.Ack:
		s.opts.OnEvent(cl)
		s.finishCommand(*m.ID, Result{OK: true})
	case *protocol.Nack:
		s.opts.OnEvent(cl)
		s.finishCommand(m.ID, Result{Code: m.Code, Msg: m.Msg})
	case *protocol.Status:
		s.opts.OnEvent(cl)
		s.opts.OnEvent(StatusChanged{Instance: m.I, Status: m.S, Msg: m.Msg})
	case *protocol.Log:
		s.opts.OnEvent(cl)
		s.opts.OnEvent(LogLine{Level: m.L, Message: m.M})
	case *protocol.Err:
		s.opts.OnEvent(cl)
		if m.Ref != nil {
			s.finishCommand(*m.Ref, Result{Code: protocol.NackCode(m.Code), Msg: m.Msg})
		}
	default:
		s.opts.OnEvent(cl)
	}
}

func (s *Session) handleAnnounce(a *protocol.Announce) {
	if a.V != protocol.Version {
		s.sendErr(&protocol.Error{Code: protocol.ErrProtoUnsupported, Msg: fmt.Sprintf("server speaks protocol v%d", protocol.Version)})
		return
	}
	if s.opts.AcceptAnnounce != nil {
		if perr := s.opts.AcceptAnnounce(a); perr != nil {
			s.errTokens = max(s.errTokens, 1)
			s.send(perr.Message())
			s.rejected = fmt.Errorf("%w: %s", ErrRejected, perr.Msg)
			return
		}
	}
	// A reboot (new uid, or the module's uptime went back) starts a new module session:
	// fresh dedup window and clock. A repeated announce of the same running module keeps
	// them, so retransmitted events are still recognised as duplicates.
	rebooted := s.announce == nil || a.UID != s.announce.UID || a.TS < s.moduleTS
	s.announce = a
	s.moduleTS = a.TS
	if rebooted {
		s.window = protocol.NewWindow(64)
		s.clock = clockSync{}
	}
	s.caps = map[string]CapInfo{}
	infos := make([]CapInfo, 0, len(a.Caps))
	for _, c := range a.Caps {
		info := CapInfo{Instance: c.I, ID: c.C, Version: c.CV, CanWake: c.W == 1}
		if cat, ok := protocol.Lookup(c.C, c.CV); ok {
			info.Catalog = cat
			info.Supported = true
			if cat.Params != nil {
				params, warnings, err := cat.DecodeParams(c.P)
				if err != nil {
					s.opts.OnEvent(LogLine{Level: "e", Message: fmt.Sprintf("%s (%s): invalid params: %v", c.I, cat.Key(), err)})
					info.Supported = false
				} else {
					info.Params = params
					for _, w := range warnings {
						s.opts.OnEvent(LogLine{Level: "w", Message: fmt.Sprintf("%s params: %s", c.I, w)})
					}
				}
			}
		}
		s.caps[c.I] = info
		infos = append(infos, info)
	}
	s.mu.Lock()
	s.snapshot = a
	if rebooted {
		s.offsetOK = false
	}
	s.mu.Unlock()

	s.opts.OnEvent(Announced{Announce: a, Caps: infos, Rebooted: rebooted})
	if !s.online {
		s.online = true
		s.missed = 0
		s.opts.OnEvent(Online{Online: true})
	}
	s.sendReady()
}

// sendReady sends `ready` with the current adoption state, followed by the full config.
func (s *Session) sendReady() {
	adopted := s.opts.Adopted
	if s.opts.AdoptedFor != nil {
		adopted = s.opts.AdoptedFor(s.announce)
	}
	sid := fmt.Sprintf("%08x", uint32(s.opts.Now().UnixNano()))
	s.send(&protocol.Ready{T: protocol.TReady, SID: sid, Adopted: adopted})
	if s.opts.ConfigOnReady != nil {
		for _, item := range s.opts.ConfigOnReady(s.announce) {
			s.sendConfig(item)
		}
	}
}

// Resync sends `ready` (and the full config) again, e.g. after the module was adopted,
// released or its config changed. The module resends unacknowledged events and its state.
func (s *Session) Resync(ctx context.Context) error {
	done := make(chan struct{})
	select {
	case s.do <- func() {
		if s.announce != nil {
			s.sendReady()
		}
		close(done)
	}:
	case <-s.closed:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Config sends a (partial) config to one instance and waits for the answer.
func (s *Session) Config(ctx context.Context, instance string, data any) (Result, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Result{}, err
	}
	return s.Command(ctx, func(id uint32) protocol.Message {
		return &protocol.Cfg{T: protocol.TCfg, ID: id, I: instance, D: raw}
	})
}

func (s *Session) handlePong(p *protocol.Pong, now time.Time) {
	sent, ok := s.pings[p.ID]
	if !ok {
		return
	}
	delete(s.pings, p.ID)
	s.missed = 0
	s.moduleTS = max(s.moduleTS, p.TS)
	if !s.online {
		s.online = true
		s.opts.OnEvent(Online{Online: true})
	}
	offset := s.clock.add(sent, now, p.TS)
	s.mu.Lock()
	s.offsetVal, s.offsetOK = offset, true
	s.mu.Unlock()
}

func (s *Session) handleEvt(e *protocol.Evt, cl ConsoleLine) {
	n := e.N
	s.moduleTS = max(s.moduleTS, e.TS)
	// Always acknowledge, also duplicates and invalid events, so the module stops resending.
	defer s.send(&protocol.Ack{T: protocol.TAck, N: &n})

	if s.window == nil {
		cl.Err = "event before announce"
		s.opts.OnEvent(cl)
		return
	}
	if s.window.Seen(n) {
		cl.Warnings = append(cl.Warnings, "duplicate (retransmission), ignored")
		s.opts.OnEvent(cl)
		return
	}
	info, ok := s.caps[e.I]
	if !ok {
		perr := &protocol.Error{Code: protocol.ErrUnknownInstance, Msg: fmt.Sprintf("instance %q not announced", e.I), Ref: &n}
		cl.Err = perr.Error()
		s.opts.OnEvent(cl)
		s.sendErr(perr)
		return
	}
	var data any
	if info.Catalog != nil {
		d, warnings, err := info.Catalog.DecodeEvent(e.E, e.D)
		cl.Warnings = append(cl.Warnings, warnings...)
		if err != nil {
			var perr *protocol.Error
			if errors.As(err, &perr) {
				perr.Ref = &n
				s.sendErr(perr)
			}
			cl.Err = err.Error()
			s.opts.OnEvent(cl)
			return
		}
		data = d
	}
	s.opts.OnEvent(cl)
	s.opts.OnEvent(ModuleEvent{
		Instance: e.I, Name: e.E, N: n, ModuleTS: e.TS,
		At: s.serverTime(e.TS), Data: data, Raw: e.D,
	})
}

func (s *Session) serverTime(moduleTS int64) time.Time {
	s.mu.RLock()
	offset, ok := s.offsetVal, s.offsetOK
	s.mu.RUnlock()
	if !ok {
		return s.opts.Now() // no clock sync yet: best effort
	}
	return time.UnixMicro(moduleTS).Add(offset)
}

// --- outgoing -----------------------------------------------------------------------

func (s *Session) send(m protocol.Message) {
	line, err := protocol.Encode(m)
	if err != nil {
		s.opts.OnEvent(ConsoleLine{At: s.opts.Now(), Dir: Out, Kind: protocol.LineJSON, Text: string(m.Type()), Err: err.Error()})
		return
	}
	s.write(line)
}

func (s *Session) write(line []byte) {
	_, err := s.rw.Write(line)
	cl := ConsoleLine{At: s.opts.Now(), Dir: Out, Kind: protocol.LineJSON, Text: string(line[:len(line)-1])}
	if err != nil {
		cl.Err = "write failed: " + err.Error()
	}
	s.opts.OnEvent(cl)
}

// SendRaw writes a line exactly as given (monitor: testing a module's error handling).
func (s *Session) SendRaw(ctx context.Context, line string) error {
	_, err := s.Command(ctx, func(uint32) protocol.Message { return rawMessage(line) })
	return err
}

type rawMessage string

func (rawMessage) Type() protocol.Type { return "raw" }

func (s *Session) sendErr(perr *protocol.Error) {
	now := s.opts.Now()
	if now.Sub(s.errReset) >= time.Second {
		s.errReset, s.errTokens = now, s.opts.ErrRateLimit
	}
	if s.errTokens <= 0 {
		return
	}
	s.errTokens--
	s.send(perr.Message())
}

func (s *Session) ping() {
	if len(s.pings) > 0 {
		s.missed += len(s.pings)
		s.pings = map[uint32]time.Time{}
		if s.online && s.missed >= s.opts.MissedPings {
			s.online = false
			s.opts.OnEvent(Online{Online: false})
		}
	}
	id := s.allocID()
	s.pings[id] = s.opts.Now()
	s.send(&protocol.Ping{T: protocol.TPing, ID: id})
}

func (s *Session) allocID() uint32 {
	id := s.nextID
	s.nextID++ // wraps to 0, compared by equality only
	return id
}

func (s *Session) sendConfig(item ConfigItem) {
	data, err := json.Marshal(item.Data)
	if err != nil {
		s.opts.OnEvent(LogLine{Level: "e", Message: "config: " + err.Error()})
		return
	}
	s.startCommand(cmdRequest{build: func(id uint32) protocol.Message {
		return &protocol.Cfg{T: protocol.TCfg, ID: id, I: item.Instance, D: data}
	}})
}

// Command sends a message that expects ack/nack (cmd, cfg, sleep, wake, identify, reboot, update)
// and waits for the answer, resending with the same id if needed (spec 01 §7).
func (s *Session) Command(ctx context.Context, build func(id uint32) protocol.Message) (Result, error) {
	req := cmdRequest{build: build, done: make(chan cmdOutcome, 1)}
	select {
	case s.requests <- req:
	case <-s.closed:
		return Result{}, ErrClosed
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	select {
	case out := <-req.done:
		return out.res, out.err
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// Cmd is a shortcut for a capability command.
func (s *Session) Cmd(ctx context.Context, instance, name string, data any) (Result, error) {
	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return Result{}, err
		}
		raw = b
	}
	return s.Command(ctx, func(id uint32) protocol.Message {
		return &protocol.Cmd{T: protocol.TCmd, ID: id, I: instance, C: name, D: raw}
	})
}

func (s *Session) startCommand(req cmdRequest) {
	id := s.allocID()
	m := req.build(id)
	var line []byte
	if raw, ok := m.(rawMessage); ok {
		line = append([]byte(raw), '\n')
		s.write(line)
		if req.done != nil {
			req.done <- cmdOutcome{res: Result{OK: true}}
		}
		return
	}
	line, err := protocol.Encode(m)
	if err != nil {
		if req.done != nil {
			req.done <- cmdOutcome{err: err}
		}
		return
	}
	s.pending[id] = &pendingCmd{id: id, line: line, tries: 1, sent: s.opts.Now(), done: req.done}
	s.write(line)
}

func (s *Session) retryCommands(now time.Time) {
	for id, p := range s.pending {
		if now.Sub(p.sent) < s.opts.CommandWait {
			continue
		}
		if p.tries >= s.opts.CommandTries {
			delete(s.pending, id)
			if p.done != nil {
				p.done <- cmdOutcome{err: fmt.Errorf("no answer to command id %d after %d tries", id, p.tries)}
			}
			continue
		}
		p.tries++
		p.sent = now
		s.write(p.line) // same id: the module answers repeats without executing twice
	}
}

func (s *Session) finishCommand(id uint32, res Result) {
	p, ok := s.pending[id]
	if !ok {
		return
	}
	delete(s.pending, id)
	if p.done != nil {
		p.done <- cmdOutcome{res: res}
	}
}

func (s *Session) failPending(err error) {
	for id, p := range s.pending {
		delete(s.pending, id)
		if p.done != nil {
			p.done <- cmdOutcome{err: err}
		}
	}
}

// --- clock sync ---------------------------------------------------------------------

// clockSync keeps the offset of the ping with the lowest round-trip time among the last 16
// (spec 01 §6): offset = (send + recv)/2 − module ts.
type clockSync struct {
	samples [16]clockSample
	n       int
}

type clockSample struct {
	rtt    time.Duration
	offset time.Duration
}

func (c *clockSync) add(sent, recv time.Time, moduleTS int64) time.Duration {
	mid := sent.Add(recv.Sub(sent) / 2)
	sample := clockSample{rtt: recv.Sub(sent), offset: mid.Sub(time.UnixMicro(moduleTS))}
	c.samples[c.n%len(c.samples)] = sample
	c.n++
	best := c.samples[0]
	for i := 1; i < min(c.n, len(c.samples)); i++ {
		if c.samples[i].rtt < best.rtt {
			best = c.samples[i]
		}
	}
	return best.offset
}
