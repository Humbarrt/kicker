package engine

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Registrations: who is playing (spec 02 §4). Each side has 2 slots; a registration is a player
// (or an unknown coin) in a slot with one or more sources. While a game is being played, the
// registrations are its roster: every change is written as player_joined / player_left /
// player_assigned events.

// Via is the kind of a registration source (spec 02 §4, the via column of player_joined).
type Via string

const (
	ViaCoin   Via = "coin"   // Ref = coin uid
	ViaCorner Via = "corner" // Ref = corner id (phones, M4)
	ViaManual Via = "manual" // added in the UI, Ref = ""
)

// Slots is the number of slots per side.
const Slots = 2

// RegSource is one reason a registration exists.
type RegSource struct {
	Via   Via
	Ref   string
	Stale bool
}

// Registration is a player – or an unknown coin (PlayerID 0) – in a slot.
type Registration struct {
	Side     Side
	Slot     int    // 1 or 2
	PlayerID int64  // 0 = unknown coin, see CoinUID
	CoinUID  string // the coin that made the registration, if it was a coin
	Sources  []RegSource
}

// Stale reports whether every source is stale: the player has left (pressed the eject button,
// took the phone away) but stays in the current game until the next kickoff or a replacement.
func (r Registration) Stale() bool {
	for _, s := range r.Sources {
		if !s.Stale {
			return false
		}
	}
	return true
}

// Via is the kind of the first source: how the player joined.
func (r Registration) Via() Via {
	if len(r.Sources) == 0 {
		return ViaManual
	}
	return r.Sources[0].Via
}

func (r Registration) source(via Via, ref string) int {
	return slices.IndexFunc(r.Sources, func(s RegSource) bool { return s.Via == via && s.Ref == ref })
}

func (r Registration) clone() Registration {
	r.Sources = slices.Clone(r.Sources)
	return r
}

// Seat is a slot of a game's roster, derived from its player_* events.
type Seat struct {
	PlayerID int64 // 0 = unknown coin (counts as guest if never assigned)
	CoinUID  string
	Via      Via
}

// Taken reports whether somebody sits here.
func (s Seat) Taken() bool { return s.PlayerID != 0 || s.CoinUID != "" }

// Roster is who plays a game, per side and slot.
type Roster struct {
	Home, Away [Slots]Seat
}

// Side returns the seats of one side.
func (r *Roster) Side(s Side) *[Slots]Seat {
	if s == Home {
		return &r.Home
	}
	return &r.Away
}

// RegisterPlayer adds a player by hand (UI, `table.players`). The caller has checked that the
// player exists and is not archived.
type RegisterPlayer struct {
	Side     Side
	PlayerID int64
	Author   int64
}

// Unregister removes whoever is in a slot (UI, `table.players`).
type Unregister struct {
	Side   Side
	Slot   int
	Author int64
}

func (RegisterPlayer) isInput() {}
func (Unregister) isInput()     {}

// Errors of registration commands.
var (
	ErrSideFull      = errors.New("both players of this side are still playing")
	ErrNotRegistered = errors.New("nobody is registered in this slot")
)

// --- the rules ----------------------------------------------------------------------------------

func (s *State) regsOn(side Side) []int {
	var out []int
	for i, r := range s.Regs {
		if r.Side == side {
			out = append(out, i)
		}
	}
	return out
}

func (s *State) removeReg(i int) {
	s.Regs = slices.Delete(s.Regs, i, i+1)
}

func (s *State) sortRegs() {
	slices.SortFunc(s.Regs, func(a, b Registration) int {
		if a.Side != b.Side {
			if a.Side == Home {
				return -1
			}
			return 1
		}
		return a.Slot - b.Slot
	})
}

