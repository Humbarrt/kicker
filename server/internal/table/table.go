// Package table is the actor around the game engine (spec 02 §2): one goroutine owns the table
// state, feeds module events, API commands and timer ticks through engine.Step, persists the
// resulting events, runs the effects and publishes the new view.
package table

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
)

// TopicTable is the bus topic of View updates.
const TopicTable = "table"

// Options configure the table.
type Options struct {
	Store  *store.Store
	Bus    *bus.Bus // nil = no publishing
	Config engine.Config
	// OnEffect runs module commands (dispenser, displays, …). Nil = effects are dropped.
	OnEffect func(engine.Effect)
	// OnGameFinished is called after a finished game is saved (ratings, stats). It must not block.
	OnGameFinished func(gameID int64)
	// OnCheckIn is called for each tournament player who inserted their coin (spec 09 §8), and
	// OnPhase when the table's phase changes (the tournament pauses when the table goes to sleep).
	// Both run on the table's goroutine: they must not block or call back into the table.
	OnCheckIn func(engine.CheckIn)
	OnPhase   func(from, to engine.Phase)
	Logger    *slog.Logger
	Now       func() time.Time // for tests; default time.Now
}

// Table is the running table. Create with New, then Run.
type Table struct {
	opts  Options
	log   *slog.Logger
	in    chan request
	done  chan struct{} // closed when Run returns
	ready chan struct{} // closed when the state is restored
	view  atomic.Pointer[View]
	// settings are the table settings; written before the input that applies them.
	settings atomic.Pointer[Settings]

	// owned by the Run goroutine
	state  engine.State
	rev    uint64
	paused []PausedGame // the paused games list, newest pause first

	editMu sync.Mutex // EditGame: one edit at a time
}

type request struct {
	in    engine.Input
	reply chan error // nil for module events
}

// ErrNotRunning is returned by Do when the table is not running.
var ErrNotRunning = errors.New("table is not running")

// New creates a table. Nothing happens before Run.
func New(opts Options) *Table {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	t := &Table{opts: opts, log: opts.Logger.With("component", "table"),
		in: make(chan request, 64), done: make(chan struct{}), ready: make(chan struct{})}
	t.view.Store(&View{Phase: string(engine.PhaseIdle)})
	def := DefaultSettings()
	t.settings.Store(&def)
	return t
}

// Run restores the table from the database and processes inputs until ctx ends.
func (t *Table) Run(ctx context.Context) error {
	defer close(t.done)
	st, err := t.restore(ctx)
	if err != nil {
		return fmt.Errorf("restoring the table: %w", err)
	}
	t.state = st
	t.refreshPaused(ctx)
	t.publish(t.opts.Now())
	if g := st.Game; g != nil {
		t.log.Info("game restored", "game", g.ID, "home", g.Home, "away", g.Away)
	}
	if err := t.apply(ctx, engine.Tick{}); err != nil { // a clock may have run out meanwhile
		t.log.Error("tick after restore", "err", err)
	}
	t.abandonOld(ctx) // before Ready: nobody sees a game as paused that is long abandoned
	close(t.ready)

	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	abandon := time.NewTicker(time.Minute) // D16: paused games are given up after abandon_after_hours
	defer abandon.Stop()
	for {
		if dl := t.state.NextDeadline(); dl.IsZero() {
			timer.Stop()
		} else {
			timer.Reset(max(0, dl.Sub(t.opts.Now())))
		}
		select {
		case <-ctx.Done():
			return nil
		case req := <-t.in:
			err := t.apply(ctx, req.in)
			if req.reply != nil {
				req.reply <- err
			} else if err != nil {
				t.log.Error("module input failed", "input", fmt.Sprintf("%T", req.in), "err", err)
			}
		case <-timer.C:
			if err := t.apply(ctx, engine.Tick{}); err != nil {
				t.log.Error("tick failed", "err", err)
			}
		case <-abandon.C:
			t.abandonOld(ctx)
		}
	}
}

// Ready is closed once Run has restored the state.
func (t *Table) Ready() <-chan struct{} { return t.ready }

