package virtual

import (
	"encoding/json"
	"errors"
	"slices"
	"sync"

	"github.com/Humbarrt/kicker/server/internal/protocol"
)

// CoinAcceptor is a virtual coin acceptor (spec 06): it holds up to Cap coins, reports every
// insert, ejects all coins at once on the `eject` command or its button, and reports what it
// holds after every `ready`.
type CoinAcceptor struct {
	*Module
	mu    sync.Mutex
	coins []string
	cap   int
}

// Errors of the simulated hardware.
var (
	ErrFull        = errors.New("the acceptor is full")
	ErrAlreadyIn   = errors.New("this coin is already inside")
	ErrInvalidCoin = errors.New("coin UIDs are 4, 7 or 10 bytes in hex")
	coinInstance   = "slot"
)

// NewCoinAcceptor returns a virtual acceptor with one coin_acceptor instance "slot".
func NewCoinAcceptor(uid string, capacity int) *CoinAcceptor {
	a := &CoinAcceptor{cap: capacity}
	a.Module = &Module{
		UID: uid, Manifest: "kicker/coin-acceptor", FWName: "kicker-coin-acceptor", FWVersion: "sim", Hardware: "virtual",
		Caps: []Cap{{Instance: coinInstance, ID: "coin_acceptor", Version: 1, CanWake: true, Params: protocol.CoinAcceptorParams{Cap: capacity}}},
	}
	a.Module.OnCommand = func(instance, name string, _ json.RawMessage) *protocol.Nack {
		if instance == coinInstance && name == "eject" {
			go a.eject("cmd")
		}
		return nil
	}
	a.Module.OnReady = func() {
		_ = a.Module.Emit(coinInstance, "held", protocol.CoinHeld{UIDs: a.Coins()})
	}
	return a
}

// Instance is the acceptor's capability instance name.
func (a *CoinAcceptor) Instance() string { return coinInstance }

// Cap is how many coins fit.
func (a *CoinAcceptor) Cap() int { return a.cap }

// Coins returns the coins inside, in the order they were inserted.
func (a *CoinAcceptor) Coins() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.coins)
}

// Insert puts a coin in, like a player pushing it through the slot.
func (a *CoinAcceptor) Insert(uid string) error {
	if !ValidCoinUID(uid) {
		return ErrInvalidCoin
	}
	a.mu.Lock()
	switch {
	case slices.Contains(a.coins, uid):
		a.mu.Unlock()
		return ErrAlreadyIn
	case len(a.coins) >= a.cap:
		a.mu.Unlock()
		return ErrFull
	}
	a.coins = append(a.coins, uid)
	a.mu.Unlock()
	return a.Module.Emit(coinInstance, "inserted", protocol.CoinInserted{UID: uid})
}

// PressButton is the physical eject button.
func (a *CoinAcceptor) PressButton() error { return a.eject("button") }

func (a *CoinAcceptor) eject(by string) error {
	a.mu.Lock()
	uids := a.coins
	a.coins = nil
	a.mu.Unlock()
	if uids == nil {
		uids = []string{}
	}
	return a.Module.Emit(coinInstance, "ejected", protocol.CoinEjected{By: by, UIDs: uids})
}

// ValidCoinUID checks an NFC UID: 4, 7 or 10 bytes as upper-case hex.
func ValidCoinUID(uid string) bool {
	switch len(uid) {
	case 8, 14, 20:
	default:
		return false
	}
	for _, c := range uid {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