// sameIdentity tells whether reg is the one a new source for (playerID, src) belongs to.
// Players are matched by id – except the guest, who can play several times at once (§4); guests
// and unknown coins are matched by the coin itself.
func (s *State) sameIdentity(reg Registration, playerID int64, src RegSource) bool {
	if playerID != 0 && playerID != s.Config.GuestID {
		return reg.PlayerID == playerID
	}
	if src.Via == ViaCoin {
		return reg.source(ViaCoin, src.Ref) >= 0
	}
	return false
}

// register applies rules 1–6 of spec 02 §4 for a new source on side.
func (s *State) register(side Side, playerID int64, coinUID string, src RegSource) error {
	// Rules 1 and 2: the same player (or coin) is already registered.
	for i, reg := range s.Regs {
		if !s.sameIdentity(reg, playerID, src) {
			continue
		}
		if reg.Side == side { // rule 1: add the source
			if j := reg.source(src.Via, src.Ref); j >= 0 {
				s.Regs[i].Sources[j].Stale = false
			} else {
				s.Regs[i].Sources = append(s.Regs[i].Sources, src)
			}
			if reg.PlayerID == 0 && playerID != 0 {
				s.Regs[i].PlayerID = playerID
			}
			return nil
		}
		s.removeReg(i) // rule 2: the player moves to this side
		break
	}

	slot, replace, err := s.placeOn(side)
	if err != nil {
		return err
	}
	for _, i := range slices.Backward(replace) {
		s.removeReg(i)
	}
	s.Regs = append(s.Regs, Registration{Side: side, Slot: slot, PlayerID: playerID, CoinUID: coinUID, Sources: []RegSource{src}})
	s.sortRegs()
	return nil
}

// placeOn finds the slot for a new registration on side (rules 3–6) and which registrations it
// replaces (indexes into Regs, ascending).
func (s *State) placeOn(side Side) (slot int, replace []int, err error) {
	on := s.regsOn(side)
	taken := map[int]bool{}
	var stale []int
	for _, i := range on {
		taken[s.Regs[i].Slot] = true
		if s.Regs[i].Stale() {
			stale = append(stale, i)
		}
	}
	for sl := 1; sl <= Slots; sl++ { // rule 3: a free slot
		if !taken[sl] {
			return sl, nil, nil
		}
	}
	switch {
	case len(stale) == len(on): // rule 4: everybody left, the whole side is replaced
		return 1, on, nil
	case len(stale) > 0: // rule 5: the one who left is replaced
		return s.Regs[stale[0]].Slot, stale[:1], nil
	}
	return 0, nil, ErrSideFull // rule 6
}

// dropSources removes the sources matching drop; registrations without sources disappear.
func (s *State) dropSources(drop func(Registration, RegSource) bool) bool {
	changed := false
	out := s.Regs[:0]
	for _, reg := range s.Regs {
		n := len(reg.Sources)
		reg.Sources = slices.DeleteFunc(reg.Sources, func(src RegSource) bool { return drop(reg, src) })
		changed = changed || len(reg.Sources) != n
		if len(reg.Sources) > 0 {
			out = append(out, reg)
		}
	}
	s.Regs = out
	return changed
}

// dropStale removes stale registrations at kickoff (§4 "removed from the next game").
func (s *State) dropStale() {
	s.Regs = slices.DeleteFunc(s.Regs, Registration.Stale)
}

// releasePlayers ends the registrations that last one game (§8 step 3): coins are ejected,
// manual registrations and one-game claims end. Tracked phones stay (M4).
func (s *State) releasePlayers(r *Result) {
	for _, side := range []Side{Home, Away} {
		// The acceptor's contents are unknown until its state sync, so a coin registration counts too.
		coinReg := slices.ContainsFunc(s.Regs, func(reg Registration) bool {
			return reg.Side == side && slices.ContainsFunc(reg.Sources, func(src RegSource) bool { return src.Via == ViaCoin })
		})
		if len(s.Acceptors[side]) > 0 || coinReg {
			r.Effects = append(r.Effects, EjectCoins{Side: side})
		}
	}
	if s.dropSources(func(_ Registration, src RegSource) bool { return src.Via != ViaCorner }) {
		r.Registrations = true
	}
}