// View returns the current view, with the game clock brought up to now (the stored view is from
// the last change). Safe from any goroutine.
func (t *Table) View() View {
	v := *t.view.Load()
	now := t.opts.Now().UnixMilli()
	if g := v.Game; g != nil && g.ClockRunning && now > v.ServerTime {
		gc := *g
		gc.PlayTimeMs += now - v.ServerTime
		v.Game = &gc
	}
	if now > v.ServerTime {
		v.ServerTime = now
	}
	return v
}

// Do runs an API command and returns the engine's verdict (engine.ErrNoGame, …).
func (t *Table) Do(ctx context.Context, in engine.Input) error {
	reply := make(chan error, 1)
	select {
	case t.in <- request{in: in, reply: reply}:
	case <-t.done:
		return ErrNotRunning
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-reply:
		return err
	case <-t.done:
		return ErrNotRunning
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Feed queues a module event. It waits at most 2 s when the queue is full, so a stuck table can't
// stall the module host; the event is then dropped and logged.
func (t *Table) Feed(in engine.Input) {
	select {
	case t.in <- request{in: in}:
		return
	default:
	}
	tm := time.NewTimer(2 * time.Second)
	defer tm.Stop()
	select {
	case t.in <- request{in: in}:
	case <-t.done:
	case <-tm.C:
		t.log.Error("table queue full, module event dropped", "input", fmt.Sprintf("%+v", in))
	}
}

// StartRequest starts a game: with a preset (which becomes the sticky current preset), with
// custom rules, or with neither (the current preset).
type StartRequest struct {
	PresetID int64
	Rules    *engine.Rules
	Author   int64
}

// Start resolves the rules and starts a new game (spec 02 §6, §7.4).
func (t *Table) Start(ctx context.Context, req StartRequest) error {
	in := engine.StartGame{Author: req.Author, FirstBall: []engine.Side{engine.Home, engine.Away}[rand.IntN(2)]}
	st := t.opts.Store
	switch {
	case req.Rules != nil:
		in.Rules = *req.Rules
	case req.PresetID != 0:
		p, err := st.Preset(ctx, req.PresetID)
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: preset %d not found", engine.ErrInvalidInput, req.PresetID)
		} else if err != nil {
			return err
		}
		in.Rules, in.PresetID = rulesFromStore(p.Rules), p.ID
	default:
		p, err := t.CurrentPreset(ctx)
		if err != nil {
			return err
		}
		in.Rules, in.PresetID = rulesFromStore(p.Rules), p.ID
	}
	if err := t.Do(ctx, in); err != nil {
		return err
	}
	if req.PresetID != 0 {
		if err := st.SetSetting(ctx, store.SettingCurrentPreset, req.PresetID); err != nil {
			t.log.Warn("saving the current preset", "err", err)
		}
		if err := t.Do(ctx, engine.SetNextGame{Next: engine.NextGame{Rules: in.Rules, PresetID: in.PresetID}}); err != nil {
			t.log.Warn("next game rules", "err", err)
		}
	}
	return nil
}

// Settings returns the table settings.
func (t *Table) Settings() Settings { return *t.settings.Load() }

// SetSettings validates, saves and applies the table settings and the current preset (0 = keep).
func (t *Table) SetSettings(ctx context.Context, s Settings, presetID int64) error {
	s.Normalize()
	if err := s.Validate(); err != nil {
		return err
	}
	st := t.opts.Store
	var preset store.Preset
	if presetID != 0 {
		var err error
		if preset, err = st.Preset(ctx, presetID); errors.Is(err, store.ErrNotFound) {
			return &SettingsError{Field: "current_preset_id"}
		} else if err != nil {
			return err
		}
	}
	if err := st.SetSetting(ctx, SettingKey, s); err != nil {
		return err
	}
	t.settings.Store(&s)
	if err := t.Do(ctx, engine.SetConfig{Config: s.apply(t.opts.Config)}); err != nil {
		return err
	}
	if presetID != 0 {
		if err := st.SetSetting(ctx, store.SettingCurrentPreset, presetID); err != nil {
			return err
		}
		return t.Do(ctx, engine.SetNextGame{Next: engine.NextGame{Rules: rulesFromStore(preset.Rules), PresetID: preset.ID}})
	}
	return nil
}

// ErrInvalidPlayer: the player doesn't exist or is archived.
var ErrInvalidPlayer = errors.New("no such player, or the player is archived")

// Register adds a player by hand (spec 02 §4, `table.players`).
func (t *Table) Register(ctx context.Context, side engine.Side, playerID, author int64) error {
	p, err := t.opts.Store.Player(ctx, playerID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && p.ArchivedAt != nil) {
		return ErrInvalidPlayer
	} else if err != nil {
		return err
	}
	return t.Do(ctx, engine.RegisterPlayer{Side: side, PlayerID: playerID, Author: author})
}

// CurrentPreset returns the sticky preset, or the first one if none is set or it was deleted.
func (t *Table) CurrentPreset(ctx context.Context) (store.Preset, error) {
	st := t.opts.Store
	var id int64
	if ok, err := st.GetSetting(ctx, store.SettingCurrentPreset, &id); err != nil {
		return store.Preset{}, err
	} else if ok {
		if p, err := st.Preset(ctx, id); err == nil {
			return p, nil
		}
	}
	ps, err := st.Presets(ctx)
	if err != nil {
		return store.Preset{}, err
	}
	if len(ps) == 0 {
		return store.Preset{}, errors.New("no rule presets")
	}
	return ps[0], nil
}

// --- inside the Run goroutine ---------------------------------------------------------------

func (t *Table) apply(ctx context.Context, in engine.Input) error {
	now := t.opts.Now()
	prevPhase := t.state.Phase
	next, res, err := engine.Step(t.state, in, now)
	if err != nil {
		return err
	}
	// A goal that has happened must be saved even while the server shuts down.
	if err := t.persist(context.WithoutCancel(ctx), &next, res.Writes); err != nil {
		t.log.Error("saving game events failed; the input is not applied", "err", err)
		return fmt.Errorf("saving: %w", err)
	}
	if res.Registrations {
		// Not fatal: the coin acceptors' state sync corrects the registrations after a restart.
		if err := t.opts.Store.SaveRegistrations(context.WithoutCancel(ctx), regsToStore(next.Regs)); err != nil {
			t.log.Error("saving the registrations failed", "err", err)
		}
	}
	prev := t.state
	t.state = next
	if touchesPaused(res.Writes) || gameID(next.Game) != gameID(prev.Game) {
		t.refreshPaused(ctx)
	}
	for _, e := range res.Effects {
		if t.opts.OnEffect != nil {
			t.opts.OnEffect(e)
		}
	}
	if t.opts.OnCheckIn != nil {
		for _, c := range res.CheckIns {
			t.opts.OnCheckIn(c)
		}
	}
	if from := prevPhase; t.opts.OnPhase != nil && from != t.state.Phase {
		t.opts.OnPhase(from, t.state.Phase)
	}
	if res.Changed {
		t.publish(now)
	}
	return nil
}

// persist writes first, effects second (spec 02 §2).
func (t *Table) persist(ctx context.Context, next *engine.State, writes []engine.Write) error {
	st := t.opts.Store
	for _, w := range writes {
		switch {
		case w.Create:
			id, err := st.CreateGame(ctx, gameToStore(w.Game), eventsToStore(w.Events))
			if err != nil {
				return err
			}
			if next.Game != nil && next.Game.ID == 0 {
				next.Game.ID = id
			}
			t.log.Info("game started", "game", id)
		case w.Delete:
			if err := st.DeleteGame(ctx, w.Game.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			t.log.Info("game discarded (no goals)", "game", w.Game.ID)
		default:
			saved, err := st.AppendEvents(ctx, w.Game.ID, eventsToStore(w.Events), updateFor(w.Game))
			if err != nil {
				return err
			}
			for i, e := range saved {
				if e.Seq != w.Events[i].Seq {
					t.log.Error("event sequence mismatch", "game", w.Game.ID, "engine", w.Events[i].Seq, "db", e.Seq)
				}
			}
			if w.Game.Status == engine.StatusFinished && hasEvent(w.Events, engine.EvFinished) {
				t.log.Info("game finished", "game", w.Game.ID, "home", w.Game.Home, "away", w.Game.Away, "winner", w.Game.Winner)
				if t.opts.OnGameFinished != nil {
					t.opts.OnGameFinished(w.Game.ID)
				}
			}
		}
	}
	return nil
}

func (t *Table) publish(now time.Time) {
	t.rev++
	v := viewOf(t.state, t.rev, now, *t.settings.Load())
	v.Paused = t.paused
	t.view.Store(&v)
	if t.opts.Bus != nil {
		t.opts.Bus.Publish(TopicTable, v)
	}
}

// loadGame rebuilds a stored game from its events.
func (t *Table) loadGame(ctx context.Context, id int64) (*engine.Game, error) {
	row, events, err := t.opts.Store.Game(ctx, id)
	if err != nil {
		return nil, err
	}
	return engine.Replay(headerFromStore(row), eventsFromStore(events)), nil
}

func (t *Table) restore(ctx context.Context) (engine.State, error) {
	st, now := t.opts.Store, t.opts.Now()
	cfg := t.opts.Config
	settings := DefaultSettings() // fields missing in older saved settings keep their default
	if ok, err := st.GetSetting(ctx, SettingKey, &settings); err != nil {
		return engine.State{}, err
	} else if ok {
		settings.Normalize()
		if err := settings.Validate(); err != nil {
			t.log.Error("stored table settings are not valid; using the defaults", "err", err)
			settings = DefaultSettings()
		}
		t.settings.Store(&settings)
		cfg = settings.apply(cfg)
	}
	guest, err := st.GuestPlayer(ctx)
	if err != nil {
		return engine.State{}, fmt.Errorf("the guest player: %w", err)
	}
	cfg.GuestID = guest.ID
	saved, err := st.Registrations(ctx)
	if err != nil {
		return engine.State{}, err
	}
	regs := regsFromStore(saved)
	open, err := st.OpenGames(ctx)
	if err != nil {
		return engine.State{}, err
	}
	d := engine.Restored{Regs: regs}
	for _, row := range open { // newest first
		g, err := t.loadGame(ctx, row.ID)
		if err != nil {
			return engine.State{}, err
		}
		switch {
		case g.Status == engine.StatusActive && d.Active == nil:
			d.Active = g
		case g.Status == engine.StatusActive:
			t.log.Warn("more than one active game; only the newest continues", "game", g.ID)
		case d.Recent == nil || g.PausedAt.After(d.Recent.PausedAt):
			d.Recent = g
		}
	}
	if d.Active == nil {
		row, events, err := st.LastFinished(ctx)
		if err == nil {
			d.Last = engine.Replay(headerFromStore(row), eventsFromStore(events))
		} else if !errors.Is(err, store.ErrNotFound) {
			return engine.State{}, err
		}
	}
	s := engine.Restore(cfg, d, now)
	if p, err := t.CurrentPreset(ctx); err == nil {
		s.Next = engine.NextGame{Rules: rulesFromStore(p.Rules), PresetID: p.ID}
	} else {
		t.log.Error("no rules for the next game: no kickoff until a preset exists", "err", err)
	}
	return s, nil
}

// --- paused games (D16) ----------------------------------------------------------------------

// PausedGame is a game in the paused games list.
type PausedGame struct {
	GameView
	PausedAt int64  `json:"paused_at"`
	Reason   string `json:"reason"` // manual | timeout | new_game | switched
	// Tournament games (M6) are never abandoned by time.
	inTournament bool
}

func gameID(g *engine.Game) int64 {
	if g == nil {
		return 0
	}
	return g.ID
}

// touchesPaused reports whether the writes may change the paused games list.
func touchesPaused(ws []engine.Write) bool {
	for _, w := range ws {
		if w.Delete || w.Game.Status == engine.StatusPaused || w.Game.Status == engine.StatusAbandoned {
			return true
		}
		for _, e := range w.Events {
			if e.Type == engine.EvResumed {
				return true
			}
		}
	}
	return false
}

// refreshPaused reloads the paused games list (inside Run).
func (t *Table) refreshPaused(ctx context.Context) {
	rows, err := t.opts.Store.GamesByState(ctx, string(engine.StatusPaused), 0)
	if err != nil {
		t.log.Error("loading the paused games", "err", err)
		return
	}
	now := t.opts.Now()
	out := make([]PausedGame, 0, len(rows))
	for _, row := range rows {
		if row.ID == gameID(t.state.Game) {
			continue // paused by hand, still on the table
		}
		g, err := t.loadGame(ctx, row.ID)
		if err != nil {
			t.log.Error("loading a paused game", "game", row.ID, "err", err)
			continue
		}
		out = append(out, PausedGame{GameView: *gameView(g, false, now), PausedAt: g.PausedAt.UnixMilli(),
			Reason: g.PauseReason, inTournament: row.PairingID != nil})
	}
	slices.SortStableFunc(out, func(a, b PausedGame) int { return cmp.Compare(b.PausedAt, a.PausedAt) })
	t.paused = out
}

// abandonOld gives up free-play games paused for longer than abandon_after_hours (D16).
func (t *Table) abandonOld(ctx context.Context) {
	limit := time.Duration(t.Settings().AbandonAfterHours) * time.Hour
	cutoff := t.opts.Now().Add(-limit).UnixMilli()
	for _, p := range slices.Clone(t.paused) {
		if p.inTournament || p.PausedAt > cutoff {
			continue
		}
		g, err := t.loadGame(ctx, p.ID)
		if err != nil {
			t.log.Error("loading a game to abandon", "game", p.ID, "err", err)
			continue
		}
		if err := t.apply(ctx, engine.AbandonGame{Game: g}); err != nil {
			t.log.Error("abandoning a game", "game", p.ID, "err", err)
			continue
		}
		t.log.Info("game abandoned after being paused too long", "game", p.ID)
	}
}

// ErrNoSuchGame: the game doesn't exist.
var ErrNoSuchGame = errors.New("no such game")

// Game rebuilds a stored game (for permission checks before ReinstateGame).
func (t *Table) Game(ctx context.Context, id int64) (*engine.Game, error) {
	g, err := t.loadGame(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNoSuchGame
	}
	return g, err
}

// ResumeGame puts a paused game back on the table (D16).
func (t *Table) ResumeGame(ctx context.Context, id, author int64) error {
	g, err := t.Game(ctx, id)
	if err != nil {
		return err
	}
	return t.Do(ctx, engine.ResumePaused{Game: g, Author: author})
}

// ReinstateGame makes an abandoned game paused again. The caller checks the permission.
func (t *Table) ReinstateGame(ctx context.Context, id, author int64) error {
	g, err := t.Game(ctx, id)
	if err != nil {
		return err
	}
	return t.Do(ctx, engine.ReinstateGame{Game: g, Author: author})
}

// AbandonedGames lists the most recently started abandoned games.
func (t *Table) AbandonedGames(ctx context.Context, limit int) ([]GameView, error) {
	rows, err := t.opts.Store.GamesByState(ctx, string(engine.StatusAbandoned), limit)
	if err != nil {
		return nil, err
	}
	now := t.opts.Now()
	out := make([]GameView, 0, len(rows))
	for _, row := range rows {
		g, err := t.loadGame(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, *gameView(g, false, now))
	}
	return out, nil
}

func hasEvent(events []engine.Event, t engine.EventType) bool {
	for _, e := range events {
		if e.Type == t {
			return true
		}
	}
	return false
}

// ProjectionsVersion is the version of the game projections (result columns, game_participants).
// BackfillProjections rebuilds them from the events when the stored version is older.
const ProjectionsVersion = 1

// SettingProjections stores the projections version.
const SettingProjections = "projections"

// BackfillProjections rebuilds every game's result and roster projection from its events if they
// were made by an older version (or not at all). It reports whether it did, so the caller can
// replay the ratings. Run it before the table starts.
func BackfillProjections(ctx context.Context, st *store.Store, log *slog.Logger) (bool, error) {
	var v int
	if _, err := st.GetSetting(ctx, SettingProjections, &v); err != nil {
		return false, err
	}
	if v >= ProjectionsVersion {
		return false, nil
	}
	ids, err := st.AllGameIDs(ctx)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		row, events, err := st.Game(ctx, id)
		if err != nil {
			return false, err
		}
		g := engine.Replay(headerFromStore(row), eventsFromStore(events))
		u := updateFor(*g)
		u.State, u.EndedAt, u.NextBallSide = row.State, row.EndedAt, row.NextBallSide // only the projections
		if err := st.SetProjection(ctx, id, *u); err != nil {
			return false, fmt.Errorf("game %d: %w", id, err)
		}
	}
	if log != nil {
		log.Info("game projections rebuilt", "games", len(ids))
	}
	return true, st.SetSetting(ctx, SettingProjections, ProjectionsVersion)
}
