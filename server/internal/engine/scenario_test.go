package engine

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Scenario files (testdata/scenarios/*.txt) drive the engine like the table actor does, with a
// fake clock and a fake database, and check the outcome. One command per line:
//
//	# comment
//	config after_goal_ball=scoring hold=10s dispenser=on
//	start official [first=away] | start win=5 by=2 max=8 limit=0s tie=golden_goal
//	wait 3s                     advance the clock; ticks fire at every deadline on the way
//	jump 3s                     advance the clock without ticks (a late timer)
//	sensor home_goal [at=-1s]   goal event (at = offset from now, as the module clock says)
//	plus home | minus away | undo
//	restart                     rebuild the state from the fake database (server restart)
//	expect phase playing
//	expect score 6:4
//	expect winner home|draw|none [reason=score]
//	expect last goal [side=away]   last persisted event
//	expect next_ball away
//	expect release home|none       effects of the previous command
//	expect error                   the previous command failed
//	expect games 2 / expect status 1 paused / expect deleted 1
//
// Registrations (spec 02 §4); players are numbers, unknown coins have player 0:
//
//	config guest=1              the guest player's id
//
// Kickoff (spec 02 §6):
//
//	config kickoff=any_player delay=5s first_ball=alternate
//	next official               the rules of the next game (the current preset)
//	expect kickoff 3s | none    time left on the countdown
//	expect first_ball away      the first ball of the current (or last) game
//	expect started_by system    source of the current game's game_started
//
// Pause, sleep, wake (spec 02 §7.4, §9, D16):
//
//	config pause=5m sleep=3m on_wake=smart   inactivity timers (0s = off)
//	pause | resume | wake       table actions (members)
//	resume_game 2               resume game 2 from the paused list
//	abandon 2 | reinstate 2     abandon after hours (the table's job) / reinstate (members)
//	expect recent 2|none        the paused game smart wake may resume (none after a new game started)
//	expect power sleep|wake|none   sleep / wake effects of the previous command
//	expect reason 2 timeout     why game 2 is paused
//	coin home 04000001 [player=7]   a coin is inserted
//	held home 04000001=7 04000002   state sync: exactly these coins are inside
//	button home | ejected home  the acceptor empties: by its button / on the server's command
//	assign 04000002 7           the coin is assigned to a player
//	add home 7 | remove home 1  manual registration / removing a slot
//	expect regs home=7,9* away=?04000002,_   per slot: player, ?coin (unknown), _ (empty); * = stale
//	expect roster home=7,9 away=_,_          the current game's roster, same notation
//	expect written goal,player_left          event types persisted by the previous command
//	expect eject home,away|none              coin eject effects of the previous command
var presets = map[string]Rules{
	"long":     {WinScore: 10, WinBy: 2, MaxScore: 15, TimeTie: TimeTieGoldenGoal},
	"short":    {WinScore: 6, WinBy: 1, MaxScore: 6, TimeTie: TimeTieGoldenGoal},
	"official": {WinScore: 5, WinBy: 2, MaxScore: 8, TimeTie: TimeTieGoldenGoal},
	"timed":    {WinScore: 10, WinBy: 1, TimeLimit: 300 * time.Second, TimeTie: TimeTieGoldenGoal},
}

type fakeGame struct {
	header  Game
	events  []Event
	deleted bool
}

type runner struct {
	t      *testing.T
	where  string
	now    time.Time
	state  State
	db     []*fakeGame // id = index + 1
	last   Result
	lastEr error
	regs   []Registration // saved registrations (the fake table_registrations)

	nextIsExpectError bool
}

func (r *runner) fail(format string, args ...any) {
	r.t.Helper()
	r.t.Errorf("%s: "+format, append([]any{r.where}, args...)...)
}

func (r *runner) step(in Input) {
	s, res, err := Step(r.state, in, r.now)
	r.last, r.lastEr = res, err
	if err != nil {
		return
	}
	for _, w := range res.Writes {
		r.persist(w, &s)
	}
	if res.Registrations {
		r.regs = nil
		for _, reg := range s.Regs {
			r.regs = append(r.regs, reg.clone())
		}
	}
	r.state = s
}

// persist plays the database: rows, seq continuity, IDs.
func (r *runner) persist(w Write, s *State) {
	if w.Create {
		if w.Game.ID != 0 || len(w.Events) == 0 || w.Events[0].Type != EvGameStarted {
			r.fail("bad create: %+v", w)
		}
		r.db = append(r.db, &fakeGame{})
		id := int64(len(r.db))
		w.Game.ID = id
		if s.Game != nil && s.Game.ID == 0 {
			s.Game.ID = id
		}
	}
	if w.Game.ID < 1 || int(w.Game.ID) > len(r.db) {
		r.fail("write for unknown game %d", w.Game.ID)
		return
	}
	fg := r.db[w.Game.ID-1]
	if w.Delete {
		fg.deleted = true
		return
	}
	for _, e := range w.Events {
		if e.Seq != len(fg.events)+1 {
			r.fail("game %d: seq %d after %d events", w.Game.ID, e.Seq, len(fg.events))
		}
		fg.events = append(fg.events, e)
	}
	fg.header = w.Game
}

func (r *runner) advance(d time.Duration) {
	target := r.now.Add(d)
	var all Result // what the whole wait wrote and did, for the expectations after it
	defer func() { r.last = all }()
	collect := func() {
		all.Writes = append(all.Writes, r.last.Writes...)
		all.Effects = append(all.Effects, r.last.Effects...)
	}
	for {
		dl := r.state.NextDeadline()
		if dl.IsZero() || dl.After(target) {
			break
		}
		if dl.After(r.now) {
			r.now = dl
		}
		before := r.state
		r.step(Tick{})
		collect()
		if r.state.NextDeadline().Equal(dl) && r.state.Phase == before.Phase {
			r.fail("tick at deadline %v changed nothing", dl.Sub(r.now))
			break
		}
	}
	r.now = target
	r.step(Tick{})
	collect()
}

// stored rebuilds a game from the fake database.
func (r *runner) stored(id int) *Game {
	fg := r.db[id-1]
	h := fg.header
	return Replay(Game{ID: int64(id), PresetID: h.PresetID, Rules: h.Rules, NextBallSide: h.NextBallSide}, fg.events)
}

func (r *runner) restart() {
	var d Restored
	var newestPaused time.Time
	for id, fg := range r.db {
		if fg.deleted {
			continue
		}
		h := fg.header
		g := Replay(Game{ID: int64(id + 1), PresetID: h.PresetID, Rules: h.Rules, NextBallSide: h.NextBallSide}, fg.events)
		switch g.Status {
		case StatusActive:
			d.Active = g
		case StatusPaused:
			if !g.PausedAt.Before(newestPaused) {
				d.Recent, newestPaused = g, g.PausedAt
			}
		case StatusFinished:
			d.Last = g
		}
	}
	d.Regs = r.regs
	next := r.state.Next // the table sets it from the current preset
	r.state = Restore(r.state.Config, d, r.now)
	r.state.Next = next
	r.step(Tick{})
}

// optID parses an optional player id; "" = 0 (unknown coin).
func optID(t *testing.T, s string) int64 {
	if s == "" {
		return 0
	}
	return int64(atoi(t, s))
}

func seatString(player int64, coin string, stale bool) string {
	s := fmt.Sprint(player)
	if player == 0 {
		s = "?" + coin
	}
	if stale {
		s += "*"
	}
	return s
}

func kv(args []string) map[string]string {
	m := map[string]string{}
	for _, a := range args {
		k, v, _ := strings.Cut(a, "=")
		m[k] = v
	}
	return m
}

func dur(t *testing.T, s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func atoi(t *testing.T, s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (r *runner) run(cmd string, args []string) {
	t := r.t
	switch cmd {
	case "config":
		for k, v := range kv(args) {
			switch k {
			case "after_goal_ball":
				r.state.Config.AfterGoalBall = BallRule(v)
			case "hold":
				r.state.Config.FinishedHold = dur(t, v)
			case "dispenser":
				r.state.Config.Dispenser = v == "on"
			case "guest":
				r.state.Config.GuestID = int64(atoi(t, v))
			case "kickoff":
				r.state.Config.KickoffRequires = KickoffRequires(v)
			case "delay":
				r.state.Config.KickoffDelay = dur(t, v)
			case "first_ball":
				r.state.Config.FirstBall = FirstBallRule(v)
			case "pause":
				r.state.Config.PauseAfter = dur(t, v)
			case "sleep":
				r.state.Config.SleepAfter = dur(t, v)
			case "on_wake":
				r.state.Config.OnWake = OnWake(v)
			default:
				t.Fatalf("%s: unknown config %s", r.where, k)
			}
		}
	case "start":
		m := kv(args)
		rules, ok := presets[args[0]]
		if !ok {
			rules = Rules{WinScore: atoi(t, m["win"]), WinBy: atoi(t, m["by"]), TimeTie: TimeTie(m["tie"])}
			if m["max"] != "" {
				rules.MaxScore = atoi(t, m["max"])
			}
			if m["limit"] != "" {
				rules.TimeLimit = dur(t, m["limit"])
			}
		}
		r.step(StartGame{Rules: rules, FirstBall: Side(m["first"]), Author: 1})
	case "wait":
		r.advance(dur(t, args[0]))
	case "jump":
		r.now = r.now.Add(dur(t, args[0]))
	case "sensor":
		at := r.now
		if v := kv(args[1:])["at"]; v != "" {
			at = at.Add(dur(t, v))
		}
		r.step(SensorGoal{Role: args[0], At: at})
	case "plus", "minus":
		d := 1
		if cmd == "minus" {
			d = -1
		}
		r.step(AdjustScore{Side: Side(args[0]), Delta: d, Author: 1})
	case "undo":
		r.step(UndoGoal{Author: 1})
	case "restart":
		r.restart()
	case "pause":
		r.step(PauseGame{Author: 1})
	case "resume":
		r.step(Resume{Author: 1})
	case "wake":
		r.step(Wake{Author: 1})
	case "resume_game", "abandon", "reinstate":
		g := r.stored(atoi(t, args[0]))
		switch cmd {
		case "resume_game":
			r.step(ResumePaused{Game: g, Author: 1})
		case "abandon":
			r.step(AbandonGame{Game: g})
		default:
			r.step(ReinstateGame{Game: g, Author: 1})
		}
	case "next":
		r.step(SetNextGame{Next: NextGame{Rules: presets[args[0]], PresetID: 1}})
	case "coin":
		r.step(CoinInserted{Side: Side(args[0]), UID: args[1], PlayerID: optID(t, kv(args[2:])["player"])})
	case "held":
		var coins []CoinRef
		for _, a := range args[1:] {
			uid, p, _ := strings.Cut(a, "=")
			coins = append(coins, CoinRef{UID: uid, PlayerID: optID(t, p)})
		}
		r.step(CoinsHeld{Side: Side(args[0]), Coins: coins})
	case "button", "ejected":
		r.step(CoinsEjected{Side: Side(args[0]), ByButton: cmd == "button"})
	case "assign":
		r.step(CoinAssigned{UID: args[0], PlayerID: int64(atoi(t, args[1]))})
	case "add":
		r.step(RegisterPlayer{Side: Side(args[0]), PlayerID: int64(atoi(t, args[1])), Author: 1})
	case "remove":
		r.step(Unregister{Side: Side(args[0]), Slot: atoi(t, args[1]), Author: 1})
	case "expect":
		r.expect(args[0], args[1:])
	default:
		t.Fatalf("%s: unknown command %q", r.where, cmd)
	}
	if cmd != "expect" && r.lastEr != nil && !r.nextIsExpectError {
		r.fail("%s: unexpected error %v", cmd, r.lastEr)
	}
}

// game is the game on the table, else the most recently paused one, else the last finished one.
func (r *runner) game() *Game {
	switch {
	case r.state.Game != nil:
		return r.state.Game
	case r.state.Recent != nil:
		return r.state.Recent
	}
	return r.state.Last
}

func (r *runner) expect(what string, args []string) {
	g := r.game()
	switch what {
	case "phase":
		if string(r.state.Phase) != args[0] {
			r.fail("phase %s, want %s", r.state.Phase, args[0])
		}
	case "score":
		if g == nil {
			r.fail("no game")
			return
		}
		if got := fmt.Sprintf("%d:%d", g.Home, g.Away); got != args[0] {
			r.fail("score %s, want %s", got, args[0])
		}
	case "winner":
		want := args[0]
		got := string(g.Winner)
		if g.Status != StatusFinished {
			got = "none"
		} else if got == "" {
			got = "draw"
		}
		if got != want {
			r.fail("winner %s, want %s", got, want)
		}
		if v := kv(args[1:])["reason"]; v != "" && v != g.EndReason {
			r.fail("end reason %s, want %s", g.EndReason, v)
		}
	case "last":
		fg := r.db[len(r.db)-1]
		if g != nil && g.ID != 0 {
			fg = r.db[g.ID-1]
		}
		e := fg.events[len(fg.events)-1]
		if string(e.Type) != args[0] {
			r.fail("last event %s, want %s", e.Type, args[0])
		}
		if v := kv(args[1:])["side"]; v != "" && string(e.Side) != v {
			r.fail("last event side %s, want %s", e.Side, v)
		}
	case "next_ball":
		if string(g.NextBallSide) != args[0] {
			r.fail("next ball %s, want %s", g.NextBallSide, args[0])
		}
	case "release":
		var got []string
		for _, e := range r.last.Effects {
			if rb, ok := e.(ReleaseBall); ok {
				got = append(got, string(rb.Side))
			}
		}
		if s := strings.Join(got, ","); s != strings.ReplaceAll(args[0], "none", "") {
			r.fail("released %q, want %s", s, args[0])
		}
	case "error":
		if r.lastEr == nil {
			r.fail("expected an error")
		}
		if len(args) > 0 && !strings.Contains(r.lastEr.Error(), strings.Join(args, " ")) {
			r.fail("error %q, want %q", r.lastEr, strings.Join(args, " "))
		}
	case "games":
		n := 0
		for _, fg := range r.db {
			if !fg.deleted {
				n++
			}
		}
		if n != atoi(r.t, args[0]) {
			r.fail("%d games stored, want %s", n, args[0])
		}
	case "status":
		fg := r.db[atoi(r.t, args[0])-1]
		if string(fg.header.Status) != args[1] {
			r.fail("game %s status %s, want %s", args[0], fg.header.Status, args[1])
		}
	case "deleted":
		if !r.db[atoi(r.t, args[0])-1].deleted {
			r.fail("game %s should be deleted", args[0])
		}
	case "regs", "roster":
		want := map[string]string{"home": "_,_", "away": "_,_"}
		for k, v := range kv(args) {
			want[k] = v
		}
		for _, side := range []Side{Home, Away} {
			var got [Slots]string
			for i := range got {
				got[i] = "_"
			}
			if what == "regs" {
				for _, reg := range r.state.Regs {
					if reg.Side == side {
						got[reg.Slot-1] = seatString(reg.PlayerID, reg.CoinUID, reg.Stale())
					}
				}
			} else {
				if g == nil {
					r.fail("no game")
					return
				}
				for i, st := range g.Roster.Side(side) {
					if st.Taken() {
						got[i] = seatString(st.PlayerID, st.CoinUID, false)
					}
				}
			}
			if s := strings.Join(got[:], ","); s != want[string(side)] {
				r.fail("%s %s = %s, want %s", what, side, s, want[string(side)])
			}
		}
	case "written":
		var got []string
		for _, w := range r.last.Writes {
			for _, e := range w.Events {
				got = append(got, string(e.Type))
			}
		}
		if s := strings.Join(got, ","); s != strings.ReplaceAll(args[0], "none", "") {
			r.fail("written %s, want %s", s, args[0])
		}
	case "eject":
		var got []string
		for _, e := range r.last.Effects {
			if ec, ok := e.(EjectCoins); ok {
				got = append(got, string(ec.Side))
			}
		}
		if s := strings.Join(got, ","); s != strings.ReplaceAll(args[0], "none", "") {
			r.fail("ejected %q, want %s", s, args[0])
		}
	case "recent":
		g := r.state.Recent
		got := "none"
		if g != nil {
			got = fmt.Sprint(g.ID)
		}
		if got != args[0] {
			r.fail("%s game %s, want %s", what, got, args[0])
		}
	case "power":
		var got []string
		for _, e := range r.last.Effects {
			switch e.(type) {
			case Sleep:
				got = append(got, "sleep")
			case WakeUp:
				got = append(got, "wake")
			}
		}
		if s := strings.Join(got, ","); s != strings.ReplaceAll(args[0], "none", "") {
			r.fail("power %q, want %s", s, args[0])
		}
	case "reason":
		g := r.stored(atoi(r.t, args[0]))
		if g.PauseReason != args[1] {
			r.fail("game %s paused for %q, want %s", args[0], g.PauseReason, args[1])
		}
	case "kickoff":
		got := "none"
		if r.state.Phase == PhasePregame {
			got = r.state.KickoffAt.Sub(r.now).String()
		}
		if got != args[0] {
			r.fail("kickoff in %s, want %s", got, args[0])
		}
	case "first_ball":
		if string(g.FirstBall) != args[0] {
			r.fail("first ball %s, want %s", g.FirstBall, args[0])
		}
	case "started_by":
		fg := r.db[g.ID-1]
		if string(fg.events[0].Source) != args[0] {
			r.fail("started by %s, want %s", fg.events[0].Source, args[0])
		}
	case "playtime":
		if pt := g.PlayTime(r.now); pt != dur(r.t, args[0]) {
			r.fail("play time %v, want %s", pt, args[0])
		}
	default:
		r.t.Fatalf("%s: unknown expectation %q", r.where, what)
	}
}

func TestScenarios(t *testing.T) {
	files, _ := filepath.Glob("testdata/scenarios/*.txt")
	if len(files) == 0 {
		t.Fatal("no scenarios")
	}
	for _, f := range files {
		t.Run(strings.TrimSuffix(filepath.Base(f), ".txt"), func(t *testing.T) {
			runScenario(t, f)
		})
	}
}

func runScenario(t *testing.T, path string) {
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	t0 := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	r := &runner{t: t, now: t0, state: NewState(DefaultConfig(), t0)}

	var lines []string
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	for i, line := range lines {
		if j := strings.Index(line, "#"); j >= 0 {
			line = line[:j]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		r.where = fmt.Sprintf("%s:%d", filepath.Base(path), i+1)
		r.nextIsExpectError = nextIsExpectError(lines[i+1:])
		r.run(f[0], f[1:])
	}
}

func nextIsExpectError(rest []string) bool {
	for _, l := range rest {
		if j := strings.Index(l, "#"); j >= 0 {
			l = l[:j]
		}
		if f := strings.Fields(l); len(f) > 0 {
			return len(f) >= 2 && f[0] == "expect" && f[1] == "error"
		}
	}
	return false
}