// --- inputs -------------------------------------------------------------------------------------

func (s *State) registerCmd(in RegisterPlayer, now time.Time, r *Result) error {
	if !in.Side.Valid() || in.PlayerID <= 0 {
		return fmt.Errorf("%w: register player %d on %q", ErrInvalidInput, in.PlayerID, in.Side)
	}
	if err := s.register(in.Side, in.PlayerID, "", RegSource{Via: ViaManual}); err != nil {
		return err
	}
	s.regsChanged(SourceTable, in.Author, now, r)
	return nil
}

func (s *State) unregisterCmd(in Unregister, now time.Time, r *Result) error {
	if !in.Side.Valid() || in.Slot < 1 || in.Slot > Slots {
		return fmt.Errorf("%w: slot %d on %q", ErrInvalidInput, in.Slot, in.Side)
	}
	i := slices.IndexFunc(s.Regs, func(reg Registration) bool { return reg.Side == in.Side && reg.Slot == in.Slot })
	if i < 0 {
		return ErrNotRegistered
	}
	s.removeReg(i)
	s.regsChanged(SourceTable, in.Author, now, r)
	return nil
}

// coins keeps track of what is inside the acceptors and registers the coins' players.
func (s *State) coins(in Input, now time.Time, r *Result) error {
	switch in := in.(type) {
	case CoinInserted:
		if !in.Side.Valid() || in.UID == "" {
			return fmt.Errorf("%w: coin on side %q", ErrInvalidInput, in.Side)
		}
		if !slices.ContainsFunc(s.Acceptors[in.Side], func(c CoinRef) bool { return c.UID == in.UID }) {
			s.Acceptors[in.Side] = append(s.Acceptors[in.Side], CoinRef{UID: in.UID, PlayerID: in.PlayerID})
		}
		s.coinIn(in.Side, CoinRef{UID: in.UID, PlayerID: in.PlayerID})
	case CoinsHeld:
		if !in.Side.Valid() {
			return fmt.Errorf("%w: coins on side %q", ErrInvalidInput, in.Side)
		}
		s.Acceptors[in.Side] = slices.Clone(in.Coins)
		// State sync (restart, reconnect): coins that are gone were taken out with the button;
		// coins that are new were inserted meanwhile.
		held := map[string]bool{}
		for _, c := range in.Coins {
			held[c.UID] = true
		}
		for i := range s.Regs {
			for j, src := range s.Regs[i].Sources {
				if s.Regs[i].Side == in.Side && src.Via == ViaCoin && !held[src.Ref] {
					s.Regs[i].Sources[j].Stale = true
				}
			}
		}
		for _, c := range in.Coins {
			s.coinIn(in.Side, c)
		}
	case CoinsEjected:
		if !in.Side.Valid() {
			return fmt.Errorf("%w: coins on side %q", ErrInvalidInput, in.Side)
		}
		// The acceptor always empties completely (D2), whatever the event lists.
		s.Acceptors[in.Side] = nil
		if in.ByButton { // the players leave: stale, but they stay in the current game
			for i := range s.Regs {
				for j, src := range s.Regs[i].Sources {
					if s.Regs[i].Side == in.Side && src.Via == ViaCoin {
						s.Regs[i].Sources[j].Stale = true
					}
				}
			}
		} else { // ejected by the server: the registrations end
			s.dropSources(func(reg Registration, src RegSource) bool { return reg.Side == in.Side && src.Via == ViaCoin })
		}
	case CoinAssigned:
		for side, coins := range s.Acceptors {
			for i := range coins {
				if coins[i].UID == in.UID {
					s.Acceptors[side][i].PlayerID = in.PlayerID
				}
			}
		}
		if in.PlayerID != 0 {
			s.assignCoin(in.UID, in.PlayerID)
		}
	}
	s.regsChanged(SourceSystem, 0, now, r)
	return nil
}

