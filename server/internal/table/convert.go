package table

import (
	"time"

	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
)

// Conversions between the engine's types and the database rows.

func rulesFromStore(r store.Rules) engine.Rules {
	out := engine.Rules{WinScore: r.WinScore, WinBy: r.WinBy, TimeTie: engine.TimeTie(r.TimeTie)}
	if r.MaxScore != nil {
		out.MaxScore = *r.MaxScore
	}
	if r.TimeLimitS != nil {
		out.TimeLimit = time.Duration(*r.TimeLimitS) * time.Second
	}
	return out
}

func rulesToStore(r engine.Rules) store.Rules {
	out := store.Rules{WinScore: r.WinScore, WinBy: r.WinBy, TimeTie: string(r.TimeTie)}
	if r.MaxScore > 0 {
		out.MaxScore = store.Ptr(r.MaxScore)
	}
	if r.TimeLimit > 0 {
		out.TimeLimitS = store.Ptr(int(r.TimeLimit / time.Second))
	}
	return out
}

func gameToStore(g engine.Game) store.Game {
	out := store.Game{ID: g.ID, Rules: rulesToStore(g.Rules), State: string(g.Status), StartedAt: g.StartedAt}
	if g.PresetID != 0 {
		out.PresetID = store.Ptr(g.PresetID)
	}
	if !g.EndedAt.IsZero() {
		out.EndedAt = store.Ptr(g.EndedAt)
	}
	if g.NextBallSide != "" {
		out.NextBallSide = store.Ptr(string(g.NextBallSide))
	}
	out.HomeGoals, out.AwayGoals, out.Winner = g.Home, g.Away, strOrNil(g.Winner)
	out.Voided, out.Edits = g.Voided, g.Edits
	if g.Pairing != nil {
		out.PairingID, out.PairingASide = store.Ptr(g.Pairing.ID), store.Ptr(string(g.Pairing.ASide))
	}
	out.Participants = participantsOf(g)
	return out
}

func updateFor(g engine.Game) *store.GameUpdate {
	sg := gameToStore(g)
	return &store.GameUpdate{State: sg.State, EndedAt: sg.EndedAt, NextBallSide: sg.NextBallSide,
		HomeGoals: sg.HomeGoals, AwayGoals: sg.AwayGoals, Winner: sg.Winner, Voided: sg.Voided, Edits: sg.Edits,
		Participants: sg.Participants}
}

// participantsOf is the roster projection of a game (game_participants).
func participantsOf(g engine.Game) []store.Participant {
	out := []store.Participant{}
	for _, side := range []engine.Side{engine.Home, engine.Away} {
		for i, st := range g.Roster.Side(side) {
			if !st.Taken() {
				continue
			}
			p := store.Participant{Side: string(side), Slot: i + 1, CoinUID: strOrNil(st.CoinUID)}
			if st.PlayerID != 0 {
				p.PlayerID = store.Ptr(st.PlayerID)
			}
			out = append(out, p)
		}
	}
	return out
}

// headerFromStore is the part of a game row that is not derived from its events.
func headerFromStore(g store.Game) engine.Game {
	out := engine.Game{ID: g.ID, Rules: rulesFromStore(g.Rules)}
	if g.PresetID != nil {
		out.PresetID = *g.PresetID
	}
	if g.NextBallSide != nil {
		out.NextBallSide = engine.Side(*g.NextBallSide)
	}
	if g.PairingID != nil {
		p := engine.GamePairing{ID: *g.PairingID, ASide: engine.Home}
		if g.PairingASide != nil {
			p.ASide = engine.Side(*g.PairingASide)
		}
		out.Pairing = &p
	}
	return out
}

func strOrNil[T ~string](v T) *string {
	if v == "" {
		return nil
	}
	s := string(v)
	return &s
}

func eventToStore(e engine.Event) store.Event {
	out := store.Event{
		Seq: e.Seq, Type: string(e.Type), Side: strOrNil(e.Side), Source: string(e.Source),
		Reason: strOrNil(e.Reason), CreatedAt: e.At,
	}
	if e.TargetSeq != 0 {
		out.TargetSeq = store.Ptr(e.TargetSeq)
	}
	if e.Slot != 0 {
		out.Slot = store.Ptr(e.Slot)
	}
	if e.PlayerID != 0 {
		out.PlayerID = store.Ptr(e.PlayerID)
	}
	out.CoinUID, out.Via = strOrNil(e.CoinUID), strOrNil(e.Via)
	if e.Value != nil {
		out.Value = store.Ptr(int64(*e.Value))
	}
	if e.Author != 0 {
		out.AuthorAccountID = store.Ptr(e.Author)
	}
	if e.PlayTime != nil {
		out.PlayTimeMs = store.Ptr(e.PlayTime.Milliseconds())
	}
	return out
}

func eventsToStore(events []engine.Event) []store.Event {
	out := make([]store.Event, len(events))
	for i, e := range events {
		out[i] = eventToStore(e)
	}
	return out
}

func eventFromStore(e store.Event) engine.Event {
	out := engine.Event{Seq: e.Seq, Type: engine.EventType(e.Type), Source: engine.Source(e.Source), At: e.CreatedAt}
	if e.Side != nil {
		out.Side = engine.Side(*e.Side)
	}
	if e.TargetSeq != nil {
		out.TargetSeq = *e.TargetSeq
	}
	if e.Slot != nil {
		out.Slot = *e.Slot
	}
	if e.PlayerID != nil {
		out.PlayerID = *e.PlayerID
	}
	if e.CoinUID != nil {
		out.CoinUID = *e.CoinUID
	}
	if e.Via != nil {
		out.Via = engine.Via(*e.Via)
	}
	if e.Value != nil {
		v := int(*e.Value)
		out.Value = &v
	}
	if e.AuthorAccountID != nil {
		out.Author = *e.AuthorAccountID
	}
	if e.Reason != nil {
		out.Reason = *e.Reason
	}
	if e.PlayTimeMs != nil {
		d := time.Duration(*e.PlayTimeMs) * time.Millisecond
		out.PlayTime = &d
	}
	return out
}

func eventsFromStore(events []store.Event) []engine.Event {
	out := make([]engine.Event, len(events))
	for i, e := range events {
		out[i] = eventFromStore(e)
	}
	return out
}

func regsToStore(regs []engine.Registration) []store.Registration {
	out := make([]store.Registration, len(regs))
	for i, r := range regs {
		sr := store.Registration{Side: string(r.Side), Slot: r.Slot, CoinUID: strOrNil(r.CoinUID)}
		if r.PlayerID != 0 {
			sr.PlayerID = store.Ptr(r.PlayerID)
		}
		for _, src := range r.Sources {
			sr.Sources = append(sr.Sources, store.RegSource{Via: string(src.Via), Ref: src.Ref, Stale: src.Stale})
		}
		out[i] = sr
	}
	return out
}

func regsFromStore(regs []store.Registration) []engine.Registration {
	out := make([]engine.Registration, len(regs))
	for i, r := range regs {
		er := engine.Registration{Side: engine.Side(r.Side), Slot: r.Slot}
		if r.PlayerID != nil {
			er.PlayerID = *r.PlayerID
		}
		if r.CoinUID != nil {
			er.CoinUID = *r.CoinUID
		}
		for _, src := range r.Sources {
			er.Sources = append(er.Sources, engine.RegSource{Via: engine.Via(src.Via), Ref: src.Ref, Stale: src.Stale})
		}
		out[i] = er
	}
	return out
}
