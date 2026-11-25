// Package coins maps NFC coins to players (D2). Coins are separate from players: a player has
// 0..n coins, guest coins map to the guest player. Permission: coins.manage (spec 03 §4).
package coins

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/store"
)

// Coin is a coin as the API shows it.
type Coin struct {
	UID        string `json:"uid"`
	PlayerID   int64  `json:"player_id"`
	Label      string `json:"label"`
	Active     bool   `json:"active"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt *int64 `json:"last_seen_at"`
}

func view(c store.Coin) Coin {
	v := Coin{UID: c.UID, PlayerID: c.PlayerID, Label: c.Label, Active: c.Active, CreatedAt: c.CreatedAt.UnixMilli()}
	if c.LastSeenAt != nil {
		ms := c.LastSeenAt.UnixMilli()
		v.LastSeenAt = &ms
	}
	return v
}

// Errors.
var (
	ErrForbidden  = errors.New("your group isn't allowed to do this")
	ErrInvalidUID = errors.New("coin UIDs are 4, 7 or 10 bytes in hex, e.g. 04A1B2C3D4E5F6")
	ErrLabel      = errors.New("labels are at most 40 characters")
	ErrPlayer     = errors.New("no such player, or the player is archived")
	ErrNotFound   = store.ErrNotFound
)

// Actor is who makes a change.
type Actor struct {
	AccountID int64
	PlayerID  int64
	Perms     auth.Perms
	IP        string
}

// Options configure the service.
type Options struct {
	Store *store.Store
	Auth  *auth.Service // audit log
	// OnChange tells the table that a coin now belongs to playerID (0 = unknown/deactivated),
	// so a coin that is inside an acceptor right now is updated at once.
	OnChange func(uid string, playerID int64)
	Logger   *slog.Logger
	Now      func() time.Time
}

// Service is the coin service.
type Service struct{ opts Options }

// New returns the service.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts}
}

// NormalizeUID accepts "04:a1:b2:c3", "04 A1 B2 C3" or "04a1b2c3" and returns "04A1B2C3".
func NormalizeUID(uid string) (string, error) {
	u := strings.ToUpper(strings.NewReplacer(":", "", " ", "", "-", "").Replace(strings.TrimSpace(uid)))
	switch len(u) {
	case 8, 14, 20:
	default:
		return "", ErrInvalidUID
	}
	for _, c := range u {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F') {
			return "", ErrInvalidUID
		}
	}
	return u, nil
}

func (s *Service) audit(ctx context.Context, by Actor, action, target, details string) {
	if s.opts.Auth == nil {
		return
	}
	var id *int64
	if by.AccountID != 0 {
		id = &by.AccountID
	}
	s.opts.Auth.Audit(ctx, id, by.IP, action, target, details)
}

func (s *Service) changed(uid string, playerID int64) {
	if s.opts.OnChange != nil {
		s.opts.OnChange(uid, playerID)
	}
}

// List returns all coins (coins.manage) or, for anyone else, the coins of their own player.
func (s *Service) List(ctx context.Context, by Actor, playerID int64) ([]Coin, error) {
	switch {
	case by.AccountID != 0 && by.Perms.Has(auth.CoinsManage):
	case by.AccountID != 0 && by.PlayerID != 0 && playerID == by.PlayerID:
	default:
		return nil, ErrForbidden
	}
	cs, err := s.opts.Store.Coins(ctx, playerID)
	if err != nil {
		return nil, err
	}
	out := make([]Coin, len(cs))
	for i, c := range cs {
		out[i] = view(c)
	}
	return out, nil
}

// Assign creates a coin or gives it to another player (coins.manage). The coin becomes active.
func (s *Service) Assign(ctx context.Context, by Actor, uid string, playerID int64, label string) (Coin, error) {
	if by.AccountID == 0 || !by.Perms.Has(auth.CoinsManage) {
		return Coin{}, ErrForbidden
	}
	uid, err := NormalizeUID(uid)
	if err != nil {
		return Coin{}, err
	}
	label = strings.TrimSpace(label)
	if utf8.RuneCountInString(label) > 40 {
		return Coin{}, ErrLabel
	}
	p, err := s.opts.Store.Player(ctx, playerID)
	if err != nil || p.ArchivedAt != nil {
		return Coin{}, ErrPlayer
	}
	old, oldErr := s.opts.Store.Coin(ctx, uid)
	if err := s.opts.Store.SaveCoin(ctx, store.Coin{UID: uid, PlayerID: playerID, Label: label, Active: true}); err != nil {
		return Coin{}, err
	}
	switch {
	case errors.Is(oldErr, store.ErrNotFound):
		s.audit(ctx, by, "coin_assigned", uid, p.Name)
	case old.PlayerID != playerID:
		s.audit(ctx, by, "coin_reassigned", uid, p.Name)
	}
	s.changed(uid, playerID)
	c, err := s.opts.Store.Coin(ctx, uid)
	return view(c), err
}

// SetActive deactivates a coin (e.g. lost; it then counts as unknown) or activates it again.
func (s *Service) SetActive(ctx context.Context, by Actor, uid string, active bool) (Coin, error) {
	if by.AccountID == 0 || !by.Perms.Has(auth.CoinsManage) {
		return Coin{}, ErrForbidden
	}
	c, err := s.opts.Store.Coin(ctx, uid)
	if err != nil {
		return Coin{}, err
	}
	if c.Active != active {
		c.Active = active
		if err := s.opts.Store.SaveCoin(ctx, c); err != nil {
			return Coin{}, err
		}
		s.audit(ctx, by, map[bool]string{true: "coin_activated", false: "coin_deactivated"}[active], uid, "")
		s.changed(uid, map[bool]int64{true: c.PlayerID, false: 0}[active])
	}
	return view(c), nil
}

// Delete forgets a coin (coins.manage). Past games are not affected: they store the player.
func (s *Service) Delete(ctx context.Context, by Actor, uid string) error {
	if by.AccountID == 0 || !by.Perms.Has(auth.CoinsManage) {
		return ErrForbidden
	}
	if err := s.opts.Store.DeleteCoin(ctx, uid); err != nil {
		return err
	}
	s.audit(ctx, by, "coin_deleted", uid, "")
	s.changed(uid, 0)
	return nil
}

// Resolve looks up an inserted coin: its player, or 0 for an unknown or deactivated coin. It
// also records when the coin was last seen.
func (s *Service) Resolve(ctx context.Context, uid string) int64 {
	c, err := s.opts.Store.Coin(ctx, uid)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.opts.Logger.Warn("coin lookup", "uid", uid, "err", err)
		}
		return 0
	}
	if err := s.opts.Store.TouchCoin(ctx, uid, s.opts.Now()); err != nil {
		s.opts.Logger.Warn("coin last seen", "uid", uid, "err", err)
	}
	if !c.Active {
		return 0
	}
	return c.PlayerID
}

// All lists every coin without a permission check. Only for the simulator panel, which plays the
// person holding the coins.
func (s *Service) All(ctx context.Context) ([]Coin, error) {
	cs, err := s.opts.Store.Coins(ctx, 0)
	if err != nil {
		return nil, err
	}
	out := make([]Coin, len(cs))
	for i, c := range cs {
		out[i] = view(c)
	}
	return out, nil
}