// coinIn registers the player of a coin that is inside an acceptor. A full side is not an error
// for a module: the coin just doesn't count (the view shows it).
func (s *State) coinIn(side Side, c CoinRef) {
	_ = s.register(side, c.PlayerID, c.UID, RegSource{Via: ViaCoin, Ref: c.UID})
}

// assignCoin fills in the player of an unknown coin's registration (§4 "Unknown coins"). If that
// player is registered already, the two registrations become one, on the coin's side.
func (s *State) assignCoin(uid string, playerID int64) {
	i := slices.IndexFunc(s.Regs, func(r Registration) bool { return r.PlayerID == 0 && r.CoinUID == uid })
	if i < 0 {
		return
	}
	reg := s.Regs[i]
	if playerID != s.Config.GuestID {
		if j := slices.IndexFunc(s.Regs, func(r Registration) bool { return r.PlayerID == playerID }); j >= 0 {
			other := s.Regs[j]
			if other.Side == reg.Side { // keep the older registration, with both sources
				for _, src := range reg.Sources {
					if other.source(src.Via, src.Ref) < 0 {
						s.Regs[j].Sources = append(s.Regs[j].Sources, src)
					}
				}
				s.removeReg(i)
				return
			}
			s.removeReg(j) // the player moves to the coin's side
			i = slices.IndexFunc(s.Regs, func(r Registration) bool { return r.PlayerID == 0 && r.CoinUID == uid })
		}
	}
	s.Regs[i].PlayerID = playerID
}

// --- roster -------------------------------------------------------------------------------------

// regsChanged brings the roster of the game being played in line with the registrations
// (§4 "During a game") and marks the registrations for saving.
func (s *State) regsChanged(src Source, author int64, now time.Time, r *Result) {
	r.Registrations = true
	r.Changed = true
	if s.smartResume(now, r) { // the paused game's players are back (§9)
		return
	}
	defer s.updateKickoff(now, true, r) // a roster change restarts the countdown (§6)
	// A resumed game keeps its own players; the registrations are for the game after it (D16).
	if s.Game == nil || s.Phase != PhasePlaying || s.Game.Status == StatusFinished || s.Resumed {
		return
	}
	// Everybody who leaves first, then who is assigned, then who joins: reads well in the log.
	var left, assigned, joined []Event
	for _, side := range []Side{Home, Away} {
		seats := *s.Game.Roster.Side(side)
		for slot := 1; slot <= Slots; slot++ {
			cur := seats[slot-1]
			var want *Registration
			for i := range s.Regs {
				if s.Regs[i].Side == side && s.Regs[i].Slot == slot {
					want = &s.Regs[i]
				}
			}
			ev := Event{Side: side, Slot: slot, Source: src, Author: author, At: now}
			switch {
			case want == nil && !cur.Taken():
			case want != nil && cur.PlayerID == want.PlayerID && cur.CoinUID == want.CoinUID:
			case want != nil && cur.PlayerID == 0 && cur.CoinUID != "" && cur.CoinUID == want.CoinUID:
				ev.Type, ev.PlayerID, ev.CoinUID = EvPlayerAssigned, want.PlayerID, want.CoinUID
				assigned = append(assigned, ev)
			default:
				if cur.Taken() {
					l := ev
					l.Type, l.PlayerID, l.CoinUID = EvPlayerLeft, cur.PlayerID, cur.CoinUID
					left = append(left, l)
				}
				if want != nil {
					joined = append(joined, joinEvent(*want, ev))
				}
			}
		}
	}
	for _, e := range slices.Concat(left, assigned, joined) {
		s.emit(e)
	}
}

func joinEvent(reg Registration, ev Event) Event {
	ev.Type, ev.Side, ev.Slot = EvPlayerJoined, reg.Side, reg.Slot
	ev.PlayerID, ev.CoinUID, ev.Via = reg.PlayerID, reg.CoinUID, reg.Via()
	return ev
}
